"""gRPC server for Adapter service."""

import os
from concurrent import futures

import grpc


def serve():
    port = os.environ.get("ADAPTER_GRPC_PORT", "50053")
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    # TODO: Register Adapter gRPC servicer once protos are compiled
    server.add_insecure_port(f"[::]:{port}")
    print(f"Adapter service listening on :{port}")
    server.start()
    server.wait_for_termination()


if __name__ == "__main__":
    serve()
