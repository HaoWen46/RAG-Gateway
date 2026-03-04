"""gRPC server for PageIndex worker."""

from __future__ import annotations

import logging
import os
import sys
from concurrent import futures

import grpc

# Allow the generated pb code to resolve its own 'retrieval.v1' imports.
_PB_DIR = os.path.join(os.path.dirname(__file__), "pb")
if _PB_DIR not in sys.path:
    sys.path.insert(0, _PB_DIR)

from retrieval.v1 import retrieval_pb2, retrieval_pb2_grpc  # noqa: E402

from pageindex_worker.indexer import DocumentIndex  # noqa: E402
from pageindex_worker.retriever import Retriever  # noqa: E402

logger = logging.getLogger(__name__)


class RetrievalServicer(retrieval_pb2_grpc.RetrievalServiceServicer):
    """Implements the RetrievalService gRPC interface."""

    def __init__(self) -> None:
        self._doc_index = DocumentIndex()
        self._retriever = Retriever(self._doc_index)

    # ------------------------------------------------------------------
    # Index RPC
    # ------------------------------------------------------------------
    def Index(self, request, context):
        try:
            n = self._doc_index.index(
                document_id=request.document_id,
                content=request.content,
                metadata=dict(request.metadata),
            )
            logger.info(
                "indexed document %s into %d sections", request.document_id, n
            )
            return retrieval_pb2.IndexResponse(
                success=True,
                message=f"indexed {n} sections",
            )
        except Exception as exc:  # pylint: disable=broad-except
            logger.exception("Index failed: %s", exc)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(str(exc))
            return retrieval_pb2.IndexResponse(success=False, message=str(exc))

    # ------------------------------------------------------------------
    # Retrieve RPC
    # ------------------------------------------------------------------
    def Retrieve(self, request, context):
        try:
            top_k = request.top_k if request.top_k > 0 else 5
            sections = self._retriever.retrieve(
                query=request.query,
                top_k=top_k,
                metadata_filters=dict(request.metadata_filters) or None,
            )
            logger.info(
                "retrieve query=%r top_k=%d returned=%d",
                request.query,
                top_k,
                len(sections),
            )
            pb_sections = [
                retrieval_pb2.RetrievedSection(
                    document_id=s.document_id,
                    section_id=s.section_id,
                    content=f"## {s.heading}\n\n{s.content}" if s.heading else s.content,
                    score=s.score,
                    trust_tier=s.trust_tier,
                    metadata=s.metadata,
                )
                for s in sections
            ]
            return retrieval_pb2.RetrieveResponse(sections=pb_sections)
        except Exception as exc:  # pylint: disable=broad-except
            logger.exception("Retrieve failed: %s", exc)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(str(exc))
            return retrieval_pb2.RetrieveResponse()


def serve():
    logging.basicConfig(level=logging.INFO)
    port = os.environ.get("PAGEINDEX_GRPC_PORT", "50052")
    max_workers = int(os.environ.get("PAGEINDEX_WORKERS", "4"))

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=max_workers))
    retrieval_pb2_grpc.add_RetrievalServiceServicer_to_server(
        RetrievalServicer(), server
    )
    server.add_insecure_port(f"[::]:{port}")
    logger.info("PageIndex worker listening on :%s", port)
    server.start()
    server.wait_for_termination()


if __name__ == "__main__":
    serve()
