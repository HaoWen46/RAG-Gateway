# Milestones 14–17: Real-World Correctness

These milestones address gaps found by code audit and the lack of any end-to-end
test with real documents. The project claims to be a "zero-trust RAG gateway" but
the core loop — ingest → retrieve → firewall → cite → answer — has never actually
been exercised. Milestones are ordered by impact.

---

## Milestone 14 — HTTP Ingest Endpoint + End-to-End Smoke Test

**Problem:** There is no HTTP API to load documents into the system. The gateway
exposes `/api/v1/query` and `/api/v1/compile` but has no ingest route. RAG mode
cannot be tested or demoed end-to-end without a separate gRPC client. The trust
tier system (the core of zero-trust) is also inert: every document silently
defaults to `trust_tier = "public"` because there is no API to set it.

**Deliverables:**

### 14a — Ingest endpoint

- `POST /api/v1/ingest` (auth-gated, `admin` or `analyst` role)
  ```json
  {
    "document_id": "policy-v1",
    "content": "...",
    "trust_tier": "internal",
    "metadata": { "owner": "security-team" }
  }
  ```
- Gateway handler forwards to the retrieval gRPC service's `Index` RPC
- Response: `{ "sections": N, "document_id": "..." }`
- `trust_tier` passed through as metadata so the retriever surfaces it on recall
- Prometheus counter: `rag_documents_indexed_total`

### 14b — Smoke test with a real document

- Update `scripts/e2e-test.sh`: add Step 3 that actually calls `/api/v1/ingest`
  with `testdata/sample-doc.md` (already exists), then queries for content from it
  and verifies the response contains a citation
- This is the first true end-to-end test of the RAG pipeline

**Files to create/modify:**
- `EDIT gateway/internal/handler/handler.go` — add `Ingest` handler
- `EDIT gateway/internal/proxy/proxy.go` — add `Ingest` method, retrieval gRPC `Index` call
- `EDIT gateway/cmd/server/main.go` — register `POST /api/v1/ingest` route
- `EDIT gateway/internal/metrics/metrics.go` — add `rag_documents_indexed_total`
- `EDIT scripts/e2e-test.sh` — use real ingest + verify citation in response

**Scope:** Small-medium. Core plumbing only; no new dependencies.

---

## Milestone 15 — Fix Security Bugs

**Problem:** Three concrete security/correctness bugs found by audit. None are
visible in normal operation but each undermines a core system guarantee.

### Bug 1: OPA retrieval policy field mismatch

**File:** `policy/rego/retrieval.rego` and `gateway/internal/policy/client.go`

- Client sends `doc_trust_tiers` (array, e.g. `["internal", "public"]`)
- Rego checks `input.doc_trust_tier` (singular string) — field never matches
- Result: `allow` for admin only (role check alone); analyst/viewer rules
  fall through to `default allow := false` — non-admins always denied with OPA live

**Fix:** Update `retrieval.rego` to use `input.doc_trust_tiers` and check using
`some tier in input.doc_trust_tiers`:
```rego
allow if {
    input.user_role == "analyst"
    some tier in input.doc_trust_tiers
    tier in {"public", "internal"}
}
```

### Bug 2: Streaming requests bypass cite-or-refuse

**File:** `gateway/internal/proxy/proxy.go` — `streamResponse`

- `bufferedResponse` checks `responseHasCitation(data)` and rejects uncited answers
- `streamResponse` has no such check — `"stream": true` requests skip the output filter
- Any client can bypass citation enforcement with a single flag

**Fix:** Buffer streamed chunks, accumulate content, apply citation check before
first flush; or refuse to stream in RAG mode (simpler: return 400 if
`"stream": true` when RAG context is active).

### Bug 3: `CheckOutput` OPA policy is dead code

**File:** `gateway/internal/proxy/proxy.go` — `bufferedResponse`

- `output.rego` is defined and loaded into OPA, but `policy.CheckOutput` is never
  called anywhere in the proxy
- The output policy (`cite_required`, `refuse`, `redact`) is effectively inert

**Fix:** Call `p.policy.CheckOutput` in `bufferedResponse` after the citation check,
passing `has_retrieval_context` and `retrieved_sections` count.

