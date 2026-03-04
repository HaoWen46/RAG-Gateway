"""gRPC server for the Adapter Service."""

import logging
import os
import sys
from concurrent import futures

import grpc

# Add pb directory to sys.path so generated imports like `from adapter.v1 import ...` resolve.
_pb_dir = os.path.join(os.path.dirname(__file__), "pb")
if _pb_dir not in sys.path:
    sys.path.insert(0, _pb_dir)

from adapter_service.pb.adapter.v1 import adapter_pb2_grpc
from adapter_service.servicer import AdapterServiceServicer

logging.basicConfig(level=logging.INFO)
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
