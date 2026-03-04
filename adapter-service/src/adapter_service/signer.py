"""Adapter signing and verification."""

import hashlib
import hmac


class Signer:
    """Signs and verifies LoRA adapters for integrity."""

    def __init__(self, secret: str = ""):
        self._secret = secret.encode()

    def sign(self, adapter_id: str, data: bytes) -> str:
        """Generate HMAC signature for an adapter."""
        msg = adapter_id.encode() + data
        return hmac.new(self._secret, msg, hashlib.sha256).hexdigest()

    def verify(self, adapter_id: str, data: bytes, signature: str) -> bool:
        """Verify an adapter's signature."""
        expected = self.sign(adapter_id, data)
        return hmac.compare_digest(expected, signature)
