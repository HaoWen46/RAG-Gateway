# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Zero-Trust RAG Gateway with "Compile-to-LoRA" Mode. A security-grade gateway that sits in front of an LLM stack enforcing retrieval safety, policy, provenance, and adapter safety. Full spec in `PROJECT_SPEC.txt`.

**Two serving modes:**
- **RAG Mode (default):** Query → PageIndex retrieves safe sections → model answers with citations
- **Compile Mode (Doc-to-LoRA):** Query → PageIndex retrieves policy-approved sections → Doc-to-LoRA generates session LoRA → vLLM serves answers

## Architecture

**Go services:**
- **Gateway API** (`gateway/`) — Auth (JWT/RBAC), request normalization, context firewall, OPA policy checks, immutable trace IDs, Prometheus metrics, OTel tracing
- **Retrieval Orchestrator** (`retrieval/`) — Doc catalog, metadata filters, ranking, Redis cache, gRPC proxy to PageIndex

**Python services:**
- **PageIndex Worker** (`pageindex-worker/`) — BM25 tree build + retrieval (PageIndex); gRPC server
- **Adapter Service** (`adapter-service/`) — LoRA compilation, HMAC signing, canary probes; behind policy gates, no direct user access

**Infrastructure (all in `docker-compose.yml`):**
- **vLLM** — Serves LLM with dynamic LoRA adapter loading/unloading
- **PostgreSQL** — Metadata, audit logs, adapter lineage (`migrations/`)
- **Redis** — Rate limits, retrieval cache
- **OPA** — Policy evaluation at `http://opa:8181`; policies in `policy/rego/`
- **Jaeger** — OTel trace collector + UI at `:16686`
- **Prometheus** — Scrapes `gateway:8080/metrics`; config in `prometheus/`
- **Grafana** — Auto-provisioned dashboard at `:3000`; config in `grafana/provisioning/`

## Key Data Flows

**RAG Mode:** Gateway auth → OPA approves doc scope → PageIndex retrieval → Context firewall (strip injection patterns, enforce trust tiers) → Prompt assembler with citations → vLLM → Output filter (cite-or-refuse) → Audit log

**Compile Mode:** Same through retrieval, then: OPA checks (compile allowed? TTL? sensitivity?) → Adapter Service generates LoRA → Adapter verification (HMAC signature + canary probes) → vLLM loads LoRA → Serve Q&A until TTL expires → Unload + revoke lineage record

## Critical Security Constraints

- **Adapter isolation:** Only the Adapter Service can mint signed adapters; only the Gateway can request vLLM to load them. Users never access the Adapter Service directly.
- **Session-scoped adapters:** TTL 5–30 min, auto-revoked; `loramanager.SetRevokeHook` calls `adapterstore.Revoke` on expiry
- **Post-compile canary probes:** "Ignore previous instructions" tests, secret canary strings, tool-use bait prompts — fail-closed
- **Context firewall:** Retrieved text is treated as hostile — strip instruction-like content, block override patterns, enforce doc trust tiers
- **Retrieval security:** Trust-tier scoring per doc, section-level instruction likelihood scoring, provenance-required answers
- OWASP LLM Top 10 is the threat model scaffold

## Completed Milestones

All 13 milestones are complete and CI passes (GitHub Actions).

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
| 13 | E2E integration test script (`scripts/e2e-test.sh`) |

## Development Notes

- **Python tooling:** Always use `uv` — never bare `pip`, `python`, etc. Use `uv run`, `uv sync`, `uv pip`.
- **Go builds:** Avoid running `go build`/`go test` locally on the shared workstation — the compiler parallelism spikes CPU and risks process termination. Push and let CI validate instead.
- **Generated proto files** are committed to the repo (not gitignored) so CI can build without the proto toolchain.
- **uv.lock files** are committed so `uv sync --frozen --extra dev` works in CI without regenerating.
- **Logging:** Go services use `zap` (JSON, via `gateway/internal/logging`); Python services use a stdlib `_JsonFormatter` emitting `ts/level/component/msg` JSON lines.

## Key External References

- PageIndex: https://github.com/VectifyAI/PageIndex
- Sakana Text-to-LoRA: https://github.com/SakanaAI/text-to-lora
- vLLM LoRA support: https://docs.vllm.ai/en/stable/features/lora.html
- OPA: https://www.openpolicyagent.org/
