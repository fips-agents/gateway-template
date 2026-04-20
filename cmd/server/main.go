package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fips-agents/gateway-template/internal/config"
	"github.com/fips-agents/gateway-template/internal/handler"
	"github.com/fips-agents/gateway-template/internal/middleware"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}

	client := &http.Client{Timeout: 120 * time.Second}

	mux := http.NewServeMux()
	mux.Handle("/v1/chat/completions", &handler.ChatHandler{
		BackendURL: cfg.BackendURL,
		Client:     client,
	})
	mux.Handle("/healthz", &handler.HealthHandler{})
	mux.Handle("/readyz", &handler.ReadyHandler{
		BackendURL: cfg.BackendURL,
		Client:     &http.Client{Timeout: 3 * time.Second},
	})
	mux.Handle("/.well-known/agent.json", &handler.WellKnownHandler{
		AgentName:    cfg.AgentName,
		AgentVersion: cfg.AgentVersion,
	})
	mux.HandleFunc("GET /v1/agent-info", func(w http.ResponseWriter, r *http.Request) {
		resp, err := client.Get(cfg.BackendURL + "/v1/agent-info")
		if err != nil {
			http.Error(w, "backend unreachable", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	var handler http.Handler = mux
	if cfg.LogRequests {
		handler = middleware.LogRequests(handler)
	}

	srv := &http.Server{
		Addr:              net.JoinHostPort("", cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown on SIGINT / SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("gateway starting",
			"port", cfg.Port,
			"backend", cfg.BackendURL,
			"agent", cfg.AgentName,
			"version", cfg.AgentVersion,
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutdown signal received, draining connections")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
		os.Exit(1)
	}
	slog.Info("gateway stopped")
}
