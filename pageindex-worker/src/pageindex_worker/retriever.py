"""Hybrid BM25 + semantic retrieval over the DocumentIndex.

When an embedding model is provided (any object with an ``.encode(texts)``
method returning a 2-D numpy array), retrieval uses Reciprocal Rank Fusion
(RRF) to combine BM25 keyword scores with cosine similarity scores.

Without a model the retriever falls back to pure BM25 (original behaviour).
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Dict, List, Optional

import numpy as np
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


def _rrf_combine(
    bm25_scores: np.ndarray,
    cosine_scores: np.ndarray,
    k: int = 60,
) -> np.ndarray:
    """Combine two score arrays via Reciprocal Rank Fusion.

    Each score array is converted to a rank order (0 = best), then the
    combined score is ``1/(k + rank_bm25) + 1/(k + rank_cosine)``.
    """
    n = len(bm25_scores)
    bm25_order = np.argsort(bm25_scores)[::-1]
    cos_order = np.argsort(cosine_scores)[::-1]

    bm25_rank = np.empty(n, dtype=float)
    cos_rank = np.empty(n, dtype=float)
    for rank, idx in enumerate(bm25_order):
        bm25_rank[idx] = rank
    for rank, idx in enumerate(cos_order):
        cos_rank[idx] = rank

    return 1.0 / (k + bm25_rank) + 1.0 / (k + cos_rank)


class Retriever:
    """Retrieval over all sections in a DocumentIndex.

    Pass *model* (a SentenceTransformer instance or any duck-typed equivalent
    with an ``.encode(texts, **kw)`` method) to enable hybrid retrieval.
    Omit it to use BM25-only retrieval (default, backward-compatible).
    """

    def __init__(self, index: DocumentIndex, model=None) -> None:
        self._index = index
        self._model = model
        # Cache section embeddings so we don't re-encode unchanged sections.
        self._embedding_cache: Dict[str, np.ndarray] = {}

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _get_section_embeddings(
        self, pairs: List[tuple]
    ) -> Optional[np.ndarray]:
        """Return an (n, dim) embedding matrix for *pairs*, or None."""
        if self._model is None:
            return None

        # Encode only sections not already in cache.
        to_encode_ids: List[str] = []
        to_encode_texts: List[str] = []
        for _doc_id, sec in pairs:
            if sec.section_id not in self._embedding_cache:
                to_encode_ids.append(sec.section_id)
                to_encode_texts.append(sec.heading + " " + sec.content)

        if to_encode_texts:
            new_embeddings = self._model.encode(
                to_encode_texts, show_progress_bar=False
            )
            for sid, emb in zip(to_encode_ids, new_embeddings):
                self._embedding_cache[sid] = np.asarray(emb, dtype=np.float32)

        return np.array(
            [self._embedding_cache[sec.section_id] for _doc_id, sec in pairs],
            dtype=np.float32,
        )

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def retrieve(
        self,
        query: str,
        top_k: int = 5,
        metadata_filters: Optional[Dict[str, str]] = None,
    ) -> List[RetrievedSection]:
        """Return up to *top_k* sections most relevant to *query*.

        Uses hybrid BM25 + cosine-RRF ranking when an embedding model is
        available; falls back to BM25-only otherwise.

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

        # BM25 scores.
        corpus = [_tokenise(sec.heading + " " + sec.content) for _, sec in all_pairs]
        bm25 = BM25Okapi(corpus)
        bm25_scores = np.array(bm25.get_scores(_tokenise(query)), dtype=float)

        # Hybrid: combine with cosine similarity if model is available.
        if self._model is not None:
            section_embeddings = self._get_section_embeddings(all_pairs)
            query_embedding = np.asarray(
                self._model.encode([query], show_progress_bar=False)[0],
                dtype=np.float32,
            )

            # Cosine similarity: (n,)
            sec_norms = np.linalg.norm(section_embeddings, axis=1)
            sec_norms[sec_norms == 0] = 1e-8
            q_norm = float(np.linalg.norm(query_embedding)) or 1e-8
            cosine_scores = (section_embeddings / sec_norms[:, None]) @ (
                query_embedding / q_norm
            )

            final_scores = _rrf_combine(bm25_scores, cosine_scores)
        else:
            final_scores = bm25_scores

        # Sort descending; in BM25-only mode, drop zero-score sections.
        ranked_indices = np.argsort(final_scores)[::-1]
        if self._model is None:
            ranked_indices = [i for i in ranked_indices if final_scores[i] > 0]
        ranked_indices = list(ranked_indices)[:top_k]

        if not ranked_indices:
            return []

        max_score = float(final_scores[ranked_indices[0]]) or 1.0

        results: List[RetrievedSection] = []
        for idx in ranked_indices:
            raw_score = float(final_scores[idx])
            doc_id, sec = all_pairs[idx]
            trust_tier = sec.metadata.get("trust_tier", "public")
            results.append(
                RetrievedSection(
                    document_id=doc_id,
                    section_id=sec.section_id,
                    heading=sec.heading,
                    content=sec.content,
                    score=raw_score / max_score,
                    trust_tier=trust_tier,
                    metadata=dict(sec.metadata),
                )
            )
        return results
