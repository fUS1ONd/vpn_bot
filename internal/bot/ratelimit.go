package bot

import (
	"sync"
	"time"
)

// userRateLimiter — per-user rate limiter для команд бота.
// Ограничивает количество обработанных сообщений на пользователя.
type userRateLimiter struct {
	mu      sync.Mutex
	buckets map[int64]*userBucket
	rate    float64 // токенов в секунду
	burst   int     // максимальный burst
}

// userBucket — token bucket для одного пользователя (по telegram_id).
type userBucket struct {
	tokens   float64
	lastTime time.Time
}

// newUserRateLimiter создаёт rate limiter для пользователей бота.
func newUserRateLimiter(rate float64, burst int) *userRateLimiter {
	rl := &userRateLimiter{
		buckets: make(map[int64]*userBucket),
		rate:    rate,
		burst:   burst,
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

// cleanupLoop удаляет устаревшие записи раз в 5 минут.
func (rl *userRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
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
