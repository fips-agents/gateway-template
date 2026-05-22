package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fips-agents/gateway-template/internal/handler"
)

func TestSidecarHandler_PostOnly(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"processed"}`))
	}))
	defer backend.Close()

	fwd, err := handler.NewForwardingHandler(backend.URL)
	if err != nil {
		t.Fatalf("NewForwardingHandler: %v", err)
	}

	h := &handler.SidecarHandler{
		Forward:  fwd,
		PostOnly: true,
		MaxBytes: 0,
		Timeout:  0,
	}

	t.Run("GET rejected with 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/sidecar", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET: want status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "method not allowed") {
			t.Errorf("GET error body should mention method not allowed, got: %s", body)
		}
	})

	t.Run("POST proxied successfully", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/sidecar", strings.NewReader(`{"data":"test"}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("POST: want status %d, got %d", http.StatusOK, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "processed") {
			t.Errorf("POST should be proxied to backend, got: %s", body)
		}
	})
}

func TestSidecarHandler_RejectsOversized(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend should not be called for oversized request")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	fwd, err := handler.NewForwardingHandler(backend.URL)
	if err != nil {
		t.Fatalf("NewForwardingHandler: %v", err)
	}

	h := &handler.SidecarHandler{
		Forward:  fwd,
		PostOnly: false,
		MaxBytes: 100,
		Timeout:  0,
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sidecar", strings.NewReader(strings.Repeat("x", 200)))
	req.ContentLength = 200
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized request: want status %d, got %d", http.StatusRequestEntityTooLarge, rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "max_bytes") {
		t.Errorf("error should mention max_bytes, got: %s", body)
	}
	if !strings.Contains(body, "100") {
		t.Errorf("error should show the 100-byte limit, got: %s", body)
	}
}

func TestSidecarHandler_AllowsUnderSize(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	fwd, err := handler.NewForwardingHandler(backend.URL)
	if err != nil {
		t.Fatalf("NewForwardingHandler: %v", err)
	}

	h := &handler.SidecarHandler{
		Forward:  fwd,
		PostOnly: false,
		MaxBytes: 100,
		Timeout:  0,
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sidecar", strings.NewReader(strings.Repeat("x", 50)))
	req.ContentLength = 50
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("under-size request: want status %d, got %d", http.StatusOK, rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ok") {
		t.Errorf("request should be proxied successfully, got: %s", body)
	}
}

func TestSidecarHandler_Timeout(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a slow backend that takes longer than the timeout
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	fwd, err := handler.NewForwardingHandler(backend.URL)
	if err != nil {
		t.Fatalf("NewForwardingHandler: %v", err)
	}

	h := &handler.SidecarHandler{
		Forward:  fwd,
		PostOnly: false,
		MaxBytes: 0,
		Timeout:  50 * time.Millisecond,
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sidecar", strings.NewReader(`{"data":"test"}`))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	// ForwardingHandler returns 502 for context deadline exceeded
	if rec.Code != http.StatusBadGateway {
		t.Errorf("timeout: want status %d (Bad Gateway), got %d", http.StatusBadGateway, rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "unreachable") {
		t.Errorf("timeout error should indicate platform unreachable, got: %s", body)
	}
}

func TestSidecarHandler_AllowsAllMethods(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"method":"` + r.Method + `"}`))
	}))
	defer backend.Close()

	fwd, err := handler.NewForwardingHandler(backend.URL)
	if err != nil {
		t.Fatalf("NewForwardingHandler: %v", err)
	}

	h := &handler.SidecarHandler{
		Forward:  fwd,
		PostOnly: false, // Allow all methods
		MaxBytes: 0,
		Timeout:  0,
	}

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete}
	for _, method := range methods {
		t.Run(method+" allowed", func(t *testing.T) {
			req := httptest.NewRequest(method, "/v1/sidecar", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("%s: want status %d, got %d", method, http.StatusOK, rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, method) {
				t.Errorf("%s should be proxied, got: %s", method, body)
			}
		})
	}
}

func TestSidecarHandler_NoMaxBytes(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	fwd, err := handler.NewForwardingHandler(backend.URL)
	if err != nil {
		t.Fatalf("NewForwardingHandler: %v", err)
	}

	h := &handler.SidecarHandler{
		Forward:  fwd,
		PostOnly: false,
		MaxBytes: 0, // No size limit
		Timeout:  0,
	}

	// Send a large request when MaxBytes=0
	largeData := strings.Repeat("x", 10000)
	req := httptest.NewRequest(http.MethodPost, "/v1/sidecar", strings.NewReader(largeData))
	req.ContentLength = int64(len(largeData))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("large request with MaxBytes=0: want status %d, got %d", http.StatusOK, rec.Code)
	}
}
