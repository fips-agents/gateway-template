package config

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// RoutingConfig holds multi-backend routing configuration loaded from a
// YAML file. Backends maps logical names to base URLs; Routes maps
// model patterns to backend names.
type RoutingConfig struct {
	Backends map[string]string `yaml:"backends"`
	Routes   map[string]string `yaml:"routes"`
}

// scanBackendEnvVars collects BACKEND_<name>=<url> variables from the
// environment, excluding BACKEND_URL (the default fallback). Names are
// lowercased with the BACKEND_ prefix stripped:
//
//	BACKEND_CODE_AGENT=http://foo  ->  code_agent -> http://foo
func scanBackendEnvVars() map[string]string {
	backends := make(map[string]string)
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, "BACKEND_") {
			continue
		}
		if k == "BACKEND_URL" {
			continue
		}
		name := strings.ToLower(strings.TrimPrefix(k, "BACKEND_"))
		backends[name] = v
	}
	return backends
}

// loadRoutingConfig reads and parses a YAML routing config from path.
// Returns an error if the file cannot be read or contains invalid YAML.
// An empty file yields a zero-value RoutingConfig (not an error).
func loadRoutingConfig(path string) (*RoutingConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rc RoutingConfig
	if err := yaml.Unmarshal(data, &rc); err != nil {
		return nil, err
	}
	return &rc, nil
}
