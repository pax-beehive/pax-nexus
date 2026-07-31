# SaaS Identity via WorkOS

Status: Accepted

Date: 2026-08-01

Related:

- [On-Prem Core with Multi-Team SaaS](./2026-07-31-onprem-saas-split.md)
- [On-Prem Identity and Agent Registry](./2026-07-21-on-prem-identity-and-agent-registry.md)

## Context

The SaaS distribution (multi-team, teams isolated; see the split ADR) needs
human sign-in. The product is already a standard OIDC relying party
(`internal/deployment/onprem/oidc.go`, go-oidc, single issuer via
`TEAM_MEMORY_OIDC_*`), and users are keyed by
`(identity_issuer, identity_subject)`.

Options considered:

- **A — direct workspace/social OIDC** (register Google + GitHub OAuth
  apps, extend config to multiple issuers): zero external dependency, but
  every future enterprise ask (customer's Okta/Azure AD, SAML, SCIM
  provisioning, MFA policy) becomes our code.
- **B — identity broker (WorkOS)**: the app keeps exactly one issuer —
  WorkOS AuthKit — and the broker terminates Google/GitHub/enterprise
  SAML/SCIM behind it.
- **C — self-hosted Keycloak/Zitadel**: rejected; adds the ops burden the
  GCP move is meant to shed.

## Decision

Route B: WorkOS AuthKit is the sole OIDC issuer for the SaaS profile.

- The existing single-issuer RP code is reused nearly unchanged — the
  issuer config points at WorkOS; the multi-issuer refactor route A would
  have required is unnecessary.
- User identity stays `(identity_issuer, identity_subject)`; the subject
  is the WorkOS user id, stable across the user's linked login methods.
- Social login (Google, GitHub) is enabled in WorkOS from day one;
  enterprise SSO (SAML/OIDC per customer) and SCIM are switched on
  per-customer later, with WorkOS Organizations mapped to our teams when
  that lands.
- Team membership, roles, sessions, CSRF, and agent credentials remain
  ours (control plane, phase 3 of the split ADR) — WorkOS answers "who is
  this human", never "what may they touch".
- The on-prem distribution is unaffected: it keeps the bring-your-own
  OIDC issuer configuration it has today.

## Consequences

- MFA, passwordless, breached-password checks, and enterprise IdP quirks
  are delegated; our auth surface stays one issuer wide.
- New external dependency on WorkOS availability for SaaS login (not for
  on-prem, not for agent-credential auth); AuthKit's free tier covers the
  foreseeable MAU, enterprise SSO connections are billed per connection —
  priced into enterprise-tier plans when those exist.
- Sign-up flow to build in phase 3: WorkOS login → create team or accept
  invitation; domain-capture auto-join deferred.
