package bot

import (
	"testing"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// TestSupportURL_Recognized: из чего кнопку собрать можно.
func TestSupportURL_Recognized(t *testing.T) {
	cases := map[string]string{
		"@support_bot":              "https://t.me/support_bot",
		"  @support_bot  ":          "https://t.me/support_bot",
		"https://t.me/support_bot":  "https://t.me/support_bot",
		"http://t.me/support_bot":   "https://t.me/support_bot",
		"t.me/support_bot":          "https://t.me/support_bot",
		"https://t.me/+AbCdEf123":   "https://t.me/+AbCdEf123",
		"telegram.me/support_bot":   "https://t.me/support_bot",
		"https://t.me/support_bot/": "https://t.me/support_bot",
		"https://T.ME/support_bot":  "https://t.me/support_bot",
	}
	for contact, want := range cases {
		got, ok := supportURL(contact)
		require.True(t, ok, "контакт %q должен давать кнопку", contact)
		assert.Equal(t, want, got, "контакт %q", contact)
	}
}

// TestSupportURL_NotRecognized: значение задаёт владелец, и битое или неопознанное
// не должно ронять сообщение об ошибке — это последнее, что осталось у пользователя.
func TestSupportURL_NotRecognized(t *testing.T) {
	for _, contact := range []string{
		"",
		"   ",
		"support@example.com",
		`<a href="https://example.com/help">поддержка</a>`,
		"пишите в личку",
		"https://example.com/support",
		"@",
		"@!!!",
		"t.me/",
		"+79991234567",
	} {
		_, ok := supportURL(contact)
		assert.False(t, ok, "контакт %q кнопкой быть не должен", contact)
	}
}

func newErrorExitBot(contact string) *Bot {
	return &Bot{config: &config.Config{SupportContact: contact}}
}

// TestErrorExitKeyboard_RetryAndSupport: из ошибки есть оба выхода.
func TestErrorExitKeyboard_RetryAndSupport(t *testing.T) {
	b := newErrorExitBot("@support_bot")

	markup := b.errorExitKeyboard(retryAction{unique: cbSubCard})
	require.NotNil(t, markup)
	require.Len(t, markup.InlineKeyboard, 2)

	assert.Equal(t, "🔄 Повторить", markup.InlineKeyboard[0][0].Text)
	assert.Equal(t, "💬 Написать в поддержку", markup.InlineKeyboard[1][0].Text)
	assert.Equal(t, "https://t.me/support_bot", markup.InlineKeyboard[1][0].URL)
}

// TestErrorExitKeyboard_NoRetryWhenPointless: где повтор бессмыслен, кнопки нет.
func TestErrorExitKeyboard_NoRetryWhenPointless(t *testing.T) {
	b := newErrorExitBot("@support_bot")

	markup := b.errorExitKeyboard(retryAction{})
	require.NotNil(t, markup)
	require.Len(t, markup.InlineKeyboard, 1)
	assert.Equal(t, "💬 Написать в поддержку", markup.InlineKeyboard[0][0].Text)
}

// TestErrorExitKeyboard_NoButtonsAtAll: контакт не опознан и повтора нет —
// клавиатуры не будет вовсе, сообщение уходит голым текстом.
func TestErrorExitKeyboard_NoButtonsAtAll(t *testing.T) {
	b := newErrorExitBot("пишите в личку")
	assert.Nil(t, b.errorExitKeyboard(retryAction{}))
}

// TestErrorExitText_KeepsContactWhenNoButton: контакт не опознан — он обязан
// остаться в тексте, иначе выхода из ошибки нет вообще.
func TestErrorExitText_KeepsContactWhenNoButton(t *testing.T) {
	b := newErrorExitBot("support@example.com")

	text := b.errorExitText("Не удалось создать платёж.")

	assert.Contains(t, text, "Не удалось создать платёж.")
	assert.Contains(t, text, "support@example.com")
}

// TestErrorExitText_NoDuplicateContactWhenButtonExists: контакт уже на кнопке —
// повторять его в тексте незачем.
func TestErrorExitText_NoDuplicateContactWhenButtonExists(t *testing.T) {
	b := newErrorExitBot("@support_bot")

	text := b.errorExitText("Не удалось создать платёж.")

	assert.Equal(t, "Не удалось создать платёж.", text)
}

// TestSendErrorExit_SendsTextAndKeyboard: хелпер доносит и текст, и выходы.
func TestSendErrorExit_SendsTextAndKeyboard(t *testing.T) {
	b := newErrorExitBot("@support_bot")
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{}}

	require.NoError(t, b.sendErrorExit(ctx, "Не удалось получить статус.", retryAction{unique: cbSubCard}))

	assert.Equal(t, "Не удалось получить статус.", ctx.sentMsg)
	require.Len(t, ctx.opts, 1)
	opts, ok := ctx.opts[0].(*tele.SendOptions)
	require.True(t, ok)
	assert.Equal(t, tele.ModeHTML, opts.ParseMode)
	require.NotNil(t, opts.ReplyMarkup)
	assert.Len(t, opts.ReplyMarkup.InlineKeyboard, 2)
}

// TestRevokeErrorAlert_NamesNextStep: у алертов клавиатуры нет, поэтому выход
// называется словами — иначе ошибка перевыпуска остаётся тупиком.
func TestRevokeErrorAlert_NamesNextStep(t *testing.T) {
	for _, err := range []error{errRevokeLoadFailed, errRevokeFailed} {
		alert := revokeErrorAlert(err)
		assert.Contains(t, alert, "поддержку", "ошибка %v должна называть адресата", err)
		assert.LessOrEqual(t, len([]rune(alert)), 200, "алерт Telegram ограничен 200 символами")
	}
}

// TestRevokeErrorAlert_StaysWithinAlertLimit: остальные тексты тоже обязаны
// влезать в алерт, иначе Telegram отвергнет ответ целиком.
func TestRevokeErrorAlert_StaysWithinAlertLimit(t *testing.T) {
	for _, err := range []error{
		errRevokeCooldown, errRevokeUnknown, errRevokeUserNotFound,
		errRevokeUnavailable, errRevokeDevicesFailed,
	} {
		assert.LessOrEqual(t, len([]rune(revokeErrorAlert(err))), 200, "ошибка %v", err)
	}
}
