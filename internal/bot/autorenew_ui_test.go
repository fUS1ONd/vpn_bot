package bot

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/fus1ond/vpn_bot/internal/yookassa"
)

func autorenewUIBot(t *testing.T) (*Bot, *database.DB) {
	t.Helper()
	b, db := setupTestBot(t)
	b.config.AutorenewEnabled = true
	b.config.YooKassaShopID = "shop-1"
	b.config.YooKassaSecretKey = "secret"
	b.yookassa = yookassa.NewClient("shop-1", "secret")
	return b, db
}

func activeRemUser(daysLeft int) *remnawave.User {
	return &remnawave.User{
		Status:   remnawave.StatusActive,
		ExpireAt: time.Now().UTC().Add(time.Duration(daysLeft) * 24 * time.Hour),
	}
}

// stubActivePanelUser заставляет клиент панели отдавать активного пользователя:
// loadAutorenewView перечитывает состояние подписки перед каждым действием.
func stubActivePanelUser(t *testing.T, b *Bot, uuid string, daysLeft int) {
	t.Helper()
	client := newTestPanelClient()
	client.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := json.Marshal(map[string]any{"response": map[string]any{
			"uuid":            uuid,
			"shortUuid":       "short",
			"username":        "u",
			"status":          "ACTIVE",
			"expireAt":        time.Now().UTC().AddDate(0, 0, daysLeft).Format(time.RFC3339),
			"subscriptionUrl": "https://sub.example.com/short",
		}})
		require.NoError(t, err)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})})
	b.remnawave = client
}

func priced(price int) *database.User {
	return &database.User{TelegramID: 1, SubscriptionPrice: &price}
}

// Четыре состояния карточки из спеки воспроизводятся целиком.
func TestAutorenewCardStates(t *testing.T) {
	b, db := autorenewUIBot(t)

	t.Run("способа нет", func(t *testing.T) {
		v := b.autorenewViewFor(1, activeRemUser(10), priced(400))
		require.Equal(t, autorenewNoMethod, v.state)
		require.Contains(t, v.cardLine(), "недоступно")
		require.Contains(t, b.autorenewScreen(v), "запомним способ оплаты")
	})

	t.Run("выключено, способ есть", func(t *testing.T) {
		require.NoError(t, db.SaveAutorenewMethod(1, "pm-1", "•••• 4242"))
		v := b.autorenewViewFor(1, activeRemUser(10), priced(400))
		require.Equal(t, autorenewOff, v.state)
		require.Contains(t, v.cardLine(), "выключено")
	})

	t.Run("включено", func(t *testing.T) {
		require.NoError(t, db.SetAutorenewEnabled(1, true))
		v := b.autorenewViewFor(1, activeRemUser(10), priced(400))
		require.Equal(t, autorenewOn, v.state)
		require.Contains(t, v.cardLine(), "включено, спишем 400 ₽")
		require.Contains(t, b.autorenewScreen(v), "•••• 4242")
	})

	t.Run("подписка истекла", func(t *testing.T) {
		require.NoError(t, db.SetAutorenewEnabled(1, false))
		expired := &remnawave.User{Status: remnawave.StatusDisabled, ExpireAt: time.Now().UTC().Add(-time.Hour)}
		v := b.autorenewViewFor(1, expired, priced(400))
		require.Equal(t, autorenewExpired, v.state)
		require.Contains(t, v.cardLine(), "недоступно")
		require.Contains(t, b.autorenewScreen(v), "Продлите подписку")
	})
}

// Строка не показывается у бессрочных, у legacy без цены и при выключенной фиче.
func TestAutorenewCardLineHidden(t *testing.T) {
	b, _ := autorenewUIBot(t)

	infinite := &remnawave.User{Status: remnawave.StatusActive, ExpireAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)}
	require.Equal(t, autorenewHidden, b.autorenewViewFor(1, infinite, priced(400)).state)

	require.Equal(t, autorenewHidden, b.autorenewViewFor(1, activeRemUser(10), &database.User{TelegramID: 1}).state)

	b.config.AutorenewEnabled = false
	require.Equal(t, autorenewHidden, b.autorenewViewFor(1, activeRemUser(10), priced(400)).state)
	require.Empty(t, b.autorenewViewFor(1, activeRemUser(10), priced(400)).cardLine())
}

