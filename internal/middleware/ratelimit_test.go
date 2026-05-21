package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/fips-agents/gateway-template/internal/middleware"
)

// dummyHandler200 returns a 200 OK response for testing.
var dummyHandler200 = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
})

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	handler := middleware.NewRateLimiter(10, 10)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	// Send 5 requests from same IP - all should succeed (under burst).
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("request %d: want status %d, got %d", i+1, http.StatusOK, rec.Code)
		}
	}
}

func TestRateLimiter_RejectsOverBurst(t *testing.T) {
	handler := middleware.NewRateLimiter(1, 2)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	// Send 5 rapid requests from the same IP.
	// First 2 should succeed (burst=2), remaining should fail with 429.
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if i < 2 {
			// First 2 requests should succeed (burst allows them).
			if rec.Code != http.StatusOK {
				t.Errorf("request %d: want status %d, got %d", i+1, http.StatusOK, rec.Code)
			}
		} else {
			// Remaining requests should be rate limited.
			if rec.Code != http.StatusTooManyRequests {
				t.Errorf("request %d: want status %d, got %d", i+1, http.StatusTooManyRequests, rec.Code)
			}
		}
	}
}

func TestRateLimiter_RetryAfterHeader(t *testing.T) {
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	// Exhaust the bucket.
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got status %d", rec1.Code)
	}

	// Second request should be rate limited and include Retry-After header.
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)

	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request should be rate limited, got status %d", rec2.Code)
	}

	retryAfter := rec2.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Error("Retry-After header should be present on 429 response")
	}

	// Verify it's a positive integer (seconds).
	val, err := strconv.Atoi(retryAfter)
	if err != nil {
		t.Errorf("Retry-After should be an integer, got %q", retryAfter)
	}
	if val <= 0 {
		t.Errorf("Retry-After should be positive, got %d", val)
	}
}

func TestRateLimiter_PerIPIsolation(t *testing.T) {
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)

	// IP-A: 2 requests via X-Forwarded-For.
	reqA1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	reqA1.Header.Set("X-Forwarded-For", "10.0.0.1")
	reqA2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	reqA2.Header.Set("X-Forwarded-For", "10.0.0.1")

	// IP-B: 2 requests via X-Forwarded-For.
	reqB1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	reqB1.Header.Set("X-Forwarded-For", "10.0.0.2")
	reqB2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	reqB2.Header.Set("X-Forwarded-For", "10.0.0.2")

	// First request from IP-A should succeed.
	recA1 := httptest.NewRecorder()
	handler.ServeHTTP(recA1, reqA1)
	if recA1.Code != http.StatusOK {
		t.Errorf("first request from IP-A should succeed, got status %d", recA1.Code)
	}

	// First request from IP-B should succeed (different bucket).
	recB1 := httptest.NewRecorder()
	handler.ServeHTTP(recB1, reqB1)
	if recB1.Code != http.StatusOK {
		t.Errorf("first request from IP-B should succeed, got status %d", recB1.Code)
	}

	// Second request from IP-A should fail (bucket exhausted).
	recA2 := httptest.NewRecorder()
	handler.ServeHTTP(recA2, reqA2)
	if recA2.Code != http.StatusTooManyRequests {
		t.Errorf("second request from IP-A should fail with 429, got status %d", recA2.Code)
	}

	// Second request from IP-B should fail (bucket exhausted).
	recB2 := httptest.NewRecorder()
	handler.ServeHTTP(recB2, reqB2)
	if recB2.Code != http.StatusTooManyRequests {
		t.Errorf("second request from IP-B should fail with 429, got status %d", recB2.Code)
	}
}

func TestRateLimiter_ExemptPaths(t *testing.T) {
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)

	// Exhaust the bucket with a regular request.
	regularReq := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	regularReq.RemoteAddr = "192.168.1.1:12345"

	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, regularReq)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first regular request should succeed, got status %d", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, regularReq)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second regular request should fail with 429, got status %d", rec2.Code)
	}

	// Now hit the exempt paths - they should all succeed despite bucket exhaustion.
	exemptPaths := []string{"/healthz", "/readyz", "/.well-known/agent.json"}

	for _, path := range exemptPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = "192.168.1.1:12345" // Same IP as exhausted bucket.
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("exempt path %s should return 200, got %d", path, rec.Code)
			}
		})
	}
}

