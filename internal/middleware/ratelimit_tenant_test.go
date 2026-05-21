package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fips-agents/gateway-template/internal/middleware"
)

func TestTenantRateLimiter_AllowsUnderLimit(t *testing.T) {
	handler := middleware.NewTenantRateLimiter(1, 1)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("X-Tenant-ID", "acme")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("first request should succeed, got status %d", rec.Code)
	}
}

func TestTenantRateLimiter_RejectsOverBurst(t *testing.T) {
	handler := middleware.NewTenantRateLimiter(1, 1)(dummyHandler200)
	tenant := "acme"

	// Send two requests quickly with same tenant.
	req1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req1.Header.Set("X-Tenant-ID", tenant)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Errorf("first request should succeed, got status %d", rec1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req2.Header.Set("X-Tenant-ID", tenant)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second request should fail with 429, got status %d", rec2.Code)
	}

	// Verify Retry-After header is present.
	retryAfter := rec2.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Error("Retry-After header should be present on 429 response")
	}

	// Verify JSON body.
	body := rec2.Body.String()
	if !strings.Contains(body, "tenant rate limit exceeded") {
		t.Errorf("response body should mention tenant rate limit, got: %q", body)
	}
}

func TestTenantRateLimiter_PerTenantIsolation(t *testing.T) {
	handler := middleware.NewTenantRateLimiter(1, 1)(dummyHandler200)

	// Exhaust tenant "acme"'s bucket.
	reqAcme1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	reqAcme1.Header.Set("X-Tenant-ID", "acme")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, reqAcme1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("first request for acme should succeed, got status %d", rec1.Code)
	}

	reqAcme2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	reqAcme2.Header.Set("X-Tenant-ID", "acme")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, reqAcme2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request for acme should fail with 429, got status %d", rec2.Code)
	}

	// Request as tenant "beta" should succeed (different bucket).
	reqBeta := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	reqBeta.Header.Set("X-Tenant-ID", "beta")
	recBeta := httptest.NewRecorder()
	handler.ServeHTTP(recBeta, reqBeta)

	if recBeta.Code != http.StatusOK {
		t.Errorf("request for beta should succeed (independent bucket), got status %d", recBeta.Code)
	}
}

func TestTenantRateLimiter_EmptyTenantSkipsLimit(t *testing.T) {
	handler := middleware.NewTenantRateLimiter(1, 1)(dummyHandler200)

	// Send multiple requests with no X-Tenant-ID header - all should pass.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("request %d without tenant should succeed, got status %d", i+1, rec.Code)
		}
	}
}

func TestTenantRateLimiter_ExemptPaths(t *testing.T) {
	handler := middleware.NewTenantRateLimiter(1, 1)(dummyHandler200)
	tenant := "acme"

	// Exhaust tenant's bucket.
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("X-Tenant-ID", tenant)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)

	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got status %d", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)

	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request should fail with 429, got status %d", rec2.Code)
	}

	// Exempt paths should succeed despite exhausted bucket.
	exemptPaths := []string{"/healthz", "/readyz", "/.well-known/agent.json"}

	for _, path := range exemptPaths {
		t.Run(path, func(t *testing.T) {
			exemptReq := httptest.NewRequest(http.MethodGet, path, nil)
			exemptReq.Header.Set("X-Tenant-ID", tenant)
			recExempt := httptest.NewRecorder()
			handler.ServeHTTP(recExempt, exemptReq)

			if recExempt.Code != http.StatusOK {
				t.Errorf("exempt path %s should return 200, got %d", path, recExempt.Code)
			}
		})
	}
}

func TestTenantRateLimiter_ResponseFormat(t *testing.T) {
	handler := middleware.NewTenantRateLimiter(1, 1)(dummyHandler200)
	tenant := "acme"

	// Exhaust bucket.
	req1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req1.Header.Set("X-Tenant-ID", tenant)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	// Trigger rate limit.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req2.Header.Set("X-Tenant-ID", tenant)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("should be rate limited, got status %d", rec2.Code)
	}

	// Verify Content-Type.
	contentType := rec2.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type should be application/json, got %q", contentType)
	}

	// Verify body contains expected message.
	body := rec2.Body.String()
	if !strings.Contains(body, "tenant rate limit exceeded") {
		t.Errorf("response body should mention tenant rate limit, got: %q", body)
	}

	// Verify Retry-After header is present.
	retryAfter := rec2.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Error("Retry-After header should be present")
	}
}