// Согласие без Способа — нормальное состояние, но списывать нечем: карточка
// честно говорит «недоступно», а не обещает списание.
func TestAutorenewEnabledWithoutMethodShowsUnavailable(t *testing.T) {
	b, db := autorenewUIBot(t)
	require.NoError(t, db.SetAutorenewEnabled(1, true))

	v := b.autorenewViewFor(1, activeRemUser(10), priced(400))
	require.Equal(t, autorenewNoMethod, v.state)
}

// Кнопка автопродления появляется в карточке ровно тогда, когда есть строка.
func TestSubscriptionCardKeyboardShowsAutorenew(t *testing.T) {
	withAutorenew := SubscriptionCardKeyboard("https://sub.example.com/a", true, true)
	require.True(t, keyboardHasCallback(withAutorenew, cbAutorenewOpen))

	without := SubscriptionCardKeyboard("https://sub.example.com/a", true, false)
	require.False(t, keyboardHasCallback(without, cbAutorenewOpen))
}

// «🔙 Назад» с экрана автопродления возвращает карточку, а не удаляет сообщение.
func TestAutorenewScreenKeyboardBackReturnsCard(t *testing.T) {
	for _, state := range []autorenewCardState{autorenewOn, autorenewOff, autorenewNoMethod, autorenewExpired} {
		kb := AutorenewScreenKeyboard(autorenewView{state: state})
		require.True(t, keyboardHasCallback(kb, cbSubCard), "состояние %d", state)
	}

	require.True(t, keyboardHasCallback(AutorenewScreenKeyboard(autorenewView{state: autorenewOn, consent: true}), cbAutorenewDisable))
	require.True(t, keyboardHasCallback(AutorenewScreenKeyboard(autorenewView{state: autorenewOff}), cbAutorenewEnable))
	// Включить при истёкшей подписке нечем — кнопки нет (Р7).
	require.False(t, keyboardHasCallback(AutorenewScreenKeyboard(autorenewView{state: autorenewExpired}), cbAutorenewEnable))
	require.False(t, keyboardHasCallback(AutorenewScreenKeyboard(autorenewView{state: autorenewNoMethod}), cbAutorenewEnable))
	// Согласие живо, а Способ пропал: выключить человек обязан мочь.
	require.True(t, keyboardHasCallback(
		AutorenewScreenKeyboard(autorenewView{state: autorenewNoMethod, consent: true}), cbAutorenewDisable))
}

// Предложение включить приходит после оплаты, пока автопродление выключено.
func TestAutorenewOfferMarkupOnlyWhenOfferable(t *testing.T) {
	b, db := autorenewUIBot(t)
	_, err := db.CreateUser(1, "u", "U", strPtrTest("uuid-ar"), nil, intPtrTest(400), nil)
	require.NoError(t, err)
	stubActivePanelUser(t, b, "uuid-ar", 10)

	// Способа нет — предлагать нечего.
	require.Nil(t, b.autorenewOfferMarkup(1))

	require.NoError(t, db.SaveAutorenewMethod(1, "pm-1", "•••• 4242"))
	markup := b.autorenewOfferMarkup(1)
	require.NotNil(t, markup)
	require.True(t, keyboardHasCallback(markup, cbAutorenewOffer))

	// Уже включено — предлагать нечего.
	require.NoError(t, db.SetAutorenewEnabled(1, true))
	require.Nil(t, b.autorenewOfferMarkup(1))
}