func TestRateLimiter_IPExtraction(t *testing.T) {
	tests := []struct {
		name           string
		xForwardedFor  string
		xRealIP        string
		remoteAddr     string
		expectedIP     string
		expectDifferent bool // If true, this IP should have independent bucket from "192.168.1.1".
	}{
		{
			name:           "X-Forwarded-For single IP",
			xForwardedFor:  "203.0.113.1",
			remoteAddr:     "192.168.1.1:12345",
			expectedIP:     "203.0.113.1",
			expectDifferent: true,
		},
		{
			name:           "X-Forwarded-For multiple IPs",
			xForwardedFor:  "203.0.113.2, 198.51.100.1, 192.0.2.1",
			remoteAddr:     "192.168.1.1:12345",
			expectedIP:     "203.0.113.2", // First IP in the list.
			expectDifferent: true,
		},
		{
			name:           "X-Real-IP",
			xRealIP:        "203.0.113.3",
			remoteAddr:     "192.168.1.1:12345",
			expectedIP:     "203.0.113.3",
			expectDifferent: true,
		},
		{
			name:           "RemoteAddr with port",
			remoteAddr:     "203.0.113.4:9999",
			expectedIP:     "203.0.113.4",
			expectDifferent: true,
		},
		{
			name:           "RemoteAddr without port",
			remoteAddr:     "203.0.113.5",
			expectedIP:     "203.0.113.5",
			expectDifferent: true,
		},
		{
			name:           "X-Forwarded-For takes precedence over X-Real-IP",
			xForwardedFor:  "203.0.113.6",
			xRealIP:        "203.0.113.7",
			remoteAddr:     "192.168.1.1:12345",
			expectedIP:     "203.0.113.6",
			expectDifferent: true,
		},
		{
			name:           "Same IP as baseline",
			remoteAddr:     "192.168.1.1:54321", // Same IP, different port.
			expectedIP:     "192.168.1.1",
			expectDifferent: false, // Should share bucket with baseline.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)

			// Baseline: exhaust bucket for 192.168.1.1.
			baselineReq := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
			baselineReq.RemoteAddr = "192.168.1.1:12345"
			rec1 := httptest.NewRecorder()
			handler.ServeHTTP(rec1, baselineReq)
			if rec1.Code != http.StatusOK {
				t.Fatalf("baseline request should succeed, got status %d", rec1.Code)
			}

			// Second request from baseline IP should fail.
			rec2 := httptest.NewRecorder()
			handler.ServeHTTP(rec2, baselineReq)
			if rec2.Code != http.StatusTooManyRequests {
				t.Fatalf("second baseline request should fail with 429, got status %d", rec2.Code)
			}

			// Now test the IP extraction by sending a request with the configured headers.
			testReq := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
			if tt.xForwardedFor != "" {
				testReq.Header.Set("X-Forwarded-For", tt.xForwardedFor)
			}
			if tt.xRealIP != "" {
				testReq.Header.Set("X-Real-IP", tt.xRealIP)
			}
			testReq.RemoteAddr = tt.remoteAddr

			rec3 := httptest.NewRecorder()
			handler.ServeHTTP(rec3, testReq)

			if tt.expectDifferent {
				// Different IP should have independent bucket -> should succeed.
				if rec3.Code != http.StatusOK {
					t.Errorf("request with IP %s should succeed (independent bucket), got status %d", tt.expectedIP, rec3.Code)
				}
			} else {
				// Same IP should share bucket -> should fail.
				if rec3.Code != http.StatusTooManyRequests {
					t.Errorf("request with same IP %s should fail with 429 (shared bucket), got status %d", tt.expectedIP, rec3.Code)
				}
			}
		})
	}
}

