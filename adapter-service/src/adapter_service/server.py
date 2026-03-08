"""gRPC server for the Adapter Service."""

import json
import logging
import os
import sys
import traceback
from concurrent import futures

import grpc

# Add pb directory to sys.path so generated imports like `from adapter.v1 import ...` resolve.
_pb_dir = os.path.join(os.path.dirname(__file__), "pb")
if _pb_dir not in sys.path:
    sys.path.insert(0, _pb_dir)

from adapter_service.pb.adapter.v1 import adapter_pb2_grpc
from adapter_service.servicer import AdapterServiceServicer


class _JsonFormatter(logging.Formatter):
    """Emit one JSON object per log line with consistent fields."""

    def format(self, record: logging.LogRecord) -> str:
        payload: dict = {
            "ts": self.formatTime(record, "%Y-%m-%dT%H:%M:%SZ"),
            "level": record.levelname.lower(),
            "component": record.name,
            "msg": record.getMessage(),
        }
        if record.exc_info:
            payload["error"] = traceback.format_exception(*record.exc_info)[-1].strip()
        return json.dumps(payload)


def _configure_logging(level: int = logging.INFO) -> None:
    handler = logging.StreamHandler()
    handler.setFormatter(_JsonFormatter())
    logging.root.handlers = []
    logging.root.addHandler(handler)
    logging.root.setLevel(level)


_configure_logging()
logger = logging.getLogger(__name__)


def serve():
    port = os.environ.get("ADAPTER_GRPC_PORT", "50053")
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    adapter_pb2_grpc.add_AdapterServiceServicer_to_server(
        AdapterServiceServicer(), server
    )
    server.add_insecure_port(f"[::]:{port}")
    logger.info("Adapter service listening on :%s", port)
    server.start()
    server.wait_for_termination()


if __name__ == "__main__":
    serve()