// Тестовый платёж админа предложения не получает.
func TestPaymentSuccessMarkupSkipsTestPayment(t *testing.T) {
	b, db := autorenewUIBot(t)
	_, err := db.CreateUser(1, "u", "U", strPtrTest("uuid-ar2"), nil, intPtrTest(400), nil)
	require.NoError(t, err)
	require.NoError(t, db.SaveAutorenewMethod(1, "pm-1", "•••• 4242"))
	stubActivePanelUser(t, b, "uuid-ar2", 10)

	markup := b.paymentSuccessMarkup(&database.Payment{TelegramID: 1, IsTest: true})
	require.False(t, keyboardHasCallback(markup, cbAutorenewOffer))

	markup = b.paymentSuccessMarkup(&database.Payment{TelegramID: 1})
	require.True(t, keyboardHasCallback(markup, cbAutorenewOffer))
}

// Экран условий называет сумму, периодичность, дату первого списания и оферту.
func TestAutorenewTermsText(t *testing.T) {
	b, _ := autorenewUIBot(t)
	v := autorenewView{
		state: autorenewOff, price: 400, methodTitle: "СБП",
		chargeAt: time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC),
		expireAt: time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC),
	}
	text := b.autorenewTermsText(v)
	require.Contains(t, text, "400 ₽")
	require.Contains(t, text, "раз в месяц")
	require.Contains(t, text, "22.09.2026")
	require.Contains(t, text, "СБП")
	require.Contains(t, text, "https://example.com/terms")
	require.Contains(t, text, "один тап")
}

// keyboardHasCallback ищет inline-кнопку по её Unique.
func keyboardHasCallback(kb *tele.ReplyMarkup, unique string) bool {
	if kb == nil {
		return false
	}
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if btn.Unique == unique {
				return true
			}
		}
	}
	return false
}

func intPtrTest(v int) *int { return &v }

// Строка автопродления в карточке админа различает все состояния.
func TestAdminAutorenewLine(t *testing.T) {
	b, db := autorenewUIBot(t)

	require.Empty(t, adminAutorenewLine(b.autorenewViewFor(1, activeRemUser(10), &database.User{TelegramID: 1})),
		"legacy без цены — строки нет")

	require.Contains(t, adminAutorenewLine(b.autorenewViewFor(1, activeRemUser(10), priced(400))), "недоступно (способ не сохранён)")

	require.NoError(t, db.SaveAutorenewMethod(1, "pm-1", "•••• 4242"))
	require.Contains(t, adminAutorenewLine(b.autorenewViewFor(1, activeRemUser(10), priced(400))), "выключено")

	require.NoError(t, db.SetAutorenewEnabled(1, true))
	line := adminAutorenewLine(b.autorenewViewFor(1, activeRemUser(10), priced(400)))
	require.Contains(t, line, "включено")
	require.Contains(t, line, "•••• 4242")
	require.Contains(t, line, "400 ₽")
}

// Кнопка выключения появляется только у включённого автопродления, кнопки
// включения не существует вовсе.
func TestAdminUserInfoKeyboardAutorenewButton(t *testing.T) {
	remUser := activeRemUser(10)

	on := AdminUserInfoKeyboardWithReferrals(1, remUser, 0, true)
	require.True(t, keyboardHasCallback(on, cbAdminAutorenewOff))

	off := AdminUserInfoKeyboardWithReferrals(1, remUser, 0, false)
	require.False(t, keyboardHasCallback(off, cbAdminAutorenewOff))
}

// Включение, когда до конца подписки меньше суток: окно T−24ч уже прошло, и
// списание уйдёт ближайшим проходом. Молчать об этом нельзя — человек включал
// автопродление, а не покупал прямо сейчас.
func TestAutorenewTermsWarnAboutImminentCharge(t *testing.T) {
	b, _ := autorenewUIBot(t)

	soon := autorenewView{state: autorenewOff, price: 400, expireAt: time.Now().UTC().Add(6 * time.Hour)}
	require.True(t, soon.chargeAt.IsZero())
	require.Contains(t, b.autorenewTermsText(soon), "ближайшие полчаса")

	later := autorenewView{
		state: autorenewOff, price: 400,
		chargeAt: time.Now().UTC().Add(9 * 24 * time.Hour),
		expireAt: time.Now().UTC().Add(10 * 24 * time.Hour),
	}
	require.NotContains(t, b.autorenewTermsText(later), "ближайшие полчаса")
}
