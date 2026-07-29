# Team Memory Portal (web/)

Human Portal for the Team Memory on-prem Identity and Agent Registry. React +
TypeScript + Vite, self-contained (own package.json, tsconfig, build). The API
contract is `docs/on-prem-identity-frontend-integration.md`; the visual design
is ported from `docs/onprem-portal-mock.html`.

## Prerequisites

- Node.js 18+ and npm.
- The backend must have Human Identity configured, otherwise every page shows
  the 501 not-configured screen (doc section 1):
  - `TEAM_MEMORY_BOOTSTRAP_SECRET`
  - `TEAM_MEMORY_OIDC_*`
  - `TEAM_MEMORY_SECRET_PEPPER`
  - `TEAM_MEMORY_PORTAL_URL`
- For local plain-HTTP development also set
  `TEAM_MEMORY_HUMAN_COOKIE_SECURE=false`, otherwise the browser drops the
  Secure session cookie and login loops forever.

## Develop

```sh
npm install
npm run dev
```

The dev server proxies `/v1` to `http://localhost:58080` (the compose host
port). Override with:

```sh
VITE_API_ORIGIN=http://localhost:8080 npm run dev
```

Login uses top-level navigation to `/v1/auth/login`, so the OIDC round trip
works through the dev proxy exactly like same-origin production.

## Test and build

```sh
npm test        # vitest unit tests (api client, action layer, continuations, capabilities)
npm run build   # tsc --noEmit + vite build -> dist/
```

In production, serve `dist/` from the same origin as the API (or behind the
same gateway); there is no CORS support for a separate portal origin.

## Layout

- `src/api/client.ts` — `humanFetch` wrapper: same-origin relative URLs,
  `credentials: "include"`, double-submit CSRF (`tm_csrf` cookie ->
  `X-CSRF-Token`), `ApiError { status, code, diagnostic }` plus the
  `apiError(err, status?, code?)` type guard used at every catch site.
- `src/api/actions.ts` — Action Layer: every mutation. Idempotency-Key per
  user action, `resource_version` body + `If-Match` optimistic locking, no
  blind retries of one-time-secret creations.
- `src/api/queries.ts` — read side; all list endpoints treat cursors as
  opaque. Wiki reads live in `src/api/wiki.ts`; shared DTOs (incl. `Page<T>`)
  in `src/api/types.ts`.
- `src/auth/` — auth context (boot `GET /v1/me`) and the discriminated-union
  route-guard states.
- `src/lib/` — capability matrix, continuations (return_url /
  pending_invitation), credential derived status, status-to-UI mapping,
  validation, and the shared hooks (`usePagedList`, `usePolling`,
  `usePolledRegion`, `useAgentDetail`).
- `src/components/` — shared widgets (badges, modals, toasts) and the larger
  composed pieces: `AgentDetailView`, `PagedListCard`, `RegionError`, and the
  wiki rendering components under `components/wiki/`.
- `src/pages/` — login, bootstrap, join, entry, suspended, My Agents, agent
  detail, and the admin console (members, invitations, agents, audit). The
  operations console is split: a slim `AdminOperationsPage` composing the
  region views, drawers, and hooks under `pages/operations/`.

## Security rules enforced here

- Invitation / enrollment / bootstrap secrets live only in memory or
  tab-scoped sessionStorage — never localStorage, URLs (except the join
  fragment, erased immediately), logs, or error reporting.
- Flow branches use HTTP status plus stable structured error codes; safe
  diagnostic messages are never string-matched.
