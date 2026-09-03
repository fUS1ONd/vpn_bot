package bot

import (
	"errors"
	"log/slog"
	"strconv"
	"sync"

	tele "gopkg.in/telebot.v3"
)

// cardTracker помнит id последней карточки «Моя подписка» на пользователя,
// чтобы следующий вызов заменил её, а не добавил в чат ещё одну.
//
// Нулевое значение готово к работе: карта создаётся при первой записи. Так
// карточка не зависит от того, собран ли Bot через New — забытая инициализация
// не должна ронять главный экран бота.
//
// Хранилище in-memory намеренно: переживать перезапуск незачем. Забыли id —
// просто отправим новую карточку. Кнопка «👤 Моя подписка» стоит в
// reply-клавиатуре безусловно, поэтому карточка всегда вызываема, а не
// находима: искать её скроллом пользователь не должен никогда.
type cardTracker struct {
	mu  sync.Mutex
	ids map[int64]int
}

func (t *cardTracker) get(telegramID int64) (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	id, ok := t.ids[telegramID]
	return id, ok
}

func (t *cardTracker) set(telegramID int64, messageID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ids == nil {
		t.ids = make(map[int64]int)
	}
	t.ids[telegramID] = messageID
}

func (t *cardTracker) forget(telegramID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.ids, telegramID)
}

// newCardSender собирает шов отправки карточки поверх Bot API. Отдельный шов
// нужен потому, что tele.Context.Send не возвращает отправленное сообщение, а
// нам нужен его id.
func newCardSender(api *tele.Bot) func(tele.Context, string, *tele.ReplyMarkup) (int, error) {
	return func(c tele.Context, msg string, markup *tele.ReplyMarkup) (int, error) {
		sent, err := api.Send(c.Recipient(), msg, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: markup,
		})
		if err != nil {
			return 0, err
		}
		return sent.ID, nil
	}
}

// newCardDeleter собирает шов удаления карточки поверх Bot API.
func newCardDeleter(api *tele.Bot) func(tele.Context, int) error {
	return func(c tele.Context, messageID int) error {
		return api.Delete(&tele.StoredMessage{
			MessageID: strconv.Itoa(messageID),
			ChatID:    c.Chat().ID,
		})
	}
}

// replaceCard показывает карточку новым сообщением внизу чата и убирает
// предыдущую. Вызывается только со стороны reply-кнопки: нажатие reply-кнопки —
// это видимое сообщение от пользователя, поэтому мы гарантированно внизу чата и
// редактировать нечего.
func (b *Bot) replaceCard(c tele.Context, msg string, markup *tele.ReplyMarkup) error {
	telegramID := c.Sender().ID
	old, hadOld := b.subCards.get(telegramID)

	// Сначала отправляем новую, потом убираем старую: если отправка сорвётся, у
	// пользователя останется прежняя карточка, а не пустой чат.
	id, err := b.sendCard(c, msg, markup)
	if err != nil {
		return err
	}
	b.rememberCard(telegramID, id)

	if hadOld {
		b.dropCard(c, old)
	}
	return nil
}

// editCardInPlace перерисовывает карточку в сообщении, из которого пришло
// нажатие: с inline-кнопки сообщение прямо под пальцем, новых между ним и
// пользователем не появилось.
func (b *Bot) editCardInPlace(c tele.Context, msg string, markup *tele.ReplyMarkup) error {
	telegramID := c.Sender().ID

	err := c.Edit(msg, &tele.SendOptions{ParseMode: tele.ModeHTML, ReplyMarkup: markup})
	// Тот же текст Telegram считает ошибкой, но на экране уже ровно то, что
	// нужно: слать дубль карточки было бы хуже, чем не делать ничего.
	if err == nil || errors.Is(err, tele.ErrSameMessageContent) {
		if m := c.Message(); m != nil {
			b.subCards.set(telegramID, m.ID)
		}
		return nil
	}

	slog.Warn("Failed to edit subscription card, sending new message",
		"error", err, "telegram_id", telegramID)

	// Сообщение не отредактировалось — считаем его потерянным и заменяем
	// карточку целиком, чтобы устаревшая не осталась в чате с живыми кнопками.
	return b.replaceCard(c, msg, markup)
}

// sendCard отправляет карточку, а при отказе Telegram повторяет отправку без
// клавиатуры: статус пользователь должен увидеть в любом случае — потеря кнопки
// терпима, потеря всего сообщения нет.
func (b *Bot) sendCard(c tele.Context, msg string, markup *tele.ReplyMarkup) (int, error) {
	if b.sendCardMessage == nil {
		// Шов не настроен: карточку всё равно показываем, id остаётся
		// неизвестным — следующий вызов просто пришлёт новую.
		return 0, sendWithInlineFallback(c, msg, markup)
	}

	id, err := b.sendCardMessage(c, msg, markup)
	if err == nil {
		return id, nil
	}

	slog.Error("Failed to send subscription card with inline keyboard, retrying without it",
		"error", err, "telegram_id", c.Sender().ID)
	return b.sendCardMessage(c, msg, nil)
}

// rememberCard запоминает карточку как текущую. Нулевой id означает, что узнать
// его не удалось: тогда прежний тоже забываем, иначе позже удалили бы чужое
// сообщение.
func (b *Bot) rememberCard(telegramID int64, messageID int) {
	if messageID == 0 {
		b.subCards.forget(telegramID)
		return
	}
	b.subCards.set(telegramID, messageID)
}

// dropCard убирает прежнюю карточку. Удалять свои сообщения Telegram разрешает
// только 48 часов — не смогли, и ладно: хуже, чем было до живой карточки, не
// станет, там старое сообщение оставалось всегда.
func (b *Bot) dropCard(c tele.Context, messageID int) {
	if b.deleteCardMessage == nil {
		return
	}
	if err := b.deleteCardMessage(c, messageID); err != nil {
		slog.Warn("Failed to delete previous subscription card",
			"error", err, "telegram_id", c.Sender().ID, "message_id", messageID)
	}
}
