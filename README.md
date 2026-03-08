# Zero-Trust RAG Gateway

[![CI](https://github.com/HaoWen46/RAG-Gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/HaoWen46/RAG-Gateway/actions/workflows/ci.yml)

A security-grade gateway that sits in front of an LLM stack, enforcing retrieval safety, policy, provenance, and adapter safety. Built as a research/educational project implementing the OWASP LLM Top 10 threat model.

## Architecture

```
Client → Gateway (Go/Gin) → OPA Policy Engine
                          → PageIndex Worker (Python/BM25)
                          → Retrieval Service (Go/gRPC)
                          → Adapter Service (Python/gRPC)
                          → vLLM (Qwen3.5)
```

**Infrastructure:** PostgreSQL · Redis · OPA · Jaeger (OTel) · Prometheus · Grafana

### Services

| Service | Language | Role |
|---|---|---|
| `gateway` | Go | Auth (JWT/RBAC), proxy, firewall, policy, audit |
| `retrieval` | Go | Doc catalog, metadata filters, ranking, Redis cache |
| `pageindex-worker` | Python | Hybrid BM25 + semantic indexing and retrieval (PageIndex) |
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
- **Citation verification** — every `[doc:<id>, sec:<id>]` citation in the LLM response is validated against the actually-retrieved section set; hallucinated citations are rejected (tracked by `rag_hallucinated_citations_total`)
- **Cite-or-refuse** — RAG-mode responses without any citation are rejected; streaming is blocked in RAG mode to prevent bypass
- **OPA policy engine** — allow/deny retrieval targets, compile decisions, output constraints; wired at retrieval, compile, and output checkpoints
- **Post-compile canary probes** — fires adversarial prompts at each loaded adapter: instruction override, canary string leakage, tool-use bait; fail-closed
- **Adapter isolation** — only the Adapter Service mints signed adapters; only the Gateway can request vLLM to load them; users have no direct access
- **Session-scoped adapters** — TTL 5–30 min, auto-revoked with lineage persisted to Postgres
- **Adapter lineage** — full audit trail of compile/probe/revoke events in Postgres
- **Rate limiting** — per-IP token bucket (configurable RPM)
- **Circuit breaker** — lockless 3-state breaker on vLLM upstream
- **Immutable trace IDs** — every request carries a trace ID propagated through OTel spans

## Observability

- **Prometheus metrics** — `GET /metrics`; request rate, latency (p50/p95/p99), blocked injections, policy denials, adapter probe failures
- **Grafana dashboard** — auto-provisioned at `http://localhost:3000`; 9 security-focused panels
- **OTel distributed tracing** — OTLP gRPC export; Jaeger UI at `http://localhost:16686`
- **Structured JSON logs** — all services emit JSON log lines with `ts`, `level`, `component`, `msg` fields
- **Structured audit logs** — every request logged to Postgres with trace ID

## Quick Start

```bash
# Start full stack
docker compose up -d

# Run E2E test (requires gateway to be up)
GATEWAY_URL=http://localhost:8080 JWT_SECRET=changeme ./scripts/e2e-test.sh
```

### Key Environment Variables

| Variable | Default | Description |
|---|---|---|
| `VLLM_ENDPOINT` | `http://localhost:8000` | vLLM base URL |
| `VLLM_API_KEY` | — | Bearer token for vLLM |
| `VLLM_PROBE_ENDPOINT` | — | vLLM URL for canary probes (unset = disabled) |
| `VLLM_MODEL` | — | Model ID for canary probe requests |
| `JWT_SECRET` | `changeme` | HS256 signing secret |
| `OPA_ENDPOINT` | `http://opa:8181` | OPA policy server |
| `REDIS_ADDR` | `localhost:6379` | Redis for retrieval cache + rate limits |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | OTLP gRPC endpoint (e.g. `localhost:4317`) |
| `RATE_LIMIT_RPM` | `60` | Per-IP requests per minute |
| `RETRIEVAL_CACHE_TTL_SECONDS` | `300` | Retrieval cache TTL |
| `EMBEDDING_MODEL` | `all-MiniLM-L6-v2` | Sentence-transformers model for hybrid retrieval (pageindex-worker); unset for BM25-only |
| `HF_HOME` | `/tmp2/.../hf_cache` | Model weight cache directory (set to avoid home-dir quota) |

## Testing

```bash
# Gateway unit tests (Go)
cd gateway && go test ./...

# Adapter service (Python)
cd adapter-service && uv run pytest tests/ -v

# Pageindex worker (Python)
cd pageindex-worker && uv run pytest tests/ -v

# E2E integration test (requires running stack)
GATEWAY_URL=http://localhost:8080 JWT_SECRET=changeme ./scripts/e2e-test.sh
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
| 10 | GitHub Actions CI (Go test/vet, Python test, Docker build matrix) |
| 11 | OPA service in docker-compose + structured JSON logging (all services) |
| 12 | Grafana dashboard (9 panels: latency histogram, security counters) |
| 13 | E2E integration test script (health, auth, ingest, query, compile, attacks, metrics) |
| 14 | HTTP ingest endpoint (`POST /api/v1/ingest`) + `rag_documents_indexed_total` metric |
| 15 | Security bug fixes: OPA field mismatch, streaming cite-or-refuse bypass, dead `CheckOutput` |
| 16 | Citation verification — `[doc:id, sec:id]` validated against retrieved set; hallucinated citations rejected |
| 17 | Hybrid retrieval — BM25 + sentence-transformers cosine similarity combined via Reciprocal Rank Fusion |

## References

- [PageIndex](https://github.com/VectifyAI/PageIndex)
- [Sakana Text-to-LoRA](https://github.com/SakanaAI/text-to-lora)
- [vLLM LoRA support](https://docs.vllm.ai/en/stable/features/lora.html)
- [OPA](https://www.openpolicyagent.org/)
- [OWASP LLM Top 10](https://owasp.org/www-project-top-10-for-large-language-model-applications/)
