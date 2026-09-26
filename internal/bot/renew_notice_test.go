package bot

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// sentButton — inline-кнопка, как она ушла в Telegram.
type sentButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

func sentButtons(t *testing.T, m sentMessage) []sentButton {
	t.Helper()
	if m.Markup == "" {
		return nil
	}
	var markup struct {
		Inline [][]sentButton `json:"inline_keyboard"`
	}
	require.NoError(t, json.Unmarshal([]byte(m.Markup), &markup))
	var out []sentButton
	for _, row := range markup.Inline {
		out = append(out, row...)
	}
	return out
}

// onlyNotice возвращает единственное сообщение с подстрокой.
func onlyNotice(t *testing.T, capture *telegramCapture, substr string) sentMessage {
	t.Helper()
	found := capture.matching(substr)
	require.Len(t, found, 1, "ожидали ровно одно сообщение с %q, ушло: %+v", substr, capture.all())
	return found[0]
}

// payOpenData — callback_data кнопки, открывающей экран оплаты.
const payOpenData = "\f" + cbPayOpen

var edgeRef = remnawave.UserRef{UUID: "uuid-edge"}

func TestRenewNoticeCarriesPayButton(t *testing.T) {
	cases := []struct {
		name     string
		expireIn time.Duration
		notice   string
	}{
		{"за 3 дня", 60 * time.Hour, "заканчивается через 3 дня"},
		{"за сутки", 12 * time.Hour, "менее чем через 24 часа"},
		{"истекла", -time.Hour, "Ваша подписка истекла"},
	}
	for _, flag := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/autorenew=%v", tc.name, flag), func(t *testing.T) {
				expireAt := time.Now().UTC().Add(tc.expireIn)
				stub := &arEdgeStub{expireAt: expireAt}
				b, db, capture := setupAutorenewEdgeBot(t, stub)
				b.config.AutorenewEnabled = flag
				// Согласие выключено: иначе предупреждения за 3 дня и за сутки
				// молчат при включённом рубильнике, и проверять было бы нечего.
				require.NoError(t, db.SetAutorenewEnabled(arEdgeUserID, false))

				b.processPaidUser(arEdgeUserID, edgeRef, expireAt, time.Now().UTC())

				msg := onlyNotice(t, capture, tc.notice)
				assert.NotContains(t, msg.Text, "Нажмите", "кнопка под сообщением — отсылать в меню незачем")
				assert.Equal(t, []sentButton{{Text: "💳 Продлить за 400 ₽", Data: payOpenData}}, sentButtons(t, msg))
			})
		}
	}
}

// С включённым автопродлением предупреждения молчат, но истечение подписки —
// повод для кнопки и при нём: списание, выходит, не прошло.
func TestExpiredNoticeCarriesPayButtonWithAutorenewConsent(t *testing.T) {
	expireAt := time.Now().UTC().Add(-time.Hour)
	b, _, capture := setupAutorenewEdgeBot(t, &arEdgeStub{expireAt: expireAt})
	require.True(t, b.autorenewAvailable())

	b.processPaidUser(arEdgeUserID, edgeRef, expireAt, time.Now().UTC())

	msg := onlyNotice(t, capture, "Ваша подписка истекла")
	assert.Equal(t, []sentButton{{Text: "💳 Продлить за 400 ₽", Data: payOpenData}}, sentButtons(t, msg))
}

func TestTrialNoticeCarriesPayButton(t *testing.T) {
	expireAt := time.Now().UTC().Add(12 * time.Hour)
	b, _, capture := setupAutorenewEdgeBot(t, &arEdgeStub{expireAt: expireAt})

	b.processTrialUser(arEdgeUserID, edgeRef, expireAt, time.Now().UTC())

	msg := onlyNotice(t, capture, "пробный период заканчивается")
	assert.Equal(t, []sentButton{{Text: "💳 Оплатить подписку — 400 ₽", Data: payOpenData}}, sentButtons(t, msg))
}

// Доступа уже нет — продлевать нечего.
func TestKickNoticesHaveNoPayButton(t *testing.T) {
	t.Run("конец триала", func(t *testing.T) {
		expireAt := time.Now().UTC().Add(-time.Hour)
		b, _, capture := setupAutorenewEdgeBot(t, &arEdgeStub{expireAt: expireAt})

		b.processTrialUser(arEdgeUserID, edgeRef, expireAt, time.Now().UTC())

		assert.Empty(t, sentButtons(t, onlyNotice(t, capture, "пробный период закончился")))
	})
	t.Run("конец grace", func(t *testing.T) {
		expireAt := time.Now().UTC().Add(-73 * time.Hour)
		b, _, capture := setupAutorenewEdgeBot(t, &arEdgeStub{expireAt: expireAt, status: remnawave.StatusDisabled})

		b.processPaidUser(arEdgeUserID, edgeRef, expireAt, time.Now().UTC())

		assert.Empty(t, sentButtons(t, onlyNotice(t, capture, "Ваш доступ удалён")))
	})
}

