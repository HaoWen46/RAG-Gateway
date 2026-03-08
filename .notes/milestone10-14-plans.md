# Milestones 10–14: Production Readiness & Demo

## Milestone 10 — GitHub Actions CI Pipeline

**Goal:** Automated test/lint/build on every push and PR.

**Deliverables:**
- `.github/workflows/ci.yml` with jobs:
  1. **go-test** — `go test ./...` for gateway/ and retrieval/
  2. **go-lint** — `go vet ./...` + `staticcheck` (or `golangci-lint`)
  3. **python-test** — `uv run pytest` for adapter-service/ and pageindex-worker/
  4. **docker-build** — `docker compose build` (no push, just verify images build)
- Matrix strategy: Go 1.23, Python 3.12
- Cache Go modules (`actions/cache`) and uv cache
- Badge in README.md

**Files to create/modify:**
- `NEW .github/workflows/ci.yml`
- `EDIT Makefile` — fix `test` target to use `uv run pytest` instead of `python -m pytest`
- `EDIT README.md` — add CI badge

**Estimated scope:** ~1 file new, 2 edits. Small milestone.

---

## Milestone 11 — OPA in Docker Compose + Structured JSON Logging

**Goal:** Complete the docker-compose stack and standardize logging.

### 11a — OPA Service

**Deliverables:**
- Add `opa` service to `docker-compose.yml` using `openpolicyagent/opa:latest`
  - Mount `policy/rego/` as a bundle directory
  - Expose port 8181
  - Gateway `depends_on` includes OPA
- Verify policy evaluation works: `POST /v1/data/retrieval/allow`

**Files to modify:**
- `EDIT docker-compose.yml` — add OPA service
- `EDIT policy/rego/output.rego` — flesh out output policy (currently empty)

### 11b — Structured JSON Logging

**Deliverables:**
- **Go (gateway):** Switch from default `log` to `slog.JSONHandler`
  - Structured fields: `trace_id`, `component`, `level`, `msg`, `error`
  - Apply across all packages (audit, proxy, firewall, etc.)
- **Python (adapter-service, pageindex-worker):** Configure `logging` with `json` formatter
  - Consistent field names matching Go output
- Both output to stdout (Docker log collection friendly)

**Files to modify:**
- `NEW gateway/internal/logging/logger.go` — slog setup helper
- `EDIT gateway/cmd/server/main.go` — init structured logger
- `EDIT` ~5-8 Go files that use `log.Printf` → `slog.Info/Warn/Error`
- `EDIT adapter-service/src/adapter_service/server.py` — JSON log config
- `EDIT pageindex-worker/src/pageindex_worker/server.py` — JSON log config

**Estimated scope:** Medium. OPA is quick; logging touches many files but each change is small.

---

## Milestone 12 — Grafana Dashboard + Observability Stack

**Goal:** Visual security dashboard that ties Prometheus metrics + Jaeger traces together.

**Deliverables:**
- Add Grafana service to `docker-compose.yml` (port 3000)
- Provisioned datasources: Prometheus + Jaeger (auto-configured, no manual setup)
- **Provisioned dashboard JSON** (`dashboards/rag-security.json`) with panels:
  1. **Request Rate** — `rate(http_requests_total[5m])` by path and status
  2. **Firewall Activity** — `rate(rag_firewall_sections_blocked_total[5m])` + sentences stripped
  3. **Policy Denials** — `rate(rag_policy_denied_total[5m])` by reason
  4. **Cite-or-Refuse Rate** — `rate(rag_cite_or_refuse_total[5m])`
  5. **Canary Probe Failures** — `rate(adapter_probe_failures_total[5m])` by probe_name
  6. **Request Latency** (requires adding a histogram metric to Go)
- Add Prometheus service to `docker-compose.yml` with scrape config for gateway:8080/metrics
- Add request duration histogram to `gateway/internal/metrics/metrics.go`

**Prerequisite:** Milestone 11 (OPA in compose) so the stack is complete.

