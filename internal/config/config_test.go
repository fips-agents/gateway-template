package config_test

import (
	"strings"
	"testing"
	"time"

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

func TestLoad_PlatformURL_UnsetMeansLegacyFanout(t *testing.T) {
	cfg, err := loadWithEnv(t, map[string]string{"BACKEND_URL": "http://agent:8080"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PlatformURL != "" {
		t.Errorf("PlatformURL = %q, want empty", cfg.PlatformURL)
	}
	if got := cfg.FeedbackTargetURL(); got != "http://agent:8080" {
		t.Errorf("FeedbackTargetURL = %q, want backend URL", got)
	}
	if got := cfg.SessionsTargetURL(); got != "http://agent:8080" {
		t.Errorf("SessionsTargetURL = %q, want backend URL", got)
	}
	if got := cfg.TracesTargetURL(); got != "http://agent:8080" {
		t.Errorf("TracesTargetURL = %q, want backend URL", got)
	}
}

func TestLoad_PlatformURL_SetRoutesAllPrefixes(t *testing.T) {
	cfg, err := loadWithEnv(t, map[string]string{
		"BACKEND_URL":          "http://agent:8080",
		"GATEWAY_PLATFORM_URL": "http://platform:8080",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PlatformURL != "http://platform:8080" {
		t.Errorf("PlatformURL = %q, want platform URL", cfg.PlatformURL)
	}
	for name, got := range map[string]string{
		"feedback": cfg.FeedbackTargetURL(),
		"sessions": cfg.SessionsTargetURL(),
		"traces":   cfg.TracesTargetURL(),
	} {
		if got != "http://platform:8080" {
			t.Errorf("%sTargetURL = %q, want platform URL", name, got)
		}
	}
}

func TestLoad_PlatformURL_PerPrefixOptOut(t *testing.T) {
	cfg, err := loadWithEnv(t, map[string]string{
		"BACKEND_URL":                     "http://agent:8080",
		"GATEWAY_PLATFORM_URL":            "http://platform:8080",
		"GATEWAY_PLATFORM_ROUTE_SESSIONS": "false",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.SessionsTargetURL(); got != "http://agent:8080" {
		t.Errorf("SessionsTargetURL = %q, want backend (opted out)", got)
	}
	if got := cfg.FeedbackTargetURL(); got != "http://platform:8080" {
		t.Errorf("FeedbackTargetURL = %q, want platform (still opted in)", got)
	}
	if got := cfg.TracesTargetURL(); got != "http://platform:8080" {
		t.Errorf("TracesTargetURL = %q, want platform (still opted in)", got)
	}
}

func TestLoad_PlatformURL_TrailingSlashTrimmed(t *testing.T) {
	cfg, err := loadWithEnv(t, map[string]string{
		"BACKEND_URL":          "http://agent:8080",
		"GATEWAY_PLATFORM_URL": "http://platform:8080/",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PlatformURL != "http://platform:8080" {
		t.Errorf("PlatformURL = %q, want trailing slash trimmed", cfg.PlatformURL)
	}
}

func TestLoad_PlatformToggle_NoOpWhenURLUnset(t *testing.T) {
	cfg, err := loadWithEnv(t, map[string]string{
		"BACKEND_URL":                     "http://agent:8080",
		"GATEWAY_PLATFORM_ROUTE_SESSIONS": "true",
		"GATEWAY_PLATFORM_ROUTE_TRACES":   "true",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Toggles set without PLATFORM_URL must still resolve to backend —
	// they're cheap config knobs, not failure modes.
	if got := cfg.SessionsTargetURL(); got != "http://agent:8080" {
		t.Errorf("SessionsTargetURL = %q, want backend (URL unset)", got)
	}
}

func TestLoad_JWKSRefreshRateLimit_DefaultZero(t *testing.T) {
	cfg, err := loadWithEnv(t, jwtBaseEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AuthJWTJWKSRefreshRateLimit != 0 {
		t.Errorf("AuthJWTJWKSRefreshRateLimit = %v, want 0 (keyfunc default)", cfg.AuthJWTJWKSRefreshRateLimit)
	}
}

func TestLoad_JWKSRefreshRateLimit_Parsed(t *testing.T) {
	env := jwtBaseEnv()
	env["GATEWAY_AUTH_JWT_JWKS_REFRESH_RATE_LIMIT"] = "30s"

	cfg, err := loadWithEnv(t, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.AuthJWTJWKSRefreshRateLimit, 30*time.Second; got != want {
		t.Errorf("AuthJWTJWKSRefreshRateLimit = %v, want %v", got, want)
	}
}

func TestLoad_JWKSRefreshRateLimit_Invalid(t *testing.T) {
	env := jwtBaseEnv()
	env["GATEWAY_AUTH_JWT_JWKS_REFRESH_RATE_LIMIT"] = "thirty seconds"

	_, err := loadWithEnv(t, env)
	if err == nil {
		t.Fatal("expected error on unparseable duration, got nil")
	}
	if !strings.Contains(err.Error(), "GATEWAY_AUTH_JWT_JWKS_REFRESH_RATE_LIMIT") {
		t.Errorf("error should reference the env var, got: %v", err)
	}
}

func TestLoad_JWKSRefreshRateLimit_Negative(t *testing.T) {
	env := jwtBaseEnv()
	env["GATEWAY_AUTH_JWT_JWKS_REFRESH_RATE_LIMIT"] = "-5s"

	_, err := loadWithEnv(t, env)
	if err == nil {
		t.Fatal("expected error on negative duration, got nil")
	}
}

func TestLoad_JWKSRefreshRateLimit_NotConsultedInAnonymousMode(t *testing.T) {
	t.Setenv("BACKEND_URL", "http://backend:8081")
	t.Setenv("GATEWAY_AUTH_MODE", "anonymous")
	t.Setenv("GATEWAY_AUTH_JWT_JWKS_REFRESH_RATE_LIMIT", "garbage")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load in anonymous mode should ignore the JWKS rate-limit var: %v", err)
	}
	if cfg.AuthJWTJWKSRefreshRateLimit != 0 {
		t.Errorf("AuthJWTJWKSRefreshRateLimit = %v, want 0 in anonymous mode", cfg.AuthJWTJWKSRefreshRateLimit)
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

func TestLoad_TenantConfig(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		validate func(t *testing.T, cfg *config.Config)
	}{
		{
			name: "tenant claim parsed in jwt mode",
			envVars: map[string]string{
				"GATEWAY_AUTH_JWT_TENANT_CLAIM": "org_id",
			},
			validate: func(t *testing.T, cfg *config.Config) {
				if cfg.AuthJWTTenantClaim != "org_id" {
					t.Errorf("AuthJWTTenantClaim = %q, want %q", cfg.AuthJWTTenantClaim, "org_id")
				}
			},
		},
		{
			name:    "tenant claim default empty",
			envVars: map[string]string{},
			validate: func(t *testing.T, cfg *config.Config) {
				if cfg.AuthJWTTenantClaim != "" {
					t.Errorf("AuthJWTTenantClaim = %q, want empty", cfg.AuthJWTTenantClaim)
				}
			},
		},
		{
			name: "proxy tenant header parsed",
			envVars: map[string]string{
				"GATEWAY_AUTH_MODE":               "proxy",
				"GATEWAY_AUTH_PROXY_TENANT_HEADER": "X-Org-Id",
			},
			validate: func(t *testing.T, cfg *config.Config) {
				if cfg.AuthProxyTenantHeader != "X-Org-Id" {
					t.Errorf("AuthProxyTenantHeader = %q, want %q", cfg.AuthProxyTenantHeader, "X-Org-Id")
				}
			},
		},
		{
			name:    "tenant enforce default false",
			envVars: map[string]string{},
			validate: func(t *testing.T, cfg *config.Config) {
				if cfg.TenantEnforce {
					t.Error("TenantEnforce = true, want false")
				}
			},
		},
		{
			name: "tenant enforce true",
			envVars: map[string]string{
				"GATEWAY_TENANT_ENFORCE": "true",
			},
			validate: func(t *testing.T, cfg *config.Config) {
				if !cfg.TenantEnforce {
					t.Error("TenantEnforce = false, want true")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := jwtBaseEnv()
			for k, v := range tc.envVars {
				env[k] = v
			}

			cfg, err := loadWithEnv(t, env)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			tc.validate(t, cfg)
		})
	}
}

func TestLoad_TenantRateLimit(t *testing.T) {
	tests := []struct {
		name      string
		rps       string
		burst     string
		wantError bool
		validate  func(t *testing.T, cfg *config.Config)
	}{
		{
			name:  "both RPS and burst set",
			rps:   "10",
			burst: "20",
			validate: func(t *testing.T, cfg *config.Config) {
				if cfg.TenantRateLimitRPS != 10 {
					t.Errorf("TenantRateLimitRPS = %d, want 10", cfg.TenantRateLimitRPS)
				}
				if cfg.TenantRateLimitBurst != 20 {
					t.Errorf("TenantRateLimitBurst = %d, want 20", cfg.TenantRateLimitBurst)
				}
				if !cfg.TenantRateLimitEnabled() {
					t.Error("TenantRateLimitEnabled() = false, want true")
				}
			},
		},
		{
			name:      "burst without RPS rejected",
			rps:       "0",
			burst:     "5",
			wantError: true,
		},
		{
			name:      "burst less than RPS rejected",
			rps:       "10",
			burst:     "5",
			wantError: true,
		},
		{
			name:  "disabled by default",
			rps:   "",
			burst: "",
			validate: func(t *testing.T, cfg *config.Config) {
				if cfg.TenantRateLimitEnabled() {
					t.Error("TenantRateLimitEnabled() = true, want false")
				}
			},
		},
		{
			name:  "enabled when both set",
			rps:   "10",
			burst: "20",
			validate: func(t *testing.T, cfg *config.Config) {
				if !cfg.TenantRateLimitEnabled() {
					t.Error("TenantRateLimitEnabled() = false, want true")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := jwtBaseEnv()
			if tc.rps != "" {
				env["GATEWAY_TENANT_RATE_LIMIT_RPS"] = tc.rps
			}
			if tc.burst != "" {
				env["GATEWAY_TENANT_RATE_LIMIT_BURST"] = tc.burst
			}

			cfg, err := loadWithEnv(t, env)

			if tc.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if tc.validate != nil {
				tc.validate(t, cfg)
			}
		})
	}
}
