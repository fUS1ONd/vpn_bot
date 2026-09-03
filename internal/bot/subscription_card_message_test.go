package bot

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// fakeCardTransport подменяет отправку и удаление карточки, чтобы проверять
// наблюдаемое поведение — что осталось в чате, — а не порядок вызовов.
type fakeCardTransport struct {
	nextID    int
	sent      []int // id отправленных карточек
	deleted   []int // id удалённых сообщений
	sendErr   error
	deleteErr error
}

func newCardTestBot(t *testing.T) (*Bot, *fakeCardTransport) {
	t.Helper()
	b := &Bot{userStates: newStateMap()}
	f := &fakeCardTransport{}
	b.deleteCardMessage = func(_ tele.Context, id int) error {
		if f.deleteErr != nil {
			return f.deleteErr
		}
		f.deleted = append(f.deleted, id)
		return nil
	}
	b.sendCardMessage = func(tele.Context, string, *tele.ReplyMarkup) (int, error) {
		if f.sendErr != nil {
			return 0, f.sendErr
		}
		f.nextID++
		f.sent = append(f.sent, f.nextID)
		return f.nextID, nil
	}
	return b, f
}

// TestReplaceCard_RemovesPrevious: повторное открытие карточки с reply-кнопки не
// плодит сообщений — предыдущая карточка убирается из чата.
func TestReplaceCard_RemovesPrevious(t *testing.T) {
	b, f := newCardTestBot(t)
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{}}

	require.NoError(t, b.replaceCard(ctx, "карточка 1", nil))
	require.NoError(t, b.replaceCard(ctx, "карточка 2", nil))

	assert.Equal(t, []int{1, 2}, f.sent, "каждое нажатие приносит карточку вниз чата")
	assert.Equal(t, []int{1}, f.deleted, "первая карточка должна быть убрана")

	id, ok := b.subCards.get(42)
	assert.True(t, ok)
	assert.Equal(t, 2, id, "запомнена последняя карточка")
}

// TestReplaceCard_FirstCallDeletesNothing: первой карточке нечего заменять.
func TestReplaceCard_FirstCallDeletesNothing(t *testing.T) {
	b, f := newCardTestBot(t)
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{}}

	require.NoError(t, b.replaceCard(ctx, "карточка", nil))

	assert.Empty(t, f.deleted)
}

// TestReplaceCard_KeepsOldWhenSendFails: если новая карточка не ушла, старую не
// трогаем — у пользователя должно остаться хоть что-то, а не пустота.
func TestReplaceCard_KeepsOldWhenSendFails(t *testing.T) {
	b, f := newCardTestBot(t)
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{}}
	require.NoError(t, b.replaceCard(ctx, "карточка", nil))

	f.sendErr = errors.New("telegram упал")
	assert.Error(t, b.replaceCard(ctx, "карточка 2", nil))

	assert.Empty(t, f.deleted, "старая карточка остаётся, раз новой нет")
	id, ok := b.subCards.get(42)
	assert.True(t, ok)
	assert.Equal(t, 1, id, "запомненной остаётся прежняя карточка")
}

// TestReplaceCard_SurvivesDeleteFailure: удалять свои сообщения Telegram даёт
// только 48 часов. Не смогли — показываем новую и забываем старую, а не падаем.
func TestReplaceCard_SurvivesDeleteFailure(t *testing.T) {
	b, f := newCardTestBot(t)
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{}}
	require.NoError(t, b.replaceCard(ctx, "карточка", nil))

	f.deleteErr = errors.New("message can't be deleted")
	require.NoError(t, b.replaceCard(ctx, "карточка 2", nil), "протухшее удаление не ошибка флоу")

	id, ok := b.subCards.get(42)
	assert.True(t, ok)
	assert.Equal(t, 2, id)
}

// TestReplaceCard_PerUser: карточки разных пользователей не путаются.
func TestReplaceCard_PerUser(t *testing.T) {
	b, _ := newCardTestBot(t)
	first := &MockContext{sender: &tele.User{ID: 1}, message: &tele.Message{}}
	second := &MockContext{sender: &tele.User{ID: 2}, message: &tele.Message{}}

	require.NoError(t, b.replaceCard(first, "a", nil))
	require.NoError(t, b.replaceCard(second, "b", nil))

	id1, _ := b.subCards.get(1)
	id2, _ := b.subCards.get(2)
	assert.Equal(t, 1, id1)
	assert.Equal(t, 2, id2)
}

// TestEditCardInPlace_RemembersMessage: вход с inline-кнопки редактирует
// сообщение под пальцем и запоминает его как текущую карточку.
func TestEditCardInPlace_RemembersMessage(t *testing.T) {
	b, f := newCardTestBot(t)
	ctx := &MockContext{
		sender:   &tele.User{ID: 42},
		callback: &tele.Callback{},
		message:  &tele.Message{ID: 777},
	}

	require.NoError(t, b.editCardInPlace(ctx, "карточка", nil))

	assert.Equal(t, "карточка", ctx.editedMsg, "карточка рисуется на месте")
	assert.Empty(t, f.sent, "новых сообщений inline-вход не плодит")
	id, ok := b.subCards.get(42)
	assert.True(t, ok)
	assert.Equal(t, 777, id)
}

// TestEditCardInPlace_SameContentIsNotAFailure: повторное нажатие «🔙 Назад»
// перерисовывает карточку тем же текстом. Telegram отвечает ошибкой, но это не
// повод слать дубль — на экране уже ровно то, что нужно.
func TestEditCardInPlace_SameContentIsNotAFailure(t *testing.T) {
	b, f := newCardTestBot(t)
	ctx := &MockContext{
		sender:   &tele.User{ID: 42},
		callback: &tele.Callback{},
		message:  &tele.Message{ID: 777},
		editErr:  tele.ErrSameMessageContent,
	}

	require.NoError(t, b.editCardInPlace(ctx, "карточка", nil))

	assert.Empty(t, f.sent, "дубль карточки слать нельзя")
	id, ok := b.subCards.get(42)
	assert.True(t, ok)
	assert.Equal(t, 777, id, "карточка осталась тем же сообщением")
}

// TestEditCardInPlace_FallsBackToNewCard: сообщение устарело и не редактируется —
// присылаем новую карточку и запоминаем уже её.
func TestEditCardInPlace_FallsBackToNewCard(t *testing.T) {
	b, f := newCardTestBot(t)
	ctx := &MockContext{
		sender:   &tele.User{ID: 42},
		callback: &tele.Callback{},
		message:  &tele.Message{ID: 777},
		editErr:  errors.New("message to edit not found"),
	}

	require.NoError(t, b.editCardInPlace(ctx, "карточка", nil))

	assert.Equal(t, []int{1}, f.sent, "карточку пользователь должен увидеть в любом случае")
	id, ok := b.subCards.get(42)
	assert.True(t, ok)
	assert.Equal(t, 1, id, "текущей становится новая карточка")
}

// TestCardTracker_ForgetsUnknownID: без известного id карточку просто шлём
// заново — это обычная ветка, а не аварийная.
func TestCardTracker_ForgetsUnknownID(t *testing.T) {
	var tr cardTracker
	_, ok := tr.get(42)
	assert.False(t, ok)

	tr.set(42, 5)
	tr.forget(42)
	_, ok = tr.get(42)
	assert.False(t, ok)
}
