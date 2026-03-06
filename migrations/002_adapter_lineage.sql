-- Milestone 8: Adapter Lineage — add columns needed for full compile pipeline audit trail.

-- adapter_id is the gRPC-returned identifier (distinct from the Postgres UUID pk).
ALTER TABLE adapters ADD COLUMN IF NOT EXISTS adapter_id TEXT;

-- probe_results stores the JSON array of canary probe outcomes from Verify RPC.
ALTER TABLE adapters ADD COLUMN IF NOT EXISTS probe_results JSONB;

-- revoke_reason records why an adapter was revoked (e.g. "canary_probe_failure", "ttl_expired").
ALTER TABLE adapters ADD COLUMN IF NOT EXISTS revoke_reason TEXT;

-- Backfill adapter_id for any existing rows (cast UUID pk as text placeholder).
UPDATE adapters SET adapter_id = id::text WHERE adapter_id IS NULL;

-- Enforce NOT NULL and uniqueness now that all rows have a value.
ALTER TABLE adapters ALTER COLUMN adapter_id SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_adapters_adapter_id ON adapters(adapter_id);