**Files to create/modify:**
- `NEW dashboards/rag-security.json` — Grafana dashboard definition
- `NEW dashboards/provisioning/datasources.yml` — Prometheus + Jaeger sources
- `NEW dashboards/provisioning/dashboards.yml` — dashboard provisioner config
- `NEW prometheus.yml` — scrape config
- `EDIT docker-compose.yml` — add Prometheus + Grafana services
- `EDIT gateway/internal/metrics/metrics.go` — add request duration histogram
- `EDIT gateway/internal/middleware/` — record duration in histogram
- `EDIT README.md` — document Grafana access

**Estimated scope:** Medium-large. Dashboard JSON is verbose but mechanical.

---

## Milestone 13 — End-to-End Integration Test

**Goal:** Prove the full pipeline works with real HTTP calls, not just mocked unit tests.

**Deliverables:**
- `scripts/e2e-test.sh` — shell script that:
  1. Checks gateway `/health` and `/ready`
  2. Obtains a JWT (call gateway auth or craft one with the JWT_SECRET)
  3. Indexes a sample document via retrieval gRPC (or a convenience HTTP endpoint)
  4. Sends a RAG query (`POST /api/v1/query`) and validates response has citations
  5. Sends a compile request (`POST /api/v1/compile`) and validates adapter lifecycle
  6. Sends an adversarial query (injection attempt) and validates firewall blocks it
  7. Checks Prometheus metrics endpoint has non-zero counters
  8. Reports PASS/FAIL for each step
- `testdata/sample-doc.md` — a small markdown document for ingestion
- Optional: Go-based integration test (`gateway/integration_test.go`) behind `//go:build integration` tag
  - Uses `httptest` or real HTTP client against a running gateway
  - Skipped in normal `go test ./...` runs

**Prerequisite:** Milestone 11 (OPA in compose so policy checks work end-to-end).

**Files to create:**
- `NEW scripts/e2e-test.sh`
- `NEW testdata/sample-doc.md`
- `OPTIONAL NEW gateway/integration_test.go`

**Estimated scope:** Medium. Script is straightforward; Go integration test adds complexity.

---

## Milestone 14 — OpenAPI Spec + Swagger UI

**Goal:** Professional API documentation auto-served from the gateway.

**Deliverables:**
- Add `swaggo/swag` annotations to handler functions in `gateway/internal/handler/handler.go`
  - `@Summary`, `@Description`, `@Tags`, `@Accept`, `@Produce`, `@Param`, `@Success`, `@Failure`, `@Router`
- Generate `docs/swagger.json` and `docs/swagger.yaml` via `swag init`
- Serve Swagger UI at `GET /swagger/*` using `swaggo/gin-swagger`
- Document all endpoints: `/health`, `/ready`, `/metrics`, `/api/v1/query`, `/api/v1/compile`
- Include request/response schemas with examples

**Files to create/modify:**
- `EDIT gateway/internal/handler/handler.go` — add swag annotations
- `EDIT gateway/cmd/server/main.go` — register swagger route
- `NEW gateway/docs/` — auto-generated swagger files
- `EDIT gateway/go.mod` — add swaggo dependencies
- `EDIT README.md` — document swagger endpoint

**Estimated scope:** Medium. Annotations are boilerplate; swagger tooling is mature.

---

## Execution Order

```
M10 (CI)  ─────────────────────────────────────────►  independent, do first
M11 (OPA + logging)  ──────► M12 (Grafana)            OPA needed for full stack
                       └───► M13 (E2E test)           OPA needed for policy tests
M14 (OpenAPI)  ────────────────────────────────────►  independent
```

**Recommended order:** 10 → 11 → 12 → 13 → 14

M10 and M14 are independent and could be done in any order, but CI first means
all subsequent milestones get automatic test validation on push.

---

## Summary Table

| Milestone | Name                      | Scope  | Key Tech                          |
|-----------|---------------------------|--------|-----------------------------------|
| 10        | GitHub Actions CI         | Small  | GitHub Actions, matrix, caching   |
| 11        | OPA + JSON Logging        | Medium | OPA, slog, structured logging     |
| 12        | Grafana Dashboard         | Medium | Prometheus, Grafana, Jaeger       |
| 13        | E2E Integration Test      | Medium | Shell/Go integration, testdata    |
| 14        | OpenAPI / Swagger         | Medium | swaggo/swag, gin-swagger          |
