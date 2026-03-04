"""Unit tests for indexer and retriever."""

import pytest

from pageindex_worker.indexer import DocumentIndex
from pageindex_worker.retriever import Retriever


SAMPLE_DOC = """
# Introduction

This document describes the security policy for the RAG Gateway.

## Authentication

All users must authenticate via JWT tokens before accessing any endpoint.
Tokens expire after 1 hour and must be refreshed.

## Rate Limiting

Each IP address is limited to 60 requests per minute.
Excess requests receive HTTP 429 with a Retry-After header.

## Audit Logging

Every request is logged to the audit database with a trace ID.
"""


def make_index():
    idx = DocumentIndex()
    idx.index("doc1", SAMPLE_DOC, metadata={"trust_tier": "internal"})
    return idx


def test_index_splits_into_sections():
    idx = make_index()
    sections = idx.get("doc1")
    assert len(sections) >= 3, "expected at least 3 sections from headings"


def test_section_ids_unique():
    idx = make_index()
    ids = [s.section_id for s in idx.get("doc1")]
    assert len(ids) == len(set(ids))


def test_retrieve_returns_relevant():
    idx = make_index()
    r = Retriever(idx)
    results = r.retrieve("JWT authentication tokens", top_k=3)
    assert len(results) > 0
    top_content = results[0].content.lower()
    assert "jwt" in top_content or "authenticate" in top_content


def test_retrieve_score_normalised():
    idx = make_index()
    r = Retriever(idx)
    results = r.retrieve("rate limiting requests", top_k=5)
    assert all(0.0 <= res.score <= 1.0 for res in results)


def test_retrieve_empty_index():
    idx = DocumentIndex()
    r = Retriever(idx)
    results = r.retrieve("anything", top_k=5)
    assert results == []


def test_retrieve_metadata_filter():
    idx = DocumentIndex()
    idx.index("public_doc", SAMPLE_DOC, metadata={"trust_tier": "public"})
    idx.index("internal_doc", SAMPLE_DOC, metadata={"trust_tier": "internal"})
    r = Retriever(idx)
    results = r.retrieve("authentication", top_k=10, metadata_filters={"trust_tier": "public"})
    assert all(res.trust_tier == "public" for res in results)


def test_retrieve_no_zero_score():
    idx = make_index()
    r = Retriever(idx)
    results = r.retrieve("xyzzy_nonexistent_term_12345", top_k=5)
    # All returned sections should have score > 0 (zero-score sections are dropped).
    assert all(res.score > 0 for res in results)
