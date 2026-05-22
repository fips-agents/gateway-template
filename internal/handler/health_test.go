package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fips-agents/gateway-template/internal/handler"
)

func TestHealthHandler(t *testing.T) {
	h := &handler.HealthHandler{}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz: want status %d, got %d", http.StatusOK, rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("GET /healthz: want Content-Type application/json, got %q", ct)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /healthz: response is not valid JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("GET /healthz: want status=ok, got status=%q", body["status"])
	}
}

func TestReadyHandler_BackendReachable(t *testing.T) {
	// Spin up a mock backend that responds to HEAD /healthz.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("readiness probe: want HEAD, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	h := &handler.ReadyHandler{
		BackendURL: backend.URL,
		Client:     backend.Client(),
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /readyz (backend up): want status %d, got %d", http.StatusOK, rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /readyz: response is not valid JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("GET /readyz: want status=ok, got status=%q", body["status"])
	}
}

func TestReadyHandler_BackendUnreachable(t *testing.T) {
	// Point at a server that was started and immediately closed, so the URL
	// is well-formed but nobody is listening.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := backend.URL
	backend.Close()

	h := &handler.ReadyHandler{
		BackendURL: closedURL,
		Client:     &http.Client{},
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz (backend down): want status %d, got %d",
			http.StatusServiceUnavailable, rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /readyz: response is not valid JSON: %v", err)
	}
	if body["status"] != "not ready" {
		t.Errorf("GET /readyz: want status=\"not ready\", got status=%q", body["status"])
	}
}

func TestReadyHandler_SidecarsIncludedInResponse(t *testing.T) {
	// Set up healthy backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.URL.Path != "/healthz" {
			t.Errorf("backend probe: want HEAD /healthz, got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	// Set up healthy sidecar services
	sttSidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.URL.Path != "/healthz" {
			t.Errorf("stt probe: want HEAD /healthz, got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer sttSidecar.Close()

	videoSidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.URL.Path != "/healthz" {
			t.Errorf("video probe: want HEAD /healthz, got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer videoSidecar.Close()

	h := &handler.ReadyHandler{
		BackendURL: backend.URL,
		Client:     backend.Client(),
		SidecarURLs: map[string]string{
			"stt":   sttSidecar.URL,
			"video": videoSidecar.URL,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /readyz (all healthy): want status %d, got %d", http.StatusOK, rec.Code)
	}

	type readyResp struct {
		Status   string            `json:"status"`
		Sidecars map[string]string `json:"sidecars,omitempty"`
	}
	var body readyResp
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /readyz: response is not valid JSON: %v", err)
	}

	if body.Status != "ok" {
		t.Errorf("status = %q, want \"ok\"", body.Status)
	}

	if body.Sidecars == nil {
		t.Fatal("sidecars field should be present when SidecarURLs configured")
	}

	if len(body.Sidecars) != 2 {
		t.Errorf("sidecars map length = %d, want 2", len(body.Sidecars))
	}

	if body.Sidecars["stt"] != "ok" {
		t.Errorf("sidecars[\"stt\"] = %q, want \"ok\"", body.Sidecars["stt"])
	}

	if body.Sidecars["video"] != "ok" {
		t.Errorf("sidecars[\"video\"] = %q, want \"ok\"", body.Sidecars["video"])
	}
}

func TestReadyHandler_SidecarUnreachableDoesNotFail(t *testing.T) {
	// Set up healthy backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	// Set up a sidecar that is unreachable (closed server)
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	unreachableURL := sidecar.URL
	sidecar.Close()

	h := &handler.ReadyHandler{
		BackendURL: backend.URL,
		Client:     backend.Client(),
		SidecarURLs: map[string]string{
			"video": unreachableURL,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	// Probe should still succeed - sidecars are optional
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /readyz (sidecar down): want status %d (sidecars are optional), got %d",
			http.StatusOK, rec.Code)
	}

	type readyResp struct {
		Status   string            `json:"status"`
		Sidecars map[string]string `json:"sidecars,omitempty"`
	}
	var body readyResp
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /readyz: response is not valid JSON: %v", err)
	}

	if body.Status != "ok" {
		t.Errorf("status = %q, want \"ok\" (backend is healthy)", body.Status)
	}

	if body.Sidecars == nil {
		t.Fatal("sidecars field should be present")
	}

	if body.Sidecars["video"] != "unreachable" {
		t.Errorf("sidecars[\"video\"] = %q, want \"unreachable\"", body.Sidecars["video"])
	}
}

func TestReadyHandler_NoSidecarsOmitted(t *testing.T) {
	// Set up healthy backend with no sidecars
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	h := &handler.ReadyHandler{
		BackendURL:  backend.URL,
		Client:      backend.Client(),
		SidecarURLs: nil, // No sidecars configured
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /readyz: want status %d, got %d", http.StatusOK, rec.Code)
	}

	// Parse response to verify sidecars field is omitted
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /readyz: response is not valid JSON: %v", err)
	}

	if body["status"] != "ok" {
		t.Errorf("status = %q, want \"ok\"", body["status"])
	}

	// Verify sidecars field does not exist (omitempty behavior)
	if _, exists := body["sidecars"]; exists {
		t.Error("sidecars field should be omitted when SidecarURLs is nil/empty")
	}
}