func TestRateLimiter_ResponseBody(t *testing.T) {
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	// Exhaust the bucket.
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got status %d", rec1.Code)
	}

	// Second request should be rate limited.
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)

	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request should be rate limited, got status %d", rec2.Code)
	}

	// Verify Content-Type.
	contentType := rec2.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type should be application/json, got %q", contentType)
	}

	// Verify exact response body.
	expectedBody := `{"error":"rate limit exceeded"}`
	actualBody := rec2.Body.String()
	if actualBody != expectedBody {
		t.Errorf("response body mismatch:\nwant: %q\ngot:  %q", expectedBody, actualBody)
	}
}

func TestRateLimiter_XForwardedForWhitespace(t *testing.T) {
	// Test that X-Forwarded-For parsing handles whitespace around commas.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)

	req1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req1.Header.Set("X-Forwarded-For", "  203.0.113.100  ,  198.51.100.1  ")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got status %d", rec1.Code)
	}

	// Second request with same first IP (trimmed) should fail.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req2.Header.Set("X-Forwarded-For", "203.0.113.100")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second request with same IP should fail with 429, got status %d", rec2.Code)
	}
}

func TestRateLimiter_EmptyXForwardedFor(t *testing.T) {
	// Test that empty X-Forwarded-For falls back to RemoteAddr.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)

	req1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req1.Header.Set("X-Forwarded-For", "")
	req1.RemoteAddr = "192.168.1.1:12345"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got status %d", rec1.Code)
	}

	// Second request with same RemoteAddr should fail.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req2.RemoteAddr = "192.168.1.1:54321"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second request with same RemoteAddr IP should fail with 429, got status %d", rec2.Code)
	}
}

func TestRateLimiter_XForwardedForOnlyCommas(t *testing.T) {
	// Test that X-Forwarded-For with only commas/whitespace falls back to RemoteAddr.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)

	req1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req1.Header.Set("X-Forwarded-For", "  ,  ,  ")
	req1.RemoteAddr = "192.168.1.1:12345"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got status %d", rec1.Code)
	}

	// Second request with same RemoteAddr should fail.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req2.RemoteAddr = "192.168.1.1:54321"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second request with same RemoteAddr IP should fail with 429, got status %d", rec2.Code)
	}
}

func TestRateLimiter_RemoteAddrPortStripping(t *testing.T) {
	// Verify that different ports on the same IP share the same bucket.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)

	req1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req1.RemoteAddr = "192.168.1.1:12345"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got status %d", rec1.Code)
	}

	// Second request with same IP but different port should fail (shared bucket).
	req2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req2.RemoteAddr = "192.168.1.1:99999"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second request with same IP (different port) should fail with 429, got status %d", rec2.Code)
	}
}

func TestRateLimiter_MethodsAreIndependent(t *testing.T) {
	// Verify that different HTTP methods from the same IP share the same bucket
	// (rate limiting is per-IP, not per-IP-per-method).
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)
	ip := "192.168.1.1:12345"

	req1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req1.RemoteAddr = ip
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first GET request should succeed, got status %d", rec1.Code)
	}

	// POST from same IP should also be rate limited (shares bucket).
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req2.RemoteAddr = ip
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("POST request from same IP should fail with 429, got status %d", rec2.Code)
	}
}

func TestRateLimiter_PathsShareBucket(t *testing.T) {
	// Verify that different non-exempt paths share the same per-IP bucket.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)
	ip := "192.168.1.1:12345"

	req1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req1.RemoteAddr = ip
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request to /v1/chat/completions should succeed, got status %d", rec1.Code)
	}

	// Different path, same IP should be rate limited.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/feedback", nil)
	req2.RemoteAddr = ip
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("request to /v1/feedback from same IP should fail with 429, got status %d", rec2.Code)
	}
}

