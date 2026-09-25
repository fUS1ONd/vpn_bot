package bot

import (
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// Рубильник AUTORENEW_ENABLED выключен — поведение оплаты и карточки ровно как до
// автопродления. Худший случай: касса настроена, а в autorenewals лежат согласие
// и Способ (рубильник включали и выключили обратно). Ни одно место фичи не
// должно ни показаться, ни сработать.

func autorenewFlagOffBot(t *testing.T, stub *arEdgeStub) (*Bot, *telegramCapture) {
	t.Helper()
	b, _, capture := setupAutorenewEdgeBot(t, stub)
	b.config.AutorenewEnabled = false
	require.False(t, b.autorenewAvailable())
	return b, capture
}

const flagOffManualBody = `{"id":"yo-off-manual","status":"pending","amount":{"value":"400.00","currency":"RUB"},
  "recipient":{"account_id":"shop-1"},"confirmation":{"confirmation_url":"https://yookassa.test/pay/off"}}`

func TestAutorenewFlagOffPayScreenUnchanged(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour), responses: []string{flagOffManualBody}}
	b, _ := autorenewFlagOffBot(t, stub)

	ctx := &MockContext{sender: &tele.User{ID: arEdgeUserID}, message: &tele.Message{}}
	require.NoError(t, b.handlePayButton(ctx))

	require.Equal(t, "💳 <b>Подписка на 1 месяц — 400 руб.</b>\n\nВыберите способ оплаты:", ctx.sentMsg,
		"без рубильника экран оплаты — без абзаца согласия")
}

func TestAutorenewFlagOffPaymentRequestUnchanged(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour), responses: []string{flagOffManualBody}}
	b, _ := autorenewFlagOffBot(t, stub)

	_, url, err := b.createPaymentForProvider(arEdgeUserID, paymentprovider.YooKassa)
	require.NoError(t, err)
	require.NotEmpty(t, url)

	require.Equal(t, 1, stub.callCount())
	_, hasSave := stub.call(0).Body["save_payment_method"]
	require.False(t, hasSave, "без рубильника касса не должна получать save_payment_method")
}

// Контроль к тесту выше: с включённым рубильником поле уходит, значит тест
// действительно видит разницу.
func TestAutorenewFlagOnPaymentRequestSavesMethod(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour), responses: []string{flagOffManualBody}}
	b, _, _ := setupAutorenewEdgeBot(t, stub)

	_, _, err := b.createPaymentForProvider(arEdgeUserID, paymentprovider.YooKassa)
	require.NoError(t, err)
	require.Equal(t, true, stub.call(0).Body["save_payment_method"])
}

func TestAutorenewFlagOffPaymentSuccessKeepsMenuKeyboard(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, _ := autorenewFlagOffBot(t, stub)

	markup := b.paymentSuccessMarkupFor(arEdgeUserID, false)
	require.Equal(t, b.userKeyboard(arEdgeUserID), markup,
		"после оплаты — обычная reply-клавиатура меню, без предложения включить автопродление")
}

func TestAutorenewFlagOffCardUnchanged(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, _ := autorenewFlagOffBot(t, stub)

	ref, ok := b.resolveUserRef(arEdgeUserID)
	require.True(t, ok)
	remUser, err := b.remnawave.GetUser(ref)
	require.NoError(t, err)

	text, markup := b.buildSubscriptionCard(arEdgeUserID, remUser)
	require.NotContains(t, text, "Автопродление")
	require.NotContains(t, text, "автосписан")
	requireNoAutorenewButtons(t, markup)

	// Карточка админа — тоже без строки и без кнопки выключения.
	adminText, adminMarkup, err := b.buildAdminUserInfo(arEdgeUserID)
	require.NoError(t, err)
	require.NotContains(t, adminText, "Автопродление")
	requireNoAutorenewButtons(t, adminMarkup)
}

func TestAutorenewFlagOffSchedulerDoesNothing(t *testing.T) {
	// Окно T−24ч: при включённом рубильнике тут было бы списание.
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{edgeSucceededBody("yo-off-charge")}}
	b, capture := autorenewFlagOffBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	require.Zero(t, stub.callCount(), "без рубильника в кассу не ходим")
	require.Empty(t, messagesTo(capture, arEdgeUserID))
	require.False(t, b.autorenewSuppressesExpiryNotice(arEdgeUserID, expireAt),
		"без рубильника expire_3d/expire_1d приходят как раньше, даже при оставшемся согласии")
}

// Inline-кнопки живут в чате вечно: старая кнопка при выключенном рубильнике
// отвечает «недоступно», ничего не показывает и ничего не пишет в autorenewals.
func TestAutorenewFlagOffOldButtonsDoNothing(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, _ := autorenewFlagOffBot(t, stub)

	handlers := map[string]func(tele.Context) error{
		"open":           b.handleAutorenewOpen,
		"offer":          b.handleAutorenewOffer,
		"enable":         b.handleAutorenewEnable,
		"disable":        b.handleAutorenewDisable,
		"method":         b.handlePaymentMethod,
		"unlink":         b.handlePaymentMethodUnlink,
		"unlink-confirm": b.handlePaymentMethodUnlinkConfirm,
	}
	for name, handle := range handlers {
		ctx := &MockContext{sender: &tele.User{ID: arEdgeUserID}, message: &tele.Message{}}
		require.NoError(t, handle(ctx), name)
		require.Equal(t, autorenewOffAlert, ctx.alertText, name)
		require.Nil(t, ctx.editedMsg, "%s: экран показываться не должен", name)
	}

	adminCtx := &MockContext{sender: &tele.User{ID: b.config.AdminID}, message: &tele.Message{}}
	require.NoError(t, b.handleAdminAutorenewDisable(adminCtx))
	require.Equal(t, autorenewOffAlert, adminCtx.alertText)

	renewal, err := b.db.GetAutorenewal(arEdgeUserID)
	require.NoError(t, err)
	require.True(t, renewal.IsEnabled(), "старая кнопка не должна трогать согласие")
	require.True(t, renewal.HasMethod(), "старая кнопка не должна трогать Способ")
}

func requireNoAutorenewButtons(t *testing.T, markup *tele.ReplyMarkup) {
	t.Helper()
	if markup == nil {
		return
	}
	for _, row := range markup.InlineKeyboard {
		for _, btn := range row {
			for _, unique := range []string{cbAutorenewOpen, cbPaymentMethod, cbAdminAutorenewOff} {
				require.False(t, btn.Unique == unique || strings.Contains(btn.Data, unique),
					"кнопка %q (%s) не должна появляться без рубильника", btn.Text, unique)
			}
		}
	}
}
