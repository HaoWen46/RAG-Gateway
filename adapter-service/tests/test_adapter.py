"""Tests for the Adapter Service: compiler, signer, servicer."""

import json
import os
import struct
import tempfile
import time

import pytest

from adapter_service.compiler import Compiler
from adapter_service.signer import Signer


# ---------------------------------------------------------------------------
# Compiler tests
# ---------------------------------------------------------------------------


class TestCompiler:
    def setup_method(self):
        self.tmp = tempfile.mkdtemp()
        self.compiler = Compiler(store_path=self.tmp)

    def test_compile_creates_directory(self):
        result = self.compiler.compile(
            session_id="sess1",
            trace_id="trace1",
            section_ids=["d1::0", "d1::1"],
            ttl_seconds=300,
        )
        assert os.path.isdir(result["adapter_path"])

    def test_compile_writes_adapter_config(self):
        result = self.compiler.compile(
            session_id="sess1",
            trace_id="trace1",
            section_ids=["d1::0"],
            ttl_seconds=300,
        )
        config_path = os.path.join(result["adapter_path"], "adapter_config.json")
        assert os.path.isfile(config_path)
        with open(config_path) as f:
            cfg = json.load(f)
        assert cfg["peft_type"] == "LORA"
        assert cfg["r"] == 16
        assert "q_proj" in cfg["target_modules"]
        assert cfg["section_ids"] == ["d1::0"]

    def test_compile_writes_safetensors(self):
        result = self.compiler.compile(
            session_id="sess1",
            trace_id="trace1",
            section_ids=["d1::0"],
            ttl_seconds=300,
        )
        weight_path = os.path.join(result["adapter_path"], "adapter_model.safetensors")
        assert os.path.isfile(weight_path)
        # Verify safetensors format: first 8 bytes are header length (little-endian uint64).
        with open(weight_path, "rb") as f:
            raw = f.read()
        header_len = struct.unpack("<Q", raw[:8])[0]
        header_json = raw[8 : 8 + header_len]
        metadata = json.loads(header_json)
        assert "stub_weight" in metadata

    def test_compile_returns_unique_adapter_ids(self):
        r1 = self.compiler.compile("s1", "t1", ["d1::0"], 300)
        r2 = self.compiler.compile("s2", "t2", ["d1::1"], 300)
        assert r1["adapter_id"] != r2["adapter_id"]

    def test_compile_expires_at_is_future(self):
        result = self.compiler.compile("s1", "t1", ["d1::0"], 60)
        assert result["expires_at"] > int(time.time())

    def test_compile_default_ttl_capped(self):
        # TTL of 0 should still produce a future expires_at (treated as 0 seconds out).
        result = self.compiler.compile("s1", "t1", ["d1::0"], 0)
        assert result["expires_at"] >= int(time.time())


# ---------------------------------------------------------------------------
# Signer tests
# ---------------------------------------------------------------------------


class TestSigner:
    def setup_method(self):
        self.signer = Signer("test-secret")

    def test_sign_returns_hex_string(self):
        sig = self.signer.sign("adapter-1", b"some data")
        assert isinstance(sig, str)
        assert len(sig) == 64  # sha256 hex digest

    def test_verify_valid_signature(self):
        data = b"section-data"
        sig = self.signer.sign("adapter-1", data)
        assert self.signer.verify("adapter-1", data, sig) is True

    def test_verify_invalid_signature(self):
        sig = self.signer.sign("adapter-1", b"data")
        assert self.signer.verify("adapter-1", b"data", "badhex" + "0" * 58) is False

    def test_verify_different_adapter_id(self):
        data = b"data"
        sig = self.signer.sign("adapter-1", data)
        # Different adapter_id → different expected signature → mismatch
        assert self.signer.verify("adapter-2", data, sig) is False

    def test_verify_different_data(self):
        sig = self.signer.sign("adapter-1", b"original")
        assert self.signer.verify("adapter-1", b"tampered", sig) is False

    def test_different_secrets_produce_different_sigs(self):
        s1 = Signer("secret-a")
        s2 = Signer("secret-b")
        data = b"data"
        assert s1.sign("adapter-1", data) != s2.sign("adapter-1", data)


