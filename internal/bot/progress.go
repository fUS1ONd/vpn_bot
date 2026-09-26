package bot

import (
	"log/slog"
	"sync"
	"time"

	tele "gopkg.in/telebot.v3"
)

// Индикация ожидания: пользователь не должен сидеть перед тишиной, пока бот
// ходит в сеть. Инструментов три, по месту:
//
//   - callback-сторож — «часики» на inline-кнопке гасятся не позже
//     callbackAnswerDeadline, даже если обработчик завис на панели или кассе;
//   - ChatAction typing — перед операциями с reply-кнопки, где ждать секунды;
//   - промежуточное сообщение «⏳ …» — там, где ожидание тянется десятками
//     секунд (создание платежа, регистрация); оно заменяется итогом или
//     удаляется и в истории не остаётся.

// callbackAnswerDeadline — сколько обработчик может держать «часики» на кнопке.
// Быстрые обработчики успевают ответить сами, со своим alert или toast. Дольше
// держать нельзя: Telegram через несколько секунд показывает пользователю
// ошибку таймаута, а поздний ответ отвергает.
const callbackAnswerDeadline = 3 * time.Second

// callbackGuard — tele.Context с гарантированным ответом на callback. Ответ
// один на нажатие, поэтому сторож и обработчик делят его под мьютексом: кто
// первый, тот и ответил.
type callbackGuard struct {
	tele.Context

	mu       sync.Mutex
	answered bool
	late     bool // ответил сторож: обработчик опоздал
}

func (g *callbackGuard) Respond(resp ...*tele.CallbackResponse) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.answered {
		g.answered = true
		return g.Context.Respond(resp...)
	}
	if !g.late || len(resp) == 0 || resp[0] == nil || !resp[0].ShowAlert || resp[0].Text == "" {
		// Toast после позднего ответа не нужен: итог уже виден на экране.
		return nil
	}
	// Alert несёт ошибку, и потерять её нельзя: показать её всплывающим окном
	// уже поздно, поэтому она приходит сообщением.
	return g.Context.Send(resp[0].Text)
}

// RespondText и RespondAlert переопределены явно: версии встроенного контекста
// ушли бы мимо мьютекса прямо в Bot API.
func (g *callbackGuard) RespondText(text string) error {
	return g.Respond(&tele.CallbackResponse{Text: text})
}

func (g *callbackGuard) RespondAlert(text string) error {
	return g.Respond(&tele.CallbackResponse{Text: text, ShowAlert: true})
}

// answerLate — ответ сторожа по истечении срока или после обработчика, который
// так и не ответил.
func (g *callbackGuard) answerLate() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.answered {
		return
	}
	g.answered = true
	g.late = true
	if err := g.Context.Respond(); err != nil {
		slog.Warn("Failed to answer callback on deadline", "error", err, "telegram_id", g.Sender().ID)
	}
}

// callbackDeadlineMiddleware гасит «часики» за обработчика, если тот не ответил
// вовремя или не ответил вовсе. Точечный ранний Respond в каждом обработчике
// лишил бы их alert-ответов об ошибках, поэтому срок держит один сторож.
func (b *Bot) callbackDeadlineMiddleware(next tele.HandlerFunc) tele.HandlerFunc {
	return func(c tele.Context) error {
		if c.Callback() == nil {
			return next(c)
		}
		deadline := b.callbackAnswerDeadline
		if deadline <= 0 {
			deadline = callbackAnswerDeadline
		}
		guard := &callbackGuard{Context: c}
		timer := time.AfterFunc(deadline, guard.answerLate)
		err := next(guard)
		timer.Stop()
		guard.answerLate()
		return err
	}
}

// showTyping показывает «печатает…» на время операции с reply-кнопки. Статус
// живёт до пяти секунд или до первого сообщения бота. Best-effort: без него
// пользователь просто подождёт.
func (b *Bot) showTyping(c tele.Context) {
	if b.notifyTyping == nil {
		return
	}
	if err := b.notifyTyping(c); err != nil {
		slog.Warn("Failed to send typing action", "error", err, "telegram_id", c.Sender().ID)
	}
}

// showProgress присылает промежуточное сообщение и возвращает его id, чтобы
// потом убрать. Шов отправки общий с карточкой: обоим нужен message_id.
func (b *Bot) showProgress(c tele.Context, text string) (int, bool) {
	if b.sendCardMessage == nil {
		return 0, false
	}
	id, err := b.sendCardMessage(c, text, nil)
	if err != nil {
		slog.Warn("Failed to send progress message", "error", err, "telegram_id", c.Sender().ID)
		return 0, false
	}
	return id, true
}

// dropProgress убирает промежуточное сообщение. Не удалилось — останется одна
// строка «⏳ …» над итогом, флоу из-за этого не ломается.
func (b *Bot) dropProgress(c tele.Context, messageID int, ok bool) {
	if !ok || b.deleteCardMessage == nil {
		return
	}
	if err := b.deleteCardMessage(c, messageID); err != nil {
		slog.Warn("Failed to delete progress message", "error", err, "telegram_id", c.Sender().ID)
	}
}
