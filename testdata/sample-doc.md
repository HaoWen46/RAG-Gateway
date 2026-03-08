# RAG Gateway Security Policy

## Overview

This document describes the security controls enforced by the RAG Gateway.
All retrieval requests are subject to policy evaluation before any content
is returned to the caller.

## Authentication

All API requests must carry a valid JWT token in the `Authorization` header
using the `Bearer` scheme. Tokens are validated against a shared secret or
an RSA public key depending on deployment configuration.

## Retrieval Safety

Retrieved document sections are passed through the context firewall before
being assembled into prompts. The firewall:

- Strips sentences that match instruction-injection patterns
- Blocks entire sections that exceed the hostility threshold
- Enforces trust-tier constraints per document source

## Output Policy

Responses that rely on retrieved context must include source citations.
If no valid sections remain after firewall processing, the gateway refuses
the request rather than hallucinating an answer (cite-or-refuse mode).

## Adapter Safety

Session LoRA adapters are:

- Generated only from policy-approved document sections
- Verified with canary probes before being loaded into vLLM
- Automatically revoked when their TTL expires
- Tracked in the adapter lineage table for audit purposes

## Threat Model

The RAG Gateway is designed to mitigate the OWASP LLM Top 10, with
particular focus on prompt injection (LLM01), insecure output handling
(LLM02), and model denial of service (LLM04).
