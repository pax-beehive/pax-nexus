# PageWiki LLM Planner design

Date: 2026-07-27
Status: approved by user (direction, degradation policy, and backlog handling)

## Problem

The deployed PageWiki (Session Lake UI) produces low-quality pages. Inspection
of the running instance (136 pages) and the code shows the whole generation
path is deterministic — no LLM is involved:

- `main.go` hardcodes `pagewiki.SessionDocumentPlanner{}` as the only planner.
- The default editor (`LLMWIKI_ORGANIZER_MODE` empty or `local`) is
  `SessionDocumentEditor`, which pastes raw source quotes as page bodies.
- `sessionKnowledgeUnits` chunks each session event by `## ` Markdown headings;
  every heading fragment becomes a page. Code diffs, JSON fragments, git branch
  names, CLI logs, and agent skill documents all became "knowledge" pages
  (e.g. pages titled `Checklist`, `+0x78`, `!= "" && parsed`,
  `main...origin/main`).
- Coverage loss: at most 8 knowledge units per source revision (first come,
  first served); content before the first `## ` heading in an event with
  headings is dropped entirely; slug collisions silently merge unrelated facts.
- Topic classification is a hardcoded keyword table; the default bucket
  (`Knowledge/Concepts`) absorbed 76 of 136 pages.

Even when the existing `LLMSessionEditor` is enabled, it only rewrites prose
for units the deterministic chunker already chose (via
`knowledgeUnitForBrief`), so it cannot merge, split, drop, or reclassify
garbage units.

## Goal

Page selection, noise rejection, titling, and topic placement become LLM
decisions; prose becomes LLM-written articles. The deterministic layers that
already work — evidence anchoring, exact-quote citation validation, CAS
publication, navigation — stay authoritative and unchanged.

Non-goals: no changes to the postgres repository, publication transaction,
citation validation, HTTP API, or UI. No migration logic for existing garbage
pages (see Backlog handling).

## Design

### LLMSessionPlanner (new, implements existing `Planner` port)

One LLM call per source revision. Request payload:

1. every event of the source revision: event ID, role, content — with
   per-event truncation and a declared truncation notice when the revision is
   too large for one call;
2. the current `PageCatalog` (slug + title list) so the model can choose
   update-vs-create.

Response (strict JSON, no prose): an array of briefs

```json
{
  "action": "create | update | skip_noise",
  "target_slug": "existing slug, update only",
  "proposed_slug": "new slug, create only",
  "proposed_title": "...",
  "reader_goal": "...",
  "topic_path": ["Engineering", "Runtime"],
  "evidence": [{"event_id": "...", "exact_quote": "..."}]
}
```

The prompt states the noise policy explicitly: skip code/diff/JSON fragments,
tool output, agent system and skill documents, CLI logs; keep only knowledge
durable for the team; when in doubt, skip.

### Deterministic validation of planner output

LLM output is untrusted. Inside the planner, after decoding:

- every `event_id` must exist in the source revision;
- every `exact_quote` must occur exactly once inside its event's content
  (matching the service's `uniqueTextRange` requirement); invalid quotes are
  dropped, and a brief with no surviving evidence is dropped;
- `update` briefs resolve `target_slug` to `TargetPageID` and
  `ExpectedBaseRevisionID` deterministically from the catalog — the LLM never
  sees or emits internal IDs;
- slug normalization and a cap of 10 briefs per revision;
- `skip_noise` briefs are dropped inside the planner — they exist only so the
  model can account for every event; the domain `PageAction` enum is unchanged.
  If every brief is dropped, the planner returns a single `source-only` brief
  (same shape the deterministic planner uses for empty sessions).

### Degradation policy (approved)

LLM call fails or returns invalid JSON → retry once → on second failure return
a single `source-only` brief and record the failure reason in the maintenance
run. Never fall back to the heading chunker: evidence is preserved without
creating garbage pages, and a later re-run can build pages from the stored
source revision.

### Editor and contract changes

- `PageBrief` gains `Evidence []EvidenceQuoteDraft` — the exact quotes the
  planner selected.
- `LLMSessionEditor` consumes `input.Brief.Evidence` instead of re-deriving
  units through `knowledgeUnitForBrief` (that dependency is removed). The
  deterministic runtime still appends the `Source evidence` section and builds
  citations from the brief evidence, exactly as it does today from unit quotes.
- `SessionDocumentPlanner` / `SessionDocumentEditor` remain as the `local`
  mode fallback pair; no behavior change in `local` mode.

### Configuration and wiring

`LLMWIKI_ORGANIZER_MODE=openai|harness` now builds the LLM planner AND the LLM
editor as a pair, reusing `workspace.DeepSeekClient` (OpenAI-compatible) with
the existing `LLMWIKI_LLM_BASE_URL` / `LLMWIKI_LLM_API_KEY` /
`LLMWIKI_LLM_MODEL` variables. `local` or empty keeps the deterministic pair.
`main.go`'s `buildPageWikiEditor` becomes `buildPageWikiMaintainers` returning
`(pagewiki.Planner, pagewiki.Editor)`.

### Backlog handling (approved)

The new planner is incremental-only. The existing 136 pages are not migrated:
the deployment is reset (wipe pages, re-inject sessions). No migration code in
this iteration.

## Testing

- Unit tests with a scripted `ChatClient` (existing `scripted.go` pattern):
  invalid event IDs, non-unique quotes, malformed JSON, retry, and the
  source-only degradation path.
- Acceptance test modeled on `inject_acceptance_test.go`: a mixed session
  fixture containing an agent skill document, a code diff, and genuine team
  knowledge; assert noise events produce no pages and the genuine knowledge
  produces a page with correctly anchored citations.
- Manual acceptance: replay sessions on the workstation deployment and verify
  the navigation tree contains no heading-fragment pages.
