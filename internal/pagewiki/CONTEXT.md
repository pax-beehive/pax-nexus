# PageWiki

The shipping wiki product: durable, cited Wiki Pages created and revised from
Evidence Lake evidence, published over HTTP and read in the Human Portal.

## Language

**Page**: an immutable-revisioned knowledge unit with cited evidence quotes.

**Session Document**: the bounded session evidence a planner run consumes.

**Planner / Editor**: the LLM pair that chooses evidence and writes page
revisions (`llm_session_planner.go`, `llm_session_editor.go`).

## Relationships

- Consumes Evidence Lake evidence via the session consumer.
- Persists through its own repository port (`ports.go`); adapters live in
  `pagewiki/postgres` and `pagewiki/memory`.
- Uses the shared LLM chat client from `internal/platform/llm`.
- Does not import Team Note domain packages, and Team Note does not import
  PageWiki domain packages.
- Team-configurable generation settings (`GenerationDirectives`,
  `generation_settings.go`) are loaded once per `InjectSession` run and
  threaded into the planner, editor, and tree indexer inputs
  (`PlanInput`/`EditInput`/`TreeIndexInput.Directives` in `ports.go`,
  `service.go`); `Service.GenerationSettings`/`SetGenerationSettings` expose
  the repository-backed store to the HTTP layer.
