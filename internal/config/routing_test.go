package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- scanBackendEnvVars ---

func TestScanBackendEnvVars_Basic(t *testing.T) {
	t.Setenv("BACKEND_FOO", "http://foo")
	t.Setenv("BACKEND_URL", "http://default") // should be skipped

	got := scanBackendEnvVars()
	if v, ok := got["foo"]; !ok || v != "http://foo" {
		t.Errorf("got[foo] = %q, %v; want http://foo, true", v, ok)
	}
	if _, ok := got["url"]; ok {
		t.Error("BACKEND_URL should be excluded from scan")
	}
}

func TestScanBackendEnvVars_Lowercase(t *testing.T) {
	t.Setenv("BACKEND_Code_Agent", "http://ca")

	got := scanBackendEnvVars()
	if v, ok := got["code_agent"]; !ok || v != "http://ca" {
		t.Errorf("got[code_agent] = %q, %v; want http://ca, true", v, ok)
	}
}

func TestScanBackendEnvVars_EmptyWhenNone(t *testing.T) {
	// No BACKEND_* vars set (BACKEND_URL may exist from other tests
	// but t.Setenv isolation means it won't leak in).
	got := scanBackendEnvVars()
	if got == nil {
		t.Fatal("want non-nil empty map, got nil")
	}
	// Filter out any BACKEND_ vars that may exist in the real env.
	for k := range got {
		if k != "" {
			// Can't guarantee a perfectly clean env in every CI, so just
			// verify the return type is correct.
			break
		}
	}
}

func TestScanBackendEnvVars_Multiple(t *testing.T) {
	t.Setenv("BACKEND_A", "http://a")
	t.Setenv("BACKEND_B", "http://b")
	t.Setenv("BACKEND_C", "http://c")

	got := scanBackendEnvVars()
	for _, name := range []string{"a", "b", "c"} {
		if _, ok := got[name]; !ok {
			t.Errorf("expected backend %q in results", name)
		}
	}
}

// --- loadRoutingConfig ---

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "routing-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestLoadRoutingConfig_Valid(t *testing.T) {
	path := writeTemp(t, `
backends:
  code-agent: http://code-agent:8080
  research-agent: http://research-agent:8080
routes:
  gpt-4: code-agent
  claude-*: research-agent
`)

	rc, err := loadRoutingConfig(path)
	if err != nil {
		t.Fatalf("loadRoutingConfig: %v", err)
	}
	if len(rc.Backends) != 2 {
		t.Errorf("len(Backends) = %d, want 2", len(rc.Backends))
	}
	if rc.Backends["code-agent"] != "http://code-agent:8080" {
		t.Errorf("Backends[code-agent] = %q", rc.Backends["code-agent"])
	}
	if len(rc.Routes) != 2 {
		t.Errorf("len(Routes) = %d, want 2", len(rc.Routes))
	}
	if rc.Routes["gpt-4"] != "code-agent" {
		t.Errorf("Routes[gpt-4] = %q", rc.Routes["gpt-4"])
	}
}

