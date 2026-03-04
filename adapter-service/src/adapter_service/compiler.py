"""Doc-to-LoRA / Text-to-LoRA compilation logic.

The Compiler generates a PEFT-format LoRA adapter directory from a set of
retrieved document section IDs. In production this would invoke Sakana
Text-to-LoRA (once a HyperModulator checkpoint is trained for the target model).
Currently it produces a structurally valid stub adapter for end-to-end testing.
"""

import json
import os
import struct
import time
import uuid


# Minimal PEFT adapter_config.json for Qwen3-MoE target modules.
_ADAPTER_CONFIG_TEMPLATE = {
    "base_model_name_or_path": "Qwen/Qwen3.5-35B-A3B",
    "bias": "none",
    "fan_in_fan_out": False,
    "lora_alpha": 32,
    "lora_dropout": 0.05,
    "peft_type": "LORA",
    "r": 16,
    "target_modules": ["q_proj", "v_proj"],
    "task_type": "CAUSAL_LM",
}

# safetensors header for a minimal tensor file (zero-weight stub).
# Format: 8-byte little-endian header length + JSON metadata + zero-padded tensors.
_SAFETENSORS_STUB = b""  # written by _write_stub_safetensors()


def _write_stub_safetensors(path: str) -> None:
    """Write a minimal valid safetensors file with a single zero-weight tensor.

    Real T2L adapters would contain actual LoRA weight tensors here.
    This stub is structurally valid so vLLM can parse the file header.
    """
    # One minimal tensor: shape [1, 1], dtype F32, offset 0, size 4 bytes.
    metadata = {
        "__metadata__": {"format": "pt"},
        "stub_weight": {
            "dtype": "F32",
            "shape": [1, 1],
            "data_offsets": [0, 4],
        },
    }
    header_json = json.dumps(metadata, separators=(",", ":")).encode("utf-8")
    header_len = struct.pack("<Q", len(header_json))  # 8-byte little-endian
    tensor_data = b"\x00" * 4  # four bytes of zeros for the 1x1 F32

    with open(path, "wb") as f:
        f.write(header_len)
        f.write(header_json)
        f.write(tensor_data)


class Compiler:
    """Compiles document sections into a session-scoped LoRA adapter.

    The output is a PEFT-format directory that vLLM can load via
    /v1/load_lora_adapter.
    """

    def __init__(self, store_path: str = "/tmp/adapters"):
        """Args:
            store_path: Root directory where adapter directories are written.
                        Must be on a filesystem shared with the Gateway and vLLM.
        """
        self._store_path = store_path
        os.makedirs(store_path, exist_ok=True)

    def compile(
        self,
        session_id: str,
        trace_id: str,
        section_ids: list[str],
        ttl_seconds: int = 300,
    ) -> dict:
        """Generate a LoRA adapter directory for the given document sections.

        Args:
            session_id: Gateway session identifier (used for correlation).
            trace_id: Request trace ID for audit logging.
            section_ids: IDs of retrieved sections to specialise the adapter for.
            ttl_seconds: Adapter lifetime in seconds (5–1800).

        Returns:
            dict with keys: adapter_id (str), adapter_path (str), expires_at (int unix ts).
        """
        adapter_id = f"adapter-{uuid.uuid4().hex[:12]}"
        adapter_dir = os.path.join(self._store_path, adapter_id)
        os.makedirs(adapter_dir, exist_ok=True)

        # Write adapter_config.json.
        config = dict(_ADAPTER_CONFIG_TEMPLATE)
        config["adapter_id"] = adapter_id
        config["session_id"] = session_id
        config["trace_id"] = trace_id
        config["section_ids"] = section_ids
        with open(os.path.join(adapter_dir, "adapter_config.json"), "w") as f:
            json.dump(config, f, indent=2)

        # Write stub weight file.
        _write_stub_safetensors(os.path.join(adapter_dir, "adapter_model.safetensors"))

        expires_at = int(time.time()) + max(0, ttl_seconds)
        return {
            "adapter_id": adapter_id,
            "adapter_path": adapter_dir,
            "expires_at": expires_at,
        }
