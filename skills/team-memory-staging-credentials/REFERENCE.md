# Provider notes

## WorkOS is not a conformant OIDC provider

It publishes an OIDC discovery document, and the parts that matter for
discovery are genuine: the issuer in the document matches the requested URL
(go-oidc rejects a mismatch outright), and the flow is authorization-code with
PKCE. Two things are not standard, and both cost a debugging session to find.

**The authorize endpoint needs a connection selector.** A request carrying
only the standard OIDC parameters is rejected as
`invalid-connection-selector` and never reaches a sign-in page. AuthKit needs
`provider=authkit`, supplied through `TEAM_MEMORY_OIDC_AUTH_PARAMS`.

**The token endpoint issues no ID token.** It returns `access_token`,
`refresh_token`, and a `user` object; the access token carries no email claim.
Identity therefore comes from the user object, selected with
`TEAM_MEMORY_OIDC_IDENTITY_SOURCE=token_response_user`. That setting is
opt-in rather than an automatic fallback, so a genuinely conformant provider
that merely fails to return an ID token cannot silently degrade to the weaker
path.

Losing the ID token loses the nonce binding. That is acceptable only because
the token response arrives over TLS straight from the token endpoint rather
than through the browser, so it is not attacker-controllable, and nonce exists
to stop a front-channel ID token replay that cannot happen when there is no ID
token. State and PKCE still bind the exchange to one browser and one flow.

Values needed:

- Issuer: `https://api.workos.com/user_management/<client id>`
- Client secret: the WorkOS API key (`oidc-client-secret`)
- Redirect URI, registered in the WorkOS dashboard under
  **Applications → your application → Redirects**, and matched exactly:
  `https://nexus-stg.paxtech.net/v1/auth/callback`

Redirect URIs are per-environment and have no public API — only the dashboard.

**Unverified**: whether WorkOS emits `email_verified: true`. If it does not,
bootstrap still succeeds but every later invitation acceptance fails
permanently with 410. Check it on a real login before inviting anyone.

## Cloudflare

The token is scoped to `Zone : DNS : Edit` on `paxtech.net` alone. It is
account-scoped, so `GET /user/tokens/verify` answers 401 even though the token
works — verify against `/zones` instead.

`paxtech.net` already serves other things: `app`, `api`, `ws`, `demo`, `cg`,
`console`, and `portal` are taken, and several sit behind Cloudflare Access.
`app.paxtech.net` in particular is **not** available for a future production
host despite what earlier planning assumed.

There is no wildcard Access application today — the apex and `portal` answer
200 rather than redirecting — which is why `nexus-stg` is reachable at all.
**Adding a wildcard Access application would put a login wall in front of the
OIDC flow and break authentication.**

The staging host is `nexus-stg`, a single-level subdomain, so Cloudflare's
universal certificate covers it. A second level (`nexus.stg`) would require
Advanced Certificate Manager.

## Embeddings

The stored vector width follows the configured model, and the
`team_notes.embedding` column is resized to match during migration. Changing
the model therefore discards existing vectors and re-embeds them, which the
backfill loop does on its own.

Staging runs OpenAI `text-embedding-3-small` at its native 1536. Local and
on-prem run a Qwen embedding sidecar that needs no credential;
`TEAM_MEMORY_EMBEDDING_API_KEY` is sent only when set.

## GCP behaviours that cost time

- Cloud SQL defaults to the Enterprise Plus edition, which rejects shared-core
  tiers. The edition has to be set explicitly.
- A Certificate Manager certificate cannot attach to a target HTTPS proxy
  directly; it needs a certificate map, and the reference needs the
  `//certificatemanager.googleapis.com/` prefix.
- Cloud Armor allows at most ten source ranges per rule, so Cloudflare's list
  is chunked across rules.
- Until the certificate map entry reaches ACTIVE, every request through
  Cloudflare is a 525. That is propagation, not misconfiguration.
- Public access — a storage bucket's objects or a Cloud Run service's invoker
  — needs an `allUsers` binding, which the organization's
  domain-restricted-sharing policy blocks by default. The staging project has
  an explicit exemption; a new project will need one too.
