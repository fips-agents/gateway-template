package routing

import (
	"fmt"
	"sort"
	"strings"
)

// Router resolves backend URLs from model names or explicit backend
// names. It supports exact and wildcard (prefix) pattern matching for
// models and case-insensitive lookup by backend name.
type Router struct {
	backends map[string]string // lowercase name -> URL
	routes   []route           // sorted: exact first, then longest wildcard prefix
	fallback string            // BACKEND_URL
}

type route struct {
	pattern     string // e.g. "gpt-4" (exact) or "claude-" (prefix, wildcard stripped)
	backendName string
	isWildcard  bool
}

// New constructs a Router. backends maps name->URL. routes maps
// model-pattern->backend-name; patterns ending in "*" are wildcards.
// Returns an error if a route references an unknown backend name.
func New(fallback string, backends map[string]string, routes map[string]string) (*Router, error) {
	normalized := make(map[string]string, len(backends))
	for name, url := range backends {
		normalized[strings.ToLower(name)] = url
	}

	sorted := make([]route, 0, len(routes))
	for pattern, backendName := range routes {
		bn := strings.ToLower(backendName)
		if _, ok := normalized[bn]; !ok {
			return nil, fmt.Errorf("route %q references unknown backend %q", pattern, backendName)
		}
		r := route{backendName: bn}
		if strings.HasSuffix(pattern, "*") {
			r.pattern = strings.TrimSuffix(pattern, "*")
			r.isWildcard = true
		} else {
			r.pattern = pattern
		}
		sorted = append(sorted, r)
	}

	// Exact matches first, then wildcards by descending prefix length.
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].isWildcard != sorted[j].isWildcard {
			return !sorted[i].isWildcard // exact before wildcard
		}
		if sorted[i].isWildcard {
			return len(sorted[i].pattern) > len(sorted[j].pattern)
		}
		return sorted[i].pattern < sorted[j].pattern // stable ordering for exact
	})

	return &Router{
		backends: normalized,
		routes:   sorted,
		fallback: fallback,
	}, nil
}

// ResolveModel resolves a model string to a backend URL. Checks exact
// matches first, then longest matching wildcard prefix, then fallback.
func (r *Router) ResolveModel(model string) string {
	if model == "" {
		return r.fallback
	}
	for _, rt := range r.routes {
		if !rt.isWildcard {
			if rt.pattern == model {
				return r.backends[rt.backendName]
			}
			continue
		}
		if strings.HasPrefix(model, rt.pattern) {
			return r.backends[rt.backendName]
		}
	}
	return r.fallback
}

// ResolveByName resolves a backend name (e.g. from an X-Backend header)
// to a URL. Case-insensitive. Unknown or empty names return fallback.
func (r *Router) ResolveByName(name string) string {
	if name == "" {
		return r.fallback
	}
	if url, ok := r.backends[strings.ToLower(name)]; ok {
		return url
	}
	return r.fallback
}

// Fallback returns the fallback URL.
func (r *Router) Fallback() string {
	return r.fallback
}

// Backends returns a copy of the backends map (safe for logging).
func (r *Router) Backends() map[string]string {
	cp := make(map[string]string, len(r.backends))
	for k, v := range r.backends {
		cp[k] = v
	}
	return cp
}

// HasBackends reports whether any non-fallback backends are configured.
func (r *Router) HasBackends() bool {
	return len(r.backends) > 0
}