**Files to modify:**
- `EDIT policy/rego/retrieval.rego` — fix `doc_trust_tier` → `doc_trust_tiers`
- `EDIT gateway/internal/proxy/proxy.go` — streaming cite-or-refuse + CheckOutput call
- `EDIT gateway/internal/policy/client.go` — update `CheckOutput` input fields to
  match what `output.rego` actually evaluates
- `EDIT gateway/internal/policy/client_test.go` — add test for CheckOutput being called

**Scope:** Small. All changes are surgical; no new dependencies.

---

## Milestone 16 — Citation Verification

**Problem:** `responseHasCitation` runs a regex on the raw JSON response body and
passes if `[doc:x, sec:y]` appears anywhere. The LLM could hallucinate
`[doc:nonexistent, sec:fake]` and pass the filter. Citations are not verified
against the sections that were actually retrieved.

**Deliverables:**

- In `ragAugment`, record the set of `(document_id, section_id)` pairs for the
  sections injected into the prompt
- Store this set in the Gin context (or pass it through to `bufferedResponse`)
- `responseHasCitation` upgraded to `verifyCitations(data, retrievedSections)`:
  - Parse citation pattern `[doc:<id>, sec:<id>]` from LLM response
  - Check each cited pair exists in the retrieved set
  - Reject if any citation is hallucinated (not in retrieved set), or if
    no citations at all (current behavior)
- Add `rag_hallucinated_citations_total` Prometheus counter

**Files to modify:**
- `EDIT gateway/internal/proxy/proxy.go` — thread retrieved section IDs through,
  upgrade citation check function
- `EDIT gateway/internal/metrics/metrics.go` — add `rag_hallucinated_citations_total`

**Scope:** Small-medium. Logic change is contained to proxy.go.

---

## Milestone 17 — Semantic Retrieval (Embeddings)

**Problem:** BM25 retrieval is keyword-based. If a document section says
"employees must not disclose salary information" and a user asks "what are the
confidentiality rules around pay?", BM25 will likely miss it — the query and
document use different words. Real RAG security depends on reliably retrieving
*all* relevant content, because a missed hostile section is a missed injection.
Dense retrieval is essential for the threat model to hold.

**Deliverables:**

- Add an embedding model to `pageindex-worker` (sentence-transformers, small model
  like `all-MiniLM-L6-v2`, ~80MB — store in /tmp2/ not ~/)
- Hybrid retrieval: BM25 score + cosine similarity score, combined (RRF or
  weighted sum), re-rank top-K
- Embedding index stored in-memory alongside BM25 index in `DocumentIndex`
- New env var `EMBEDDING_MODEL` (default `all-MiniLM-L6-v2`); if unset, fall back
  to BM25-only (backward compatible)
- Update `pageindex-worker` tests to cover embedding path

**Files to modify:**
- `EDIT pageindex-worker/pyproject.toml` — add `sentence-transformers`
- `EDIT pageindex-worker/src/pageindex_worker/indexer.py` — store embeddings
- `EDIT pageindex-worker/src/pageindex_worker/retriever.py` — hybrid scoring
- `EDIT pageindex-worker/tests/test_indexer_retriever.py` — embedding tests
- `EDIT pageindex-worker/uv.lock` — regenerate

**Scope:** Medium-large. sentence-transformers is a heavy dependency; model
download on first run. Validate it doesn't break CI (mock embeddings in tests).

---

## Execution Order

```
M14 (Ingest API + smoke test)  ──► unblocks everything; do first
M15 (Fix security bugs)        ──► small, self-contained; do second
M16 (Citation verification)    ──► depends on M14 (need real sections to verify)
M17 (Semantic retrieval)       ──► independent but large; do last
```

**Recommended order:** 14 → 15 → 16 → 17

---

## Summary Table

| Milestone | Name                    | Scope        | Key impact                                      |
|-----------|-------------------------|--------------|-------------------------------------------------|
| 14        | HTTP Ingest + smoke test | Small-medium | First real end-to-end RAG test                 |
| 15        | Fix security bugs        | Small        | OPA policy works, streaming secured, output OPA |
| 16        | Citation verification    | Small-medium | Hallucinated citations rejected                 |
| 17        | Semantic retrieval       | Medium-large | Retrieval actually finds relevant content       |
