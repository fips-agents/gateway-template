package proxy

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const heartbeatInterval = 15 * time.Second

// RelayOption configures optional behavior for RelaySSE.
type RelayOption func(*relayConfig)

type relayConfig struct {
	onUsage func(totalTokens int64)
}

// WithUsageCallback registers a callback invoked with the total_tokens
// value extracted from the final SSE data event. Called at most once per
// stream, after the usage-bearing event has been forwarded to the client.
func WithUsageCallback(fn func(totalTokens int64)) RelayOption {
	return func(c *relayConfig) { c.onUsage = fn }
}

// RelaySSE reads an SSE stream from backendResp and writes each event to the
// client. It flushes after every line and sends periodic heartbeat comments
// to keep the connection alive through intermediate proxies.
func RelaySSE(backendResp *http.Response, w http.ResponseWriter, opts ...RelayOption) {
	var cfg relayConfig
	for _, o := range opts {
		o(&cfg)
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		slog.Error("response writer does not support flushing")
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	scanner := bufio.NewScanner(backendResp.Body)
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	lines := make(chan string)
	done := make(chan struct{})
	defer close(done)

	var lastData string

	// Read lines from the backend in a goroutine so we can interleave heartbeats.
	go func() {
		defer close(lines)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-done:
				return
			}
		}
		if err := scanner.Err(); err != nil {
			slog.Warn("scanner error reading backend SSE stream", "error", err)
		}
	}()

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				// Backend closed the stream.
				return
			}

			// Track the last non-[DONE] data line for usage extraction.
			if strings.HasPrefix(line, "data: ") && strings.TrimSpace(line) != "data: [DONE]" {
				lastData = line
			}

			// Write the line as-is (preserves "data: ..." formatting).
			_, _ = w.Write([]byte(line + "\n"))
			flusher.Flush()

			// Detect the OpenAI SSE terminator.
			if strings.TrimSpace(line) == "data: [DONE]" {
				if cfg.onUsage != nil && lastData != "" {
					parseAndReportUsage(lastData, cfg.onUsage)
				}
				return
			}

		case <-heartbeat.C:
			// SSE comment to keep the connection alive.
			_, _ = w.Write([]byte(": heartbeat\n\n"))
			flusher.Flush()
		}
	}
}

func parseAndReportUsage(dataLine string, report func(int64)) {
	payload := strings.TrimPrefix(dataLine, "data: ")
	var env struct {
		Usage *struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(payload), &env) == nil && env.Usage != nil && env.Usage.TotalTokens > 0 {
		report(env.Usage.TotalTokens)
	}
}
