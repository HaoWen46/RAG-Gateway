# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Zero-Trust RAG Gateway with "Compile-to-LoRA" Mode. A security-grade gateway that sits in front of an LLM stack enforcing retrieval safety, policy, provenance, and adapter safety. Full spec in `PROJECT_SPEC.txt`.

**Two serving modes:**
- **RAG Mode (default):** Query → PageIndex retrieves safe sections → model answers with citations
- **Compile Mode (Doc-to-LoRA):** Query → PageIndex retrieves policy-approved sections → Doc-to-LoRA generates session LoRA → vLLM serves answers

## Architecture

**Go services:**
- **Gateway API** (Gin or Fiber) — Auth (JWT/OIDC + RBAC), request normalization, policy checks, immutable trace IDs
- **Policy Engine** (OPA) — Allow/deny retrieval targets, compile-to-LoRA decisions, output constraints (redact, cite-required, refuse)
- **Retrieval Orchestrator** — Doc catalog, metadata filters, ranking, caching
- **Audit/Telemetry backend** — OpenTelemetry integration

**Python services:**
- **PageIndex Worker** — Tree build + retrieval (PageIndex is Python-first)
- **Adapter Service** — Wraps Sakana Doc-to-LoRA / Text-to-LoRA; behind policy gates, no direct user access

**Infrastructure:**
- **vLLM** — Serves Qwen3.5-35B-A3B (BF16) with dynamic LoRA adapter loading/unloading
- **PostgreSQL** — Metadata, audit logs, adapter lineage
- **Redis** — Rate limits, retrieval cache

## Key Data Flows

**RAG Mode:** Gateway auth → Policy engine approves doc scope → PageIndex retrieval → Context firewall (strip instruction-like text, block override patterns, enforce trust tiers) → Prompt assembler with citations → vLLM generation → Output filter (redaction + cite-or-refuse) → Audit log

**Compile Mode:** Same through retrieval, then: Policy checks (compile allowed? TTL? sensitivity?) → Adapter Service generates LoRA from approved sections → Adapter verification (signature/hash, lineage, behavioral probes) → vLLM loads LoRA dynamically → Serve Q&A until TTL expires → Unload/revoke

## Critical Security Constraints

- **Adapter isolation:** Only the Adapter Service can mint signed adapters; only the Gateway can request vLLM to load them. Users never access the Adapter Service directly.
- **Session-scoped adapters:** TTL 5–30 min, auto-revoked
- **Post-compile canary probes:** "Ignore previous instructions" tests, secret canary strings, tool-use bait prompts
- **Context firewall:** Retrieved text is treated as hostile — strip instruction-like content, block override patterns, enforce doc trust tiers
- **Retrieval security:** Trust-tier scoring per doc, section-level instruction likelihood scoring, provenance-required answers
- OWASP LLM Top 10 is the threat model scaffold

## Implementation Milestones

1. Go Gateway + Auth + Audit IDs; vLLM serving Qwen3.5-35B-A3B (no adapters)
2. PageIndex ingestion + retrieval service; cite-or-refuse output mode
3. Context firewall + policy gating
4. Doc-to-LoRA compile mode + vLLM dynamic LoRA load/unload
5. Attack suite + dashboards (blocked injections, contaminated retrieval rate, adapter probe failures)

## Key External References

- PageIndex: https://github.com/VectifyAI/PageIndex
- Sakana Text-to-LoRA: https://github.com/SakanaAI/text-to-lora
- vLLM LoRA support: https://docs.vllm.ai/en/stable/features/lora.html
- Qwen3.5-35B-A3B: https://huggingface.co/Qwen/Qwen3.5-35B-A3B
- OPA: https://www.openpolicyagent.org/
