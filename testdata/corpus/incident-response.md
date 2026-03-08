# Incident Response Playbook

## Overview

This playbook covers how to respond to security and availability incidents
involving the RAG Gateway. Incidents are classified by severity (P1–P3).
All P1 and P2 incidents require a blameless postmortem within 48 hours.

## Severity Classification

P1 (Critical): The gateway is completely unavailable, or a confirmed security
breach has occurred (e.g., unauthorised access to confidential documents,
prompt injection that produced policy-violating output, adapter loading bypass).
SLO: on-call engineer acknowledged within 5 minutes.

P2 (High): A significant degradation is occurring, such as: policy bypass
suspected but unconfirmed, circuit breaker stuck open for more than 10 minutes,
rate limiting not enforcing correctly, or canary probes failing on every
adapter compile. SLO: acknowledged within 15 minutes.

P3 (Low): Elevated error rates (above 5% of requests failing), slow response
times (p99 latency above 30 seconds), or a single failed canary probe that
did not repeat. SLO: triaged within 2 hours, resolved within 24 hours.

## P1 Response Procedure

Step 1: Page the on-call engineer immediately. Do not wait for auto-escalation.
Step 2: Within 5 minutes, assess whether to roll back the last deployment.
Step 3: If a security breach is suspected, freeze all compile jobs by setting
the OPA compile policy to deny-all, and revoke all active LoRA adapters.
Step 4: Preserve evidence — capture gateway logs, audit table snapshot, and
active adapter lineage records before any rollback.
Step 5: Notify security@example.com and the team lead within 15 minutes.
Step 6: Restore service using the last known-good container image.

## P2 Response Procedure

Step 1: Acknowledge the alert and open a war-room channel.
Step 2: Check the Grafana dashboard for the affected metric panels.
Step 3: Review the last 50 audit log entries for anomalous patterns.
Step 4: If policy bypass is suspected, run the OPA dry-run tool against
recent request inputs to confirm whether the policy rule is correct.
Step 5: Escalate to P1 if the issue cannot be contained within 30 minutes.

## Escalation Matrix

Security incidents (policy bypass, injection that reached the LLM, adapter
signature mismatch): escalate immediately to the security team lead.
Availability incidents (gateway down, vLLM unreachable): escalate to the
platform on-call engineer. Data incidents (audit logs missing, Postgres
unavailable): escalate to the data team. All P1/P2 incidents are reported
to security@example.com within 15 minutes of detection.

## Post-Incident Review

A blameless postmortem is mandatory for all P1 and P2 incidents. The review
must be completed within 48 hours. It must cover: timeline, root cause,
impact assessment, what worked well, what did not, and action items with
owners and due dates. Postmortems are stored in the internal wiki and
reviewed quarterly for systemic patterns.