// Legacy без цены: кнопки оплаты нет и в меню, текст прежний.
func TestRenewNoticeWithoutPriceKeepsPlainText(t *testing.T) {
	expireAt := time.Now().UTC().Add(60 * time.Hour)
	b, db, capture := setupAutorenewEdgeBot(t, &arEdgeStub{expireAt: expireAt})
	require.NoError(t, db.SetAutorenewEnabled(arEdgeUserID, false))
	_, err := db.Conn().Exec(`UPDATE users SET subscription_price = NULL WHERE telegram_id = ?`, arEdgeUserID)
	require.NoError(t, err)

	b.processPaidUser(arEdgeUserID, edgeRef, expireAt, time.Now().UTC())

	msg := onlyNotice(t, capture, "заканчивается через 3 дня")
	assert.Contains(t, msg.Text, "Нажмите \"💳 Продлить подписку\"")
	assert.Empty(t, sentButtons(t, msg))
}

// Без настроенной кассы оплатить нечем — кнопка вела бы в «не настроено».
func TestRenewNoticeWithoutProviderHasNoButton(t *testing.T) {
	expireAt := time.Now().UTC().Add(60 * time.Hour)
	b, db, capture := setupAutorenewEdgeBot(t, &arEdgeStub{expireAt: expireAt})
	require.NoError(t, db.SetAutorenewEnabled(arEdgeUserID, false))
	b.yookassa = nil

	b.processPaidUser(arEdgeUserID, edgeRef, expireAt, time.Now().UTC())

	assert.Empty(t, sentButtons(t, onlyNotice(t, capture, "заканчивается через 3 дня")))
}

// Кнопка — часть сообщения, а не новый повод его слать.
func TestRenewNoticeStillSentOnce(t *testing.T) {
	expireAt := time.Now().UTC().Add(60 * time.Hour)
	b, db, capture := setupAutorenewEdgeBot(t, &arEdgeStub{expireAt: expireAt})
	require.NoError(t, db.SetAutorenewEnabled(arEdgeUserID, false))

	b.processPaidUser(arEdgeUserID, edgeRef, expireAt, time.Now().UTC())
	b.processPaidUser(arEdgeUserID, edgeRef, expireAt, time.Now().UTC())

	assert.Len(t, capture.matching("заканчивается через 3 дня"), 1)
}

func payOpenCallback() *MockContext {
	return &MockContext{
		sender:   &tele.User{ID: arEdgeUserID},
		message:  &tele.Message{ID: 77, Text: "⏳ Ваша подписка заканчивается через 3 дня."},
		callback: &tele.Callback{Unique: cbPayOpen},
	}
}

// Нажатие открывает живой экран оплаты новым сообщением: уведомление под
// ним не затирается.
func TestPayOpenShowsMethodScreen(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, _, _ := setupAutorenewEdgeBot(t, stub)
	b.config.AutorenewEnabled = false

	ctx := payOpenCallback()
	require.NoError(t, b.handlePayOpen(ctx))

	assert.True(t, ctx.responded)
	assert.Nil(t, ctx.editedMsg, "уведомление остаётся как было")
	assert.Contains(t, ctx.sentMsg, "400 руб.")
	opts, ok := ctx.opts[0].(*tele.SendOptions)
	require.True(t, ok)
	assert.True(t, keyboardHasCallback(opts.ReplyMarkup, cbPayMethod))
	assert.Zero(t, stub.callCount(), "платёж создаётся только выбором способа")
}

// Старое уведомление при уже оплаченной надолго подписке упирается в предел
// 90 дней, а не создаёт второй платёж.
func TestPayOpenFromStaleNoticeHitsNinetyDayLimit(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(120 * 24 * time.Hour)}
	b, db, _ := setupAutorenewEdgeBot(t, stub)
	panel := newTestPanelClient()
	panel.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		payload := fmt.Sprintf(`{"response":[{"uuid":"uuid-edge","telegramId":%d,"username":"u","status":"ACTIVE","expireAt":%q}]}`,
			arEdgeUserID, stub.expireAt.Format(time.RFC3339))
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(payload))}, nil
	})})
	b.remnawave = panel

	ctx := payOpenCallback()
	require.NoError(t, b.handlePayOpen(ctx))

	assert.Contains(t, ctx.sentMsg, "не раньше чем за 90 дней")
	assert.Zero(t, stub.callCount())
	pending, err := db.GetPendingPayment(arEdgeUserID)
	require.NoError(t, err)
	assert.Nil(t, pending)
}

func TestPayOpenRespectsMaintenance(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, _, _ := setupAutorenewEdgeBot(t, stub)
	b.setMaintenanceMode(true)

	ctx := payOpenCallback()
	require.NoError(t, b.handlePayOpen(ctx))

	assert.Contains(t, ctx.sentMsg, "на обслуживании")
	assert.True(t, ctx.responded)
}
