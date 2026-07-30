# Todo App

The first preset application: a todo list with suggestions derived from team memory.
Accepts tasks and completions from the human operator, reports actions as Evidence Lake evidence streams.

## Language

**Todo Item**: a task created or suggested to the operator, with accept/complete/dismiss actions.

**Suggestion**: a note excerpt (blocker or handoff) proposed as a potential todo item, deduplicated by note fingerprint.

**Action**: an operator response to a todo item: `accept` (add to active list), `complete` (mark done), or `dismiss` (ignore).

## Relationships

- Persists through its own repository port (`ports.go`); adapters live in
  `todoapp/postgres` and `todoapp/memory`.
- Consumes the team memory through platform adapters:
  - **NoteDirectory** (read.list): retrieves active blocker and handoff notes as suggestion sources.
  - **EvidenceSink** (report): reports operator actions as `app:todo` evidence streams (human-only source).
- Does not write to the Evidence Lake or Team Note knowledge directly.
- Uses the shared LLM chat client from `internal/platform/llm` to rewrite blocker/handoff note copy into actionable todo title/body text (no ranking is performed).
