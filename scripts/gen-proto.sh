#!/usr/bin/env bash
set -euo pipefail

PROTO_DIR="proto"
GO_GATEWAY_OUT="gateway/internal/pb"
GO_RETRIEVAL_OUT="retrieval/internal/pb"
PY_PAGEINDEX_OUT="pageindex-worker/src/pageindex_worker/pb"
PY_ADAPTER_OUT="adapter-service/src/adapter_service/pb"

mkdir -p "$GO_GATEWAY_OUT" "$GO_RETRIEVAL_OUT" "$PY_PAGEINDEX_OUT" "$PY_ADAPTER_OUT"

# Go: retrieval proto
protoc \
  --proto_path="$PROTO_DIR" \
  --go_out="$GO_RETRIEVAL_OUT" --go_opt=paths=source_relative \
  --go-grpc_out="$GO_RETRIEVAL_OUT" --go-grpc_opt=paths=source_relative \
  retrieval/v1/retrieval.proto

# Go: adapter proto (gateway needs it as client)
protoc \
  --proto_path="$PROTO_DIR" \
  --go_out="$GO_GATEWAY_OUT" --go_opt=paths=source_relative \
  --go-grpc_out="$GO_GATEWAY_OUT" --go-grpc_opt=paths=source_relative \
  retrieval/v1/retrieval.proto adapter/v1/adapter.proto

# Python: retrieval proto
python -m grpc_tools.protoc \
  --proto_path="$PROTO_DIR" \
  --python_out="$PY_PAGEINDEX_OUT" \
  --grpc_python_out="$PY_PAGEINDEX_OUT" \
  retrieval/v1/retrieval.proto

# Python: adapter proto
python -m grpc_tools.protoc \
  --proto_path="$PROTO_DIR" \
  --python_out="$PY_ADAPTER_OUT" \
  --grpc_python_out="$PY_ADAPTER_OUT" \
  adapter/v1/adapter.proto

echo "Proto generation complete."
