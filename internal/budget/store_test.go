package budget

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestStore_AddAndUsage(t *testing.T) {
	s := NewStore()

	// Initial usage should be 0
	if got := s.Usage("acme"); got != 0 {
		t.Errorf("Usage(acme) initial = %d, want 0", got)
	}

	// Add tokens
	s.Add("acme", 100)
	if got := s.Usage("acme"); got != 100 {
		t.Errorf("Usage(acme) after Add(100) = %d, want 100", got)
	}

	// Add more tokens
	s.Add("acme", 50)
	if got := s.Usage("acme"); got != 150 {
		t.Errorf("Usage(acme) after Add(50) = %d, want 150", got)
	}
}

func TestStore_EmptyTenantIgnored(t *testing.T) {
	s := NewStore()

	// Should not panic
	s.Add("", 100)

	// Usage should remain 0
	if got := s.Usage(""); got != 0 {
		t.Errorf("Usage(\"\") = %d, want 0", got)
	}
}

func TestStore_ZeroTokensIgnored(t *testing.T) {
	s := NewStore()

	s.Add("acme", 100)
	initialUsage := s.Usage("acme")

	// Zero tokens should be ignored
	s.Add("acme", 0)
	if got := s.Usage("acme"); got != initialUsage {
		t.Errorf("Usage(acme) after Add(0) = %d, want %d", got, initialUsage)
	}

	// Negative tokens should be ignored
	s.Add("acme", -50)
	if got := s.Usage("acme"); got != initialUsage {
		t.Errorf("Usage(acme) after Add(-50) = %d, want %d", got, initialUsage)
	}
}

func TestStore_MultipleTenants(t *testing.T) {
	s := NewStore()

	s.Add("acme", 100)
	s.Add("beta", 200)

	if got := s.Usage("acme"); got != 100 {
		t.Errorf("Usage(acme) = %d, want 100", got)
	}
	if got := s.Usage("beta"); got != 200 {
		t.Errorf("Usage(beta) = %d, want 200", got)
	}

	// Adding to one tenant shouldn't affect the other
	s.Add("acme", 50)
	if got := s.Usage("acme"); got != 150 {
		t.Errorf("Usage(acme) = %d, want 150", got)
	}
	if got := s.Usage("beta"); got != 200 {
		t.Errorf("Usage(beta) = %d, want 200 (unchanged)", got)
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := NewStore()
	const goroutines = 100
	const tenant = "acme"

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			s.Add(tenant, 1)
		}()
	}

	wg.Wait()

	if got := s.Usage(tenant); got != goroutines {
		t.Errorf("Usage(%s) after %d concurrent Add(1) = %d, want %d", tenant, goroutines, got, goroutines)
	}
}

func TestConfig_BudgetFor(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
		tenant string
		want   int64
	}{
		{
			name: "per-tenant override",
			config: &Config{
				Default: 1000,
				Tenants: map[string]int64{"acme": 5000},
			},
			tenant: "acme",
			want:   5000,
		},
		{
			name: "default fallback",
			config: &Config{
				Default: 1000,
				Tenants: map[string]int64{},
			},
			tenant: "unknown",
			want:   1000,
		},
		{
			name: "zero means unlimited",
			config: &Config{
				Default: 0,
				Tenants: nil,
			},
			tenant: "any",
			want:   0,
		},
		{
			name: "nil tenants map uses default",
			config: &Config{
				Default: 2000,
				Tenants: nil,
			},
			tenant: "some-tenant",
			want:   2000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.BudgetFor(tt.tenant); got != tt.want {
				t.Errorf("BudgetFor(%q) = %d, want %d", tt.tenant, got, tt.want)
			}
		})
	}
}

func TestStore_CheckBudget(t *testing.T) {
	tests := []struct {
		name      string
		usage     int64
		budget    int64
		wantError bool
	}{
		{
			name:      "under budget",
			usage:     50,
			budget:    100,
			wantError: false,
		},
		{
			name:      "at budget",
			usage:     100,
			budget:    100,
			wantError: true,
		},
		{
			name:      "over budget",
			usage:     150,
			budget:    100,
			wantError: true,
		},
		{
			name:      "unlimited budget",
			usage:     9999,
			budget:    0,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStore()
			cfg := &Config{Default: tt.budget}
			tenant := "acme"

			s.Add(tenant, tt.usage)

			err := s.CheckBudget(tenant, cfg)
			if tt.wantError {
				if err == nil {
					t.Errorf("CheckBudget() = nil, want error")
				}
			} else {
				if err != nil {
					t.Errorf("CheckBudget() = %v, want nil", err)
				}
			}
		})
	}
}

func TestStore_CheckBudget_EmptyTenant(t *testing.T) {
	s := NewStore()
	cfg := &Config{Default: 100}

	// Empty tenant should never trigger budget check
	err := s.CheckBudget("", cfg)
	if err != nil {
		t.Errorf("CheckBudget(\"\", cfg) = %v, want nil", err)
	}
}

func TestStore_CheckBudget_NilConfig(t *testing.T) {
	s := NewStore()
	s.Add("acme", 1000)

	// Nil config should never trigger budget check
	err := s.CheckBudget("acme", nil)
	if err != nil {
		t.Errorf("CheckBudget(acme, nil) = %v, want nil", err)
	}
}

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "budget.yaml")

	yamlContent := `default: 1000
tenants:
  acme: 5000
  beta: 3000
`

	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil", err)
	}

	if cfg.Default != 1000 {
		t.Errorf("cfg.Default = %d, want 1000", cfg.Default)
	}

	if len(cfg.Tenants) != 2 {
		t.Errorf("len(cfg.Tenants) = %d, want 2", len(cfg.Tenants))
	}

	if cfg.Tenants["acme"] != 5000 {
		t.Errorf("cfg.Tenants[acme] = %d, want 5000", cfg.Tenants["acme"])
	}

	if cfg.Tenants["beta"] != 3000 {
		t.Errorf("cfg.Tenants[beta] = %d, want 3000", cfg.Tenants["beta"])
	}
}

func TestLoadConfig_InvalidPath(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/budget.yaml")
	if err == nil {
		t.Error("LoadConfig() error = nil, want error for nonexistent path")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "bad.yaml")

	if err := os.WriteFile(configPath, []byte("invalid: [yaml: content"), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("LoadConfig() error = nil, want error for invalid YAML")
	}
}
