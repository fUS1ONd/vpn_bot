package callback

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// ipRateLimiter — per-IP rate limiter на основе token bucket.
type ipRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rate    float64 // токенов в секунду
	burst   int     // максимальный burst
	done    chan struct{}
}

// tokenBucket — простой token bucket для одного IP.
type tokenBucket struct {
	tokens   float64
	lastTime time.Time
}

// newIPRateLimiter создаёт rate limiter с заданной скоростью (req/s) и burst.
// done — канал, при закрытии которого cleanup-горутина завершается.
func newIPRateLimiter(rate float64, burst int, done chan struct{}) *ipRateLimiter {
	rl := &ipRateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    rate,
		burst:   burst,
		done:    done,
	}
	go rl.cleanupLoop()
	return rl
}

// allow проверяет, разрешён ли запрос для данного IP.
func (rl *ipRateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	b, ok := rl.buckets[ip]
	if !ok {
		b = &tokenBucket{
			tokens:   float64(rl.burst),
			lastTime: now,
		}
		rl.buckets[ip] = b
	}

	// Пополняем токены с момента последнего запроса
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
func (rl *ipRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-rl.done:
			return
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for ip, b := range rl.buckets {
				if now.Sub(b.lastTime) > 10*time.Minute {
					delete(rl.buckets, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// rateLimitMiddleware оборачивает HTTP-обработчик проверкой rate limit по IP.
func (s *Server) rateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		if !s.limiter.allow(ip) {
			http.Error(w, "Too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// extractIP извлекает IP из запроса (без порта).
func extractIP(r *http.Request) string {
	// Используем RemoteAddr напрямую (без X-Forwarded-For — callback-сервер не за reverse proxy)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
