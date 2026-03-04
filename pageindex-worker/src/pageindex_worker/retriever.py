"""BM25-based retrieval over the DocumentIndex.

Tokenises the query and each section's (heading + content) text, ranks with
BM25, and returns the top-k results together with a normalised score.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Dict, List, Optional

from rank_bm25 import BM25Okapi

from pageindex_worker.indexer import DocumentIndex, Section


@dataclass
class RetrievedSection:
    document_id: str
    section_id: str
    heading: str
    content: str
    score: float
    trust_tier: str
    metadata: Dict[str, str]


def _tokenise(text: str) -> List[str]:
    """Lower-case, strip punctuation, split on whitespace."""
    text = text.lower()
    text = re.sub(r"[^\w\s]", " ", text)
    return text.split()


class Retriever:
    """BM25 retrieval over all sections in a DocumentIndex."""

    def __init__(self, index: DocumentIndex) -> None:
        self._index = index

    def retrieve(
        self,
        query: str,
        top_k: int = 5,
        metadata_filters: Optional[Dict[str, str]] = None,
    ) -> List[RetrievedSection]:
        """Return up to *top_k* sections most relevant to *query*.

        *metadata_filters* is an optional key-value map; sections whose
        metadata does not match all filters are excluded before ranking.
        """
        all_pairs = self._index.all_sections()
        if not all_pairs:
            return []

        # Apply metadata filters.
        if metadata_filters:
            all_pairs = [
                (doc_id, sec)
                for doc_id, sec in all_pairs
                if all(sec.metadata.get(k) == v for k, v in metadata_filters.items())
            ]
        if not all_pairs:
            return []

        # Build corpus for BM25.
        corpus = [_tokenise(sec.heading + " " + sec.content) for _, sec in all_pairs]
        bm25 = BM25Okapi(corpus)
        scores = bm25.get_scores(_tokenise(query))

        # Rank and take top-k.
        ranked = sorted(
            zip(scores, all_pairs), key=lambda x: x[0], reverse=True
        )[:top_k]

        # Normalise scores to [0, 1].
        max_score = ranked[0][0] if ranked and ranked[0][0] > 0 else 1.0

        results: List[RetrievedSection] = []
        for raw_score, (doc_id, sec) in ranked:
            if raw_score <= 0:
                break  # remaining sections have zero relevance
            trust_tier = sec.metadata.get("trust_tier", "public")
            results.append(
                RetrievedSection(
                    document_id=doc_id,
                    section_id=sec.section_id,
                    heading=sec.heading,
                    content=sec.content,
                    score=float(raw_score / max_score),
                    trust_tier=trust_tier,
                    metadata=dict(sec.metadata),
                )
            )
        return results
