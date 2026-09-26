package bot

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// TestIsReplyButtonTap_KnownCaptions: подписи reply-кнопок — навигация, а не
// содержимое переписки.
func TestIsReplyButtonTap_KnownCaptions(t *testing.T) {
	for _, caption := range []string{BtnStatus, BtnInfo, BtnBack, BtnCancel, BtnPay, BtnRenew,
		BtnInvites, BtnServers, BtnBugReport, BtnAdminManage, BtnAdminBack} {
		assert.True(t, isReplyButtonTap(caption), "подпись кнопки %q должна считаться тапом", caption)
	}
}

// TestIsReplyButtonTap_UserContent: набранное руками не трогаем никогда —
// стереть это значило бы уничтожить содержимое переписки.
func TestIsReplyButtonTap_UserContent(t *testing.T) {
	for _, text := range []string{"ABCD1234", "не подключается на телефоне", "123456789",
		"👤 Моя подписка не работает", "", "/start"} {
		assert.False(t, isReplyButtonTap(text), "текст %q удалять нельзя", text)
	}
}

// TestIsReplyButtonTap_ConfirmYesIsNotATap: «Да» — единственная подпись,
// неотличимая от обычного слова. Набранное руками «Да» это реплика.
func TestIsReplyButtonTap_ConfirmYesIsNotATap(t *testing.T) {
	assert.False(t, isReplyButtonTap(BtnConfirmYes))
}

// TestDropReplyTapMiddleware_RemovesTap: нажатие кнопки уходит из истории после
// того, как обработчик отработал.
func TestDropReplyTapMiddleware_RemovesTap(t *testing.T) {
	var deleted []int
	b := &Bot{deleteUserMessage: func(_ tele.Context, id int) error {
		deleted = append(deleted, id)
		return nil
	}}

	called := false
	handler := b.dropReplyTapMiddleware(func(tele.Context) error {
		called = true
		return nil
	})

	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnStatus}}
	require.NoError(t, handler(ctx))

	assert.True(t, called, "обработчик должен отработать")
	assert.Equal(t, []int{10}, deleted)
}

// TestDropReplyTapMiddleware_KeepsTypedText: инвайт-код и прочий набранный текст
// остаются в чате.
func TestDropReplyTapMiddleware_KeepsTypedText(t *testing.T) {
	var deleted []int
	b := &Bot{deleteUserMessage: func(_ tele.Context, id int) error {
		deleted = append(deleted, id)
		return nil
	}}

	handler := b.dropReplyTapMiddleware(func(tele.Context) error { return nil })
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: "ABCD1234"}}
	require.NoError(t, handler(ctx))

	assert.Empty(t, deleted)
}

// TestDropReplyTapMiddleware_DeletesAfterHandler: удаляем только после того, как
// обработчик отработал — сообщение-триггер не должно исчезать раньше работы.
func TestDropReplyTapMiddleware_DeletesAfterHandler(t *testing.T) {
	var order []string
	b := &Bot{deleteUserMessage: func(tele.Context, int) error {
		order = append(order, "delete")
		return nil
	}}

	handler := b.dropReplyTapMiddleware(func(tele.Context) error {
		order = append(order, "handle")
		return nil
	})

	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnStatus}}
	require.NoError(t, handler(ctx))

	assert.Equal(t, []string{"handle", "delete"}, order)
}

// TestDropReplyTapMiddleware_SurvivesDeleteFailure: чужое сообщение не наше,
// чтобы ронять из-за него флоу.
func TestDropReplyTapMiddleware_SurvivesDeleteFailure(t *testing.T) {
	b := &Bot{deleteUserMessage: func(tele.Context, int) error {
		return errors.New("message can't be deleted")
	}}

	handler := b.dropReplyTapMiddleware(func(tele.Context) error { return nil })
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnStatus}}

	assert.NoError(t, handler(ctx))
}

// TestDropReplyTapMiddleware_KeepsHandlerError: ошибка обработчика доходит до
// вызывающего, уборка чата её не проглатывает.
func TestDropReplyTapMiddleware_KeepsHandlerError(t *testing.T) {
	b := &Bot{deleteUserMessage: func(tele.Context, int) error { return nil }}
	boom := errors.New("boom")

	handler := b.dropReplyTapMiddleware(func(tele.Context) error { return boom })
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnStatus}}

	assert.ErrorIs(t, handler(ctx), boom)
}

// TestDropReplyTapMiddleware_IgnoresNonText: callback'и и медиа удалять нечего.
func TestDropReplyTapMiddleware_IgnoresNonText(t *testing.T) {
	var deleted []int
	b := &Bot{deleteUserMessage: func(_ tele.Context, id int) error {
		deleted = append(deleted, id)
		return nil
	}}

	handler := b.dropReplyTapMiddleware(func(tele.Context) error { return nil })
	ctx := &MockContext{sender: &tele.User{ID: 42}, callback: &tele.Callback{}}
	require.NoError(t, handler(ctx))

	assert.Empty(t, deleted)
}
