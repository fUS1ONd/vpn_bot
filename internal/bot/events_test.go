package bot

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/funnels"
	"github.com/fus1ond/vpn_bot/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// fakeRecorder — журнал Событий в памяти.
type fakeRecorder struct {
	mu     sync.Mutex
	events []journal.Event
}

func (r *fakeRecorder) Record(event journal.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *fakeRecorder) recorded() []journal.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]journal.Event(nil), r.events...)
}

// Нажатие reply-кнопки «Приглашения» — Действие со стабильным id, от подписи
// не зависящее; время — момент нажатия, а не конец обработки.
func TestEventsMiddleware_RecordsInvitesTap(t *testing.T) {
	recorder := &fakeRecorder{}
	b := &Bot{events: recorder}

	before := time.Now()
	handler := b.eventsMiddleware(func(tele.Context) error {
		time.Sleep(10 * time.Millisecond)
		return nil
	})
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnInvites}}
	require.NoError(t, handler(ctx))

	events := recorder.recorded()
	require.Len(t, events, 1)
	assert.Equal(t, int64(42), events[0].TelegramID)
	assert.Equal(t, funnels.ActionInvitesOpen, events[0].Action)
	assert.Equal(t, journal.SourceUser, events[0].Source)
	assert.Empty(t, events[0].Param)
	assert.WithinDuration(t, before, events[0].At, 5*time.Millisecond, "время — момент нажатия")
}

// Событие пишется и тогда, когда обработчик вернул ошибку, а ошибка доходит
// до вызывающего.
func TestEventsMiddleware_RecordsOnHandlerError(t *testing.T) {
	recorder := &fakeRecorder{}
	b := &Bot{events: recorder}
	boom := errors.New("boom")

	handler := b.eventsMiddleware(func(tele.Context) error { return boom })
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnInvites}}

	assert.ErrorIs(t, handler(ctx), boom)
	require.Len(t, recorder.recorded(), 1)
}

// Событие пишется после обработчика.
func TestEventsMiddleware_RecordsAfterHandler(t *testing.T) {
	recorder := &fakeRecorder{}
	b := &Bot{events: recorder}

	var seenDuringHandler int
	handler := b.eventsMiddleware(func(tele.Context) error {
		seenDuringHandler = len(recorder.recorded())
		return nil
	})
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnInvites}}
	require.NoError(t, handler(ctx))

	assert.Zero(t, seenDuringHandler)
	assert.Len(t, recorder.recorded(), 1)
}

// Набранный человеком текст, совпадающий с подписью лишь частично, — не
// нажатие «Приглашений».
func TestEventsMiddleware_TypedTextIsNotInvitesTap(t *testing.T) {
	recorder := &fakeRecorder{}
	b := &Bot{events: recorder}

	handler := b.eventsMiddleware(func(tele.Context) error { return nil })
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnInvites + " не открываются"}}
	require.NoError(t, handler(ctx))

	for _, event := range recorder.recorded() {
		assert.NotEqual(t, funnels.ActionInvitesOpen, event.Action)
	}
}

// Без журнала бот работает как раньше.
func TestEventsMiddleware_WithoutJournal(t *testing.T) {
	b := &Bot{}
	called := false
	handler := b.eventsMiddleware(func(tele.Context) error {
		called = true
		return nil
	})
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnInvites}}

	assert.NotPanics(t, func() { require.NoError(t, handler(ctx)) })
	assert.True(t, called)
}
