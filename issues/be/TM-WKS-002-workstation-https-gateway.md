# TM-WKS-002: Put the workstation Portal behind the supported HTTPS gateway

- Area: backend/deployment
- Priority: P1
- Status: implemented in repository; live workstation rollout pending
- Source: [2026-07-22 workstation exploration](../../docs/workstation-portal-exploration-2026-07-22.md)

## Problem

The browser-visible workstation Portal is currently served from
`http://100.125.72.76:58080`. The root document and `/healthz` respond through
nginx over plain HTTP.

This does not match `deployment-instruction.md`, which requires a stable HTTPS
Portal origin, exact OIDC/Portal URL alignment, and exposure of only the TLS
gateway to the network.

The mismatch is already causing a frontend functional failure:
`crypto.randomUUID()` is unavailable on the plain-HTTP, non-localhost origin,
which makes Create Agent crash. Private-network transport encryption does not
turn an HTTP URL into a browser secure context.

## Observed backend behavior

- `GET /healthz` returned `200` and `{"status":"ok"}`.
- unauthenticated `GET /v1/me` returned `401 unauthorized` as expected.
- authenticated Audit and Operations queries loaded successfully through the
  Portal.

The issue is therefore the workstation exposure and gateway configuration, not
a confirmed handler or storage failure.

## Acceptance criteria

- The normal user-facing URL is `https://<stable-hostname>/` with a certificate
  trusted by every supported client workstation.
- Only the TLS gateway is network-reachable; PostgreSQL and the backend HTTP
  listener remain internal or loopback-bound as documented.
- Plain HTTP redirects to the canonical HTTPS origin or returns an explicit
  operator-facing error.
- `TEAM_MEMORY_PORTAL_URL`, `TEAM_MEMORY_OIDC_REDIRECT_URL`, the OIDC provider
  registration, and the browser-visible origin match exactly.
- Portal, `/v1/...`, and `/healthz` are served on the same canonical origin.
- A black-box workstation smoke test uses the externally reachable HTTPS URL,
  verifies the certificate chain without `-k`, signs in, and opens Create Agent.
- Deployment validation fails when the rendered Compose/gateway configuration
  exposes the backend port directly to the network.

## Frontend dependency

- [TM-WKS-001](../fe/TM-WKS-001-action-key-crashes-http-portal.md) tracks
  frontend resilience when `crypto.randomUUID` is unavailable.

## Resolution

Implemented in the repository:

- the workstation Compose override binds PostgreSQL and Team Memory to
  `127.0.0.1` without changing the base development Compose behavior;
- `make workstation-config-check` renders the two-file workstation Compose
  configuration without printing it and rejects:
  - backend or PostgreSQL bindings outside `127.0.0.1`;
  - an IP address, URL, or `localhost` as the persistent Portal host;
  - missing, remapped, or loopback-only Caddy host ports 80 or 443;
  - a Portal URL or OIDC callback that differs from the canonical HTTPS host;
  - non-Secure Human Session cookies;
- the deployment runbook now requires this validator before startup and in the
  acceptance checklist;
- the validator is covered by fifteen Node tests and is included in
  `make test-scripts`.

Verification completed on 2026-07-22:

- `make workstation-config-check` passed against a rendered two-file Compose
  fixture with dummy, non-secret values;
- rendered two-file workstation Compose ports were confirmed as
  `127.0.0.1:55432 -> postgres:5432` and
  `127.0.0.1:58080 -> team-memory:8080`;
- Caddy validation passed and reported automatic HTTP-to-HTTPS redirects;
- `make lint test` passed in a fresh isolated PostgreSQL project with 80.0%
  aggregate coverage;
- `make onprem-e2e` passed the public-HTTP Docker observation/recall and Capsule
  Channel flows.

The currently observed workstation remains on
`http://100.125.72.76:58080` until the operator rolls out this configuration.
SSH deployment could not be performed from this session because the host
rejected the available public-key authentication. The local Docker daemon is
attached to a different Tailscale workstation (`100.78.51.20`), so its similarly
named Compose project is not the deployment target and was left unchanged. Do
not mark the issue closed until the following live checks pass:

- [ ] choose and resolve the stable Portal DNS hostname;
- [ ] deploy with `compose.yaml` plus `deploy/workstation/compose.yaml` after
  `make workstation-config-check` succeeds on the workstation;
- [ ] install or distribute trust for the Caddy internal root certificate;
- [ ] verify HTTPS `/healthz` without `-k` from the client workstation;
- [ ] verify HTTP redirects to the canonical HTTPS URL;
- [ ] complete OIDC login and open Create Agent from the HTTPS Portal.
