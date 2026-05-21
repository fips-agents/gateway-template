package handler_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fips-agents/gateway-template/internal/handler"
)

// recordedRequest captures everything ForwardingHandler-wired tests
// commonly assert on without re-reading the http.Request after the
// upstream has consumed the body.
type recordedRequest struct {
	mu      sync.Mutex
	Method  string
	Path    string
	Query   string
	Headers http.Header
	Body    []byte
}

// newRecordingUpstream stands in for the platform service. It captures
// the inbound request and replies with the configured status + body.
func newRecordingUpstream(t *testing.T, status int, replyBody string) (*httptest.Server, *recordedRequest) {
	t.Helper()
	rec := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.Method = r.Method
		rec.Path = r.URL.Path
		rec.Query = r.URL.RawQuery
		rec.Headers = r.Header.Clone()
		rec.Body = body
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, replyBody)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestForwardingHandler_PreservesPathAndQuery(t *testing.T) {
	upstream, rec := newRecordingUpstream(t, 200, `{"ok":true}`)

	h, err := handler.NewForwardingHandler(upstream.URL)
	if err != nil {
		t.Fatalf("NewForwardingHandler: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/sessions/abc/cost_data?include=true", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if rec.Path != "/v1/sessions/abc/cost_data" {
		t.Errorf("upstream Path = %q, want /v1/sessions/abc/cost_data", rec.Path)
	}
	if rec.Query != "include=true" {
		t.Errorf("upstream Query = %q, want include=true", rec.Query)
	}
}

func TestForwardingHandler_ForwardsAuthHeaders(t *testing.T) {
	upstream, rec := newRecordingUpstream(t, 200, `{}`)
	h, err := handler.NewForwardingHandler(upstream.URL)
	if err != nil {
		t.Fatalf("NewForwardingHandler: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/feedback", strings.NewReader(`{"rating":1}`))
	req.Header.Set("Authorization", "Bearer swap-token")
	req.Header.Set("X-Auth-Subject", "u-123")
	req.Header.Set("X-Auth-User", "wes")
	req.Header.Set("X-Auth-Email", "wes@example.com")
	req.Header.Set("X-Tenant", "acme")
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	want := map[string]string{
		"Authorization":  "Bearer swap-token",
		"X-Auth-Subject": "u-123",
		"X-Auth-User":    "wes",
		"X-Auth-Email":   "wes@example.com",
		"X-Tenant":       "acme",
		"Traceparent":    "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
	}
	for k, v := range want {
		if got := rec.Headers.Get(k); got != v {
			t.Errorf("upstream header %s = %q, want %q", k, got, v)
		}
	}
}

func TestForwardingHandler_PreservesBodyAndMethod(t *testing.T) {
	upstream, rec := newRecordingUpstream(t, 202, `{"id":"fb_42"}`)
	h, err := handler.NewForwardingHandler(upstream.URL)
	if err != nil {
		t.Fatalf("NewForwardingHandler: %v", err)
	}

	body := `{"comment":"thanks","rating":1}`
	req := httptest.NewRequest("PATCH", "/v1/feedback/fb_42", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if rec.Method != "PATCH" {
		t.Errorf("upstream method = %q, want PATCH", rec.Method)
	}
	if string(rec.Body) != body {
		t.Errorf("upstream body = %q, want %q", string(rec.Body), body)
	}
	if w.Code != 202 {
		t.Errorf("status = %d, want 202 (passthrough)", w.Code)
	}
	if got := w.Body.String(); got != `{"id":"fb_42"}` {
		t.Errorf("response body = %q, want passthrough", got)
	}
}

func TestForwardingHandler_HEADRequestSupported(t *testing.T) {
	// HttpSessionStore.exists() uses HEAD. The proxy must forward it
	// without choking on the empty body.
	upstream, rec := newRecordingUpstream(t, 200, "")
	h, err := handler.NewForwardingHandler(upstream.URL)
	if err != nil {
		t.Fatalf("NewForwardingHandler: %v", err)
	}

	req := httptest.NewRequest("HEAD", "/v1/sessions/s-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if rec.Method != "HEAD" {
		t.Errorf("upstream method = %q, want HEAD", rec.Method)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestForwardingHandler_UpstreamUnreachable(t *testing.T) {
	// Build a handler against a closed upstream — connect should fail.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // close before any request
	h, err := handler.NewForwardingHandler(srv.URL)
	if err != nil {
		t.Fatalf("NewForwardingHandler: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/traces", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 BadGateway on upstream failure", w.Code)
	}
}

func TestForwardingHandler_TargetWithBasePath(t *testing.T) {
	// Some platform deployments live under a sub-path (eg /platform).
	// Inbound paths should land under the base.
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(204)
	}))
	t.Cleanup(srv.Close)

	h, err := handler.NewForwardingHandler(srv.URL + "/platform")
	if err != nil {
		t.Fatalf("NewForwardingHandler: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/sessions/s-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if gotPath != "/platform/v1/sessions/s-1" {
		t.Errorf("upstream Path = %q, want /platform prefix preserved", gotPath)
	}
}

func TestForwardingHandler_SessionForkEndpoints(t *testing.T) {
	// POST /v1/sessions/{id}/fork and GET /v1/sessions/{id}/forks are
	// conversation fork endpoints. They don't have dedicated route
	// registrations — they're handled by the /v1/sessions/ catch-all
	// in cmd/server/main.go. This test proves the ForwardingHandler
	// forwards them with path, method, body, and auth headers intact.

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "POST fork",
			method: "POST",
			path:   "/v1/sessions/sess-123/fork",
			body:   `{"title":"what-if branch"}`,
		},
		{
			name:   "GET forks",
			method: "GET",
			path:   "/v1/sessions/sess-123/forks",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream, rec := newRecordingUpstream(t, 200, `{"ok":true}`)
			h, err := handler.NewForwardingHandler(upstream.URL)
			if err != nil {
				t.Fatalf("NewForwardingHandler: %v", err)
			}

			var reqBody io.Reader
			if tc.body != "" {
				reqBody = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, reqBody)
			req.Header.Set("X-Auth-Subject", "u-456")
			req.Header.Set("X-Auth-User", "alice")
			req.Header.Set("X-Auth-Email", "alice@example.com")
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != 200 {
				t.Errorf("status = %d, want 200", w.Code)
			}
			if rec.Method != tc.method {
				t.Errorf("upstream method = %q, want %q", rec.Method, tc.method)
			}
			if rec.Path != tc.path {
				t.Errorf("upstream path = %q, want %q", rec.Path, tc.path)
			}
			if tc.body != "" && string(rec.Body) != tc.body {
				t.Errorf("upstream body = %q, want %q", string(rec.Body), tc.body)
			}

			// Auth headers must arrive at the backend.
			wantHeaders := map[string]string{
				"X-Auth-Subject": "u-456",
				"X-Auth-User":    "alice",
				"X-Auth-Email":   "alice@example.com",
			}
			for k, v := range wantHeaders {
				if got := rec.Headers.Get(k); got != v {
					t.Errorf("upstream header %s = %q, want %q", k, got, v)
				}
			}
		})
	}
}

func TestNewForwardingHandler_RejectsBadURL(t *testing.T) {
	if _, err := handler.NewForwardingHandler("://bad-url"); err == nil {
		t.Error("expected error on malformed URL, got nil")
	}
}
