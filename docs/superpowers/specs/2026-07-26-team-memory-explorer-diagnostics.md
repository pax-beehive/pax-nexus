# Team Memory Explorer and Diagnostics

## Goal

Add an Owner-only, read-only Human Portal surface for inspecting current Team
Notes and tracing how source Session Events became notes and were later used by
recall. Extend the existing Operations detail links so Extraction Runs and
Capsule Channel Envelopes can be diagnosed without database access.

## Security boundary

- Add a server-issued `view.team-memory` Human capability.
- Only an active Owner Membership receives the capability.
- Admin and Member Memberships receive `403 forbidden` from every Explorer and
  diagnostic endpoint.
- The Portal gates navigation and routes on the server-issued capability.
- Team Note bodies and source Session Event content may be returned to an
  authorized Owner.
- Never return raw recall queries, credential secrets, authorization headers,
  enrollment or invitation tokens, Channel idempotency keys, or unfiltered
  Session Event metadata.
- The feature is read-only. It does not edit, delete, re-run, or replay product
  state.

## HTTP interface

### List Team Notes

`GET /v1/admin/team-notes`

Optional filters:

- `q`
- `kind`
- `state`
- `agent_id`
- `task_ref`
- `thread_ref`
- `limit`
- `cursor`

Results are ordered by `updated_at DESC, note_id DESC`. Cursors are opaque.
Changing a filter resets pagination.

### Get Team Note

`GET /v1/admin/team-notes/:note_id`

Returns:

- the current Team Note;
- related Team Notes resolved from `related_subjects`;
- immutable revision history;
- evidence Session Events for each revision;
- the Extraction Run and Candidate that produced each revision;
- deliveries of each revision;
- Recall Observations whose trace considered or delivered the note.

Recall Observations expose only the existing safe projection: IDs, recipient
Agent/Session, time, dispositions and rejection/budget reasons. They do not
return the raw query, recalled text, or full raw trace.

### Get Extraction Run diagnostic

`GET /v1/admin/diagnostics/extractions/:run_id`

Returns run status, actor, sequence range, model/prompt version, token usage,
timing, safe error code or reason, candidate admission/rejection results,
source Session Events, and resulting Team Note references.

### Get Channel Envelope diagnostic

`GET /v1/admin/diagnostics/channels/:envelope_id`

Returns sender/recipient Agent IDs, lifecycle timestamps, status, message and a
safe Knowledge Capsule projection containing capsule ID, source Agent/Session,
keyword, title, summary, content, route match type and truncation state. It
does not return the idempotency key or arbitrary payload JSON.

## Portal

- Add `Explorer` to the existing `Insights` navigation group.
- Add `/admin/explorer` with current card, badge, filter, table and cursor
  pagination styles.
- Add `/admin/explorer/notes/:noteId` with cards for the note, relations,
  revisions/evidence, Extraction provenance and Recall usage.
- Link recent Team Notes in Team Pulse to the note detail page.
- Operation Event rows with `recall_observation`, `extraction_run`, or
  `channel_envelope` detail IDs open the matching diagnostic drawer.
- Reuse the current warm-white/terracotta palette, typography, cards, badges,
  tables, drawers, region-local loading/error states and responsive behavior.

## Required behavior tests

- Active Owner receives `view.team-memory`; Admin, Member and inactive Owner do
  not.
- Owner can list filtered notes with stable cursor pagination.
- Admin and Member cannot list notes or load any diagnostic.
- Note detail preserves revision/evidence/run/delivery relationships and
  returns safe Recall Observation projections.
- Extraction detail distinguishes admitted and rejected candidates.
- Channel detail decodes supported Knowledge Capsule payloads and rejects or
  safely degrades malformed/unknown payloads without leaking raw JSON.
- Explorer filters reset pagination.
- Portal does not mount or request Explorer routes without the capability.
- Pulse and Operations links open the correct detail surfaces.

## Non-goals

- Team Note mutation or deletion.
- Extraction replay.
- Showing raw recall queries or complete raw recall traces.
- General-purpose Session Lake browsing.
- Multi-Team routing.
