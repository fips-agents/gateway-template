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

	// Token exchange (RFC 8693): consulted only when AuthMode == "jwt".
	// When all four required fields (URL, ClientID, ClientSecret, Audience)
	// are non-empty, the gateway swaps the inbound user JWT for a
	// downstream-audienced token before forwarding to the backend. Partial
	// configuration (some set, some empty) is rejected at Load() time.
	AuthJWTExchangeURL          string
	AuthJWTExchangeClientID     string
	AuthJWTExchangeClientSecret string
	AuthJWTExchangeAudience     string
	AuthJWTExchangeScope        string
}

// JWTExchangeEnabled reports whether all four required token-exchange
// fields are populated. Used by the wiring layer to decide whether to
// construct a TokenExchanger.
func (c *Config) JWTExchangeEnabled() bool {
	return c.AuthJWTExchangeURL != "" &&
		c.AuthJWTExchangeClientID != "" &&
		c.AuthJWTExchangeClientSecret != "" &&
		c.AuthJWTExchangeAudience != ""
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
		AuthJWTJWKSURL:              os.Getenv("GATEWAY_AUTH_JWT_JWKS_URL"),
		AuthJWTIssuer:               os.Getenv("GATEWAY_AUTH_JWT_ISSUER"),
		AuthJWTAudience:             os.Getenv("GATEWAY_AUTH_JWT_AUDIENCE"),
		AuthJWTSubjectClaim:         envOrDefault("GATEWAY_AUTH_JWT_SUBJECT_CLAIM", "sub"),
		AuthJWTUserClaim:            envOrDefault("GATEWAY_AUTH_JWT_USER_CLAIM", "preferred_username"),
		AuthJWTEmailClaim:           envOrDefault("GATEWAY_AUTH_JWT_EMAIL_CLAIM", "email"),
		AuthJWTExchangeURL:          os.Getenv("GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_URL"),
		AuthJWTExchangeClientID:     os.Getenv("GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_CLIENT_ID"),
		AuthJWTExchangeClientSecret: os.Getenv("GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_CLIENT_SECRET"),
		AuthJWTExchangeAudience:     os.Getenv("GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_AUDIENCE"),
		AuthJWTExchangeScope:        os.Getenv("GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_SCOPE"),
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
		if err := validateExchangeConfig(cfg); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// validateExchangeConfig fails closed on partially-configured token
// exchange. Either all four required fields are set (exchange enabled) or
// none are (exchange disabled). Anything in between is almost certainly a
// deployment bug — flag it loudly at startup rather than silently disabling
// the swap.
func validateExchangeConfig(cfg *Config) error {
	required := map[string]string{
		"GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_URL":           cfg.AuthJWTExchangeURL,
		"GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_CLIENT_ID":     cfg.AuthJWTExchangeClientID,
		"GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_CLIENT_SECRET": cfg.AuthJWTExchangeClientSecret,
		"GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_AUDIENCE":      cfg.AuthJWTExchangeAudience,
	}
	var setNames, missingNames []string
	for name, val := range required {
		if val != "" {
			setNames = append(setNames, name)
		} else {
			missingNames = append(missingNames, name)
		}
	}
	if len(setNames) > 0 && len(missingNames) > 0 {
		return fmt.Errorf("token exchange is partially configured: %v set but %v missing — set all four or none", setNames, missingNames)
	}
	return nil
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
