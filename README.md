# Zero-Trust RAG Gateway

[![CI](https://github.com/b11902156/RAG-Gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/b11902156/RAG-Gateway/actions/workflows/ci.yml)

A security-grade gateway that sits in front of an LLM stack, enforcing retrieval safety, policy, provenance, and adapter safety. Built as a research/educational project implementing the OWASP LLM Top 10 threat model.

## Architecture

```
Client → Gateway (Go/Gin) → OPA Policy Engine
                          → PageIndex Worker (Python/BM25)
                          → Retrieval Service (Go/gRPC)
                          → Adapter Service (Python/gRPC)
                          → vLLM (Qwen3.5)
```

**Infrastructure:** PostgreSQL · Redis · Jaeger (OTel) · Prometheus

### Services

| Service | Language | Role |
|---|---|---|
| `gateway` | Go | Auth (JWT/RBAC), proxy, firewall, policy, audit |
| `retrieval` | Go | Doc catalog, metadata filters, ranking |
| `pageindex-worker` | Python | BM25 tree indexing and retrieval (PageIndex) |
| `adapter-service` | Python | LoRA compilation, signing, canary probes |

## Serving Modes

**RAG Mode (default)**
```
Query → Auth → Policy → PageIndex retrieval → Context firewall
      → Prompt assembly (citations) → vLLM → Output filter → Audit log
```

**Compile Mode (Doc-to-LoRA)**
```
Query → Auth → Policy → Retrieval → Adapter Service (compile + sign)
      → Canary probes → vLLM (load LoRA) → Serve Q&A → TTL expiry → Unload
```

## Security Features

- **Context firewall** — strips instruction-like text from retrieved docs, blocks override patterns, enforces doc trust tiers
- **OPA policy engine** — allow/deny retrieval targets, compile decisions, output constraints (redact, cite-required, refuse)
- **Post-compile canary probes** — fires adversarial prompts at each loaded adapter: instruction override, canary string leakage, tool-use bait; fail-closed
- **Adapter isolation** — only the Adapter Service mints signed adapters; only the Gateway can request vLLM to load them; users have no direct access
- **Session-scoped adapters** — TTL 5–30 min, auto-revoked with lineage persisted to Postgres
- **Adapter lineage** — full audit trail of compile/probe/revoke events in Postgres
- **Rate limiting** — per-IP token bucket (configurable RPM)
- **Circuit breaker** — lockless 3-state breaker on vLLM upstream
- **Immutable trace IDs** — every request carries a trace ID propagated through OTel spans

## Observability

- **Prometheus metrics** — `GET /metrics`; blocked injections, contaminated retrieval rate, adapter probe failures
- **OTel distributed tracing** — OTLP gRPC export; Jaeger UI at `http://localhost:16686`
- **Structured audit logs** — every request logged to Postgres with trace ID

## Quick Start

```bash
# Start infrastructure
docker compose up -d postgres redis jaeger

# Run gateway (requires VLLM_ENDPOINT, JWT_SECRET, etc.)
cd gateway && go run ./cmd/server

# Run retrieval + pageindex
docker compose up retrieval pageindex-worker

# Run adapter service
cd adapter-service && uv run python -m adapter_service.server
```

### Key Environment Variables

| Variable | Default | Description |
|---|---|---|
| `VLLM_ENDPOINT` | `http://localhost:8000` | vLLM base URL |
| `VLLM_API_KEY` | — | Bearer token for vLLM |
| `VLLM_PROBE_ENDPOINT` | — | vLLM URL for canary probes (unset = disabled) |
| `VLLM_MODEL` | — | Model ID for canary probe requests |
| `JWT_SECRET` | `changeme` | HS256 signing secret |
| `OPA_ENDPOINT` | `http://localhost:8181` | OPA policy server |
| `REDIS_ADDR` | `localhost:6379` | Redis for retrieval cache + rate limits |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | OTLP gRPC endpoint (e.g. `localhost:4317`) |
| `RATE_LIMIT_RPM` | `60` | Per-IP requests per minute |
| `RETRIEVAL_CACHE_TTL_SECONDS` | `300` | Retrieval cache TTL |

## Testing

```bash
# Gateway (Go)
cd gateway && go test ./...

# Adapter service (Python)
cd adapter-service && uv run pytest tests/ -v

# Pageindex worker (Python)
cd pageindex-worker && uv run pytest tests/ -v
```

## Milestones

| # | Feature |
|---|---|
| 1 | Go gateway + JWT/RBAC auth + audit IDs + rate limiter + circuit breaker |
| 2 | PageIndex BM25 ingestion + retrieval gRPC + cite-or-refuse output |
| 3 | Context firewall + OPA policy engine + output filter |
| 4 | Doc-to-LoRA compile mode + vLLM dynamic LoRA load/unload |
| 5 | Attack suite (10 adversarial scenarios) + Prometheus metrics |
| 6 | OTel distributed tracing (OTLP/Jaeger) |
| 7 | Redis retrieval cache (SHA256-keyed, fail-open) |
| 8 | Adapter lineage in Postgres (compile/probe/revoke audit trail) |
| 9 | Real canary probes against live vLLM inference |

## References

- [PageIndex](https://github.com/VectifyAI/PageIndex)
- [Sakana Text-to-LoRA](https://github.com/SakanaAI/text-to-lora)
- [vLLM LoRA support](https://docs.vllm.ai/en/stable/features/lora.html)
- [OPA](https://www.openpolicyagent.org/)
- [OWASP LLM Top 10](https://owasp.org/www-project-top-10-for-large-language-model-applications/)
