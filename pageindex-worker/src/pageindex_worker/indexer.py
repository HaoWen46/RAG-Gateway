"""PageIndex-style document tree builder.

Documents are split into sections by Markdown headings (# / ## / ###) or by
double-newline paragraph boundaries when no headings are found.  Each section
becomes a leaf node in the tree.  The in-memory store maps document_id ->
list[Section].
"""

from __future__ import annotations

import re
import threading
from dataclasses import dataclass, field
from typing import Dict, List, Optional


@dataclass
class Section:
    section_id: str
    heading: str
    content: str
    level: int  # heading depth; 0 = paragraph (no heading)
    metadata: Dict[str, str] = field(default_factory=dict)


_HEADING_RE = re.compile(r"^(#{1,6})\s+(.+)$", re.MULTILINE)


def _split_by_headings(text: str) -> List[tuple]:
    """Return list of (level, heading, body) tuples."""
    matches = list(_HEADING_RE.finditer(text))
    if not matches:
        return []

    results: List[tuple] = []
    for i, m in enumerate(matches):
        level = len(m.group(1))
        heading = m.group(2).strip()
        start = m.end()
        end = matches[i + 1].start() if i + 1 < len(matches) else len(text)
        body = text[start:end].strip()
        results.append((level, heading, body))
    return results


def _split_by_paragraphs(text: str) -> List[tuple]:
    """Fall back: split on blank lines, treat each paragraph as a section."""
    paragraphs = [p.strip() for p in re.split(r"\n{2,}", text) if p.strip()]
    return [(0, f"Section {i + 1}", p) for i, p in enumerate(paragraphs)]


class DocumentIndex:
    """Holds all indexed documents in memory."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        # document_id -> list of sections
        self._docs: Dict[str, List[Section]] = {}

    def index(
        self,
        document_id: str,
        content: str,
        metadata: Optional[Dict[str, str]] = None,
    ) -> int:
        """Parse content into sections and store under document_id.

        Returns the number of sections created.
        """
        metadata = metadata or {}
        raw_sections = _split_by_headings(content) or _split_by_paragraphs(content)

        sections: List[Section] = []
        for i, (level, heading, body) in enumerate(raw_sections):
            sec = Section(
                section_id=f"{document_id}::{i}",
                heading=heading,
                content=body,
                level=level,
                metadata=dict(metadata),
            )
            sections.append(sec)

        with self._lock:
            self._docs[document_id] = sections

        return len(sections)

    def get(self, document_id: str) -> List[Section]:
        with self._lock:
            return list(self._docs.get(document_id, []))

    def all_sections(self) -> List[tuple]:
        """Return [(document_id, section), ...] for every indexed section."""
        with self._lock:
            result: List[tuple] = []
            for doc_id, sections in self._docs.items():
                for sec in sections:
                    result.append((doc_id, sec))
            return result
