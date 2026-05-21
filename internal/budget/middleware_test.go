package budget

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnforceMiddleware_RejectsOverBudget(t *testing.T) {
	store := NewStore()
	cfg := &Config{Default: 100}

	// Exceed budget
	store.Add("acme", 150)

	middleware := EnforceMiddleware(store, cfg)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusPaymentRequired {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusPaymentRequired)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "tenant budget exceeded") {
		t.Errorf("body = %q, want to contain 'tenant budget exceeded'", body)
	}
	if !strings.Contains(body, "acme") {
		t.Errorf("body = %q, want to contain tenant ID 'acme'", body)
	}
	if !strings.Contains(body, "150") {
		t.Errorf("body = %q, want to contain usage '150'", body)
	}
	if !strings.Contains(body, "100") {
		t.Errorf("body = %q, want to contain budget '100'", body)
	}

	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want 'application/json'", contentType)
	}
}

func TestEnforceMiddleware_AllowsUnderBudget(t *testing.T) {
	store := NewStore()
	cfg := &Config{Default: 100}

	// Below budget
	store.Add("acme", 50)

	handlerCalled := false
	middleware := EnforceMiddleware(store, cfg)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !handlerCalled {
		t.Error("expected handler to be called for tenant under budget")
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestEnforceMiddleware_SkipsNonChatPaths(t *testing.T) {
	store := NewStore()
	cfg := &Config{Default: 100}

	// Exceed budget
	store.Add("acme", 150)

	handlerCalled := false
	middleware := EnforceMiddleware(store, cfg)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/feedback", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !handlerCalled {
		t.Error("expected handler to be called for non-chat path")
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestEnforceMiddleware_SkipsGET(t *testing.T) {
	store := NewStore()
	cfg := &Config{Default: 100}

	// Exceed budget
	store.Add("acme", 150)

	handlerCalled := false
	middleware := EnforceMiddleware(store, cfg)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !handlerCalled {
		t.Error("expected handler to be called for GET request")
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestEnforceMiddleware_SkipsEmptyTenant(t *testing.T) {
	store := NewStore()
	cfg := &Config{Default: 100}

	// Store has no usage, but that doesn't matter without tenant ID
	handlerCalled := false
	middleware := EnforceMiddleware(store, cfg)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	// No X-Tenant-ID header
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !handlerCalled {
		t.Error("expected handler to be called when X-Tenant-ID is empty")
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestEnforceMiddleware_SkipsUnlimitedBudget(t *testing.T) {
	store := NewStore()
	cfg := &Config{Default: 0} // Unlimited budget

	// High usage
	store.Add("acme", 999999)

	handlerCalled := false
	middleware := EnforceMiddleware(store, cfg)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !handlerCalled {
		t.Error("expected handler to be called with unlimited budget (budget=0)")
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestEnforceMiddleware_PerTenantOverride(t *testing.T) {
	store := NewStore()
	cfg := &Config{
		Default: 100,
		Tenants: map[string]int64{"vip": 10000},
	}

	// VIP tenant is under their higher budget
	store.Add("vip", 5000)

	handlerCalled := false
	middleware := EnforceMiddleware(store, cfg)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Tenant-ID", "vip")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !handlerCalled {
		t.Error("expected handler to be called for VIP tenant under their higher budget")
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestEnforceMiddleware_MultipleRequests(t *testing.T) {
	store := NewStore()
	cfg := &Config{Default: 100}

	middleware := EnforceMiddleware(store, cfg)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request - tenant at 50 tokens (allowed)
	store.Add("acme", 50)
	req1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req1.Header.Set("X-Tenant-ID", "acme")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Errorf("first request status = %d, want %d", rec1.Code, http.StatusOK)
	}

	// Second request - tenant now at 150 tokens (rejected)
	store.Add("acme", 100)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req2.Header.Set("X-Tenant-ID", "acme")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusPaymentRequired {
		t.Errorf("second request status = %d, want %d", rec2.Code, http.StatusPaymentRequired)
	}
}
