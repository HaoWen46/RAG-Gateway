"""gRPC server for PageIndex worker."""

import os
from concurrent import futures

import grpc


def serve():
    port = os.environ.get("PAGEINDEX_GRPC_PORT", "50052")
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    # TODO: Register PageIndex gRPC servicer once protos are compiled
    server.add_insecure_port(f"[::]:{port}")
    print(f"PageIndex worker listening on :{port}")
    server.start()
    server.wait_for_termination()


if __name__ == "__main__":
    serve()
