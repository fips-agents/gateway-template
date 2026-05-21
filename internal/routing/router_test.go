package routing

import (
	"testing"
)

// testRouter builds a Router with two backends and several routes for
// use across test cases. Panics on construction error.
func testRouter(t *testing.T) *Router {
	t.Helper()
	backends := map[string]string{
		"openai":    "http://openai:8080",
		"anthropic": "http://anthropic:8080",
	}
	routes := map[string]string{
		"gpt-4":     "openai",
		"gpt-4o":    "openai",
		"claude-*":  "anthropic",
		"claude-3-*": "anthropic",
	}
	r, err := New("http://fallback:8080", backends, routes)
	if err != nil {
		t.Fatalf("testRouter: %v", err)
	}
	return r
}

func TestResolveModel(t *testing.T) {
	r := testRouter(t)

	tests := []struct {
		name  string
		model string
		want  string
	}{
		{
			name:  "exact match gpt-4",
			model: "gpt-4",
			want:  "http://openai:8080",
		},
		{
			name:  "exact match gpt-4o",
			model: "gpt-4o",
			want:  "http://openai:8080",
		},
		{
			name:  "wildcard matches claude-3-opus",
			model: "claude-3-opus",
			want:  "http://anthropic:8080",
		},
		{
			name:  "wildcard matches claude-2",
			model: "claude-2",
			want:  "http://anthropic:8080",
		},
		{
			name:  "longest wildcard wins: claude-3-sonnet matches claude-3-* over claude-*",
			model: "claude-3-sonnet",
			want:  "http://anthropic:8080",
		},
		{
			name:  "unknown model returns fallback",
			model: "llama-3",
			want:  "http://fallback:8080",
		},
		{
			name:  "empty model returns fallback",
			model: "",
			want:  "http://fallback:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.ResolveModel(tt.model)
			if got != tt.want {
				t.Errorf("ResolveModel(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestResolveModel_ExactBeatsWildcard(t *testing.T) {
	// "claude-3" is both an exact route and matches "claude-*".
	// Exact must win.
	backends := map[string]string{
		"exact-be":    "http://exact:8080",
		"wildcard-be": "http://wildcard:8080",
	}
	routes := map[string]string{
		"claude-3":  "exact-be",
		"claude-*":  "wildcard-be",
	}
	r, err := New("http://fallback:8080", backends, routes)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := r.ResolveModel("claude-3")
	if got != "http://exact:8080" {
		t.Errorf("exact match should beat wildcard, got %q", got)
	}

	// "claude-3-opus" should still hit the wildcard.
	got = r.ResolveModel("claude-3-opus")
	if got != "http://wildcard:8080" {
		t.Errorf("non-exact should hit wildcard, got %q", got)
	}
}

func TestResolveModel_LongestWildcardWins(t *testing.T) {
	backends := map[string]string{
		"short": "http://short:8080",
		"long":  "http://long:8080",
	}
	routes := map[string]string{
		"claude-*":   "short",
		"claude-3-*": "long",
	}
	r, err := New("http://fallback:8080", backends, routes)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		model string
		want  string
	}{
		{"claude-3-opus", "http://long:8080"},
		{"claude-3-sonnet", "http://long:8080"},
		{"claude-2", "http://short:8080"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := r.ResolveModel(tt.model)
			if got != tt.want {
				t.Errorf("ResolveModel(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestResolveByName(t *testing.T) {
	r := testRouter(t)

	tests := []struct {
		name string
		key  string
		want string
	}{
		{
			name: "known backend lowercase",
			key:  "openai",
			want: "http://openai:8080",
		},
		{
			name: "known backend uppercase",
			key:  "OPENAI",
			want: "http://openai:8080",
		},
		{
			name: "known backend mixed case",
			key:  "Anthropic",
			want: "http://anthropic:8080",
		},
		{
			name: "unknown backend returns fallback",
			key:  "mistral",
			want: "http://fallback:8080",
		},
		{
			name: "empty name returns fallback",
			key:  "",
			want: "http://fallback:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.ResolveByName(tt.key)
			if got != tt.want {
				t.Errorf("ResolveByName(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestNew_UnknownBackendError(t *testing.T) {
	backends := map[string]string{
		"openai": "http://openai:8080",
	}
	routes := map[string]string{
		"gpt-4":    "openai",
		"claude-*": "anthropic", // not in backends
	}
	_, err := New("http://fallback:8080", backends, routes)
	if err == nil {
		t.Fatal("expected error for route referencing unknown backend")
	}
}

func TestNew_FallbackOnly(t *testing.T) {
	r, err := New("http://fallback:8080", nil, nil)
	if err != nil {
		t.Fatalf("New with nil maps: %v", err)
	}
	if got := r.ResolveModel("anything"); got != "http://fallback:8080" {
		t.Errorf("ResolveModel on fallback-only router = %q, want fallback", got)
	}
	if got := r.ResolveByName("anything"); got != "http://fallback:8080" {
		t.Errorf("ResolveByName on fallback-only router = %q, want fallback", got)
	}
	if r.HasBackends() {
		t.Error("HasBackends should be false with no backends")
	}
}

func TestNew_EmptyMaps(t *testing.T) {
	r, err := New("http://fallback:8080", map[string]string{}, map[string]string{})
	if err != nil {
		t.Fatalf("New with empty maps: %v", err)
	}
	if got := r.Fallback(); got != "http://fallback:8080" {
		t.Errorf("Fallback() = %q, want %q", got, "http://fallback:8080")
	}
}

func TestFallback(t *testing.T) {
	r := testRouter(t)
	if got := r.Fallback(); got != "http://fallback:8080" {
		t.Errorf("Fallback() = %q, want %q", got, "http://fallback:8080")
	}
}

func TestBackends_ReturnsCopy(t *testing.T) {
	r := testRouter(t)

	cp := r.Backends()
	if len(cp) != 2 {
		t.Fatalf("Backends() returned %d entries, want 2", len(cp))
	}

	// Mutating the copy must not affect the router.
	cp["injected"] = "http://evil:8080"
	if r.ResolveByName("injected") != "http://fallback:8080" {
		t.Error("mutating Backends() copy affected router state")
	}
	if len(r.Backends()) != 2 {
		t.Error("Backends() length changed after external mutation")
	}
}

func TestHasBackends(t *testing.T) {
	noBackends, _ := New("http://fallback:8080", nil, nil)
	if noBackends.HasBackends() {
		t.Error("HasBackends should be false with nil backends")
	}

	withBackends := testRouter(t)
	if !withBackends.HasBackends() {
		t.Error("HasBackends should be true with configured backends")
	}
}

func TestNew_BackendNameNormalization(t *testing.T) {
	backends := map[string]string{
		"OpenAI": "http://openai:8080",
	}
	routes := map[string]string{
		"gpt-4": "OPENAI", // different case than backends key
	}
	r, err := New("http://fallback:8080", backends, routes)
	if err != nil {
		t.Fatalf("New should normalize case: %v", err)
	}
	if got := r.ResolveModel("gpt-4"); got != "http://openai:8080" {
		t.Errorf("case-insensitive route lookup failed, got %q", got)
	}
}