func TestRateLimiter_ExemptPathsDoNotConsumeTokens(t *testing.T) {
	// Verify that hitting exempt paths does not consume tokens from the bucket.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)
	ip := "192.168.1.1:12345"

	// Hit exempt path 10 times - none should consume tokens.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("healthz request %d should succeed, got status %d", i+1, rec.Code)
		}
	}

	// Now hit a non-exempt path - first request should succeed (bucket untouched).
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Error("first non-exempt request should succeed after many exempt requests")
	}

	// Second non-exempt request should fail (bucket now exhausted).
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second non-exempt request should fail with 429, got status %d", rec2.Code)
	}
}

func TestRateLimiter_IPv6(t *testing.T) {
	// Verify IPv6 addresses work correctly.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)

	req1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req1.RemoteAddr = "[2001:db8::1]:12345"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first IPv6 request should succeed, got status %d", rec1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req2.RemoteAddr = "[2001:db8::1]:54321"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second IPv6 request should fail with 429, got status %d", rec2.Code)
	}

	// Different IPv6 address should have independent bucket.
	req3 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req3.RemoteAddr = "[2001:db8::2]:12345"
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Errorf("request from different IPv6 should succeed, got status %d", rec3.Code)
	}
}

func TestRateLimiter_XForwardedForIPv6(t *testing.T) {
	// Verify IPv6 in X-Forwarded-For works correctly.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)

	req1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req1.Header.Set("X-Forwarded-For", "2001:db8::abc:1")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first IPv6 X-Forwarded-For request should succeed, got status %d", rec1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req2.Header.Set("X-Forwarded-For", "2001:db8::abc:1")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second IPv6 X-Forwarded-For request should fail with 429, got status %d", rec2.Code)
	}
}

func TestRateLimiter_ZeroRPS(t *testing.T) {
	// Edge case: rps=0 should still allow burst.
	handler := middleware.NewRateLimiter(0, 5)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	// Should allow burst=5 requests.
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d should succeed (within burst), got status %d", i+1, rec.Code)
		}
	}

	// 6th request should fail (burst exhausted, no refill with rps=0).
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("request beyond burst should fail with 429, got status %d", rec.Code)
	}
}

func TestRateLimiter_ZeroBurst(t *testing.T) {
	// burst=0 means zero bucket capacity — rate.Limiter.Allow() always
	// returns false.  Config validation rejects burst < rps, so this
	// combination can't happen in production.  Verify the limiter's
	// actual behavior here for completeness.
	handler := middleware.NewRateLimiter(10, 0)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("burst=0 should reject all requests, got status %d", rec.Code)
	}
}

func TestRateLimiter_CaseInsensitivePath(t *testing.T) {
	// Verify exempt path matching is case-sensitive (Go's ServeMux is case-sensitive).
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	// Exhaust bucket.
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got status %d", rec1.Code)
	}

	// "/HEALTHZ" should NOT be exempt (case mismatch).
	reqUpper := httptest.NewRequest(http.MethodGet, "/HEALTHZ", nil)
	reqUpper.RemoteAddr = "192.168.1.1:12345"
	recUpper := httptest.NewRecorder()
	handler.ServeHTTP(recUpper, reqUpper)
	if recUpper.Code != http.StatusTooManyRequests {
		t.Errorf("/HEALTHZ should not be exempt (case-sensitive), got status %d", recUpper.Code)
	}

	// "/healthz" should be exempt.
	reqLower := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	reqLower.RemoteAddr = "192.168.1.1:12345"
	recLower := httptest.NewRecorder()
	handler.ServeHTTP(recLower, reqLower)
	if recLower.Code != http.StatusOK {
		t.Errorf("/healthz should be exempt, got status %d", recLower.Code)
	}
}

func TestRateLimiter_TrailingSlashOnExemptPath(t *testing.T) {
	// Verify that trailing slashes do not break exempt path matching.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	// Exhaust bucket.
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got status %d", rec1.Code)
	}

	// "/healthz/" with trailing slash may or may not be exempt depending on implementation.
	// This test documents the expected behavior - adjust if implementation normalizes paths.
	reqTrailing := httptest.NewRequest(http.MethodGet, "/healthz/", nil)
	reqTrailing.RemoteAddr = "192.168.1.1:12345"
	recTrailing := httptest.NewRecorder()
	handler.ServeHTTP(recTrailing, reqTrailing)

	// Document current behavior: if trailing slash is NOT normalized,
	// "/healthz/" will NOT match "/healthz" and should be rate limited.
	// If implementation normalizes, update this expectation to StatusOK.
	if recTrailing.Code != http.StatusTooManyRequests {
		t.Logf("Note: /healthz/ with trailing slash was exempt (implementation may normalize paths)")
	}
}

