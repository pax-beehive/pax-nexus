# TM-WKS-001: Action-key generation crashes the Portal

- Area: frontend
- Priority: P1
- Status: open
- Source: [2026-07-22 workstation exploration](../../docs/workstation-portal-exploration-2026-07-22.md)

## Problem

Opening the Create Agent form on the deployed Portal causes an uncaught render
exception and leaves a blank page.

The frontend generates idempotency keys with an unguarded call to
`crypto.randomUUID()` in `web/src/api/actions.ts`. The API is unavailable on
the deployed plain-HTTP, non-localhost origin. `CreateAgentModal` invokes the
helper during render, so the form cannot open.

Other consumers of the same helper are also at risk:

- invitation acceptance;
- Agent suspend, resume, and retire;
- enrollment revocation;
- credential revocation.

## Reproduction

1. Sign in to the workstation Portal as an active member.
2. Open `/agents`.
3. Click `+ Create Agent`.

Observed result:

- the complete Portal becomes blank;
- the console reports `TypeError: crypto.randomUUID is not a function`;
- a full page reload is required to recover.

Expected result:

- the Create Agent form opens with one stable idempotency key for that form
  instance.

## Relevant code

- `web/src/api/actions.ts`: `beginAction()` calls `crypto.randomUUID()`.
- `web/src/pages/MyAgentsPage.tsx`: Create Agent allocates its action key while
  rendering the modal.
- `web/src/pages/JoinPage.tsx`
- `web/src/components/AgentGovernanceCard.tsx`
- `web/src/components/AgentArtifacts.tsx`

## Acceptance criteria

- `beginAction()` does not make an unguarded call to an optional browser API.
- Every action receives a sufficiently unique, opaque idempotency key.
- One user-action instance reuses its key for retries; reopening or initiating
  a new action gets a new key.
- Opening Create Agent, invitation acceptance, Agent governance, and artifact
  revocation controls does not throw when `crypto.randomUUID` is absent.
- Vitest covers both native `randomUUID` and fallback/unavailable conditions.
- A production browser smoke test opens each action-key-allocating UI on every
  supported deployment scheme.

## Out of scope

- Changing backend idempotency semantics.
- Treating frontend fallback support as a replacement for the HTTPS deployment
  fix tracked in [TM-WKS-002](../be/TM-WKS-002-workstation-https-gateway.md).
