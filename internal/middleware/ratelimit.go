package middleware

import (
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type ipLimiter struct {
	rps      rate.Limit
	burst    int
	mu       sync.Mutex
	limiters map[string]*entry
}

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func NewRateLimiter(rps int, burst int) func(http.Handler) http.Handler {
	il := &ipLimiter{
		rps:      rate.Limit(rps),
		burst:    burst,
		limiters: make(map[string]*entry),
	}

	go il.cleanup()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/.well-known/agent.json" {
				next.ServeHTTP(w, r)
				return
			}

			ip := clientIP(r)
			limiter := il.getLimiter(ip)

			if !limiter.Allow() {
				res := limiter.Reserve()
				delay := res.Delay()
				res.Cancel()

				retryAfter := int(math.Ceil(delay.Seconds()))
				if retryAfter < 1 {
					retryAfter = 1
				}

				slog.Info("rate limit exceeded", "client_ip", ip, "retry_after", retryAfter)

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"rate limit exceeded"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (il *ipLimiter) getLimiter(ip string) *rate.Limiter {
	il.mu.Lock()
	defer il.mu.Unlock()

	e, exists := il.limiters[ip]
	if !exists {
		e = &entry{
			limiter: rate.NewLimiter(il.rps, il.burst),
		}
		il.limiters[ip] = e
	}
	e.lastSeen = time.Now()

	return e.limiter
}

func (il *ipLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		il.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for ip, e := range il.limiters {
			if e.lastSeen.Before(cutoff) {
				delete(il.limiters, ip)
			}
		}
		il.mu.Unlock()
	}
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}

	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
