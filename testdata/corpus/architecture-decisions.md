# Architecture Decision Records

## Overview

This document records the key architectural decisions made during the design
and implementation of the RAG Gateway. Each ADR covers the context, the
decision, and the rationale for rejecting alternatives.

## ADR-001: Go for the Gateway Service

Go was chosen for the Gateway because its goroutine-based concurrency model
handles high connection counts without thread-per-request overhead. The Gin
HTTP framework provides low-latency routing with minimal allocations. Static
compilation produces a single binary with no runtime dependencies, which
simplifies container images and deployment. Alternatives considered: Python
(FastAPI) was rejected due to GIL constraints under heavy I/O; Node.js was
rejected due to the team's stronger Go proficiency and better static typing.

## ADR-002: gRPC for Internal Service Communication

gRPC with Protocol Buffers was chosen for gateway-to-pageindex-worker and
gateway-to-adapter-service communication. Protobuf schemas enforce a typed
contract between services, catching breaking changes at compile time. HTTP/2
transport enables multiplexed streams and lower latency than REST over HTTP/1.
Alternatives considered: REST/JSON between services was rejected because it
requires manual serialisation validation; message queues (Kafka) were rejected
as overkill for synchronous request-response patterns.

## ADR-003: BM25 + Reciprocal Rank Fusion for Retrieval

The pageindex-worker uses BM25 as the primary retrieval method because it
requires no GPU, no model downloads in CI, and gives strong keyword recall.
Hybrid retrieval adds sentence-transformer cosine similarity combined via
Reciprocal Rank Fusion (k=60) when an embedding model is available. RRF
was chosen over weighted score combination because it is rank-based and
robust to score scale differences between BM25 and cosine. The embedding
model is optional and falls back to BM25-only mode when not installed.

## ADR-004: OPA for Policy Evaluation

Open Policy Agent (OPA) was chosen to externalise retrieval, compile, and
output access control decisions. Rego policies can be updated without
redeploying the gateway, and OPA's decision log provides an auditable policy
trail. The gateway is designed to fail-open when OPA is unreachable (except
for compile decisions which fail-closed). Alternatives considered: hard-coded
Go RBAC was rejected because it cannot be updated without redeployment;
a custom policy microservice was rejected as unnecessary complexity when OPA
already solves the problem.

## ADR-005: Redis for Retrieval Cache

Redis is used as a fail-open retrieval cache keyed by SHA256(query+topK).
Cache hits avoid a gRPC round-trip to the pageindex-worker. The default TTL
is 300 seconds (configurable via `RETRIEVAL_CACHE_TTL_SECONDS`). Redis errors
are logged and ignored — the gateway falls back to direct gRPC without
returning an error to the caller. Alternatives considered: in-process cache
was rejected because it cannot be shared across horizontally scaled gateway
instances; PostgreSQL-backed cache was rejected due to higher write latency.
