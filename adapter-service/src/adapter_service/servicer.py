"""gRPC servicer implementation for AdapterService."""

import logging
import os
import sys

# Add pb directory to sys.path so generated imports like `from adapter.v1 import ...` resolve.
_pb_dir = os.path.join(os.path.dirname(__file__), "pb")
if _pb_dir not in sys.path:
    sys.path.insert(0, _pb_dir)

from adapter_service.compiler import Compiler
from adapter_service.probes import CanaryProbes
from adapter_service.signer import Signer
from adapter_service.pb.adapter.v1 import adapter_pb2, adapter_pb2_grpc

logger = logging.getLogger(__name__)


class AdapterServiceServicer(adapter_pb2_grpc.AdapterServiceServicer):
    """Implements the AdapterService gRPC contract.

    Responsible for:
    - Compiling document sections into a PEFT-format LoRA adapter (Compile)
    - Verifying adapter integrity via HMAC + canary probes (Verify)
    - Revoking adapters (Revoke)
    """

    def __init__(self):
        secret = os.environ.get("ADAPTER_SIGNING_SECRET", "changeme-signing-secret")
        store_path = os.environ.get("ADAPTER_STORE_PATH", "/tmp/adapters")
        self._signer = Signer(secret)
        self._compiler = Compiler(store_path)
        self._probes = CanaryProbes()
        # In-memory registry: adapter_id → {"path": str, "signature": str}
        self._registry: dict[str, dict] = {}

    def Compile(self, request, context):
        """Generate a LoRA adapter from the given section IDs."""
        logger.info(
            "Compile: session=%s trace=%s sections=%d ttl=%ds",
            request.session_id,
            request.trace_id,
            len(request.section_ids),
            request.ttl_seconds,
        )

        result = self._compiler.compile(
            session_id=request.session_id,
            trace_id=request.trace_id,
            section_ids=list(request.section_ids),
            ttl_seconds=request.ttl_seconds if request.ttl_seconds > 0 else 300,
        )

        adapter_id = result["adapter_id"]
        adapter_path = result["adapter_path"]
        expires_at = result["expires_at"]

        # Sign: HMAC over adapter_id + section_ids (sorted for determinism).
        signed_data = (adapter_id + "".join(sorted(request.section_ids))).encode()
        signature = self._signer.sign(adapter_id, signed_data)

        self._registry[adapter_id] = {
            "path": adapter_path,
            "signature": signature,
            "signed_data": signed_data,
        }

        logger.info("Compile: adapter_id=%s expires_at=%d", adapter_id, expires_at)
        return adapter_pb2.CompileResponse(
            adapter_id=adapter_id,
            signature=signature,
            expires_at=expires_at,
        )

    def Verify(self, request, context):
        """Verify adapter integrity and run canary probes."""
        adapter_id = request.adapter_id
        logger.info("Verify: adapter_id=%s", adapter_id)

        entry = self._registry.get(adapter_id)
        if entry is None:
            logger.warning("Verify: unknown adapter_id=%s", adapter_id)
            return adapter_pb2.VerifyResponse(valid=False, probe_results=[
                adapter_pb2.ProbeResult(
                    probe_name="registry_lookup",
                    passed=False,
                    detail="adapter_id not found in registry",
                )
            ])

        # Verify HMAC signature.
        sig_ok = self._signer.verify(adapter_id, entry["signed_data"], request.signature)
        if not sig_ok:
            logger.warning("Verify: HMAC mismatch for adapter_id=%s", adapter_id)
            return adapter_pb2.VerifyResponse(valid=False, probe_results=[
                adapter_pb2.ProbeResult(
                    probe_name="hmac_check",
                    passed=False,
                    detail="signature mismatch — adapter integrity compromised",
                )
            ])

        # Run canary probes.
        probe_results_raw = self._probes.run_all(adapter_id)
        probe_results = [
            adapter_pb2.ProbeResult(
                probe_name=p["probe_name"],
                passed=p["passed"],
                detail=p["detail"],
            )
            for p in probe_results_raw
        ]

        all_passed = all(p.passed for p in probe_results)
        logger.info(
            "Verify: adapter_id=%s all_passed=%s", adapter_id, all_passed
        )
        return adapter_pb2.VerifyResponse(valid=all_passed, probe_results=probe_results)

    def Revoke(self, request, context):
        """Remove an adapter from the registry."""
        adapter_id = request.adapter_id
        existed = adapter_id in self._registry
        self._registry.pop(adapter_id, None)
        logger.info("Revoke: adapter_id=%s existed=%s", adapter_id, existed)
        return adapter_pb2.RevokeResponse(success=existed)