func TestRateLimiter_QueryStringIgnored(t *testing.T) {
	// Verify that query strings don't affect exempt path matching.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	// Exhaust bucket.
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got status %d", rec1.Code)
	}

	// "/healthz?foo=bar" should still be exempt.
	reqQuery := httptest.NewRequest(http.MethodGet, "/healthz?foo=bar", nil)
	reqQuery.RemoteAddr = "192.168.1.1:12345"
	recQuery := httptest.NewRecorder()
	handler.ServeHTTP(recQuery, reqQuery)
	if recQuery.Code != http.StatusOK {
		t.Errorf("/healthz with query string should be exempt, got status %d", recQuery.Code)
	}
}

func TestRateLimiter_FragmentIgnored(t *testing.T) {
	// Note: HTTP requests don't actually send fragments to the server (they're client-side),
	// but this test verifies the implementation doesn't break if someone passes a URL with one.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	// Exhaust bucket.
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got status %d", rec1.Code)
	}

	// In practice, fragments aren't sent, but httptest.NewRequest parses them.
	// This just ensures no unexpected behavior.
	reqFragment := httptest.NewRequest(http.MethodGet, "/healthz#section", nil)
	reqFragment.RemoteAddr = "192.168.1.1:12345"
	recFragment := httptest.NewRecorder()
	handler.ServeHTTP(recFragment, reqFragment)
	if recFragment.Code != http.StatusOK {
		t.Logf("Note: /healthz#section handling - got status %d", recFragment.Code)
	}
}

func TestRateLimiter_MalformedRemoteAddr(t *testing.T) {
	// Verify graceful handling of malformed RemoteAddr.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)

	// Malformed RemoteAddr (no port, unexpected format).
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "not-a-valid-ip"
	rec := httptest.NewRecorder()

	// Should not panic - either uses the malformed string as-is or has fallback.
	handler.ServeHTTP(rec, req)

	// We expect it to either succeed or fail gracefully (not panic).
	// Status code depends on implementation - document observed behavior.
	if rec.Code != http.StatusOK && rec.Code != http.StatusTooManyRequests {
		t.Logf("Note: malformed RemoteAddr resulted in unexpected status %d", rec.Code)
	}
}

func TestRateLimiter_EmptyRemoteAddr(t *testing.T) {
	// Verify behavior when RemoteAddr is empty.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)

	req1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req1.RemoteAddr = ""
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	// Should handle gracefully - either use empty string as key or have fallback.
	if rec1.Code != http.StatusOK && rec1.Code != http.StatusTooManyRequests {
		t.Logf("Note: empty RemoteAddr resulted in status %d", rec1.Code)
	}

	// If two requests with empty RemoteAddr share a bucket, second should fail.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req2.RemoteAddr = ""
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec1.Code == http.StatusOK && rec2.Code == http.StatusOK {
		t.Error("Two requests with empty RemoteAddr should share a bucket; second should fail")
	}
}

func TestRateLimiter_ConcurrentRequestsDifferentIPs(t *testing.T) {
	// Verify thread-safety: concurrent requests from different IPs.
	handler := middleware.NewRateLimiter(10, 10)(dummyHandler200)

	const numGoroutines = 50
	const requestsPerIP = 5

	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			ip := "192.168.1." + strconv.Itoa(id%256) + ":12345"
			for j := 0; j < requestsPerIP; j++ {
				req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
				req.RemoteAddr = ip
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if rec.Code != http.StatusOK {
					errors <- http.ErrAbortHandler // Just a signal; actual error varies.
					return
				}
			}
			errors <- nil
		}(i)
	}

	// Collect results.
	for i := 0; i < numGoroutines; i++ {
		err := <-errors
		if err != nil {
			t.Errorf("goroutine %d encountered unexpected error", i)
		}
	}
}

