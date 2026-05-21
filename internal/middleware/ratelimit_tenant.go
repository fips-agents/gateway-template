package middleware

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type tenantLimiter struct {
	rps      rate.Limit
	burst    int
	mu       sync.Mutex
	limiters map[string]*entry
}

func NewTenantRateLimiter(rps int, burst int) func(http.Handler) http.Handler {
	tl := &tenantLimiter{
		rps:      rate.Limit(rps),
		burst:    burst,
		limiters: make(map[string]*entry),
	}

	go tl.cleanup()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/.well-known/agent.json" {
				next.ServeHTTP(w, r)
				return
			}

			tenant := r.Header.Get("X-Tenant-ID")
			if tenant == "" {
				next.ServeHTTP(w, r)
				return
			}

			limiter := tl.getLimiter(tenant)

			if !limiter.Allow() {
				res := limiter.Reserve()
				delay := res.Delay()
				res.Cancel()

				retryAfter := int(math.Ceil(delay.Seconds()))
				if retryAfter < 1 {
					retryAfter = 1
				}

				slog.Info("tenant rate limit exceeded", "tenant_id", tenant, "retry_after", retryAfter)

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"tenant rate limit exceeded"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (tl *tenantLimiter) getLimiter(tenant string) *rate.Limiter {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	e, exists := tl.limiters[tenant]
	if !exists {
		e = &entry{
			limiter: rate.NewLimiter(tl.rps, tl.burst),
		}
		tl.limiters[tenant] = e
	}
	e.lastSeen = time.Now()

	return e.limiter
}

func (tl *tenantLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		tl.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for tenant, e := range tl.limiters {
			if e.lastSeen.Before(cutoff) {
				delete(tl.limiters, tenant)
			}
		}
		tl.mu.Unlock()
	}
}
