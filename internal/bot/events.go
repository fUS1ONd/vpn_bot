package bot

import (
	"time"

	"github.com/fus1ond/vpn_bot/internal/funnels"
	"github.com/fus1ond/vpn_bot/internal/journal"
	tele "gopkg.in/telebot.v3"
)

// eventRecorder — запись Событий в журнал. Реализация (journal.Journal) не
// блокирует и не возвращает ошибок: аналитика не имеет права замедлить или
// уронить обработку нажатия.
type eventRecorder interface {
	Record(event journal.Event)
}

// recordEvent пишет Событие, если журнал подключён.
func (b *Bot) recordEvent(event journal.Event) {
	if b.events == nil {
		return
	}
	b.events.Record(event)
}

// eventsMiddleware пишет в журнал Действие пользователя.
//
// Запись идёт после обработчика и не зависит от его ошибки: нажатие было,
// чем бы оно ни кончилось. Время — момент нажатия: факты, которые обработчик
// кладёт в таблицы бота (платёж, приглашение), не должны оказаться в журнале
// раньше вызвавшего их Действия.
func (b *Bot) eventsMiddleware(next tele.HandlerFunc) tele.HandlerFunc {
	return func(c tele.Context) error {
		at := time.Now()
		err := next(c)

		if action, ok := userAction(c); ok {
			b.recordEvent(journal.Event{
				At:         at,
				TelegramID: c.Sender().ID,
				Action:     action,
				Source:     journal.SourceUser,
			})
		}
		return err
	}
}

// userAction определяет id Действия по апдейту. Содержимое сообщения в id не
// попадает никогда: reply-кнопка узнаётся по точному совпадению подписи.
func userAction(c tele.Context) (string, bool) {
	if c.Sender() == nil {
		return "", false
	}
	if msg := c.Message(); msg != nil && c.Callback() == nil {
		action, ok := replyButtonActions[msg.Text]
		return action, ok
	}
	return "", false
}

// AttachAnalytics подключает журнал Событий и модуль воронок. Вызывается до
// Run; любой из них может быть nil — тогда бот работает без записи Событий
// или без экрана воронок, а остальное не меняется. Проверка на nil здесь, а не
// у вызывающего: nil-указатель, положенный в поле-интерфейс, не равен nil.
func (b *Bot) AttachAnalytics(events *journal.Journal, reporter *funnels.Funnels) {
	if events != nil {
		b.events = events
	}
	if reporter != nil {
		b.funnels = reporter
	}
}