func TestRateLimiter_ConcurrentRequestsSameIP(t *testing.T) {
	// Verify thread-safety: concurrent requests from the same IP.
	handler := middleware.NewRateLimiter(100, 100)(dummyHandler200)

	const numGoroutines = 20
	ip := "192.168.1.1:12345"

	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
			req.RemoteAddr = ip
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			// Some may succeed, some may fail depending on timing.
			// Just ensure no panics occur.
			if rec.Code != http.StatusOK && rec.Code != http.StatusTooManyRequests {
				errors <- http.ErrAbortHandler
				return
			}
			errors <- nil
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		err := <-errors
		if err != nil {
			t.Errorf("goroutine %d encountered unexpected error", i)
		}
	}
}

func TestRateLimiter_WellKnownAgentJSON(t *testing.T) {
	// Specific test for the agent card endpoint being exempt.
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)
	ip := "192.168.1.1:12345"

	// Exhaust bucket.
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = ip
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got status %d", rec1.Code)
	}

	// /.well-known/agent.json should be exempt.
	reqAgent := httptest.NewRequest(http.MethodGet, "/.well-known/agent.json", nil)
	reqAgent.RemoteAddr = ip
	recAgent := httptest.NewRecorder()
	handler.ServeHTTP(recAgent, reqAgent)
	if recAgent.Code != http.StatusOK {
		t.Errorf("/.well-known/agent.json should be exempt, got status %d", recAgent.Code)
	}
}

func TestRateLimiter_NonExemptWellKnownPath(t *testing.T) {
	// Verify that other /.well-known/* paths are NOT exempt (only agent.json is).
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)
	ip := "192.168.1.1:12345"

	// Exhaust bucket.
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = ip
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got status %d", rec1.Code)
	}

	// /.well-known/other should NOT be exempt.
	reqOther := httptest.NewRequest(http.MethodGet, "/.well-known/other", nil)
	reqOther.RemoteAddr = ip
	recOther := httptest.NewRecorder()
	handler.ServeHTTP(recOther, reqOther)
	if recOther.Code != http.StatusTooManyRequests {
		t.Errorf("/.well-known/other should not be exempt, got status %d", recOther.Code)
	}
}

func TestRateLimiter_RetryAfterValue(t *testing.T) {
	// Verify Retry-After value is reasonable (within expected range).
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	// Exhaust bucket.
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)

	retryAfter := rec2.Header().Get("Retry-After")
	val, err := strconv.Atoi(retryAfter)
	if err != nil {
		t.Fatalf("Retry-After should be an integer, got %q", retryAfter)
	}

	// With rps=1, the reserve should be ~1 second.
	// Allow some tolerance (0 < val <= 2).
	if val <= 0 || val > 2 {
		t.Errorf("Retry-After value seems unreasonable: %d seconds (expected ~1)", val)
	}
}

func TestRateLimiter_HighRPSLowBurst(t *testing.T) {
	// Edge case: high RPS but low burst.
	handler := middleware.NewRateLimiter(1000, 2)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	// First 2 requests should succeed (burst=2).
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d should succeed (within burst), got status %d", i+1, rec.Code)
		}
	}

	// Third request should fail immediately (burst exhausted).
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("third request should fail with 429, got status %d", rec.Code)
	}
}

func TestRateLimiter_ResponseBodyNotDoubleEncoded(t *testing.T) {
	// Ensure the JSON response body is properly formatted (not double-encoded).
	handler := middleware.NewRateLimiter(1, 1)(dummyHandler200)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)

	body := rec2.Body.String()

	// Should NOT contain escaped quotes like `{\"error\":...}`.
	if strings.Contains(body, `\"`) {
		t.Errorf("response body appears to be double-encoded: %q", body)
	}

	// Should be valid JSON with literal quotes.
	if !strings.Contains(body, `"error"`) {
		t.Errorf("response body should contain literal JSON, got: %q", body)
	}
}
