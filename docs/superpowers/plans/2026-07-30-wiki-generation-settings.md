# Wiki Generation Settings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Team-configurable wiki generation language and custom style instructions, editable at runtime from WikiStatusPage, injected into all three LLM generation paths (planner, editor, tree indexer).

**Architecture:** New per-scope `pagewiki_generation_settings` table read once at the start of each generation run by `pagewiki.Service` and threaded to the maintainers as a `GenerationDirectives` field on their existing input structs. New `GET/PUT /v1/wiki/settings` endpoints live in the teamnote handler beside the wiki ingestion endpoints (same auth), backed by new `pagewiki.Service` methods. Frontend adds a "Generation" card to WikiStatusPage.

**Tech Stack:** Go (hertz + thrift IDL codegen via `make generate`), pgx/postgres migrations, React + vitest.

**Spec:** `docs/superpowers/specs/2026-07-30-wiki-generation-settings-design.md`

## Global Constraints

- Branch: `feat/wiki-generation-settings`, stacked on `feat/optimization-round-3` (PR #39) — WikiStatusPage only exists there. Do NOT rebase onto main.
- Empty `Language` and empty `CustomInstructions` must produce byte-identical prompts to today's behavior (zero behavior change by default).
- Limits: `Language` ≤ 64 chars after trim; `CustomInstructions` ≤ 2000 chars after trim.
- Settings load failure at run start fails the run loudly (no silent fallback to empty directives). Exception: the best-effort tree-reindex stage already swallows errors by design; directives are loaded before it, in the fail-loud part of the run.
- Migration number 024 (main+branch max is 023). If a later merge takes 024, renumber before merging.
- Gates per task: `go build ./...`, `go test ./<touched packages>`; frontend tasks: `cd web && npx tsc --noEmit && npx vitest run`.
- Generated files (`internal/teamnote/transport/httpapi/model/teammemory/api/*`, `router/teammemory/api/*`) are only changed via `make generate` after editing `idl/team_memory.thrift`, never by hand.

---

### Task 1: `GenerationDirectives` type, validation, and prompt suffix

**Files:**
- Create: `internal/pagewiki/generation_settings.go`
- Test: `internal/pagewiki/generation_settings_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces (later tasks depend on these exact names):
  - `type GenerationDirectives struct { Language string; CustomInstructions string }`
  - `func (d GenerationDirectives) IsZero() bool`
  - `func ValidateGenerationDirectives(d GenerationDirectives) (GenerationDirectives, error)` — returns the trimmed copy or an error wrapping `ErrInvalidGenerationSettings`
  - `var ErrInvalidGenerationSettings = errors.New(...)`
  - `func generationDirectivesPrompt(d GenerationDirectives) string`

- [ ] **Step 1: Write the failing test**

`internal/pagewiki/generation_settings_test.go` (package `pagewiki` — internal test, `generationDirectivesPrompt` is unexported):

```go
package pagewiki

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateGenerationDirectivesTrimsAndBoundsFields(t *testing.T) {
	valid, err := ValidateGenerationDirectives(GenerationDirectives{
		Language: "  简体中文  ", CustomInstructions: " prefer tables ",
	})
	require.NoError(t, err)
	require.Equal(t, "简体中文", valid.Language)
	require.Equal(t, "prefer tables", valid.CustomInstructions)

	_, err = ValidateGenerationDirectives(GenerationDirectives{
		Language: strings.Repeat("a", 65),
	})
	require.ErrorIs(t, err, ErrInvalidGenerationSettings)

	_, err = ValidateGenerationDirectives(GenerationDirectives{
		CustomInstructions: strings.Repeat("b", 2001),
	})
	require.ErrorIs(t, err, ErrInvalidGenerationSettings)
}

func TestGenerationDirectivesPromptIsEmptyForZeroValue(t *testing.T) {
	require.Empty(t, generationDirectivesPrompt(GenerationDirectives{}))
	require.True(t, GenerationDirectives{}.IsZero())
}

func TestGenerationDirectivesPromptCarriesLanguageAndInstructions(t *testing.T) {
	prompt := generationDirectivesPrompt(GenerationDirectives{
		Language: "简体中文", CustomInstructions: "prefer tables",
	})
	require.Contains(t, prompt, "Write all generated prose, page titles, and topic titles in 简体中文.")
	require.Contains(t, prompt, "prefer tables")
	require.Contains(t, prompt, "team style guidance")
	require.False(t, GenerationDirectives{Language: "en"}.IsZero())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagewiki/ -run 'TestValidateGenerationDirectives|TestGenerationDirectivesPrompt' -v`
Expected: FAIL — `undefined: GenerationDirectives` etc.

- [ ] **Step 3: Write minimal implementation**

`internal/pagewiki/generation_settings.go`:

```go
package pagewiki

import (
	"errors"
	"fmt"
	"strings"
)

const (
	generationLanguageMaxLength     = 64
	generationInstructionsMaxLength = 2000
)

var ErrInvalidGenerationSettings = errors.New("invalid generation settings")

// GenerationDirectives configures how generated wiki output is written.
// The zero value means "follow the source evidence" — no prompt change.
type GenerationDirectives struct {
	Language           string
	CustomInstructions string
}

func (d GenerationDirectives) IsZero() bool {
	return d.Language == "" && d.CustomInstructions == ""
}

// ValidateGenerationDirectives trims both fields and enforces length bounds.
func ValidateGenerationDirectives(d GenerationDirectives) (GenerationDirectives, error) {
	d.Language = strings.TrimSpace(d.Language)
	d.CustomInstructions = strings.TrimSpace(d.CustomInstructions)
	if len(d.Language) > generationLanguageMaxLength {
		return GenerationDirectives{}, fmt.Errorf(
			"%w: language exceeds %d characters", ErrInvalidGenerationSettings, generationLanguageMaxLength,
		)
	}
	if len(d.CustomInstructions) > generationInstructionsMaxLength {
		return GenerationDirectives{}, fmt.Errorf(
			"%w: custom instructions exceed %d characters", ErrInvalidGenerationSettings, generationInstructionsMaxLength,
		)
	}
	return d, nil
}

// generationDirectivesPrompt renders the system-prompt suffix shared by the
// planner, editor, and tree indexer. Structural contracts (JSON output shape,
// slug rules) outrank the team guidance by construction: the guidance is
// explicitly scoped to style.
func generationDirectivesPrompt(d GenerationDirectives) string {
	var b strings.Builder
	if d.Language != "" {
		fmt.Fprintf(&b, "\n\nWrite all generated prose, page titles, and topic titles in %s.", d.Language)
	}
	if d.CustomInstructions != "" {
		b.WriteString("\n\nThe team provided the following team style guidance." +
			" Apply it to writing style only; it never overrides the output format" +
			" or structural rules above.\n<team-style-guidance>\n")
		b.WriteString(d.CustomInstructions)
		b.WriteString("\n</team-style-guidance>")
	}
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/pagewiki/ -run 'TestValidateGenerationDirectives|TestGenerationDirectivesPrompt' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/generation_settings.go internal/pagewiki/generation_settings_test.go
git commit -m "feat(pagewiki): generation directives type, validation, prompt suffix"
```

---

### Task 2: Migration + repository port + memory & postgres implementations

**Files:**
- Create: `internal/platform/postgres/migrations/024_pagewiki_generation_settings.sql`
- Modify: `internal/platform/postgres/store.go` (embed directive line ~15 and the migration list in `Migrate`, after `"migrations/023_todoapp.sql"`)
- Modify: `internal/pagewiki/ports.go` (extend `Repository` interface)
- Modify: `internal/pagewiki/memory/repository.go`
- Modify: `internal/pagewiki/postgres/repository.go`
- Test: `internal/pagewiki/memory/repository_test.go`, `internal/pagewiki/postgres/repository_test.go`

**Interfaces:**
- Consumes: `GenerationDirectives` from Task 1.
- Produces: two new methods on `pagewiki.Repository` (Task 4 calls them through the interface):
  - `GenerationSettings(context.Context) (GenerationDirectives, error)` — zero value when unset, never "not found" errors
  - `SetGenerationSettings(context.Context, GenerationDirectives) error` — upsert

- [ ] **Step 1: Write the migration**

`internal/platform/postgres/migrations/024_pagewiki_generation_settings.sql`:

```sql
CREATE TABLE IF NOT EXISTS pagewiki_generation_settings (
    scope_id            TEXT PRIMARY KEY,
    language            TEXT NOT NULL DEFAULT '',
    custom_instructions TEXT NOT NULL DEFAULT '',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Register it in `internal/platform/postgres/store.go`: append ` migrations/024_pagewiki_generation_settings.sql` to the `//go:embed` line, and add `"migrations/024_pagewiki_generation_settings.sql",` after `"migrations/023_todoapp.sql",` in the `Migrate` list.

- [ ] **Step 2: Extend the `Repository` interface**

In `internal/pagewiki/ports.go`, add to the `Repository` interface after `ReplaceTopicTree`:

```go
	GenerationSettings(context.Context) (GenerationDirectives, error)
	SetGenerationSettings(context.Context, GenerationDirectives) error
```

Run: `go build ./...`
Expected: FAIL — `*memory.Repository` and `*postgres.Repository` no longer satisfy `pagewiki.Repository`. That failure is the "failing test" for this task's interface change.

- [ ] **Step 3: Write the failing memory-repository test**

Append to `internal/pagewiki/memory/repository_test.go` (match the file's existing package clause and imports):

```go
func TestGenerationSettingsRoundTrip(t *testing.T) {
	repository := memory.NewRepository()
	ctx := context.Background()

	directives, err := repository.GenerationSettings(ctx)
	require.NoError(t, err)
	require.True(t, directives.IsZero())

	want := pagewiki.GenerationDirectives{Language: "简体中文", CustomInstructions: "prefer tables"}
	require.NoError(t, repository.SetGenerationSettings(ctx, want))
	got, err := repository.GenerationSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)

	// Second write overwrites (upsert semantics).
	require.NoError(t, repository.SetGenerationSettings(ctx, pagewiki.GenerationDirectives{Language: "English"}))
	got, err = repository.GenerationSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, pagewiki.GenerationDirectives{Language: "English"}, got)
}
```

If the existing file uses a suite instead of bare funcs, convert this test to the suite style used there. Run to confirm it fails to compile.

- [ ] **Step 4: Implement the memory repository**

In `internal/pagewiki/memory/repository.go`: add field `generation pagewiki.GenerationDirectives` to `Repository`, reset it in `Reset()` (`r.generation = pagewiki.GenerationDirectives{}`), and add:

```go
func (r *Repository) GenerationSettings(_ context.Context) (pagewiki.GenerationDirectives, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.generation, nil
}

func (r *Repository) SetGenerationSettings(_ context.Context, directives pagewiki.GenerationDirectives) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.generation = directives
	return nil
}
```

Run: `go test ./internal/pagewiki/memory/ -run TestGenerationSettings -v`
Expected: PASS

- [ ] **Step 5: Implement the postgres repository**

In `internal/pagewiki/postgres/repository.go` (mirror the style of `TopicTree`/`ReplaceTopicTree` around line 149; the struct already holds `pool` and `scopeID`):

```go
func (r *Repository) GenerationSettings(ctx context.Context) (pagewiki.GenerationDirectives, error) {
	var directives pagewiki.GenerationDirectives
	err := r.pool.QueryRow(ctx, `
SELECT language, custom_instructions
FROM pagewiki_generation_settings
WHERE scope_id = $1`, r.scopeID).Scan(&directives.Language, &directives.CustomInstructions)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return pagewiki.GenerationDirectives{}, nil
	case err != nil:
		return pagewiki.GenerationDirectives{}, fmt.Errorf("load Page Wiki generation settings: %w", err)
	}
	return directives, nil
}

func (r *Repository) SetGenerationSettings(ctx context.Context, directives pagewiki.GenerationDirectives) error {
	if _, err := r.pool.Exec(ctx, `
INSERT INTO pagewiki_generation_settings (scope_id, language, custom_instructions)
VALUES ($1, $2, $3)
ON CONFLICT (scope_id) DO UPDATE
SET language = EXCLUDED.language,
    custom_instructions = EXCLUDED.custom_instructions,
    updated_at = NOW()`, r.scopeID, directives.Language, directives.CustomInstructions); err != nil {
		return fmt.Errorf("save Page Wiki generation settings: %w", err)
	}
	return nil
}
```

Add a postgres round-trip test to `internal/pagewiki/postgres/repository_test.go` with the same assertions as Step 3, using that file's existing DB-test harness (setup/skip pattern). Copy how the file's other tests acquire the repository; do not invent a new harness. Note: DB tests may be red/skipped in this environment (known main-gate issue) — a skip is acceptable, a compile error is not.

- [ ] **Step 6: Run gates**

Run: `go build ./... && go test ./internal/pagewiki/... ./internal/platform/postgres/`
Expected: PASS (or pre-existing DB-test skips)

- [ ] **Step 7: Commit**

```bash
git add internal/platform/postgres/ internal/pagewiki/
git commit -m "feat(pagewiki): persist per-scope generation settings"
```

---

### Task 3: Thread directives into planner, editor, and tree indexer prompts

**Files:**
- Modify: `internal/pagewiki/ports.go` (add `Directives` field to the three input structs)
- Modify: `internal/pagewiki/llm_session_planner.go:101` (system prompt)
- Modify: `internal/pagewiki/llm_session_editor.go:78` (system prompt)
- Modify: `internal/pagewiki/llm_tree_indexer.go:100` (system prompt)
- Test: `internal/pagewiki/llm_session_planner_test.go`, `internal/pagewiki/llm_session_editor_test.go`, `internal/pagewiki/llm_tree_indexer_test.go`

**Interfaces:**
- Consumes: `generationDirectivesPrompt` (Task 1).
- Produces: `PlanInput.Directives`, `EditInput.Directives`, `TreeIndexInput.Directives` — all of type `GenerationDirectives`. Task 4 sets them.

- [ ] **Step 1: Add the struct fields**

In `internal/pagewiki/ports.go` add `Directives GenerationDirectives` as the last field of `PlanInput`, `EditInput`, and `TreeIndexInput`.

- [ ] **Step 2: Write the failing tests**

Each of the three test files already builds a fake `llm.ChatClient` that captures the outgoing `llm.ChatRequest` — reuse each file's existing fake/harness. Add one test per file asserting: (a) with `Directives` set, the captured system message (`Messages[0].Content`) contains the language sentence and the instructions; (b) with zero `Directives`, the system message equals the exact prompt it is today. Planner example (adapt naming to the file's existing style; same shape for editor and indexer):

```go
func TestPlannerAppendsGenerationDirectivesToSystemPrompt(t *testing.T) {
	var captured llm.ChatRequest
	client := &fakeChatClient{ // reuse the file's existing fake; this shows required behavior
		complete: func(_ context.Context, request llm.ChatRequest) (llm.ChatResponse, error) {
			captured = request
			return validPlannerResponse(), nil // reuse the file's existing canned success response
		},
	}
	planner := newTestPlanner(t, client) // reuse the file's existing constructor helper
	_, err := planner.Plan(context.Background(), PlanInput{
		SourceRevision: minimalSourceRevision(), PageCatalog: PageCatalog{},
		Directives: GenerationDirectives{Language: "简体中文", CustomInstructions: "prefer tables"},
	})
	require.NoError(t, err)
	require.Contains(t, captured.Messages[0].Content, "in 简体中文.")
	require.Contains(t, captured.Messages[0].Content, "prefer tables")
	require.True(t, strings.HasPrefix(captured.Messages[0].Content, pageWikiPlannerPrompt))
}
```

For the zero-directives assertions use the exact current expressions: planner `pageWikiPlannerPrompt`, editor `pageWikiEnglishEditorPrompt`, indexer `treeIndexerPrompt(x.maxDepth)` (indexer test: construct with a known MaxDepth and compare against `treeIndexerPrompt(thatDepth)`).

Run: `go test ./internal/pagewiki/ -run 'Directives' -v` — expected FAIL (prompt does not contain the suffix).

- [ ] **Step 3: Implement the three prompt changes**

- `llm_session_planner.go:101`: `{Role: "system", Content: pageWikiPlannerPrompt + generationDirectivesPrompt(input.Directives)}` (the method's input param name may differ — use it).
- `llm_session_editor.go:78`: `{Role: "system", Content: pageWikiEnglishEditorPrompt + generationDirectivesPrompt(input.Directives)}`
- `llm_tree_indexer.go:100`: `{Role: "system", Content: treeIndexerPrompt(x.maxDepth) + generationDirectivesPrompt(input.Directives)}` — `Index` receives `input TreeIndexInput`; if the prompt is built outside the retry loop, compute the concatenation once.

- [ ] **Step 4: Run gates**

Run: `go build ./... && go test ./internal/pagewiki/`
Expected: PASS (all pre-existing prompt tests must still pass — the zero-value suffix is empty).

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/
git commit -m "feat(pagewiki): inject generation directives into all three LLM prompts"
```

---

### Task 4: Service — load directives per run + settings accessor methods

**Files:**
- Modify: `internal/pagewiki/service.go` (`InjectSession`, `processTarget`, `maybeReindexTree`)
- Create: `internal/pagewiki/service_generation_settings_test.go`
- Modify (if needed): `internal/pagewiki/CONTEXT.md` — one line noting generation settings & where they flow

**Interfaces:**
- Consumes: Repository methods (Task 2), input-struct fields (Task 3), `ValidateGenerationDirectives` (Task 1).
- Produces (Task 5's endpoints call these):
  - `func (s *Service) GenerationSettings(ctx context.Context) (GenerationDirectives, error)`
  - `func (s *Service) SetGenerationSettings(ctx context.Context, d GenerationDirectives) (GenerationDirectives, error)` — validates (trim+bounds), persists, returns the stored value

- [ ] **Step 1: Write the failing tests**

`internal/pagewiki/service_generation_settings_test.go` (package `pagewiki_test`, using `memory.NewRepository()` and the scripted planner/editor helpers this package's service tests already use — mirror `service_helpers_test.go`):

```go
// Test 1: SetGenerationSettings validates and round-trips through the service.
// Test 2: SetGenerationSettings rejects an over-limit field with ErrInvalidGenerationSettings
//         and persists nothing.
// Test 3: InjectSession threads stored directives into planner, editor, and tree indexer:
//         seed settings via SetGenerationSettings, run a minimal successful InjectSession
//         (reuse an existing acceptance-test fixture from inject_acceptance_test.go or
//         tree_reindex_acceptance_test.go), and assert via scripted maintainers that each
//         received input.Directives equal to the stored value.
// Test 4: InjectSession fails loudly when GenerationSettings errors: wrap the memory
//         repository in a failingSettingsRepository{pagewiki.Repository} whose
//         GenerationSettings returns an error; expect InjectSession to return that error
//         and the planner to never be called.
```

Write these as real tests against the package's existing scripted/fixture helpers (the scripted planner/editor in `scripted.go` record their inputs or can be wrapped to). The wrapper for Test 4:

```go
type failingSettingsRepository struct {
	pagewiki.Repository
}

func (failingSettingsRepository) GenerationSettings(context.Context) (pagewiki.GenerationDirectives, error) {
	return pagewiki.GenerationDirectives{}, errors.New("settings unavailable")
}
```

Run: `go test ./internal/pagewiki/ -run GenerationSettings -v` — expected FAIL (`s.GenerationSettings` undefined).

- [ ] **Step 2: Implement the service methods**

In `internal/pagewiki/service.go`:

```go
func (s *Service) GenerationSettings(ctx context.Context) (GenerationDirectives, error) {
	directives, err := s.repository.GenerationSettings(ctx)
	if err != nil {
		return GenerationDirectives{}, fmt.Errorf("load generation settings: %w", err)
	}
	return directives, nil
}

func (s *Service) SetGenerationSettings(
	ctx context.Context,
	directives GenerationDirectives,
) (GenerationDirectives, error) {
	valid, err := ValidateGenerationDirectives(directives)
	if err != nil {
		return GenerationDirectives{}, err
	}
	if err := s.repository.SetGenerationSettings(ctx, valid); err != nil {
		return GenerationDirectives{}, fmt.Errorf("save generation settings: %w", err)
	}
	return valid, nil
}
```

- [ ] **Step 3: Thread directives through the run**

In `InjectSession`, after the `MaintenanceRun` idempotency short-circuit and before the planner call (i.e. right after the `catalog` load at service.go:97-100):

```go
	directives, err := s.repository.GenerationSettings(ctx)
	if err != nil {
		return InjectResult{}, fmt.Errorf("load generation settings: %w", err)
	}
```

Then:
- planner call: `PlanInput{SourceRevision: sourceRevision, PageCatalog: catalog, Directives: directives}`
- `processTarget`: add a `directives GenerationDirectives` parameter (callers: service.go:122) and set `Directives: directives` on the `EditInput` it builds
- `maybeReindexTree`: add a `directives GenerationDirectives` parameter (caller: service.go:129) and set `Directives: directives` on `TreeIndexInput`

- [ ] **Step 4: Run gates**

Run: `go build ./... && go test ./internal/pagewiki/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/
git commit -m "feat(pagewiki): load generation settings per run and expose service accessors"
```

---

### Task 5: HTTP endpoints (IDL + handler + wiring)

**Files:**
- Modify: `idl/team_memory.thrift` (structs near line 327, service methods near line 1134)
- Generate: `make generate` (updates `internal/teamnote/transport/httpapi/model/teammemory/api/` and `router/teammemory/api/`)
- Create: `internal/teamnote/transport/httpapi/handler/wiki_settings_endpoints.go`
- Modify: `internal/teamnote/transport/httpapi/handler/dependencies.go` (new interface + option)
- Modify: `main.go` (wire the option in `buildApplicationHTTPHandlers` near the `WithWikiControl` wiring at main.go:675)
- Test: `internal/teamnote/transport/httpapi/handler/wiki_settings_endpoints_test.go`

**Interfaces:**
- Consumes: `pagewiki.Service.GenerationSettings` / `SetGenerationSettings` (Task 4), `pagewiki.ErrInvalidGenerationSettings` (Task 1).
- Produces: `GET /v1/wiki/settings`, `PUT /v1/wiki/settings` returning `{"language": string, "custom_instructions": string}`; handler option `handler.WithWikiSettings(settings WikiSettings)`.

- [ ] **Step 1: IDL**

In `idl/team_memory.thrift` after the wiki ingestion structs (~line 341):

```thrift
struct WikiGenerationSettingsRequest {}

struct UpdateWikiGenerationSettingsRequest {
  1: required string language (api.body="language")
  2: required string custom_instructions (api.body="custom_instructions")
}

struct WikiGenerationSettingsResponse {
  1: required string language
  2: required string custom_instructions
}
```

In the service block after the `RebuildWiki` line (~1134):

```thrift
  WikiGenerationSettingsResponse GetWikiGenerationSettings(1: WikiGenerationSettingsRequest request) (api.get="/v1/wiki/settings")
  WikiGenerationSettingsResponse UpdateWikiGenerationSettings(1: UpdateWikiGenerationSettingsRequest request) (api.put="/v1/wiki/settings")
```

Run: `make generate`, then `go build ./...` — expected FAIL: router references `handler.GetWikiGenerationSettings` / `handler.UpdateWikiGenerationSettings` which don't exist yet.

- [ ] **Step 2: Handler dependency**

In `dependencies.go`, next to `WikiControl` (line 129):

```go
type WikiSettings interface {
	GenerationSettings(context.Context) (pagewiki.GenerationDirectives, error)
	SetGenerationSettings(context.Context, pagewiki.GenerationDirectives) (pagewiki.GenerationDirectives, error)
}

func WithWikiSettings(settings WikiSettings) OnPremOption {
	return func(configured *Handler) error {
		configured.wikiSettings = settings
		return nil
	}
}
```

Add field `wikiSettings WikiSettings` to the `Handler` struct (near `wikiControl` at line 39). Import `pagewiki "github.com/pax-beehive/pax-nexus/internal/pagewiki"`.

- [ ] **Step 3: Write the failing handler test**

`wiki_settings_endpoints_test.go`, mirroring `wiki_ingestion_endpoints_test.go`'s suite (same `SetupTest` shape, adding `handler.WithWikiSettings(s.wikiSettings)` and a fake:

```go
type wikiSettingsService struct {
	stored pagewiki.GenerationDirectives
	err    error
}

func (f *wikiSettingsService) GenerationSettings(context.Context) (pagewiki.GenerationDirectives, error) {
	return f.stored, f.err
}

func (f *wikiSettingsService) SetGenerationSettings(
	_ context.Context, d pagewiki.GenerationDirectives,
) (pagewiki.GenerationDirectives, error) {
	if f.err != nil {
		return pagewiki.GenerationDirectives{}, f.err
	}
	valid, err := pagewiki.ValidateGenerationDirectives(d)
	if err != nil {
		return pagewiki.GenerationDirectives{}, err
	}
	f.stored = valid
	return valid, nil
}
```

Tests (use the suite's `perform` helper like the ingestion suite does):
1. GET returns defaults: `{"language":"","custom_instructions":""}`, 200.
2. PUT stores and echoes: body `{"language":"简体中文","custom_instructions":"prefer tables"}` → 200, same JSON back, fake's `stored` updated.
3. PUT with a 65-char language → 400 with error code `invalid_request`.
4. Handler built WITHOUT `WithWikiSettings` → both routes return 501 `not_configured` (mirror `authorizeWikiControl`'s nil guard).

Run: compile fails (`GetWikiGenerationSettings` undefined) — that is the failing state.

- [ ] **Step 4: Implement the endpoints**

`wiki_settings_endpoints.go`, mirroring `wiki_ingestion_endpoints.go` exactly (auth via `h.authorizeHumanMember(ctx, c, mutation)`; GET is `mutation=false`, PUT `mutation=true`):

```go
package handler

import (
	"context"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func (h *Handler) GetWikiGenerationSettings(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.authorizeWikiSettings(ctx, c, false); !ok {
		return
	}
	directives, err := h.wikiSettings.GenerationSettings(ctx)
	if err != nil {
		h.logger.Error("get Wiki generation settings", "error", err)
		writeHumanAPIError(c, consts.StatusInternalServerError, "internal_error", "the request could not be completed")
		return
	}
	c.JSON(consts.StatusOK, &api.WikiGenerationSettingsResponse{
		Language: directives.Language, CustomInstructions: directives.CustomInstructions,
	})
}

func (h *Handler) UpdateWikiGenerationSettings(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.authorizeWikiSettings(ctx, c, true); !ok {
		return
	}
	var request api.UpdateWikiGenerationSettingsRequest
	if err := c.BindAndValidate(&request); err != nil {
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", "the request is invalid")
		return
	}
	stored, err := h.wikiSettings.SetGenerationSettings(ctx, pagewiki.GenerationDirectives{
		Language: request.Language, CustomInstructions: request.CustomInstructions,
	})
	switch {
	case errors.Is(err, pagewiki.ErrInvalidGenerationSettings):
		writeHumanAPIError(c, consts.StatusBadRequest, "invalid_request", err.Error())
		return
	case err != nil:
		h.logger.Error("update Wiki generation settings", "error", err)
		writeHumanAPIError(c, consts.StatusInternalServerError, "internal_error", "the request could not be completed")
		return
	}
	c.JSON(consts.StatusOK, &api.WikiGenerationSettingsResponse{
		Language: stored.Language, CustomInstructions: stored.CustomInstructions,
	})
}

func (h *Handler) authorizeWikiSettings(
	ctx context.Context,
	c *app.RequestContext,
	mutation bool,
) (onprem.HumanPrincipal, bool) {
	if h.wikiSettings == nil {
		writeHumanAPIError(c, consts.StatusNotImplemented, "not_configured", "Wiki settings are not configured")
		return onprem.HumanPrincipal{}, false
	}
	return h.authorizeHumanMember(ctx, c, mutation)
}
```

(Add the `onprem` import; check the generated field names in the api model — hz may render `CustomInstructions` differently — and adapt.)

- [ ] **Step 5: Wire in main.go**

In `buildApplicationHTTPHandlers`, where `WithWikiControl` is appended (main.go:675), the pagewiki `service` must be in scope (it is created in `buildPageWikiHTTPHandler` — extend that function to also return the `*pagewiki.Service`, or move the option wiring to where the service exists). Add:

```go
		options = append(options, handler.WithWikiSettings(service))
```

Follow the existing wiring structure with minimal reshuffling; `*pagewiki.Service` already satisfies `WikiSettings` structurally.

- [ ] **Step 6: Run gates**

Run: `go build ./... && go test ./internal/teamnote/transport/httpapi/handler/ ./internal/pagewiki/...`
Expected: PASS. Also run `go test ./` (root `main_test.go` exercises wiring).

- [ ] **Step 7: Commit**

```bash
git add idl/ internal/teamnote/ main.go
git commit -m "feat(api): GET/PUT /v1/wiki/settings for generation language and instructions"
```

---

### Task 6: Frontend — Generation card on WikiStatusPage

**Files:**
- Modify: `web/src/api/wiki.ts` (type + GET)
- Modify: `web/src/api/actions.ts` (PUT, next to `setWikiAutoInject` at line 338)
- Modify: `web/src/pages/WikiStatusPage.tsx`
- Test: `web/tests/wiki-status.dom.test.tsx`

**Interfaces:**
- Consumes: `GET/PUT /v1/wiki/settings` (Task 5).
- Produces (UI only):

```ts
// wiki.ts
export interface WikiGenerationSettings {
  language: string;
  custom_instructions: string;
}
export function getWikiSettings(signal?: AbortSignal): Promise<WikiGenerationSettings>;
// actions.ts
export function updateWikiSettings(settings: WikiGenerationSettings): Promise<WikiGenerationSettings>;
```

- [ ] **Step 1: Write the failing DOM tests**

Append to `web/tests/wiki-status.dom.test.tsx`, following its existing fetch-stub pattern (stub `GET /v1/wiki/settings` alongside the existing `GET /v1/wiki/ingestion` stub):

1. **Renders defaults**: with `{"language":"","custom_instructions":""}` the card shows the language select with "Follow source evidence" selected and an empty instructions textarea.
2. **Saves settings**: select "简体中文", type instructions, click "Save generation settings"; assert the PUT body `{language:"简体中文", custom_instructions:"..."}` and a success message appears.
3. **Custom language fallback**: with `{"language":"日本語", ...}` (not in presets) the select shows "Custom…" and a text input contains `日本語`.

Run: `cd web && npx vitest run tests/wiki-status.dom.test.tsx` — expected FAIL.

- [ ] **Step 2: API client functions**

`wiki.ts` (mirror `getWikiIngestionStatus`'s style in that file):

```ts
export interface WikiGenerationSettings {
  language: string;
  custom_instructions: string;
}

export function getWikiSettings(signal?: AbortSignal): Promise<WikiGenerationSettings> {
  return humanFetch<WikiGenerationSettings>("/v1/wiki/settings", { signal });
}
```

`actions.ts` (after `setWikiAutoInject`):

```ts
export function updateWikiSettings(settings: WikiGenerationSettings): Promise<WikiGenerationSettings> {
  return humanFetch<WikiGenerationSettings>("/v1/wiki/settings", {
    method: "PUT",
    body: JSON.stringify(settings),
  });
}
```

(Import `WikiGenerationSettings` from `./wiki`; match how the file imports `WikiIngestionStatus`.)

- [ ] **Step 3: The Generation card**

In `WikiStatusPage.tsx`, add below the ingestion card, following the page's existing card markup/classes:

- State: `language`, `customLanguage`, `instructions`, `settingsBusy`, `settingsMessage`; load once via `getWikiSettings` in a `useEffect` (not polled — settings don't change underneath the editor; follow the page's error handling).
- Preset list: `const LANGUAGE_PRESETS = ["", "简体中文", "English"]` rendered as "Follow source evidence" / "简体中文" / "English" / "Custom…". A loaded value outside presets selects "Custom…" and fills the text input.
- Instructions `<textarea maxLength={2000}>` with a live `${2000 - instructions.length} characters left` hint.
- Save button → `updateWikiSettings({ language: effectiveLanguage, custom_instructions: instructions.trim() })`, success message "Generation settings saved. They apply to future runs only; use Rebuild to regenerate existing pages."
- Helper copy on the card: "Applies to future generation runs only. Use Rebuild to switch the whole wiki."

- [ ] **Step 4: Run gates**

Run: `cd web && npx tsc --noEmit && npx vitest run`
Expected: PASS (all suites, not just the new one).

- [ ] **Step 5: Commit**

```bash
git add web/
git commit -m "feat(web): generation settings card on the wiki status page"
```

---

### Task 7: Full verification + PR

- [ ] **Step 1: Full gates**

```bash
go build ./... && go test ./... 2>&1 | grep -v '^ok\|no test files'
cd web && npx tsc --noEmit && npx vitest run
```

Expected: no new failures (known pre-existing main-gate red items excepted).

- [ ] **Step 2: Zero-default sanity**

Confirm `git grep -n 'generationDirectivesPrompt' internal/pagewiki/` shows exactly the three prompt call sites + definition, and that all pre-existing prompt/acceptance tests pass unchanged — proving default behavior is untouched.

- [ ] **Step 3: Push and open PR**

```bash
git push -u origin feat/wiki-generation-settings
gh pr create --base feat/optimization-round-3 --title "feat(pagewiki): team-configurable generation language and style instructions" --body "<summarize: spec link, per-run directives, three prompt paths, /v1/wiki/settings, Generation card; note stacked on #39 and migration 024>"
```

PR base is `feat/optimization-round-3` (stacked); retarget to main after #39 merges.
