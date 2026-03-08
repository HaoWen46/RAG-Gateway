# RAG Gateway Security Policy

## Overview

The RAG Gateway enforces a zero-trust security model across all API requests.
Every request is authenticated, authorised by role, policy-checked by OPA,
and logged with an immutable trace ID before any content is returned.

## Authentication

All API requests must carry a valid JWT in the `Authorization: Bearer` header.
Tokens use HS256 signing with a shared secret (`JWT_SECRET`). The gateway
validates the `exp` claim and rejects expired tokens with HTTP 401. Tokens
contain a `role` claim (`admin`, `analyst`, or `viewer`) that governs what
content the caller may access. There is no anonymous access.

## Rate Limiting

Each client IP is limited to 60 requests per minute (configurable via
`RATE_LIMIT_RPM`). The gateway uses a per-IP token bucket. Requests that
exceed the limit receive HTTP 429 with a `Retry-After` header indicating
when the bucket refills. Rate limiting is enforced before authentication to
prevent credential-stuffing attacks from consuming backend resources.

## Context Firewall

Retrieved document sections pass through the context firewall before reaching
the LLM. The firewall operates at sentence level, stripping fragments that
match any of 12 injection patterns (e.g., "ignore previous instructions",
"you are now a", "system prompt:"). Entire sections are dropped if all
sentences are hostile or if the document's trust tier exceeds the caller's
role. The firewall is fail-closed: if a section cannot be sanitised, it is
dropped rather than passed through.

## Circuit Breaker

The gateway protects the vLLM upstream with a 3-state circuit breaker
(CLOSED → OPEN → HALF_OPEN). Five consecutive upstream failures open the
breaker. After 30 seconds the breaker enters HALF_OPEN and allows one
probe request. A successful probe closes the breaker; a failed probe
resets the 30-second timer. While open, all requests receive HTTP 503
without touching the upstream.

## Cite-or-Refuse and Citation Verification

In RAG mode the LLM response must contain at least one citation in the format
`[doc:<document_id>, sec:<section_id>]`. Responses without citations are
rejected with HTTP 422. Additionally, every cited `(document_id, section_id)`
pair is checked against the set of sections that were actually retrieved.
Citations referencing content that was not in the retrieved context are
rejected as hallucinated, and the `rag_hallucinated_citations_total` counter
is incremented.

## Audit Logging

Every request is written to a Postgres audit table with: trace ID, timestamp,
user role, request type (query/compile/ingest), policy decision, and whether
the firewall acted. Adapter lifecycle events (compile, probe, revoke) are
recorded separately in the adapter lineage table with full probe results.
