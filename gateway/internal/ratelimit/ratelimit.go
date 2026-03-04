// Package ratelimit provides per-IP token bucket rate limiting.
//
// Each IP gets a bucket that refills at rate r tokens/second up to a burst
// capacity of r tokens (one full minute's worth). A background goroutine
// evicts buckets that have been idle for more than 5 minutes to prevent
// unbounded memory growth.
package ratelimit

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	cleanupInterval = 5 * time.Minute
	idleExpiry      = 5 * time.Minute
)

// bucket is a token bucket for a single IP.
type bucket struct {
	tokens   float64
	lastSeen time.Time
	mu       sync.Mutex
}

// allow tries to consume one token. Returns the wait duration if denied (0 if allowed).
func (b *bucket) allow(rate float64, burst float64) (allowed bool, retryAfter time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.lastSeen = now

	// Refill tokens based on elapsed time.
	b.tokens += elapsed * rate
	if b.tokens > burst {
		b.tokens = burst
	}

	if b.tokens >= 1.0 {
		b.tokens--
		return true, 0
	}

	// Compute how long until 1 token is available.
	wait := time.Duration((1.0-b.tokens)/rate*1000) * time.Millisecond
	return false, wait
}

// Limiter is a per-IP rate limiter backed by token buckets.
type Limiter struct {
	rate    float64 // tokens per second
	burst   float64 // max burst (= 1 minute of requests)
	buckets sync.Map
}

// New creates a Limiter. reqPerMin is the sustained rate (e.g. 60).
func New(reqPerMin int) *Limiter {
	if reqPerMin <= 0 {
		reqPerMin = 60
	}
	rate := float64(reqPerMin) / 60.0
	l := &Limiter{
		rate:  rate,
		burst: float64(reqPerMin), // allow full minute burst
	}
	go l.cleanup()
	return l
}

// Middleware returns a Gin handler that enforces the rate limit.
// Denied requests get 429 with a Retry-After header (seconds).
func (l *Limiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		b := l.getOrCreate(ip)
		allowed, retryAfter := b.allow(l.rate, l.burst)
		if !allowed {
			secs := int(retryAfter.Seconds()) + 1
			c.Header("Retry-After", strconv.Itoa(secs))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate limit exceeded",
				"retry_after": secs,
			})
			return
		}
		c.Next()
	}
}

func (l *Limiter) getOrCreate(ip string) *bucket {
	v, _ := l.buckets.LoadOrStore(ip, &bucket{
		tokens:   l.burst, // new IPs start with a full bucket
		lastSeen: time.Now(),
	})
	return v.(*bucket)
}

// cleanup periodically removes buckets that have been idle.
func (l *Limiter) cleanup() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		l.buckets.Range(func(k, v any) bool {
			b := v.(*bucket)
			b.mu.Lock()
			idle := now.Sub(b.lastSeen) > idleExpiry
			b.mu.Unlock()
			if idle {
				l.buckets.Delete(k)
			}
			return true
		})
	}
}
