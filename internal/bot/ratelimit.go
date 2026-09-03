package bot

import (
	"log/slog"
	"sync"
	"time"

	tele "gopkg.in/telebot.v3"
)

// userRateLimiter — per-user rate limiter для команд бота.
// Ограничивает количество обработанных сообщений на пользователя.
type userRateLimiter struct {
	mu      sync.Mutex
	buckets map[int64]*userBucket
	rate    float64 // токенов в секунду
	burst   int     // максимальный burst
	done    chan struct{}
}

// userBucket — token bucket для одного пользователя (по telegram_id).
type userBucket struct {
	tokens   float64
	lastTime time.Time
	// warnedText — предупреждение о превышении лимита уже отправлено текстом.
	// Живёт ровно столько же, сколько бакет: cleanupLoop выбрасывает его вместе
	// с записью, и следующая серия спама снова получит одно объяснение.
	warnedText bool
}

// newUserRateLimiter создаёт rate limiter для пользователей бота.
// done — канал, при закрытии которого cleanup-горутина завершается.
func newUserRateLimiter(rate float64, burst int, done chan struct{}) *userRateLimiter {
	rl := &userRateLimiter{
		buckets: make(map[int64]*userBucket),
		rate:    rate,
		burst:   burst,
		done:    done,
	}
	go rl.cleanupLoop()
	return rl
}

// allow проверяет, разрешена ли обработка следующего сообщения для данного пользователя.
func (rl *userRateLimiter) allow(telegramID int64) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	b, ok := rl.buckets[telegramID]
	if !ok {
		b = &userBucket{
			tokens:   float64(rl.burst),
			lastTime: now,
		}
		rl.buckets[telegramID] = b
	}

	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	b.lastTime = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// claimTextWarning столбит право на единственное текстовое предупреждение о
// превышении лимита за жизнь бакета: первый вызов возвращает true, дальнейшие —
// false. Отвечать на каждый дроп нельзя: исходящие у бота лимитированы примерно
// одним сообщением в секунду на чат, а входящий поток спамеру ничего не стоит —
// мы бы усиливали флуд за свой счёт.
//
// Бакет к этому моменту всегда существует: claim зовут сразу после allow,
// вернувшего false. Если его всё же нет — предупреждать не о чем, молчим и не
// заводим запись на пустом месте.
func (rl *userRateLimiter) claimTextWarning(telegramID int64) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[telegramID]
	if !ok || b.warnedText {
		return false
	}
	b.warnedText = true
	return true
}

// cleanupLoop удаляет устаревшие записи раз в 5 минут.
func (rl *userRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-rl.done:
			return
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for id, b := range rl.buckets {
				if now.Sub(b.lastTime) > 10*time.Minute {
					delete(rl.buckets, id)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// rateLimitMiddleware отсекает спам и объясняет пользователю, что произошло.
// Лимитер защищает не Telegram (входящие никто не режет), а панель: за каждым
// нажатием стоит запрос в Remnawave, и панель одна на всех.
func (b *Bot) rateLimitMiddleware(next tele.HandlerFunc) tele.HandlerFunc {
	return func(c tele.Context) error {
		sender := c.Sender()
		if sender == nil || b.userLimiter.allow(sender.ID) {
			return next(c)
		}
		slog.Warn("Rate limit exceeded", "telegram_id", sender.ID)
		return b.reportRateLimited(c)
	}
}

// reportRateLimited сообщает о превышении лимита. Middleware не знает про экраны
// и состояния: он отвечает «слишком быстро» и выходит, ничего не открывая, не
// редактируя и не трогая userStates — превышение лимита не должно ронять
// начатый флоу.
func (b *Bot) reportRateLimited(c tele.Context) error {
	// Callback молча не дропаем никогда: без ответа телеграм-клиент крутит на
	// кнопке часики, которые не разрешаются ничем, и человек жмёт ещё чаще,
	// удлиняя блокировку. Дедупликация не нужна — всплывашка не уходит в чат и
	// не тратит квоту исходящих.
	if c.Callback() != nil {
		return c.Respond(&tele.CallbackResponse{Text: MsgRateLimitedCallback})
	}
	if c.Message() == nil {
		return nil
	}
	if !b.userLimiter.claimTextWarning(c.Sender().ID) {
		return nil
	}
	return c.Send(MsgRateLimitedText, &tele.SendOptions{ParseMode: tele.ModeHTML})
}
