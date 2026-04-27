package auth

import (
	"log/slog"
	"net/http"
)

// Middleware returns an HTTP middleware that resolves caller identity using
// the supplied Authenticator and projects it onto canonical X-Auth-*
// headers. Inbound copies of the canonical headers are stripped before the
// strategy runs so clients cannot spoof identity.
//
// On ErrMissingProxyHeaders the middleware returns 503 — fail-closed, since
// a missing upstream identity in proxy mode means the deployment is
// misconfigured.
func Middleware(a Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stripCanonicalHeaders(r.Header)

			id, err := a.Authenticate(r)
			if err != nil {
				slog.Error("auth: identity resolution failed",
					"error", err,
					"path", r.URL.Path,
					"method", r.Method,
				)
				http.Error(w, `{"error":"upstream identity unavailable"}`, http.StatusServiceUnavailable)
				return
			}

			setCanonicalHeaders(r.Header, id)
			next.ServeHTTP(w, r)
		})
	}
}

// stripCanonicalHeaders removes any inbound copies of the canonical X-Auth-*
// headers so a client cannot pre-populate them.
func stripCanonicalHeaders(h http.Header) {
	for _, name := range CanonicalHeaders {
		h.Del(name)
	}
}

// setCanonicalHeaders writes the canonical headers from id onto h. Empty
// fields are still written (as empty strings) so downstream handlers see a
// uniform contract.
func setCanonicalHeaders(h http.Header, id Identity) {
	h.Set(HeaderSubject, id.Subject)
	h.Set(HeaderUser, id.User)
	h.Set(HeaderEmail, id.Email)
	h.Set(HeaderMode, id.Mode)
}
