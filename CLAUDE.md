# CLAUDE.md

## Project Overview

Go HTTP reverse proxy that provides an OpenAI-compatible interface in front of AI agent backends. Accepts `/v1/chat/completions` requests (sync and streaming), proxies them to a configurable backend, and handles SSE lifecycle management.

## Development Commands

```bash
# Build the binary
make build

# Run locally (backend URL required)
BACKEND_URL=http://localhost:8081 make run

# Run tests
make test

# Run linter
make lint

# Build container image
make image-build
```

## Architecture

This is a thin reverse proxy -- minimal external dependencies. The core proxy and auth middleware use the Go standard library only; the `jwt` auth mode adds two stdlib-crypto-only third-party deps (`github.com/golang-jwt/jwt/v5`, `github.com/MicahParks/keyfunc/v3`) to validate inbound bearer tokens against a JWKS endpoint. The optional RFC 8693 token-exchange path on top of `jwt` mode adds no new third-party deps — it speaks plain `application/x-www-form-urlencoded` to the IdP's token endpoint via stdlib `net/http`. Both crypto libraries route through Go's FIPS-certified crypto module when the binary is built with FIPS enabled.

```
Client --> Gateway (:8080) --> Backend Agent
             |
             +-- /v1/chat/completions  (POST, sync + SSE streaming, propagates X-Trace-Id)
             +-- /v1/feedback          (POST/GET, pass-through, forwards auth headers)
             +-- /v1/feedback/{id}     (PATCH, in-place edit of an existing record)
             +-- /v1/feedback/stats    (GET, pass-through)
             +-- /v1/agent-info        (GET, pass-through to backend)
             +-- /healthz              (GET, liveness)
             +-- /readyz               (GET, checks backend)
             +-- /.well-known/agent.json (GET, agent card)
```

Key packages:
- `cmd/server/` -- entry point, wiring, graceful shutdown
- `internal/config/` -- environment variable parsing
- `internal/handler/` -- HTTP handlers for each route
- `internal/middleware/` -- request logging (structured, skips health probes)
- `internal/auth/` -- inbound auth strategies (`anonymous`, `proxy`, `jwt`) + middleware that strips spoofed `X-Auth-*` headers and projects canonical identity onto the request. `jwt` mode validates `Authorization: Bearer <token>` against a configured JWKS endpoint (cached by `kid`), enforces `iss`/`aud`/`exp`/`nbf`, and maps invalid tokens → 401 vs. JWKS-cold-cache failures → 503. Optional RFC 8693 token exchange (`exchange.go`) swaps the inbound user JWT for a downstream-audienced token before the handler runs; `Identity.BearerToken` carries the swapped value, the middleware projects it as `Authorization: Bearer <token>` on the request (or strips Authorization entirely when no swap is configured), and handlers forward it to the backend
- `internal/proxy/` -- SSE relay logic

## Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `BACKEND_URL` | Yes | -- | Backend agent base URL |
| `PORT` | No | `8080` | Listen port |
| `AGENT_NAME` | No | `gateway-template` | Name in agent card |
| `AGENT_VERSION` | No | `0.1.0` | Version in agent card |
| `LOG_REQUESTS` | No | `false` | Enable structured request logging |
| `GATEWAY_AUTH_MODE` | No | `anonymous` | Inbound auth strategy: `anonymous`, `proxy`, or `jwt` |
| `GATEWAY_AUTH_PROXY_USER_HEADER` | No | `X-Forwarded-User` | (`proxy` mode) upstream-validated username header |
| `GATEWAY_AUTH_PROXY_EMAIL_HEADER` | No | `X-Forwarded-Email` | (`proxy` mode) upstream-validated email header |
| `GATEWAY_AUTH_JWT_JWKS_URL` | jwt mode | -- | (`jwt` mode) JWKS endpoint URL |
| `GATEWAY_AUTH_JWT_ISSUER` | jwt mode | -- | (`jwt` mode) expected `iss` claim |
| `GATEWAY_AUTH_JWT_AUDIENCE` | jwt mode | -- | (`jwt` mode) expected `aud` claim |
| `GATEWAY_AUTH_JWT_SUBJECT_CLAIM` | No | `sub` | (`jwt` mode) claim → `X-Auth-Subject` |
| `GATEWAY_AUTH_JWT_USER_CLAIM` | No | `preferred_username` | (`jwt` mode) claim → `X-Auth-User` |
| `GATEWAY_AUTH_JWT_EMAIL_CLAIM` | No | `email` | (`jwt` mode) claim → `X-Auth-Email` |
| `GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_URL` | exchange | -- | (`jwt` mode) RFC 8693 token endpoint; setting all four required exchange vars enables the swap |
| `GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_CLIENT_ID` | exchange | -- | (`jwt` mode) gateway service-account client ID |
| `GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_CLIENT_SECRET` | exchange | -- | (`jwt` mode) gateway service-account client secret |
| `GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_AUDIENCE` | exchange | -- | (`jwt` mode) downstream audience the swapped token targets |
| `GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_SCOPE` | No | -- | (`jwt` mode) optional space-separated scope set requested on the swap |

## Auth contract

The gateway emits canonical `X-Auth-Subject` / `X-Auth-User` / `X-Auth-Email` / `X-Auth-Mode` headers to the backend on every `/v1/*` request. Inbound copies are stripped before the strategy runs so clients cannot spoof identity. Header names match Kagenti's AuthBridge JWT claim shape, so an AuthBridge token and a fipsagents-issued token resolve onto the same canonical contract. `proxy` mode fails closed with 503 when the upstream user header is missing. `jwt` mode validates `Authorization: Bearer <token>` against a JWKS endpoint, returning 401 on bad/expired/wrong-issuer/wrong-audience tokens and 503 only when the JWKS endpoint is unreachable AND the cache is cold.

`Authorization` itself is part of the contract: by default the middleware strips inbound Authorization before the handler runs (so the gateway never forwards a raw user JWT). When the four `GATEWAY_AUTH_JWT_TOKEN_EXCHANGE_*` env vars are set together, the gateway performs an RFC 8693 swap of the inbound token (subject_token) for a downstream-audienced token (the `audience` form parameter), caches it for `min(expires_in − 30s, 5min)` keyed by `sha256(inbound-token)`, and forwards it as `Authorization: Bearer <swapped>` to the backend. Exchange failures fail closed with 503. Partial token-exchange config (some required vars set, others not) is rejected at startup so a typo cannot silently disable the swap.

### Live integration test

`internal/auth/jwt_keycloak_integration_test.go` is build-tagged `integration` and exercises `jwt` mode against a real Keycloak. To run:

```bash
eval "$(scripts/keycloak-test-setup.sh)"   # bootstraps a clean realm/client/user
go test -tags integration -run TestJWTAuth_LiveKeycloak ./internal/auth/...
```

The setup script targets the keycloak operator instance in the `keycloak` namespace of the `mcp-rhoai` cluster (overridable via `KC_CONTEXT` / `KC_NAMESPACE`). Without `KC_INTEGRATION=1` the tests skip, so CI without cluster access stays green.

## Deployment

Deploy to OpenShift using the Helm chart in `chart/`:

```bash
helm upgrade --install my-gateway chart/ \
  -n my-namespace \
  --set config.BACKEND_URL=http://my-agent:8080 \
  --set image.repository=image-registry.openshift-image-registry.svc:5000/my-namespace/gateway-template
```

## Sentinel Values

This is a template repository. The string `gateway-template` appears throughout and is replaced with the actual project name during scaffolding by `fips-agents create gateway <name>`.
