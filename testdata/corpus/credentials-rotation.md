# Credentials Rotation Procedures

## Overview

All credentials used by the RAG Gateway have defined rotation schedules.
Rotation must be performed without service interruption where possible.
Emergency rotation (suspected compromise) takes priority over schedules.
All rotation events must be logged in the change management system.

## JWT Secret Rotation

The `JWT_SECRET` environment variable must be rotated every 90 days.
Generate a new secret with `openssl rand -hex 32`. Update the secret in
the secrets manager, then restart the gateway within 1 hour. All existing
JWTs signed with the old secret will be immediately invalidated on restart.
Notify API users 24 hours in advance when possible. After rotation, verify
that the `/health` endpoint returns 200 and that a newly minted token is
accepted by the `/api/v1/query` endpoint.

## Database Credentials

The Postgres `rag_gateway` user password must be rotated monthly.
Procedure: (1) generate a new password with `openssl rand -base64 24`;
(2) run `ALTER USER rag_gateway PASSWORD '<new-password>';` on the database;
(3) update `DATABASE_URL` in the secrets manager; (4) perform a rolling
restart of the gateway. Verify connectivity by checking that the audit
log table receives new entries after restart.

## vLLM API Key

The `VLLM_API_KEY` must be rotated every 30 days. Coordinate with the
vLLM operator to generate a new key and update both the vLLM configuration
and the gateway's `VLLM_API_KEY` environment variable simultaneously to
avoid a window of downtime. After rotation, send a test request and confirm
HTTP 200 from the `/ready` endpoint.

## Adapter HMAC Signing Key

The HMAC key used by the Adapter Service to sign compiled adapters is
embedded at build time. Rotate it with each production deployment by
rebuilding the adapter-service container with a new `ADAPTER_SIGNING_KEY`
secret. This invalidates all adapters that were compiled before the rotation,
so rotate only during a maintenance window when no compile sessions are active.

## Emergency Rotation

If any credential is suspected to be compromised: (1) immediately revoke all
active LoRA adapters via the OPA deny-all policy for compile; (2) rotate the
affected credential without waiting for the schedule; (3) review audit logs
for the past 24 hours for anomalous access patterns; (4) file a P1 incident
report and notify security@example.com. Do not wait for confirmation of
compromise before rotating — rotate on suspicion.
