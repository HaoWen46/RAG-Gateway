# RAG Gateway API Reference

## Overview

All endpoints are served by the Gateway on port 8080. Every request except
`/health` and `/metrics` requires a valid JWT in `Authorization: Bearer`.
Request bodies must be JSON with `Content-Type: application/json`.
Responses include an `X-Trace-ID` header for correlation with logs.

## POST /api/v1/query

Sends a user message to the LLM in RAG mode. The gateway retrieves relevant
document sections, sanitises them through the context firewall, assembles a
system prompt with citations, forwards to vLLM, and returns the response.
Request body fields: `messages` (array, required), `model` (string, optional).
Maximum body size is 4 MB. Streaming mode (`"stream": true`) is blocked in
RAG mode to prevent cite-or-refuse bypass; streaming is allowed in non-RAG
mode only. Successful responses contain `[doc:<id>, sec:<id>]` citations.
Returns HTTP 200 on success, 401 if token invalid, 403 if policy denies,
422 if no relevant sections found or response lacks citations, 502 if vLLM
is unreachable.

## POST /api/v1/compile

Compiles a session LoRA adapter from retrieved document sections. The pipeline
is: retrieve → firewall → OPA compile policy → Adapter Service compile →
canary probe verification → vLLM load. Request body fields: `query` (string,
required), `ttl_seconds` (integer, 300–1800, default 300). Returns the
`adapter_id` and `expires_at` timestamp on success. Compile mode requires
the Adapter Service to be configured; returns HTTP 501 if not configured.
Returns HTTP 422 if the adapter fails canary probes (fail-closed).

## POST /api/v1/ingest

Indexes a document into the retrieval service so it is searchable by RAG
queries. Requires `admin` or `analyst` role; `viewer` receives HTTP 403.
Request body fields: `document_id` (string, required), `content` (string,
required), `trust_tier` (string: public/internal/confidential/secret),
`metadata` (object, optional key-value pairs). Returns the `document_id`
and `trust_tier` on success. Increments the `rag_documents_indexed_total`
Prometheus counter on each successful ingest.

## GET /health

Liveness endpoint. Always returns HTTP 200 with `{"status":"ok"}`. No
authentication required. Used by load balancers and container orchestrators
to determine if the process is alive.

## GET /ready

Readiness endpoint. Returns HTTP 200 if vLLM is reachable, HTTP 503 with
`{"status":"not ready","reason":"upstream unavailable"}` if not. No
authentication required. Used to hold traffic until the LLM backend is warm.

## GET /metrics

Prometheus metrics endpoint. No authentication required. Exposes counters
and histograms including `http_requests_total`, `http_request_duration_seconds`
(p50/p95/p99 buckets), `rag_firewall_sections_blocked_total`,
`rag_policy_denied_total`, `rag_cite_or_refuse_total`,
`rag_hallucinated_citations_total`, `rag_documents_indexed_total`,
and `adapter_probe_failures_total` labelled by probe name.
