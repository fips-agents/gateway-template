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

	// AuthMode selects the inbound auth strategy: "anonymous" (default),
	// "proxy", or "jwt". See internal/auth.
	AuthMode string
	// AuthProxyUserHeader is the upstream-projected username header
	// consulted in proxy mode. Defaults to "X-Forwarded-User"
	// (oauth-proxy convention).
	AuthProxyUserHeader string
	// AuthProxyEmailHeader is the upstream-projected email header. Empty
	// means the deployment does not surface email.
	AuthProxyEmailHeader string

	// JWT mode: in-process bearer-token validation against a JWKS endpoint.
	// All AuthJWT* fields are consulted only when AuthMode == "jwt".
	AuthJWTJWKSURL      string
	AuthJWTIssuer       string
	AuthJWTAudience     string
	AuthJWTSubjectClaim string
	AuthJWTUserClaim    string
	AuthJWTEmailClaim   string
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
		AuthJWTJWKSURL:       os.Getenv("GATEWAY_AUTH_JWT_JWKS_URL"),
		AuthJWTIssuer:        os.Getenv("GATEWAY_AUTH_JWT_ISSUER"),
		AuthJWTAudience:      os.Getenv("GATEWAY_AUTH_JWT_AUDIENCE"),
		AuthJWTSubjectClaim:  envOrDefault("GATEWAY_AUTH_JWT_SUBJECT_CLAIM", "sub"),
		AuthJWTUserClaim:     envOrDefault("GATEWAY_AUTH_JWT_USER_CLAIM", "preferred_username"),
		AuthJWTEmailClaim:    envOrDefault("GATEWAY_AUTH_JWT_EMAIL_CLAIM", "email"),
	}

	if cfg.BackendURL == "" {
		return nil, fmt.Errorf("BACKEND_URL environment variable is required")
	}
	if cfg.AuthMode == "jwt" {
		if cfg.AuthJWTJWKSURL == "" {
			return nil, fmt.Errorf("GATEWAY_AUTH_JWT_JWKS_URL is required when GATEWAY_AUTH_MODE=jwt")
		}
		if cfg.AuthJWTIssuer == "" {
			return nil, fmt.Errorf("GATEWAY_AUTH_JWT_ISSUER is required when GATEWAY_AUTH_MODE=jwt")
		}
		if cfg.AuthJWTAudience == "" {
			return nil, fmt.Errorf("GATEWAY_AUTH_JWT_AUDIENCE is required when GATEWAY_AUTH_MODE=jwt")
		}
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
