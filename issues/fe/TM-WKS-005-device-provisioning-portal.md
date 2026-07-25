# TM-WKS-005: Portal support for device-scoped agent provisioning

- Area: frontend
- Priority: P1
- Status: open
- Source: [Device-scoped agent provisioning ADR](../../docs/decisions/2026-07-24-device-scoped-agent-provisioning.md)

## Problem

Onboarding a workstation with multiple agents currently requires one Portal
enrollment round trip per agent, and the same credential must be configured
into both paxl and paxm by hand. The accepted ADR introduces device-level
credentials: an Owner/Admin creates one device enrollment per machine, and
agents on that machine self-provision through the device credential. The
Portal must expose this lifecycle.

Backend contract: `POST /v1/device/agent-provisions` plus device listing and
cascade-revocation endpoints (tracked separately; see the ADR sections 2
and 4 for the exact shapes and authorization rules).

## Acceptance criteria

- Enrollment creation offers a `device` type. A device enrollment carries a
  human-readable device name and always grants exactly the `agent_provision`
  permission; the agent-type permission matrix is hidden for this type.
- A Devices list page shows each device credential: device name, creator,
  provisioned agent count, last activity, and status (active/revoked).
- A Device detail page lists the agents provisioned by that device
  (`provisioned_by` attribution) with their own status and last-used time.
- Revoking a device shows a cascade preview (the agent credentials that will
  be revoked with it) before confirmation, then revokes the device and its
  provisioned agent credentials.
- Agent list and agent detail show a `provisioned_by` badge distinguishing
  human-registered agents from device self-registered agents.
- All mutations go through `web/src/api/actions.ts` (Idempotency-Key,
  `resource_version` + `If-Match`, CSRF header); one-time secrets follow the
  existing enrollment-token handling rules.
- Vitest covers the device enrollment form, list/detail rendering, cascade
  preview, and the provisioned-by badge; `npm test` and `npm run build` pass.
