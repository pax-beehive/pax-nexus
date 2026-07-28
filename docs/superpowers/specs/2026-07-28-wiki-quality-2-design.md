# Wiki quality phase 2 design

Date: 2026-07-28
Status: approved by user (design A/B/C plus reset-rebuild deployment confirmed)

## Problem

With the LLM planner live (PR #26), the user reports three quality gaps in the
generated wiki, confirmed by inspecting the deployment:

1. **Pages are too short.** The editor receives only the planner-selected
   exact quotes as material, so articles are thin summaries of fragments.
2. **The maintainer prefers creating pages over updating them.** Overlapping
   subjects accumulate near-duplicates (four separate single-team-deployment
   pages) instead of one evolving page. The planner prompt's "prefer updating"
   is too weak, and the catalog it sees is polluted by legacy heading-chunk
   pages that were never wiped.
3. **Low-value knowledge still gets in.** The user calibrated four noise
   categories that must be skipped: one-off session narratives, transient
   operational/verification records, content unrelated to the team, and
   single bug-fix details that establish no lasting convention.

Additionally the user wants the article-bottom `Source evidence` section
collapsed by default in the reader UI.

## Design

### A. Planner prompt — durability test, expanded noise list, hard update bias

Rewrite `pageWikiPlannerPrompt` (`internal/pagewiki/llm_session_planner.go`):

- Durability test: keep only knowledge a teammate would still need in a
  month — decisions and their rationale, architecture, conventions, durable
  project state, domain facts.
- Noise list extended with the user's four categories (in addition to the
  existing code/diff/log/tool/skill-doc/branch/timestamp list): one-off
  session narratives, transient operational or verification records
  (release checks, approvals, test-run logs), content unrelated to the
  team's work, single bug-fix details that establish no lasting convention.
- Update bias stated as a rule, not a preference: if any existing page covers
  the same subject or a parent subject, or the new evidence continues that
  subject's story, the action MUST be update; creating an overlapping page is
  an error. Group aggressively; most sessions should yield zero to two briefs.

No deterministic-layer changes: the slug-collision create→update remap and all
validation stay as shipped.

### B. Editor — full evidence context and article-length guidance

`LLMSessionEditor` (`internal/pagewiki/llm_session_editor.go`):

- `llmEditRequest` gains `EvidenceContext []string` (JSON `evidence_context`):
  the FULL content of each event in `Brief.EvidenceEventIDs` (in order,
  deduplicated), each capped at 8 KiB. The existing `Evidence` field (exact
  quotes) is unchanged and still drives deterministic citations.
- `pageWikiEnglishEditorPrompt` is rewritten to direct a complete article: a
  substantive lead plus two to six sections organized by meaning, expanding
  every point the material supports (background, rationale, consequences,
  current state) and stopping where the evidence stops. The "1-5 concise
  sections" phrasing is removed. Grounding, English-output, no-invention, and
  no-quotation rules stay.

### C. Reader UI — Source evidence collapsed by default

`web/src/pages/WikiPage.tsx` (`WikiMarkdown`): the section whose key is
`source-evidence` renders inside a `<details>` element (collapsed by default)
with `<summary>Source evidence</summary>`; all other sections render as
today. Minimal CSS for the summary affordance. Citations data and API are
untouched.

### D. Deployment

After A+B merge and the deployment updates, run the PageWiki reset-and-rebuild
(shipped in PR #28, exposed in the wiki UI) so legacy heading-chunk pages are
wiped and all sessions replay through the new prompts. This also cleans the
catalog that the update-bias rule matches against.

## Non-goals

- No deterministic validation or citation changes.
- No new configuration.
- No changes to Team Note extraction.

## Testing

- Planner: existing suite stays green (prompt-shape assertions in tests that
  check the system prompt must be updated only if they pin removed phrases);
  one assertion that the system prompt names the update-must rule and at least
  one of the new noise categories, so future prompt edits cannot silently drop
  them.
- Editor: request-payload test asserting `evidence_context` carries the full
  event content (beyond the exact quote) and respects the per-event cap;
  existing editor/acceptance tests stay green.
- UI: DOM test asserting the source-evidence section renders collapsed
  (`<details>` without `open`) and expands on toggle; the wiki suite stays
  green.
