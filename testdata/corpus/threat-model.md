# OWASP LLM Top 10 Threat Model

## Overview

The RAG Gateway is designed to mitigate the OWASP LLM Top 10 (2023) threats.
Each relevant threat is listed with the specific countermeasures implemented.
This document is reviewed each time a new security milestone is completed.

## LLM01 Prompt Injection

Prompt injection attacks attempt to override the system prompt or hijack the
LLM's behaviour by embedding instructions in retrieved content or user input.
The context firewall mitigates this at the retrieval stage: it applies
sentence-level stripping using 12 compiled regular expressions covering
"ignore previous instructions", "you are now a", "system prompt:", [INST]
markers, and similar patterns. Injections embedded in documents are stripped
before the document content reaches the LLM prompt. Direct user-input
injection is harder to prevent but is partially mitigated by treating the
user message as untrusted and never giving it access to tool calls.

## LLM02 Insecure Output Handling

Without output validation, the LLM may return content that violates policy
or references information it was not authorised to use. The gateway enforces
three output controls: (1) cite-or-refuse — the response must contain at
least one `[doc:<id>, sec:<id>]` citation or it is rejected; (2) citation
verification — every cited (document_id, section_id) is checked against the
set of actually retrieved sections, blocking hallucinated citations; (3) OPA
output policy — a final policy check gate before the response is returned.

## LLM04 Model Denial of Service

Resource exhaustion attacks can make the LLM backend unavailable. The gateway
prevents this with: (1) per-IP rate limiting at 60 RPM using a token bucket
with HTTP 429 responses; (2) a circuit breaker that opens after 5 consecutive
upstream failures and stays open for 30 seconds before probing; (3) a 4 MB
request body limit enforced before the request reaches downstream services.

## LLM06 Sensitive Information Disclosure

The trust-tier system prevents unauthorised access to sensitive documents.
Documents are tagged with one of four trust tiers: public, internal,
confidential, or secret. JWT role claims (viewer, analyst, admin) map to
maximum accessible tiers: viewer can only see public, analyst can see public
and internal, admin can see all tiers. The context firewall enforces these
limits at retrieval time — sections above the caller's tier are dropped
before the LLM ever sees them.

## LLM09 Overreliance

The gateway requires the LLM to ground all responses in retrieved context.
Citation enforcement (cite-or-refuse) prevents the model from generating
unsupported answers. Hallucination detection (verifyCitations) catches cases
where the model invents plausible-looking but invalid citation references.
Together these controls ensure that every response is anchored to a specific
retrieved document section that the administrator ingested and approved.

## LLM10 Model Theft via Adapter

Session LoRA adapters compiled from sensitive documents could leak their
training content. Three controls address this: (1) canary probes fire
adversarial prompts at each newly compiled adapter to detect knowledge
leakage; (2) adapters are automatically revoked at TTL expiry (5–30 min)
so exposure windows are short; (3) adapter lineage records in Postgres provide
a full audit trail of what was compiled, probed, and when it was revoked.
