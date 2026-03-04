"""pytest configuration: add pb directory to sys.path so generated protos resolve."""
import sys
import os

# The generated adapter_pb2_grpc.py uses bare `from adapter.v1 import ...` imports.
# Adding the pb directory to sys.path makes these resolve correctly.
_pb_dir = os.path.join(
    os.path.dirname(__file__), "..", "src", "adapter_service", "pb"
)
if _pb_dir not in sys.path:
    sys.path.insert(0, os.path.abspath(_pb_dir))
