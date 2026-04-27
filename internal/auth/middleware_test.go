package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fips-agents/gateway-template/internal/auth"
)

// captureHandler records the headers of the request that reaches it so
// tests can assert on what the middleware projected.
type captureHandler struct {
	got http.Header
}

func (c *captureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.got = r.Header.Clone()
	w.WriteHeader(http.StatusNoContent)
}

func TestMiddleware_AnonymousProjectsCanonicalHeaders(t *testing.T) {
	cap := &captureHandler{}
	mw := auth.Middleware(&auth.AnonymousAuth{})
	h := mw(cap)

	req := httptest.NewRequest("POST", "/v1/feedback", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", rec.Code)
	}
	if got := cap.got.Get(auth.HeaderSubject); got != "anonymous" {
		t.Errorf("X-Auth-Subject: got %q, want %q", got, "anonymous")
	}
	if got := cap.got.Get(auth.HeaderMode); got != auth.ModeAnonymous {
		t.Errorf("X-Auth-Mode: got %q, want %q", got, auth.ModeAnonymous)
	}
}

func TestMiddleware_StripsInboundSpoofedHeaders(t *testing.T) {
	cap := &captureHandler{}
	mw := auth.Middleware(&auth.AnonymousAuth{})
	h := mw(cap)

	req := httptest.NewRequest("POST", "/v1/feedback", nil)
	// Client tries to forge identity. Middleware must replace these with
	// the strategy's resolved identity.
	req.Header.Set("X-Auth-Subject", "evil-admin")
	req.Header.Set("X-Auth-User", "evil")
	req.Header.Set("X-Auth-Email", "evil@example.com")
	req.Header.Set("X-Auth-Mode", "proxy")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := cap.got.Get(auth.HeaderSubject); got != "anonymous" {
		t.Errorf("forged X-Auth-Subject leaked through: got %q", got)
	}
	if got := cap.got.Get(auth.HeaderMode); got != auth.ModeAnonymous {
		t.Errorf("forged X-Auth-Mode leaked through: got %q", got)
	}
	if got := cap.got.Get(auth.HeaderUser); got != "" {
		t.Errorf("forged X-Auth-User leaked through: got %q", got)
	}
	if got := cap.got.Get(auth.HeaderEmail); got != "" {
		t.Errorf("forged X-Auth-Email leaked through: got %q", got)
	}
}

func TestMiddleware_ProxyModeProjectsUpstreamIdentity(t *testing.T) {
	cap := &captureHandler{}
	pa := &auth.ProxyAuth{UserHeader: "X-Forwarded-User", EmailHeader: "X-Forwarded-Email"}
	h := auth.Middleware(pa)(cap)

	req := httptest.NewRequest("POST", "/v1/feedback", nil)
	req.Header.Set("X-Forwarded-User", "alice")
	req.Header.Set("X-Forwarded-Email", "alice@example.com")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := cap.got.Get(auth.HeaderSubject); got != "alice" {
		t.Errorf("X-Auth-Subject: got %q, want alice", got)
	}
	if got := cap.got.Get(auth.HeaderUser); got != "alice" {
		t.Errorf("X-Auth-User: got %q, want alice", got)
	}
	if got := cap.got.Get(auth.HeaderEmail); got != "alice@example.com" {
		t.Errorf("X-Auth-Email: got %q, want alice@example.com", got)
	}
	if got := cap.got.Get(auth.HeaderMode); got != auth.ModeProxy {
		t.Errorf("X-Auth-Mode: got %q, want proxy", got)
	}
}

func TestMiddleware_ProxyModeFailsClosedOn503(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })

	pa := &auth.ProxyAuth{UserHeader: "X-Forwarded-User"}
	h := auth.Middleware(pa)(next)

	req := httptest.NewRequest("POST", "/v1/feedback", nil)
	// No X-Forwarded-User → ErrMissingProxyHeaders.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", rec.Code)
	}
	if called {
		t.Error("downstream handler ran despite auth failure")
	}
}

// TestMiddleware_StubError ensures that any non-nil error from the
// authenticator (not just ErrMissingProxyHeaders) causes a 503.
func TestMiddleware_AnyAuthErrorReturns503(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })

	stub := stubAuth{err: errors.New("boom")}
	h := auth.Middleware(stub)(next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
	if called {
		t.Error("downstream handler ran despite auth error")
	}
}

type stubAuth struct {
	id  auth.Identity
	err error
}

func (s stubAuth) Authenticate(*http.Request) (auth.Identity, error) {
	return s.id, s.err
}
