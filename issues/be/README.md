# Backend and deployment issues

Source: [Workstation Portal exploratory test](../../docs/workstation-portal-exploration-2026-07-22.md).

| Priority | Status | Issue | Summary |
| --- | --- | --- | --- |
| P1 | Implemented; rollout pending | [TM-WKS-002](TM-WKS-002-workstation-https-gateway.md) | Enforce the supported TLS gateway and reject unsafe workstation configurations. |

No separate backend API logic defect was confirmed in this pass. `/healthz`,
authentication rejection for an unauthenticated `/v1/me`, audit detail, the
Operations summary, storage snapshot, and storage history behaved as expected.
