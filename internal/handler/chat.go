package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/fips-agents/gateway-template/internal/budget"
	"github.com/fips-agents/gateway-template/internal/proxy"
	"github.com/fips-agents/gateway-template/internal/routing"
)

// ChatHandler proxies OpenAI-compatible /v1/chat/completions requests to a
// backend agent service. It supports both synchronous and streaming modes.
type ChatHandler struct {
	BackendURL   string
	Client       *http.Client
	Router       *routing.Router // nil = single-backend mode
	Budget       *budget.Store   // nil = no usage tracking
	BudgetConfig *budget.Config  // nil = no budget limits
}

// ServeHTTP dispatches the request to either streaming or synchronous proxy
// based on the "stream" field in the JSON body.
func (h *ChatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("failed to read request body", "error", err)
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Peek at "stream" and "model" to decide proxy mode and backend routing.
	var envelope struct {
		Stream bool   `json:"stream"`
		Model  string `json:"model"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		slog.Warn("failed to parse request JSON", "error", err)
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	if envelope.Stream {
		h.proxyStreaming(w, r, body, envelope.Model)
	} else {
		h.proxySync(w, r, body, envelope.Model)
	}
}

// passThroughHeaders are response headers copied from the backend to the
// client. The list is deliberately narrow — keep the gateway thin and
// avoid leaking internal headers.
var passThroughHeaders = []string{
	"X-Trace-Id",
	"X-Request-ID",
}

// copyPassThroughHeaders copies the allowlisted headers from src to dst.
func copyPassThroughHeaders(dst http.Header, src http.Header) {
	for _, name := range passThroughHeaders {
		if v := src.Get(name); v != "" {
			dst.Set(name, v)
		}
	}
}

// usageEnvelope is the minimal structure needed to extract token usage from
// sync chat completion responses.
type usageEnvelope struct {
	Usage *struct {
		TotalTokens int64 `json:"total_tokens"`
	} `json:"usage"`
}

// proxySync forwards the request and returns the full backend response.
func (h *ChatHandler) proxySync(w http.ResponseWriter, r *http.Request, body []byte, model string) {
	resp, err := h.doBackendRequest(r, body, model)
	if err != nil {
		slog.Error("backend request failed", "error", err)
		http.Error(w, `{"error":"backend request failed"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Warn("error reading backend response", "error", err)
		http.Error(w, `{"error":"failed to read backend response"}`, http.StatusBadGateway)
		return
	}

	var tokens int64
	if resp.StatusCode == http.StatusOK {
		var env usageEnvelope
		if json.Unmarshal(respBody, &env) == nil && env.Usage != nil {
			tokens = env.Usage.TotalTokens
		}
	}

	if tokens > 0 && h.Budget != nil {
		tenant := r.Header.Get("X-Tenant-ID")
		if tenant != "" {
			h.Budget.Add(tenant, tokens)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	copyPassThroughHeaders(w.Header(), resp.Header)
	if tokens > 0 {
		w.Header().Set("X-Token-Usage", strconv.FormatInt(tokens, 10))
		if h.Budget != nil && h.BudgetConfig != nil {
			tenant := r.Header.Get("X-Tenant-ID")
			remaining := int64(-1)
			if tenant != "" {
				limit := h.BudgetConfig.BudgetFor(tenant)
				if limit > 0 {
					remaining = limit - h.Budget.Usage(tenant)
					if remaining < 0 {
						remaining = 0
					}
				}
			}
			w.Header().Set("X-Budget-Remaining", strconv.FormatInt(remaining, 10))
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// proxyStreaming connects to the backend with streaming enabled and relays
// SSE chunks to the client.
func (h *ChatHandler) proxyStreaming(w http.ResponseWriter, r *http.Request, body []byte, model string) {
	resp, err := h.doBackendRequest(r, body, model)
	if err != nil {
		slog.Error("backend streaming request failed", "error", err)
		http.Error(w, `{"error":"backend request failed"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		copyPassThroughHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	copyPassThroughHeaders(w.Header(), resp.Header)

	var relayOpts []proxy.RelayOption
	if h.Budget != nil {
		tenant := r.Header.Get("X-Tenant-ID")
		if tenant != "" {
			relayOpts = append(relayOpts, proxy.WithUsageCallback(func(totalTokens int64) {
				h.Budget.Add(tenant, totalTokens)
			}))
		}
	}
	proxy.RelaySSE(resp, w, relayOpts...)
}

// forwardedAuthHeaders are the auth-related headers projected by the auth
// middleware and forwarded to the backend agent.
//
// Authorization is included so jwt-mode token-exchange (RFC 8693) works:
// the middleware replaces the inbound user JWT with a downstream-audienced
// swapped token (Identity.BearerToken) before the handler runs, or strips
// Authorization entirely when no swap is configured. We never forward the
// raw inbound user JWT.
var forwardedAuthHeaders = []string{
	"X-Auth-Subject",
	"X-Auth-User",
	"X-Auth-Email",
	"X-Auth-Mode",
	"X-Tenant-ID",
	"Authorization",
}

// doBackendRequest sends the request body to the backend's chat completions
// endpoint, forwarding the canonical X-Auth-* headers from the inbound
// request so the agent can attribute the call to the resolved identity.
// When a Router is configured, model selects the target backend; otherwise
// h.BackendURL is used directly (single-backend mode).
func (h *ChatHandler) doBackendRequest(r *http.Request, body []byte, model string) (*http.Response, error) {
	backendURL := h.BackendURL
	if h.Router != nil {
		backendURL = h.Router.ResolveModel(model)
	}
	url := backendURL + "/v1/chat/completions"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for _, name := range forwardedAuthHeaders {
		if v := r.Header.Get(name); v != "" {
			req.Header.Set(name, v)
		}
	}
	copyPropagationHeaders(req.Header, r.Header)

	return h.Client.Do(req)
}
