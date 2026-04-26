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

## Endpoints

| Path | Method | Description |
|---|---|---|
| `/v1/chat/completions` | POST | OpenAI-compatible chat completions (sync + streaming) |
| `/healthz` | GET | Liveness probe |
| `/readyz` | GET | Readiness probe (checks backend connectivity) |
| `/v1/agent-info` | GET | Pass-through to backend agent info (UI settings) |
| `/.well-known/agent.json` | GET | Agent discovery card |

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
