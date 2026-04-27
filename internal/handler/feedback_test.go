package handler_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fips-agents/gateway-template/internal/handler"
)

func TestFeedbackHandler_PostProxy(t *testing.T) {
	var capturedBody []byte
	var capturedAuth string
	var capturedUser string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("backend: want POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/feedback" {
			t.Errorf("backend: want path /v1/feedback, got %s", r.URL.Path)
		}
		capturedBody, _ = io.ReadAll(r.Body)
		capturedAuth = r.Header.Get("Authorization")
		capturedUser = r.Header.Get("X-User-ID")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"feedback_id":"fb_abc123"}`))
	}))
	defer backend.Close()

	h := &handler.FeedbackHandler{
		BackendURL: backend.URL,
		Client:     backend.Client(),
	}

	reqBody := `{"trace_id":"tr_1","rating":1,"comment":"great"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/feedback", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("X-User-ID", "user-42")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("post proxy: want status %d, got %d (body: %s)", http.StatusCreated, rec.Code, rec.Body.String())
	}
	if string(capturedBody) != reqBody {
		t.Errorf("post proxy: want body %q forwarded, got %q", reqBody, string(capturedBody))
	}
	if capturedAuth != "Bearer secret-token" {
		t.Errorf("post proxy: Authorization header not forwarded, got %q", capturedAuth)
	}
	if capturedUser != "user-42" {
		t.Errorf("post proxy: X-User-ID header not forwarded, got %q", capturedUser)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("post proxy: response is not valid JSON: %v", err)
	}
	if got["feedback_id"] != "fb_abc123" {
		t.Errorf("post proxy: want feedback_id=fb_abc123, got %v", got["feedback_id"])
	}
}

func TestFeedbackHandler_GetProxyWithQuery(t *testing.T) {
	var capturedQuery string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("backend: want GET, got %s", r.Method)
		}
		if r.URL.Path != "/v1/feedback" {
			t.Errorf("backend: want path /v1/feedback, got %s", r.URL.Path)
		}
		capturedQuery = r.URL.RawQuery

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"feedback_id":"fb_1","rating":1}]`))
	}))
	defer backend.Close()

	h := &handler.FeedbackHandler{
		BackendURL: backend.URL,
		Client:     backend.Client(),
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/feedback?trace_id=tr_1&limit=10", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get proxy: want status %d, got %d", http.StatusOK, rec.Code)
	}
	if capturedQuery != "trace_id=tr_1&limit=10" {
		t.Errorf("get proxy: query string not forwarded, got %q", capturedQuery)
	}
}

func TestFeedbackHandler_MethodNotAllowed(t *testing.T) {
	h := &handler.FeedbackHandler{
		BackendURL: "http://localhost:0",
		Client:     &http.Client{},
	}

	req := httptest.NewRequest(http.MethodDelete, "/v1/feedback", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method not allowed: want status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}

func TestFeedbackHandler_BackendUnreachable(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := backend.URL
	backend.Close()

	h := &handler.FeedbackHandler{
		BackendURL: closedURL,
		Client:     &http.Client{},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/feedback", strings.NewReader(`{"trace_id":"x","rating":1}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("backend unreachable: want status %d, got %d", http.StatusBadGateway, rec.Code)
	}
}

func TestFeedbackHandler_BackendError(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"trace_id is required"}`))
	}))
	defer backend.Close()

	h := &handler.FeedbackHandler{
		BackendURL: backend.URL,
		Client:     backend.Client(),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/feedback", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("backend error: want status %d, got %d", http.StatusBadRequest, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "trace_id is required") {
		t.Errorf("backend error: expected error message in response, got %q", rec.Body.String())
	}
}

func TestFeedbackStatsHandler_GetProxy(t *testing.T) {
	var capturedQuery string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("backend: want GET, got %s", r.Method)
		}
		if r.URL.Path != "/v1/feedback/stats" {
			t.Errorf("backend: want path /v1/feedback/stats, got %s", r.URL.Path)
		}
		capturedQuery = r.URL.RawQuery

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"window_start":"2026-04-01T00:00:00Z","thumbs_up":3,"thumbs_down":1,"total":4}]`))
	}))
	defer backend.Close()

	h := &handler.FeedbackStatsHandler{
		BackendURL: backend.URL,
		Client:     backend.Client(),
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/feedback/stats?window=day&agent_type=chat", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("stats proxy: want status %d, got %d", http.StatusOK, rec.Code)
	}
	if capturedQuery != "window=day&agent_type=chat" {
		t.Errorf("stats proxy: query string not forwarded, got %q", capturedQuery)
	}
}

func TestFeedbackStatsHandler_MethodNotAllowed(t *testing.T) {
	h := &handler.FeedbackStatsHandler{
		BackendURL: "http://localhost:0",
		Client:     &http.Client{},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/feedback/stats", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method not allowed: want status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}
