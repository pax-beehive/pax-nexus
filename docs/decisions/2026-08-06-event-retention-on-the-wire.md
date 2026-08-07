# Where event retention belongs on the wire

Status: accepted

Date: 2026-08-06

## Context

`TEAM_MEMORY_OPERATIONS_EVENT_RETENTION` accepts anything from 24h to 90d
(`internal/app/config.go`). `OperationsService.normalizeTimeFilter` rejects any
request whose window exceeds it with a bare `operations.ErrInvalidInput`, which
the transport turns into a generic `400 invalid_request`.

The portal did not know the value. `TIME_WINDOW_PRESETS` was the fixed list
`["1h", "24h", "7d"]`, offered unconditionally on both the Overview page and
the Operations page. On any deployment configured below 7 days — including the
24h floor — the `7d` button was always present, always failed, and failed with
an error the user could not act on.

Modernist Portal phase 2b shipped a stopgap: a `title` tooltip reading
"Windows beyond the deployment retention are rejected by the backend". That
says the failure is expected without saying which windows are safe, and it is
invisible on touch input. Issue #86 deferred the real fix specifically so this
question could be settled first.

## Decision

**The service that enforces the limit publishes it, on the responses it serves.**

`operations.Summary` carries an `EventRetention` field. `OperationsService.Summary`
stamps it from `s.config.EventRetention` — the same field `normalizeTimeFilter`
just bounds-checked against — on the way out. It reaches the client as
`event_retention_seconds` on both `OperationsSummaryResponse` and
`OverviewResponse`, the two surfaces that each own a window selector.

Repositories do not set it. If one ever does, the service's value wins.

## Alternatives rejected

**On the `/v1/me` boot payload.** Tempting because it arrives before first
paint, so the selector would never offer a bad option even transiently. Rejected
because `/v1/me` answers *who the caller is*, and retention is a property of the
deployment's storage. It would also require plumbing operations configuration
into the identity handler, which today has no reason to know it — a layer
crossing bought purely to save a transient. Under this decision the first
response arrives with the number, and the default 24h window equals the
retention floor, so the very first request a page makes is always answerable.

**A dedicated deployment-descriptor endpoint.** Clean semantics, but a new
round trip on boot for one integer, and nothing else currently wants to live
there. Worth revisiting if a second deployment-level limit appears.

## Consequences

- The published limit and the enforced limit are the same value read once.
  They cannot drift, and the service test walks the boundary in both
  directions (exactly-retention accepted, one second past rejected) so a
  stamp wired to the wrong field fails rather than merely reporting oddly.
- The field is repeated across two response types. Accepted: both surfaces are
  independently reachable and each must be able to prune its own selector.
- The client treats an absent field as *unknown* and offers every window, so a
  portal build meeting an older backend degrades to today's behaviour rather
  than guessing a ceiling and hiding windows that do work.
- Windows beyond retention render disabled rather than omitted, so there is
  somewhere to put the reason ("This deployment keeps 24h of events"). The
  phase 2b tooltip is removed from both pages.
