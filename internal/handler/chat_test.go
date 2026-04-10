package handler_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/redhat-ai-americas/gateway-template/internal/handler"
)

// sampleCompletion is a minimal OpenAI-compatible chat completion response.
var sampleCompletion = map[string]any{
	"id":      "chatcmpl-abc123",
	"object":  "chat.completion",
	"created": 1700000000,
	"model":   "test-model",
	"choices": []map[string]any{
		{
			"index": 0,
			"message": map[string]string{
				"role":    "assistant",
				"content": "Hello!",
			},
			"finish_reason": "stop",
		},
	},
}

func TestChatHandler_SyncProxy(t *testing.T) {
	expected, _ := json.Marshal(sampleCompletion)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("backend: want POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("backend: want path /v1/chat/completions, got %s", r.URL.Path)
		}

		// Verify the body was forwarded.
		body, _ := io.ReadAll(r.Body)
		var envelope struct {
			Stream bool `json:"stream"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Errorf("backend: cannot parse forwarded body: %v", err)
		}
		if envelope.Stream {
			t.Errorf("backend: expected stream=false in forwarded body")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(expected)
	}))
	defer backend.Close()

	h := &handler.ChatHandler{
		BackendURL: backend.URL,
		Client:     backend.Client(),
	}

	reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("sync proxy: want status %d, got %d (body: %s)", http.StatusOK, rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("sync proxy: want Content-Type application/json, got %q", ct)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("sync proxy: response is not valid JSON: %v", err)
	}
	if got["id"] != "chatcmpl-abc123" {
		t.Errorf("sync proxy: want id=chatcmpl-abc123, got %v", got["id"])
	}
}

func TestChatHandler_StreamingProxy(t *testing.T) {
	// Build SSE chunks that the mock backend will emit.
	chunks := []string{
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"delta":{"content":"Hel"},"index":0}]}`,
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"delta":{"content":"lo!"},"index":0}]}`,
		`data: [DONE]`,
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("backend: ResponseWriter does not implement Flusher")
		}

		for _, chunk := range chunks {
			fmt.Fprintf(w, "%s\n\n", chunk)
			flusher.Flush()
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer backend.Close()

	h := &handler.ChatHandler{
		BackendURL: backend.URL,
		Client:     backend.Client(),
	}

	reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("streaming proxy: want status %d, got %d (body: %s)", http.StatusOK, rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("streaming proxy: want Content-Type text/event-stream, got %q", ct)
	}

	body := rec.Body.String()
	for _, chunk := range chunks {
		if !strings.Contains(body, chunk) {
			t.Errorf("streaming proxy: response missing expected chunk %q", chunk)
		}
	}
}

func TestChatHandler_BackendError(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal backend error"}`))
	}))
	defer backend.Close()

	h := &handler.ChatHandler{
		BackendURL: backend.URL,
		Client:     backend.Client(),
	}

	reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	// The gateway should forward the backend's 500 status.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("backend error: want status %d, got %d", http.StatusInternalServerError, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "internal backend error") {
		t.Errorf("backend error: expected backend error message in response, got %q", body)
	}
}

func TestChatHandler_BackendUnreachable(t *testing.T) {
	// Closed server -- nothing listening.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := backend.URL
	backend.Close()

	h := &handler.ChatHandler{
		BackendURL: closedURL,
		Client:     &http.Client{},
	}

	reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("backend unreachable: want status %d, got %d", http.StatusBadGateway, rec.Code)
	}
}

func TestChatHandler_MethodNotAllowed(t *testing.T) {
	h := &handler.ChatHandler{
		BackendURL: "http://localhost:0",
		Client:     &http.Client{},
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method not allowed: want status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}

func TestChatHandler_InvalidJSON(t *testing.T) {
	h := &handler.ChatHandler{
		BackendURL: "http://localhost:0",
		Client:     &http.Client{},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON: want status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestChatHandler_StreamingBackendError(t *testing.T) {
	// Backend returns 500 on a streaming request -- gateway should forward the
	// error status rather than switching to SSE mode.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"model overloaded"}`))
	}))
	defer backend.Close()

	h := &handler.ChatHandler{
		BackendURL: backend.URL,
		Client:     backend.Client(),
	}

	reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("streaming backend error: want status %d, got %d", http.StatusInternalServerError, rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("streaming backend error: want Content-Type application/json, got %q", ct)
	}
}
