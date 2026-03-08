# Deployment Guide

## Overview

This guide covers deploying the RAG Gateway stack with Docker Compose for
local development and small-scale production environments. All services are
defined in `docker-compose.yml`. The stack requires Docker 24 or later.

## Hardware Requirements

Minimum: 4 CPU cores, 8 GB RAM (gateway + supporting services only, no GPU).
Recommended with vLLM: 8 CPU cores, 32 GB RAM, NVIDIA GPU with 16 GB VRAM.
CPU-only vLLM mode works but inference is approximately 10× slower than GPU.
Disk: 50 GB for model weights (cached in `HF_HOME`), 10 GB for logs and
Postgres data. The gateway binary itself is under 50 MB.

## Quick Start

Run `docker compose up -d` to start all services. The gateway becomes
available at `http://localhost:8080` after approximately 10–30 seconds.
vLLM requires 60–120 seconds for the first cold start while model weights
load into GPU memory. Monitor readiness with `curl http://localhost:8080/ready`
until it returns HTTP 200. The Grafana dashboard is available at
`http://localhost:3000` (default credentials admin/admin). Jaeger traces
are available at `http://localhost:16686`.

## Key Environment Variables

Set these in a `.env` file in the repository root before running compose.
`VLLM_ENDPOINT`: vLLM base URL, default `http://localhost:8000`.
`VLLM_API_KEY`: Bearer token for vLLM authentication, no default.
`JWT_SECRET`: HS256 signing secret for gateway tokens, default `changeme`.
`OPA_ENDPOINT`: OPA policy server URL, default `http://opa:8181`.
`REDIS_ADDR`: Redis address for cache and rate limits, default `localhost:6379`.
`RATE_LIMIT_RPM`: per-IP requests per minute, default 60.
`EMBEDDING_MODEL`: sentence-transformers model name for hybrid retrieval;
unset or empty string disables hybrid mode and uses BM25-only retrieval.
`HF_HOME`: Hugging Face model cache directory; set to a path with sufficient
disk space outside the home directory to avoid quota issues.

## Horizontal Scaling

The gateway is stateless and can be scaled horizontally behind a load balancer.
Redis is required for cross-instance rate limiting — without Redis, each
instance enforces its own per-IP limit independently. The pageindex-worker
holds its document index in memory and must be scaled with care: all gateway
instances must point to the same pageindex-worker instance, or document
ingestion must be replicated across all workers.

## Troubleshooting

Gateway logs are structured JSON on stdout. Run `docker compose logs gateway`
to inspect them. Each log line has fields: `ts`, `level`, `component`, `msg`,
and `trace_id` where applicable. If the `/ready` endpoint returns 503, check
that the vLLM container is running and that `VLLM_ENDPOINT` is correct. If
retrieval returns no results, verify that documents were ingested successfully
via `POST /api/v1/ingest` and check pageindex-worker logs for indexing errors.