# ---------------------------------------------------------------------------
# Servicer integration tests (without gRPC transport — call methods directly)
# ---------------------------------------------------------------------------


class TestAdapterServiceServicer:
    def setup_method(self):
        self.tmp = tempfile.mkdtemp()
        os.environ["ADAPTER_SIGNING_SECRET"] = "test-secret"
        os.environ["ADAPTER_STORE_PATH"] = self.tmp
        # Import here so env vars are set before instantiation
        from adapter_service.servicer import AdapterServiceServicer
        self.servicer = AdapterServiceServicer()

    def _make_compile_request(self, section_ids=None):
        from adapter_service.pb.adapter.v1 import adapter_pb2
        req = adapter_pb2.CompileRequest()
        req.session_id = "sess-test"
        req.trace_id = "trace-test"
        req.ttl_seconds = 300
        for sid in (section_ids or ["d1::0"]):
            req.section_ids.append(sid)
        return req

    def test_compile_returns_adapter_id_and_signature(self):
        req = self._make_compile_request()
        resp = self.servicer.Compile(req, context=None)
        assert resp.adapter_id.startswith("adapter-")
        assert len(resp.signature) == 64
        assert resp.expires_at > int(time.time())

    def test_verify_valid_adapter(self):
        compile_req = self._make_compile_request()
        compile_resp = self.servicer.Compile(compile_req, context=None)

        from adapter_service.pb.adapter.v1 import adapter_pb2
        verify_req = adapter_pb2.VerifyRequest()
        verify_req.adapter_id = compile_resp.adapter_id
        verify_req.signature = compile_resp.signature
        verify_resp = self.servicer.Verify(verify_req, context=None)

        assert verify_resp.valid is True
        assert len(verify_resp.probe_results) > 0
        assert all(p.passed for p in verify_resp.probe_results)

    def test_verify_bad_signature_fails(self):
        compile_req = self._make_compile_request()
        compile_resp = self.servicer.Compile(compile_req, context=None)

        from adapter_service.pb.adapter.v1 import adapter_pb2
        verify_req = adapter_pb2.VerifyRequest()
        verify_req.adapter_id = compile_resp.adapter_id
        verify_req.signature = "0" * 64  # wrong signature
        verify_resp = self.servicer.Verify(verify_req, context=None)

        assert verify_resp.valid is False

    def test_verify_unknown_adapter_fails(self):
        from adapter_service.pb.adapter.v1 import adapter_pb2
        verify_req = adapter_pb2.VerifyRequest()
        verify_req.adapter_id = "nonexistent-adapter"
        verify_req.signature = "0" * 64
        verify_resp = self.servicer.Verify(verify_req, context=None)

        assert verify_resp.valid is False

    def test_revoke_removes_adapter(self):
        compile_req = self._make_compile_request()
        compile_resp = self.servicer.Compile(compile_req, context=None)

        from adapter_service.pb.adapter.v1 import adapter_pb2
        revoke_req = adapter_pb2.RevokeRequest()
        revoke_req.adapter_id = compile_resp.adapter_id
        revoke_resp = self.servicer.Revoke(revoke_req, context=None)
        assert revoke_resp.success is True

        # Verify after revoke should fail (not in registry).
        verify_req = adapter_pb2.VerifyRequest()
        verify_req.adapter_id = compile_resp.adapter_id
        verify_req.signature = compile_resp.signature
        verify_resp = self.servicer.Verify(verify_req, context=None)
        assert verify_resp.valid is False

    def test_revoke_nonexistent_returns_false(self):
        from adapter_service.pb.adapter.v1 import adapter_pb2
        req = adapter_pb2.RevokeRequest()
        req.adapter_id = "does-not-exist"
        resp = self.servicer.Revoke(req, context=None)
        assert resp.success is False
