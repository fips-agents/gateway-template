# gateway-template

An OpenAI-compatible HTTP reverse proxy for AI agent backends. It accepts `/v1/chat/completions` requests (synchronous and SSE streaming), proxies them to a configurable backend agent service, and handles the SSE connection lifecycle including heartbeats and flush. Built with the Go standard library only -- no external dependencies.

## Quick Start

```bash
# Build
make build

# Run (set BACKEND_URL to your agent)
BACKEND_URL=http://localhost:8081 make run

# Test
curl http://localhost:8080/healthz
```

## Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `BACKEND_URL` | Yes | -- | Base URL of the backend agent service |
| `PORT` | No | `8080` | HTTP listen port |
| `AGENT_NAME` | No | `gateway-template` | Agent name in `/.well-known/agent.json` |
| `AGENT_VERSION` | No | `0.1.0` | Agent version in `/.well-known/agent.json` |
| `LOG_REQUESTS` | No | `false` | Enable structured request logging (skips health probes) |
| `GATEWAY_AUTH_MODE` | No | `anonymous` | Inbound auth strategy: `anonymous` or `proxy` |
| `GATEWAY_AUTH_PROXY_USER_HEADER` | No | `X-Forwarded-User` | (`proxy` mode) header carrying the upstream-validated username |
| `GATEWAY_AUTH_PROXY_EMAIL_HEADER` | No | `X-Forwarded-Email` | (`proxy` mode) header carrying the upstream-validated email; empty disables email projection |

## Authentication

The gateway issues a canonical set of trusted headers to the backend agent on every request:

| Header | Description |
|---|---|
| `X-Auth-Subject` | Stable identifier (`anonymous` or the upstream-validated username) |
| `X-Auth-User` | Human-readable username (may be empty in `anonymous` mode) |
| `X-Auth-Email` | Email address (may be empty) |
| `X-Auth-Mode` | `anonymous` or `proxy` |

Inbound copies of these headers are stripped before the strategy runs, so a client cannot spoof identity by setting them directly. The header names match Kagenti's JWT claim shape so the contract survives a future swap to in-process JWKS validation without breaking the agent.

**Modes** (`GATEWAY_AUTH_MODE`):

- `anonymous` *(default)* — no validation. `X-Auth-Subject` is set to `anonymous`. Use for local dev, smoke tests, or any deployment that does not need user attribution.
- `proxy` — trust an upstream OAuth proxy (e.g. OpenShift `oauth-proxy` sidecar) or service-mesh `outputClaimToHeaders` filter. The gateway reads `X-Forwarded-User` / `X-Forwarded-Email` (header names configurable) and projects them onto the canonical headers. **The gateway pod must be unreachable except via that proxy** — otherwise a client can spoof the upstream headers. If the user header is missing, the gateway returns 503 (fail closed). In-process JWKS validation is deferred to v2.

## Endpoints

| Path | Method | Description |
|---|---|---|
| `/v1/chat/completions` | POST | OpenAI-compatible chat completions (sync + streaming) |
| `/v1/feedback` | POST, GET | User feedback submit/list (pass-through to backend) |
| `/v1/feedback/{feedback_id}` | PATCH | In-place edit of an existing feedback record |
| `/v1/feedback/stats` | GET | Aggregated feedback stats (pass-through to backend) |
| `/healthz` | GET | Liveness probe |
| `/readyz` | GET | Readiness probe (checks backend connectivity) |
| `/v1/agent-info` | GET | Pass-through to backend agent info (UI settings) |
| `/.well-known/agent.json` | GET | Agent discovery card |

All `/v1/*` endpoints forward the canonical `X-Auth-Subject` / `X-Auth-User` / `X-Auth-Email` / `X-Auth-Mode` headers (see Authentication above) so the backend can attribute requests to the resolved identity. Other request headers are dropped.

On the response side the gateway propagates a small allowlist back to the client — currently just `X-Trace-Id`, which the agent backend sets on every chat completion response so the UI can submit feedback against a known trace.

## Deployment

Deploy to OpenShift with the included Helm chart:

```bash
helm upgrade --install my-gateway chart/ \
  -n my-namespace \
  --set config.BACKEND_URL=http://my-agent:8080 \
  --set image.repository=<registry>/my-gateway
```

## Scaffolding

This repository is a template used by [fips-agents-cli](https://github.com/fips-agents/fips-agents-cli). To create a new gateway project:

```bash
fips-agents create gateway my-gateway-name
```
