package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// HealthHandler returns a simple liveness check.
type HealthHandler struct{}

func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// ReadyHandler checks whether the backend is reachable before reporting ready.
// When SidecarURLs is populated, each sidecar is probed in parallel and its
// status is included in the response. Sidecar failures are logged but do not
// fail the probe — sidecars are optional features.
type ReadyHandler struct {
	BackendURL  string
	Client      *http.Client
	SidecarURLs map[string]string // name → base URL (e.g. "stt" → "http://stt:8080")
}

func (h *ReadyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, h.BackendURL+"/healthz", nil)
	if err != nil {
		slog.Warn("readiness check: failed to create request", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"not ready","reason":"bad backend url"}`))
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("readiness check: backend unreachable", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"not ready","reason":"backend unreachable"}`))
		return
	}
	resp.Body.Close()

	result := readyResponse{Status: "ok"}

	if len(h.SidecarURLs) > 0 {
		result.Sidecars = h.probeSidecars(ctx, client)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

type readyResponse struct {
	Status   string            `json:"status"`
	Sidecars map[string]string `json:"sidecars,omitempty"`
}

func (h *ReadyHandler) probeSidecars(ctx context.Context, client *http.Client) map[string]string {
	type probeResult struct {
		name   string
		status string
	}

	var wg sync.WaitGroup
	results := make(chan probeResult, len(h.SidecarURLs))

	for name, url := range h.SidecarURLs {
		wg.Add(1)
		go func(name, url string) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodHead, url+"/healthz", nil)
			if err != nil {
				slog.Warn("readiness check: sidecar probe failed", "sidecar", name, "error", err)
				results <- probeResult{name, "unreachable"}
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				slog.Warn("readiness check: sidecar unreachable", "sidecar", name, "error", err)
				results <- probeResult{name, "unreachable"}
				return
			}
			resp.Body.Close()
			results <- probeResult{name, "ok"}
		}(name, url)
	}

	wg.Wait()
	close(results)

	sidecars := make(map[string]string, len(h.SidecarURLs))
	for r := range results {
		sidecars[r.name] = r.status
	}
	return sidecars
}
