// Package budget tracks per-tenant token usage and enforces budget limits.
// Usage is accumulated in-memory from chat response data and is best-effort
// (resets on gateway restart). The agent's GET /v1/sessions/{id}/usage
// endpoint remains the authoritative source for historical cost.
package budget

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

var ErrBudgetExceeded = errors.New("budget: tenant budget exceeded")

type Store struct {
	mu     sync.Mutex
	usage  map[string]*atomic.Int64
}

func NewStore() *Store {
	return &Store{usage: make(map[string]*atomic.Int64)}
}

func (s *Store) Add(tenant string, tokens int64) {
	if tenant == "" || tokens <= 0 {
		return
	}
	s.counter(tenant).Add(tokens)
}

func (s *Store) Usage(tenant string) int64 {
	if tenant == "" {
		return 0
	}
	s.mu.Lock()
	c, ok := s.usage[tenant]
	s.mu.Unlock()
	if !ok {
		return 0
	}
	return c.Load()
}

func (s *Store) CheckBudget(tenant string, cfg *Config) error {
	if tenant == "" || cfg == nil {
		return nil
	}
	limit := cfg.BudgetFor(tenant)
	if limit <= 0 {
		return nil
	}
	used := s.Usage(tenant)
	if used >= limit {
		return fmt.Errorf("%w: tenant %q used %d of %d tokens", ErrBudgetExceeded, tenant, used, limit)
	}
	return nil
}

func (s *Store) counter(tenant string) *atomic.Int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.usage[tenant]
	if !ok {
		c = &atomic.Int64{}
		s.usage[tenant] = c
	}
	return c
}

// Config holds per-tenant token budgets. A budget of 0 means unlimited.
type Config struct {
	Default int64            `yaml:"default"`
	Tenants map[string]int64 `yaml:"tenants"`
}

func (c *Config) BudgetFor(tenant string) int64 {
	if c.Tenants != nil {
		if v, ok := c.Tenants[tenant]; ok {
			return v
		}
	}
	return c.Default
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("budget: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("budget: invalid YAML: %w", err)
	}
	return &cfg, nil
}
