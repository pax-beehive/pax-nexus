# Workstation Portal exploratory test, 2026-07-22

## Scope

This was a black-box exploratory pass against the deployed Human Portal at
`http://100.125.72.76:58080` from an authenticated Chrome session with the
Owner role. No mutation was submitted. Create/edit forms were opened and
cancelled; read-only filters and detail views were exercised.

The following surfaces were covered:

- My Agents list and the Create Agent entry point;
- Members list and edit form;
- Invitations list and create form;
- All Agents list and filters;
- Audit Events list and event detail;
- Operations summary, storage snapshot/history, and recent activity;
- unauthenticated public HTTP checks for `/healthz` and `/v1/me`.

The backend was reachable during the pass: `/healthz` returned `200` with
`{"status":"ok"}`, and `/v1/me` without a browser session returned the expected
`401 unauthorized`. Audit detail and Operations storage history loaded
successfully in the authenticated browser.

## Findings

The actionable work is split by ownership:

- [frontend issues](../issues/fe/README.md);
- [backend and deployment issues](../issues/be/README.md).

### TM-WKS-001 - P1 - Create Agent crashes the entire Portal on the deployed HTTP origin

Steps to reproduce:

1. Open `/agents` while authenticated.
2. Click `+ Create Agent`.

Observed:

- the entire Portal becomes a blank page, including navigation and recovery
  controls;
- the browser reports `TypeError: crypto.randomUUID is not a function` from the
  deployed JavaScript bundle;
- reloading `/agents` restores the Portal.

Expected:

- the Create Agent form opens and the Portal remains usable.

The failure is deterministic on the deployed origin. `beginAction()` calls
`crypto.randomUUID()` directly in `web/src/api/actions.ts`, and the Create Agent
modal calls it during render in `web/src/pages/MyAgentsPage.tsx`. The Portal is
served over plain HTTP at a non-localhost address, where the browser does not
provide this secure-context API.

The same helper is also used by invitation acceptance, Agent
suspend/resume/retire, and enrollment/credential revocation. Those paths are
therefore at risk on the same deployment even though no destructive action was
attempted in this pass.

Suggested acceptance criteria:

- opening Create Agent never unmounts the Portal on a supported deployment;
- action-key generation works in every supported browser context, or the
  Portal refuses to start on an unsupported origin with a useful message;
- add a browser test that serves the production build on the actual supported
  scheme and opens every action that allocates an idempotency key.

### TM-WKS-002 - P1 - The workstation Portal is exposed through plain HTTP instead of the documented TLS gateway

The browser-visible origin is `http://100.125.72.76:58080`. The root document
and `/healthz` are served by nginx over HTTP. This does not match
`deployment-instruction.md`, which requires a stable HTTPS Portal origin and
states that only the TLS gateway should be exposed to the network.

This is already a functional failure, not only hardening debt: it is the
precondition for TM-WKS-001. It also makes browser security behavior depend on
an insecure origin even if the underlying private network encrypts transport.

Suggested acceptance criteria:

- the user-facing URL is `https://<stable-hostname>/` with a certificate trusted
  by the client workstation;
- the backend port is not the normal browser entry point;
- HTTP redirects to HTTPS or returns a clear operator-facing error;
- OIDC redirect URL, Portal URL, and the actual browser origin match exactly.

### TM-WKS-003 - P2 - An action-rendering exception has no in-product recovery boundary

TM-WKS-001 removes the whole Portal rather than isolating the failing form or
showing a recovery action. No React error boundary is present under `web/src`.
The only observed recovery was a full browser reload.

Suggested acceptance criteria:

- a form/rendering exception preserves the shell and current authenticated
  session;
- the affected region shows a concise error and a retry/close action;
- the exception is logged without exposing one-time secrets or request bodies.

### TM-WKS-004 - P2 - Several controls are ambiguous or missing dialog semantics

Observed examples:

- Members has two separate filter rows whose first buttons both have the
  accessible name `all`; neither group is labelled as status versus role.
- All Agents has an Owner filter `<select>` without an accessible label.
- Operations Recent activity has operation and outcome `<select>` controls
  without accessible labels, and the page has two distinct buttons both named
  `刷新`.
- the shared Modal component has no `role="dialog"`, `aria-modal`, labelled
  title relationship, Escape handling, or focus management.

These make keyboard and assistive-technology operation unreliable, and they
also make the controls difficult to address unambiguously in browser tests.

Suggested acceptance criteria:

- every filter group and select has a unique accessible name;
- same-label actions are scoped or named by region, such as `刷新存储` and
  `刷新最近活动`;
- Modal exposes dialog semantics, moves focus into the dialog, traps focus,
  closes on Escape, and restores focus to its trigger.

## Non-findings and limits

- Members, Invitations, All Agents, Audit Events, and Operations rendered and
  loaded their current empty/minimal datasets successfully.
- The Operations `1h` window updated correctly after its asynchronous refresh;
  an intermediate snapshot briefly showed the previous `24h` data, so this is
  not recorded as a bug.
- No invitation, Agent, enrollment, credential, membership, or session was
  created, changed, revoked, or deleted.
- Mutation behavior after form submission, OIDC login/logout, invitation
  acceptance, Agent enrollment/exchange, and mobile breakpoints remain
  untested in this pass.
