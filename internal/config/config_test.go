package config_test

import (
	"strings"
	"testing"

	"github.com/fips-agents/gateway-template/internal/config"
)

// jwtBaseEnv returns the minimum env vars needed for AuthMode=jwt to pass
// validation. Tests can override individual entries before calling
// loadWithEnv.
func jwtBaseEnv() map[string]string {
	return map[string]string{
		"BACKEND_URL":                "http://backend:8081",
		"GATEWAY_AUTH_MODE":          "jwt",
		"GATEWAY_AUTH_JWT_JWKS_URL":  "https://kc/realms/x/protocol/openid-connect/certs",
		"GATEWAY_AUTH_JWT_ISSUER":    "https://kc/realms/x",
		"GATEWAY_AUTH_JWT_AUDIENCE":  "gateway-template",
	}
}

// loadWithEnv sets env vars for the lifetime of t and calls config.Load.
func loadWithEnv(t *testing.T, env map[string]string) (*config.Config, error) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	return config.Load()
}

func TestLoad_JWTExchange_AllFieldsSet(t *testing.T) {
	env := jwtBaseEnv()
	env["GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_URL"] = "https://kc/realms/x/protocol/openid-connect/token"
	env["GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_CLIENT_ID"] = "gw-svc"
	env["GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_CLIENT_SECRET"] = "shh"
	env["GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_AUDIENCE"] = "backend-agent"

	cfg, err := loadWithEnv(t, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.JWTExchangeEnabled() {
		t.Fatal("JWTExchangeEnabled() = false, want true")
	}
}

func TestLoad_JWTExchange_NoFieldsSet(t *testing.T) {
	cfg, err := loadWithEnv(t, jwtBaseEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.JWTExchangeEnabled() {
		t.Fatal("JWTExchangeEnabled() = true with no exchange fields set, want false")
	}
}

func TestLoad_JWTExchange_PartialConfigRejected(t *testing.T) {
	cases := []struct {
		name    string
		setKeys []string
	}{
		{"only url", []string{"GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_URL"}},
		{"url+client_id", []string{
			"GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_URL",
			"GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_CLIENT_ID",
		}},
		{"missing audience", []string{
			"GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_URL",
			"GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_CLIENT_ID",
			"GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_CLIENT_SECRET",
		}},
		{"missing secret", []string{
			"GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_URL",
			"GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_CLIENT_ID",
			"GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_AUDIENCE",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := jwtBaseEnv()
			for _, k := range tc.setKeys {
				env[k] = "value"
			}
			_, err := loadWithEnv(t, env)
			if err == nil {
				t.Fatal("expected error on partial token-exchange config, got nil")
			}
			if !strings.Contains(err.Error(), "token exchange") {
				t.Errorf("error message should mention token exchange, got: %v", err)
			}
		})
	}
}

func TestLoad_JWTExchange_NotConsultedInAnonymousMode(t *testing.T) {
	// Setting exchange env vars while in anonymous mode should not trigger
	// the partial-config validator. They're inert outside jwt mode, so
	// leaving them set during a mode flip should not break startup.
	t.Setenv("BACKEND_URL", "http://backend:8081")
	t.Setenv("GATEWAY_AUTH_MODE", "anonymous")
	t.Setenv("GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_URL", "https://kc/.../token")
	// (deliberately leave the other three unset)

	if _, err := config.Load(); err != nil {
		t.Fatalf("Load in anonymous mode should ignore exchange env vars: %v", err)
	}
}
