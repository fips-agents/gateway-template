package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/fips-agents/gateway-template/internal/middleware"
)

// UUID v4 regex pattern: 8-4-4-4-12 hex digits.
var uuidv4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestRequestID_GeneratesWhenMissing(t *testing.T) {
	var capturedID string
	handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = r.Header.Get("X-Request-ID")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify the downstream handler saw a non-empty X-Request-ID.
	if capturedID == "" {
		t.Error("downstream handler should see non-empty X-Request-ID when none was provided")
	}

	// Verify the response has the same X-Request-ID.
	responseID := rec.Header().Get("X-Request-ID")
	if responseID == "" {
		t.Error("response should have X-Request-ID header")
	}

	if capturedID != responseID {
		t.Errorf("request ID mismatch: handler saw %q, response has %q", capturedID, responseID)
	}

	// Verify the format matches UUID v4.
	if !uuidv4Pattern.MatchString(capturedID) {
		t.Errorf("generated ID %q does not match UUID v4 format", capturedID)
	}
}

func TestRequestID_PreservesWhenPresent(t *testing.T) {
	customID := "my-custom-id"
	var capturedID string

	handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = r.Header.Get("X-Request-ID")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("X-Request-ID", customID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify the downstream handler sees the custom ID.
	if capturedID != customID {
		t.Errorf("downstream handler should see X-Request-ID=%q, got %q", customID, capturedID)
	}

	// Verify the response echoes the custom ID.
	responseID := rec.Header().Get("X-Request-ID")
	if responseID != customID {
		t.Errorf("response should have X-Request-ID=%q, got %q", customID, responseID)
	}
}

func TestRequestID_EchoesOnResponse(t *testing.T) {
	handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	responseID := rec.Header().Get("X-Request-ID")
	if responseID == "" {
		t.Error("response should have X-Request-ID header set before handler runs")
	}

	// Verify it matches UUID v4 format.
	if !uuidv4Pattern.MatchString(responseID) {
		t.Errorf("response ID %q does not match UUID v4 format", responseID)
	}
}

func TestRequestID_UUIDFormat(t *testing.T) {
	handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const numRequests = 10
	ids := make(map[string]bool, numRequests)

	for i := 0; i < numRequests; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		id := rec.Header().Get("X-Request-ID")

		// Verify UUID v4 format.
		if !uuidv4Pattern.MatchString(id) {
			t.Errorf("request %d: ID %q does not match UUID v4 format", i+1, id)
		}

		// Verify uniqueness.
		if ids[id] {
			t.Errorf("request %d: duplicate ID %q", i+1, id)
		}
		ids[id] = true
	}

	// Verify we got the expected number of unique IDs.
	if len(ids) != numRequests {
		t.Errorf("expected %d unique IDs, got %d", numRequests, len(ids))
	}
}
