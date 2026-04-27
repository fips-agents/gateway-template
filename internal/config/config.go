package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds the gateway configuration loaded from environment variables.
type Config struct {
	Port         string
	BackendURL   string
	AgentName    string
	AgentVersion string
	LogRequests  bool

	// AuthMode selects the inbound auth strategy: "anonymous" (default)
	// or "proxy". See internal/auth.
	AuthMode string
	// AuthProxyUserHeader is the upstream-projected username header
	// consulted in proxy mode. Defaults to "X-Forwarded-User"
	// (oauth-proxy convention).
	AuthProxyUserHeader string
	// AuthProxyEmailHeader is the upstream-projected email header. Empty
	// means the deployment does not surface email.
	AuthProxyEmailHeader string
}

// Load reads configuration from environment variables and validates required fields.
func Load() (*Config, error) {
	cfg := &Config{
		Port:                 envOrDefault("PORT", "8080"),
		BackendURL:           os.Getenv("BACKEND_URL"),
		AgentName:            envOrDefault("AGENT_NAME", "gateway-template"),
		AgentVersion:         envOrDefault("AGENT_VERSION", "0.1.0"),
		LogRequests:          envBool("LOG_REQUESTS"),
		AuthMode:             envOrDefault("GATEWAY_AUTH_MODE", "anonymous"),
		AuthProxyUserHeader:  envOrDefault("GATEWAY_AUTH_PROXY_USER_HEADER", "X-Forwarded-User"),
		AuthProxyEmailHeader: envOrDefault("GATEWAY_AUTH_PROXY_EMAIL_HEADER", "X-Forwarded-Email"),
	}

	if cfg.BackendURL == "" {
		return nil, fmt.Errorf("BACKEND_URL environment variable is required")
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string) bool {
	v := strings.ToLower(os.Getenv(key))
	return v == "true" || v == "1" || v == "yes"
}
