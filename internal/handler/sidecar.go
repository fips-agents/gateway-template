package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// SidecarHandler proxies requests to a sidecar service with optional
// method restriction, size cap, and per-request timeout. It wraps a
// ForwardingHandler for the actual proxying.
type SidecarHandler struct {
	Forward  *ForwardingHandler
	MaxBytes int64
	Timeout  time.Duration
	PostOnly bool
}

func (h *SidecarHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.PostOnly && r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if h.MaxBytes > 0 && r.ContentLength > 0 && r.ContentLength > h.MaxBytes {
		http.Error(w, fmt.Sprintf(
			`{"error":"upload exceeds max size","max_bytes":%d}`, h.MaxBytes,
		), http.StatusRequestEntityTooLarge)
		return
	}

	if h.Timeout > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
		defer cancel()
		r = r.WithContext(ctx)
	}

	h.Forward.ServeHTTP(w, r)
}