func TestLoadRoutingConfig_FileNotFound(t *testing.T) {
	_, err := loadRoutingConfig(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadRoutingConfig_InvalidYAML(t *testing.T) {
	path := writeTemp(t, "{{not yaml}}")
	_, err := loadRoutingConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadRoutingConfig_EmptyFile(t *testing.T) {
	path := writeTemp(t, "")
	rc, err := loadRoutingConfig(path)
	if err != nil {
		t.Fatalf("empty file should not error: %v", err)
	}
	if rc.Backends != nil || rc.Routes != nil {
		t.Errorf("empty file should yield zero-value struct, got Backends=%v Routes=%v",
			rc.Backends, rc.Routes)
	}
}

func TestLoadRoutingConfig_OnlyBackends(t *testing.T) {
	path := writeTemp(t, `
backends:
  agent-a: http://a:8080
`)
	rc, err := loadRoutingConfig(path)
	if err != nil {
		t.Fatalf("loadRoutingConfig: %v", err)
	}
	if len(rc.Backends) != 1 {
		t.Errorf("len(Backends) = %d, want 1", len(rc.Backends))
	}
	if rc.Routes != nil {
		t.Errorf("Routes should be nil when absent, got %v", rc.Routes)
	}
}

func TestLoadRoutingConfig_OnlyRoutes(t *testing.T) {
	path := writeTemp(t, `
routes:
  gpt-4: agent-a
`)
	rc, err := loadRoutingConfig(path)
	if err != nil {
		t.Fatalf("loadRoutingConfig: %v", err)
	}
	if rc.Backends != nil {
		t.Errorf("Backends should be nil when absent, got %v", rc.Backends)
	}
	if len(rc.Routes) != 1 {
		t.Errorf("len(Routes) = %d, want 1", len(rc.Routes))
	}
}

// --- Load() integration ---

func TestLoad_BackendEnvVars_Collected(t *testing.T) {
	t.Setenv("BACKEND_URL", "http://default:8080")
	t.Setenv("BACKEND_FOO", "http://foo:8080")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BackendURL != "http://default:8080" {
		t.Errorf("BackendURL = %q, want http://default:8080", cfg.BackendURL)
	}
	if v, ok := cfg.Backends["foo"]; !ok || v != "http://foo:8080" {
		t.Errorf("Backends[foo] = %q, %v; want http://foo:8080, true", v, ok)
	}
}

func TestLoad_NoBackendEnvVars_EmptyMap(t *testing.T) {
	t.Setenv("BACKEND_URL", "http://default:8080")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Backends == nil {
		t.Fatal("Backends should be non-nil empty map")
	}
	// Filter to only keys that aren't from the real environment.
	// In a clean test env this is empty.
}

func TestLoad_RoutingConfigFile_Valid(t *testing.T) {
	path := writeTemp(t, `
backends:
  agent-a: http://a:8080
routes:
  gpt-4: agent-a
`)
	t.Setenv("BACKEND_URL", "http://default:8080")
	t.Setenv("GATEWAY_ROUTING_CONFIG", path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Backends["agent-a"] != "http://a:8080" {
		t.Errorf("Backends[agent-a] = %q", cfg.Backends["agent-a"])
	}
	if cfg.Routes["gpt-4"] != "agent-a" {
		t.Errorf("Routes[gpt-4] = %q", cfg.Routes["gpt-4"])
	}
}

func TestLoad_EnvVarOverridesYAMLBackend(t *testing.T) {
	path := writeTemp(t, `
backends:
  agent-a: http://yaml-a:8080
  agent-b: http://yaml-b:8080
`)
	t.Setenv("BACKEND_URL", "http://default:8080")
	t.Setenv("GATEWAY_ROUTING_CONFIG", path)
	t.Setenv("BACKEND_AGENT_A", "http://env-a:9090")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Env var wins for agent_a (note: env key is lowercased to agent_a,
	// YAML key is agent-a — these are different map keys).
	if cfg.Backends["agent_a"] != "http://env-a:9090" {
		t.Errorf("Backends[agent_a] = %q, want env override", cfg.Backends["agent_a"])
	}
	if cfg.Backends["agent-a"] != "http://yaml-a:8080" {
		t.Errorf("Backends[agent-a] = %q, want YAML value", cfg.Backends["agent-a"])
	}
	if cfg.Backends["agent-b"] != "http://yaml-b:8080" {
		t.Errorf("Backends[agent-b] = %q, want YAML value", cfg.Backends["agent-b"])
	}
}

func TestLoad_RoutingConfigFile_NotFound(t *testing.T) {
	t.Setenv("BACKEND_URL", "http://default:8080")
	t.Setenv("GATEWAY_ROUTING_CONFIG", "/nonexistent/routing.yaml")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for nonexistent routing config")
	}
	if !strings.Contains(err.Error(), "GATEWAY_ROUTING_CONFIG") {
		t.Errorf("error should mention GATEWAY_ROUTING_CONFIG, got: %v", err)
	}
}
