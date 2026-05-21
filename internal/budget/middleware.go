package budget

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// EnforceMiddleware rejects POST /v1/chat/completions requests from tenants
// that have exceeded their token budget. Other paths and methods pass through
// unconditionally — only chat completions consume tokens.
//
// When a tenant is over budget the middleware returns 402 Payment Required
// with a JSON body describing the tenant, current usage, and budget limit.
// Requests with no tenant (X-Tenant-ID is empty) or unlimited budget
// (budget == 0) are never rejected.
func EnforceMiddleware(store *Store, cfg *Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
				next.ServeHTTP(w, r)
				return
			}

			tenant := r.Header.Get("X-Tenant-ID")
			if tenant == "" {
				next.ServeHTTP(w, r)
				return
			}

			if err := store.CheckBudget(tenant, cfg); err != nil {
				if errors.Is(err, ErrBudgetExceeded) {
					used := store.Usage(tenant)
					limit := cfg.BudgetFor(tenant)
					slog.Info("budget enforcement: rejecting request",
						"tenant_id", tenant,
						"usage", used,
						"budget", limit,
					)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusPaymentRequired)
					fmt.Fprintf(w, `{"error":"tenant budget exceeded","tenant_id":%q,"usage":%d,"budget":%d}`,
						tenant, used, limit)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
