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

This is a thin reverse proxy -- no business logic, no external dependencies. All code uses the Go standard library only.

```
Client --> Gateway (:8080) --> Backend Agent
             |
             +-- /v1/chat/completions  (POST, sync + SSE streaming)
             +-- /v1/feedback          (POST/GET, pass-through, forwards auth headers)
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
- `internal/proxy/` -- SSE relay logic

## Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `BACKEND_URL` | Yes | -- | Backend agent base URL |
| `PORT` | No | `8080` | Listen port |
| `AGENT_NAME` | No | `gateway-template` | Name in agent card |
| `AGENT_VERSION` | No | `0.1.0` | Version in agent card |
| `LOG_REQUESTS` | No | `false` | Enable structured request logging |

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
