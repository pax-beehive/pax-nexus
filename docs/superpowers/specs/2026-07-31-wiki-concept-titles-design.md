# Wiki Concept Titles Design

Date: 2026-07-31
Status: approved

## Problem

Generated wiki pages read like session reports, not encyclopedia entries.
Two confirmed symptoms:

1. **Titles are sentence-shaped.** The planner's only constraint on
   `proposed_title` is "English title"; the editor's only constraint is
   "English title". Nothing tells either model that a title should be a
   concise concept name, so titles come out like "Fixing Xanadu links on
   the LLM planner path" instead of "Xanadu Links".
2. **Page granularity is event-shaped.** The planner decides page identity
   by "what happened this session" rather than "which durable concept is
   this evidence about", so pages accrete around activities instead of
   concepts. The editor and tree indexer inherit both problems downstream.

## Decision

Prompt-level fix across all three pipeline stages (planner, editor, tree
indexer), plus two cheap deterministic guards in the planner's brief
validation. No schema change, no migration. Existing pages are corrected by
the standard reset-and-rebuild replay after merge.

## Prompt changes

### Planner (`internal/pagewiki/llm_session_planner.go`, `pageWikiPlannerPrompt`)

Add a **page identity rule**:

- A page's subject is a durable concept — a component, subsystem, decision,
  convention, or domain fact — never an activity, task, fix, or session.
  Before proposing a create, name the concept the evidence is about; the
  page is about that concept, and this session's events are merely new
  evidence for it.
- `proposed_title` is a concise noun phrase naming that concept: at most
  five words, no sentence, no trailing period, and never opening with a
  verb or gerund such as "Fixing", "Adding", "Improving", "Updating".

### Editor (`internal/pagewiki/llm_session_editor.go`, `pageWikiEnglishEditorPrompt`)

- `title` must remain a concise concept noun phrase (same ≤5-word rule);
  when `current_text` already carries a good concise title, keep it rather
  than rewriting it into a sentence.
- Section `heading`s are likewise short noun phrases, not sentences.

### Tree indexer (`internal/pagewiki/llm_tree_indexer.go`, `pageWikiTreeIndexerPromptTemplate`)

- Topic `title` is a one-to-three-word noun phrase naming the subject
  area, never a sentence.

## Deterministic guards (planner only)

In `acceptBrief` (`llm_session_planner.go:240`), on the create path after
trimming the title:

- **Normalize:** strip trailing `.` / `。` from the title.
- **Reject:** a title longer than 9 words or 80 runes fails the brief
  (returns `false`, same as an empty title today — the events fall through
  as unplanned, which matches the existing behavior for invalid briefs).

Bounds are deliberately looser than the prompt's ≤5 words: the prompt does
the shaping, the guard only catches egregious sentence-titles. No gerund
blacklist in code — English morphology heuristics are brittle, and the
prompt covers it.

## Testing

- **Planner unit tests** (`llm_session_planner_test.go` style, table-driven
  on `acceptBrief`): trailing-period title is normalized; 10-word and
  81-rune titles are rejected; a 9-word/80-rune title passes.
- **Prompt regression:** existing tests that assert prompt content (if any)
  updated; otherwise no prompt snapshot tests are added — replay is the
  acceptance vehicle.

## Acceptance

Merge, then reset-and-rebuild replay on the workstation corpus; inspect
that new page titles are concept noun phrases and that session-shaped
pages from the same evidence no longer appear.

## Out of scope

- A consolidation/"gardener" pass that merges existing event-shaped pages
  into concept pages (方案 C) — revisit only if the replay still produces
  event-shaped pages.
- New evaluation dimensions in `internal/llmwiki/effecteval` — that harness
  scores Q&A over the spike workspace, not production pagewiki output.
- Code-level title validation in the editor or tree indexer.
