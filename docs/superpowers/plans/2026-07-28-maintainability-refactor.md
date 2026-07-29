# Maintainability Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the LLM chat client to `internal/platform/llm`, rewrite the architecture test as a default-deny whitelist, and retire five extraction candidate strategies with a documented retirement policy.

**Architecture:** Three independent parts landed in order: client relocation first (removes the `pagewiki → llmwiki` dependency), whitelist second (locks the cleaned-up graph), strategy retirement last. Spec: `docs/superpowers/specs/2026-07-28-maintainability-refactor-design.md`.

**Tech Stack:** Go 1.x, testify suites, golangci-lint, make.

## Global Constraints

- `make lint` and `make coverage` must pass after every task's commit; the coverage gate is 80% and must not drop.
- Commit messages: conventional-commit style, lowercase subject, ending with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.
- Moved code is verbatim: no behavior changes, no prompt-byte changes. `claim-card-v2`'s prompt must remain byte-identical (it is built from `rollingSystemPromptClaimCardV1`, which is built from `rollingSystemPromptV2` — both are RETAINED as building blocks even though their strategies retire).
- Production default strategy stays `source-clause-v1`; kept strategies: `source-clause-v1`, `interaction-slim`, `source-span-v1`, `source-span-v2`, `claim-card-v2`.
- Module path is `github.com/pax-beehive/pax-nexus`.

---

### Task 1: Create `internal/platform/llm` and rewire all consumers

**Files:**
- Create: `internal/platform/llm/chat.go`
- Create (move): `internal/platform/llm/deepseek.go` (from `internal/llmwiki/workspace/deepseek.go`)
- Create (move): `internal/platform/llm/deepseek_test.go` (from `internal/llmwiki/workspace/deepseek_test.go`)
- Modify: `internal/llmwiki/workspace/agent.go` (delete moved types, import `llm`), every other `internal/llmwiki/workspace/*.go` and `*_test.go` referencing the moved symbols
- Modify: `internal/pagewiki/llm_session_planner.go`, `internal/pagewiki/llm_session_editor.go`, `internal/pagewiki/llm_session_planner_test.go`, `internal/pagewiki/llm_session_editor_test.go`, `internal/pagewiki/llm_plan_acceptance_test.go` (if it references workspace)
- Modify: `main.go` (~line 214, `llmwikiworkspace.NewDeepSeekClient`), `cmd/llmwiki-spike/main.go`, `cmd/llmwiki-spike/main_test.go`

**Interfaces:**
- Produces: package `llm` exporting `ToolFunction`, `ToolCall`, `ChatMessage`, `ToolDefinition`, `ToolFunctionSchema`, `ChatRequest`, `TokenUsage`, `ChatResponse`, `ChatClient`, `DeepSeekConfig`, `DeepSeekClient`, `NewDeepSeekClient` — signatures identical to today's `workspace` versions.
- Consumed by: Task 4's whitelist entries `platform/llm` for `pagewiki` and `llmwiki`.

- [ ] **Step 1: Create `internal/platform/llm/chat.go`**

Move these types verbatim from `internal/llmwiki/workspace/agent.go:17-64` (delete them there in Step 3):

```go
// Package llm provides the shared LLM chat-completion client used by
// product contexts. It is technical infrastructure, peer to
// platform/observability: domain packages may import it.
package llm

import "context"

type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function ToolFunctionSchema `json:"function"`
}

type ToolFunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ChatRequest struct {
	Model    string
	Messages []ChatMessage
	Tools    []ToolDefinition
}

type TokenUsage struct {
	InputTokens  int
	OutputTokens int
}

type ChatResponse struct {
	Message ChatMessage
	Usage   TokenUsage
}

type ChatClient interface {
	Complete(context.Context, ChatRequest) (ChatResponse, error)
}
```

- [ ] **Step 2: Move the DeepSeek client**

```bash
git mv internal/llmwiki/workspace/deepseek.go internal/platform/llm/deepseek.go
git mv internal/llmwiki/workspace/deepseek_test.go internal/platform/llm/deepseek_test.go
```

Change the package clause in both files from `workspace` (or `workspace_test`) to `llm` (or `llm_test`). In `deepseek_test.go`, if it referenced `workspace.X` symbols, change them to `llm.X`. No other edits.

- [ ] **Step 3: Delete the moved types from `workspace` and rewire the workspace package**

In `internal/llmwiki/workspace/agent.go`: delete the nine moved type declarations (lines 17–64), add import `"github.com/pax-beehive/pax-nexus/internal/platform/llm"`, and qualify every use: `ChatClient` → `llm.ChatClient`, `ChatMessage` → `llm.ChatMessage`, `ChatRequest` → `llm.ChatRequest`, `ChatResponse` → `llm.ChatResponse`, `ToolCall` → `llm.ToolCall`, `ToolDefinition` → `llm.ToolDefinition`, `ToolFunctionSchema` → `llm.ToolFunctionSchema`, `ToolFunction` → `llm.ToolFunction`, `TokenUsage` → `llm.TokenUsage`.

Find every other file needing the same treatment:

```bash
grep -rln 'ChatClient\|ChatMessage\|ChatRequest\|ChatResponse\|ToolCall\|ToolDefinition\|ToolFunctionSchema\|ToolFunction\|TokenUsage\|DeepSeek' internal/llmwiki --include='*.go'
```

Apply the same qualification in each (test files included). Do NOT add type aliases in `workspace`.

- [ ] **Step 4: Rewire pagewiki, main.go, and the spike CLI**

In `internal/pagewiki/llm_session_planner.go:11` and `llm_session_editor.go:11`: replace the import `"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"` with `"github.com/pax-beehive/pax-nexus/internal/platform/llm"` and change `workspace.ChatClient` / `workspace.ChatRequest` / `workspace.ChatMessage` to `llm.` equivalents. Same in their `_test.go` files.

In `main.go` (~line 214): replace the `llmwikiworkspace` import and `llmwikiworkspace.NewDeepSeekClient(llmwikiworkspace.DeepSeekConfig{...})` with `platformllm "github.com/pax-beehive/pax-nexus/internal/platform/llm"` and `platformllm.NewDeepSeekClient(platformllm.DeepSeekConfig{...})`.

In `cmd/llmwiki-spike/main.go` (and its test): the spike still imports `workspace` for agent types; only the chat/DeepSeek symbols move to the `llm` import.

- [ ] **Step 5: Verify no references remain and the tree builds**

```bash
grep -rn 'workspace\.\(Chat\|Tool\|TokenUsage\|DeepSeek\|NewDeepSeek\)' --include='*.go' . ; echo "expect no output"
go build ./... && go test ./internal/platform/llm/... ./internal/llmwiki/... ./internal/pagewiki/... .
```

Expected: build passes, all listed tests pass.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(platform): move the LLM chat client out of llmwiki

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: Correct the context documentation

**Files:**
- Modify: `CONTEXT-MAP.md` (full rewrite of Contexts/Relationships)
- Modify: `internal/llmwiki/doc.go`, `internal/llmwiki/CONTEXT.md` (reposition as experimental spike)
- Create: `internal/pagewiki/CONTEXT.md`

**Interfaces:** none (documentation only).

- [ ] **Step 1: Rewrite `CONTEXT-MAP.md`**

Replace the `## Contexts` and `## Relationships` sections with (keep the trailing paragraph pointing at the processor guide and frontend guide):

```markdown
# PAX Nexus Context Map

## Contexts

- [Session](./internal/session/CONTEXT.md) — shared agent identity and immutable session evidence.
- [Team Note](./internal/teamnote/CONTEXT.md) — short-lived passive collaboration recall.
- [PageWiki](./internal/pagewiki/CONTEXT.md) — the shipping wiki product: durable, cited pages maintained from session evidence.
- Recall (`internal/recall`) — routes recall requests across product paths; owns no adapters.
- Explorer (`internal/explorer`) — read-only team-memory diagnostics for operators.
- [Evaluation](./internal/eval/CONTEXT.md) — reproducible quality measurement and benchmark adapters.
- [On-prem Identity](./internal/deployment/onprem/CONTEXT.md) — human membership, Agent ownership, and credential-bound access for one installation.
- [Operations](./internal/operations/CONTEXT.md) — bounded service activity, diagnostics, and storage accounting for operators.
- Platform (`internal/platform`) — technical infrastructure: Postgres adapters, observability, text embedding, and the shared LLM chat client (`platform/llm`).
- [LLM Wiki](./internal/llmwiki/CONTEXT.md) — experimental spike (workspace agent, effect eval, session datasets) and a reserved name for a future actively browsed knowledge module. Not a shipping product; PageWiki is.

## Relationships

- **Session → Team Note**: Team Note extracts bounded facts from Session Lake events.
- **Session → PageWiki**: PageWiki maintains durable pages from Session Lake batches.
- **Recall → Team Note**: Recall routes across product recall paths; product contexts never import Recall.
- **Evaluation → products**: Evaluation may exercise any product context; product contexts never import Evaluation.
- **Platform → products**: Platform adapters implement ports defined by product contexts (dependency points at the domain). Exception: `platform/observability` and `platform/llm` are shared technical services that domains may import.
- **On-prem Identity → Session/Team Note/PageWiki**: On-prem Identity authenticates principals; product contexts consume the resulting identity but do not own accounts or credentials.
- **Operations → products/On-prem Identity**: Operations observes bounded outcomes and storage measurements without owning product state.
- **Team Note ↔ PageWiki**: They share Session evidence but do not import each other's domain packages.

The dependency rules are enforced by `internal/architecture/dependencies_test.go`.
```

- [ ] **Step 2: Fix `internal/llmwiki/doc.go`**

```go
// Package llmwiki holds the LLM wiki workspace spike (bounded file-tool
// agent, effect eval, session datasets) and reserves the name for a future
// durable, actively browsed knowledge module. The shipping wiki product is
// internal/pagewiki. The shared LLM chat client lives in
// internal/platform/llm.
package llmwiki
```

- [ ] **Step 3: Update `internal/llmwiki/CONTEXT.md`**

Insert directly under the `# LLM Wiki` heading:

```markdown
> **Status:** experimental spike + reserved name. The shipping wiki product is
> [PageWiki](../pagewiki/CONTEXT.md). `effecteval`, `sessiondataset`, and
> `cmd/llmwiki-spike` are spike code; the shared LLM chat client formerly in
> `workspace` now lives in `internal/platform/llm`.
```

- [ ] **Step 4: Create `internal/pagewiki/CONTEXT.md`**

```markdown
# PageWiki

The shipping wiki product: durable, cited Wiki Pages created and revised from
Session Lake evidence, published over HTTP and read in the Human Portal.

## Language

**Page**: an immutable-revisioned knowledge unit with cited evidence quotes.

**Session Document**: the bounded session evidence a planner run consumes.

**Planner / Editor**: the LLM pair that chooses evidence and writes page
revisions (`llm_session_planner.go`, `llm_session_editor.go`).

## Relationships

- Consumes Session Lake evidence via the session consumer.
- Persists through its own repository port (`ports.go`); adapters live in
  `pagewiki/postgres` and `pagewiki/memory`.
- Uses the shared LLM chat client from `internal/platform/llm`.
- Does not import Team Note domain packages, and Team Note does not import
  PageWiki domain packages.
```

- [ ] **Step 5: Commit**

```bash
git add CONTEXT-MAP.md internal/llmwiki/doc.go internal/llmwiki/CONTEXT.md internal/pagewiki/CONTEXT.md
git commit -m "docs: correct the context map and llmwiki positioning

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: Rewrite the architecture test as a default-deny whitelist

**Files:**
- Modify: `internal/architecture/dependencies_test.go` (full rewrite; keep the `productionImports` / `parseImports` helpers)

**Interfaces:**
- Consumes: the post-Task-1 import graph (verified with `go list`, production imports only — do not trust grep over test files).
- Produces: `dependencyRule` struct and `importAllowed` function used only within this test file.

The as-built production import graph this whitelist encodes (from `go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./internal/...`, internal deps only, post Task 1):

| package (excl. own subtree) | internal imports |
|---|---|
| deployment/onprem | explorer, operations, teamnote/runtime |
| llmwiki/* | platform/llm |
| pagewiki (core+memory+postgres+sessionconsumer) | platform/llm, platform/observability, session |
| pagewiki/transport/... | pagewiki, teamnote/transport/httpapi/router/pagewiki/api |
| platform/postgres | deployment/onprem, explorer, operations, pagewiki/sessionconsumer, session, teamnote, teamnote/extractor |
| recall | teamnote |
| sessionlake | session |
| teamnote (core+extractor+extractionbudget+extractionqueue+runtime+paxmprovider+mocks) | platform/observability, session, sessionlake |
| teamnote/transport/... | deployment/onprem, explorer, operations, pagewiki/sessionconsumer, pagewiki/transport/httpapi, recall, teamnote |
| session, explorer, operations, architecture | none |

- [ ] **Step 1: Replace the rule model and test body in `dependencies_test.go`**

Keep `package architecture_test`, the imports, `modulePath`, the suite scaffolding (`dependencySuite`, `TestDependencySuite`, `SetupSuite`), and the helpers `productionImports`, `shouldSkipDirectory`, `isProductionGoFile`, `parseImports`. Delete `TestProductDependencyDirection` and `hasModulePrefix`. Add:

```go
// dependencyRule whitelists the internal imports of one directory subtree.
// A package may always import its own subtree, minus excluded
// subdirectories, which must carry their own rule.
type dependencyRule struct {
	directory    string   // relative to internal/
	allowed      []string // allowed internal import prefixes outside the subtree
	excluded     []string // immediate subdirectories owned by another rule
	unrestricted bool     // may import any internal package (eval only)
}

// Default deny: a package not listed here fails the registration test.
// Grant the minimum set — no headroom. Retire entries with the code.
var dependencyRules = []dependencyRule{
	{directory: "architecture"},
	{directory: "deployment", allowed: []string{"explorer", "operations", "teamnote/runtime"}},
	{directory: "eval", unrestricted: true},
	{directory: "explorer"},
	{directory: "llmwiki", allowed: []string{"platform/llm"}},
	{directory: "operations"},
	{directory: "pagewiki", excluded: []string{"transport"},
		allowed: []string{"platform/llm", "platform/observability", "session"}},
	{directory: "pagewiki/transport",
		allowed: []string{"pagewiki", "teamnote/transport/httpapi/router/pagewiki/api"}},
	{directory: "platform", allowed: []string{"deployment/onprem", "explorer", "operations",
		"pagewiki/sessionconsumer", "session", "teamnote"}},
	{directory: "recall", allowed: []string{"teamnote"}},
	{directory: "session"},
	{directory: "sessionlake", allowed: []string{"session"}},
	{directory: "teamnote", excluded: []string{"transport"},
		allowed: []string{"platform/observability", "session", "sessionlake"}},
	{directory: "teamnote/transport", allowed: []string{"deployment/onprem", "explorer",
		"operations", "pagewiki/sessionconsumer", "pagewiki/transport/httpapi", "recall", "teamnote"}},
}

func (s *dependencySuite) TestEveryInternalPackageIsRegistered() {
	entries, err := os.ReadDir(s.root)
	s.Require().NoError(err)
	registered := make(map[string]struct{}, len(dependencyRules))
	for _, rule := range dependencyRules {
		top, _, _ := strings.Cut(rule.directory, "/")
		registered[top] = struct{}{}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		_, ok := registered[entry.Name()]
		s.True(ok, "internal/%s has no dependency whitelist entry; add it to "+
			"dependencyRules with an explicit minimal allowed-import list", entry.Name())
	}
}

func (s *dependencySuite) TestDependencyWhitelist() {
	for _, rule := range dependencyRules {
		if rule.unrestricted {
			continue
		}
		s.Run(rule.directory, func() {
			imports, err := productionImports(filepath.Join(s.root, rule.directory), rule.excluded...)
			s.Require().NoError(err)
			for _, imported := range imports {
				if !strings.HasPrefix(imported, modulePath) {
					continue
				}
				relative := strings.TrimPrefix(imported, modulePath)
				s.True(importAllowed(relative, rule),
					"%s imports %s which is not in its whitelist", rule.directory, imported)
			}
		})
	}
}

func (s *dependencySuite) TestOnlyEvalImportsEval() {
	for _, rule := range dependencyRules {
		if rule.directory == "eval" {
			continue
		}
		imports, err := productionImports(filepath.Join(s.root, rule.directory))
		s.Require().NoError(err)
		for _, imported := range imports {
			s.False(hasPathPrefix(strings.TrimPrefix(imported, modulePath), "eval"),
				"%s imports %s; only eval may import eval", rule.directory, imported)
		}
	}
}

func (s *dependencySuite) TestImportAllowedDefaultsToDeny() {
	rule := dependencyRule{directory: "example", excluded: []string{"transport"},
		allowed: []string{"session"}}
	s.False(importAllowed("platform/postgres", rule), "unlisted import must be denied")
	s.False(importAllowed("example/transport/httpapi", rule), "excluded subtree must be denied")
	s.True(importAllowed("session", rule))
	s.True(importAllowed("session/sub", rule))
	s.True(importAllowed("example/inner", rule))
}

func importAllowed(relative string, rule dependencyRule) bool {
	if hasPathPrefix(relative, rule.directory) {
		for _, excluded := range rule.excluded {
			if hasPathPrefix(relative, rule.directory+"/"+excluded) {
				return false
			}
		}
		return true
	}
	for _, allowed := range rule.allowed {
		if hasPathPrefix(relative, allowed) {
			return true
		}
	}
	return false
}

func hasPathPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
```

Add `"os"` to the imports.

- [ ] **Step 2: Run the suite — the deny unit test proves the deny path**

```bash
go test ./internal/architecture/ -v -run TestDependencySuite
```

Expected: PASS on all four subtests. If `TestDependencyWhitelist` fails, the whitelist missed a real as-built import — re-derive with `go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./internal/...` and tighten/correct the table (imports must match the table in this task's header; investigate any surprise rather than blindly widening).

- [ ] **Step 3: Commit**

```bash
git add internal/architecture/dependencies_test.go
git commit -m "test(architecture): enforce a default-deny dependency whitelist

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: Retire five extraction strategies

**Files:**
- Modify: `internal/teamnote/extractor/candidate_strategy.go` (drop 5 entries, add policy header)
- Modify: `internal/teamnote/extractor/extractor.go:68-90` (drop 5 constants + 5 aliases)
- Modify: `internal/teamnote/extractor/v2_prompt.go` (drop retired revision constants + prompts; comment retained building blocks)
- Modify: `internal/teamnote/extractor/v2.go:17` (drop claim-card-v1 revision constant), rename `mapExtractionClaimCardV1` → `mapExtractionClaimCard` wherever defined
- Modify: `internal/teamnote/extractor/rolling.go:352-354` (drop the claim-card-v1 case), `internal/teamnote/extractor/episode.go:35-37` (drop `ExtractionVersionClaimCardV1`)
- Delete: `internal/teamnote/extractor/v2_typed.go`, `v2_typed_prompt.go`, `v2_typed_test.go`
- Modify: `internal/teamnote/extractor/candidate_strategy_test.go`, `v2_test.go`, `admission_test.go`, `main_test.go`, `cmd/team-memory-extraction-eval-v1/main_test.go`
- Modify: `Makefile:37`, `scripts/test-extraction-candidate-builds.sh`, `.env.example:28-32`

**Interfaces:**
- Produces: `CandidateStrategyNames()` now returns exactly `[current 5]`: `interaction-slim`, `source-clause-v1`, `source-span-v1`, `source-span-v2`, `claim-card-v2` (table order: interaction-slim, source-clause, source-span-v1, source-span-v2, claim-card-v2).
- Constraint: prompt bytes of all five kept strategies are unchanged.

- [ ] **Step 1: Shrink the strategy table and add the retirement policy header**

In `candidate_strategy.go`, delete the entries for `CandidateStrategyCurrent`, `CandidateStrategyEvidenceFidelity`, `CandidateStrategyImplicitState`, `CandidateStrategyTyped2`, `CandidateStrategyClaimCardV1`. Rename the `mapExtractionClaimCardV1` reference in the `claim-card-v2` entry to `mapExtractionClaimCard`. Add above `var candidateStrategies`:

```go
// Retirement policy (see docs/decisions/2026-07-28-extraction-strategy-retirement.md):
// a strategy enters this table with a stated experiment goal and an eval exit
// condition. When the experiment concludes it is either promoted to the build
// default or deleted in the same change that records the conclusion. Git
// history is the archive — no dormant entries.
```

- [ ] **Step 2: Shrink the constant block in `extractor.go`**

Delete `CandidateStrategyCurrent`, `CandidateStrategyEvidenceFidelity`, `CandidateStrategyImplicitState`, `CandidateStrategyTyped2`, `CandidateStrategyClaimCardV1` and the aliases `V2VariantCurrent`, `V2VariantEvidenceFidelity`, `V2VariantImplicitState`, `V2VariantTypedCurrent`, `V2VariantClaimCardV1`. Keep the other five of each.

- [ ] **Step 3: Prune `v2_prompt.go` while preserving claim-card-v2's bytes**

Delete: `extractionProtocolV2RevisionCurrent`, `extractionProtocolV2RevisionEvidenceFidelity`, `extractionProtocolV2RevisionImplicitState` (lines 7, 9, 11), `rollingSystemPromptV2EvidenceFidelity` (:42), `rollingSystemPromptV2ImplicitState` (:59), `evidenceFidelityPromptV2` (:200), `implicitStateReviewPromptV2` (:218).

KEEP `rollingSystemPromptV2` (:19), `interactionPromptV2Current` (:174), and `rollingSystemPromptClaimCardV1` (:68) — add above `rollingSystemPromptV2`:

```go
// rollingSystemPromptV2 no longer ships as its own strategy ("current",
// retired 2026-07-28); it is retained verbatim because the claim-card
// prompts are built on it and must keep their exact bytes.
```

- [ ] **Step 4: Remove the remaining claim-card-v1 and typed-2 code**

- `v2.go:17`: delete `extractionProtocolV2RevisionClaimCardV1`; keep `...ClaimCardV2` which references `rollingSystemPromptClaimCardV2`.
- Rename `mapExtractionClaimCardV1` → `mapExtractionClaimCard` at its definition (find with `grep -rn 'func mapExtractionClaimCardV1' internal/teamnote/extractor/`).
- `rolling.go` `resultExtractionVersion()`: delete the `case CandidateStrategyClaimCardV1: return ExtractionVersionClaimCardV1` arm.
- `episode.go:35-37`: delete the `ExtractionVersionClaimCardV1` constant and its comment.
- `git rm internal/teamnote/extractor/v2_typed.go internal/teamnote/extractor/v2_typed_prompt.go internal/teamnote/extractor/v2_typed_test.go`
- Sweep for leftovers: `grep -rn 'Typed\|typed-2' internal/teamnote/extractor/ --include='*.go' | grep -v _test` — delete any orphaned typed decoder helpers this reveals (e.g. `decodeExtractionResponseV2Typed`, `decodeExtractionContentV2Typed` if they live outside `v2_typed.go`).

- [ ] **Step 5: Run the extractor tests to see what breaks**

```bash
go build ./... && go test ./internal/teamnote/extractor/ 2>&1 | head -50
```

Expected: compile failures in the five test files listing retired names.

- [ ] **Step 6: Update the tests**

In `candidate_strategy_test.go`, `v2_test.go`, `admission_test.go`, `main_test.go`, `cmd/team-memory-extraction-eval-v1/main_test.go`: update expected-name lists to the five kept strategies and delete subtests/cases that exercise retired prompts, decoders, or variants (do not port them). Also check `grep -rn '"current"\|"typed-2"\|"claim-card-v1"\|"evidence-fidelity-v1"\|"source-clause-implicit-state-v1"' cmd/team-memory-extraction-eval-v1/main.go internal/eval/ --include='*.go' | grep -v _test` — if eval production code hardcodes a retired name, update it to a kept strategy and note it in the commit message.

```bash
go test ./internal/teamnote/... ./cmd/... . && go test ./internal/architecture/
```

Expected: PASS.

- [ ] **Step 7: Update Makefile, build script, and .env.example**

Makefile line 37 case pattern becomes:

```
interaction-slim|source-clause-v1|source-span-v1|source-span-v2|claim-card-v2) ;; \
```

Also update the `EXTRACTION_CANDIDATE_STRATEGIES` help variable near line 22 to the same five names.

`scripts/test-extraction-candidate-builds.sh`: first loop list becomes `interaction-slim source-clause-v1 source-span-v1 source-span-v2 claim-card-v2`; second loop becomes `for strategy in unknown current typed-2 "interaction-slim typed-2"; do` (retired names must now fail validation).

`.env.example` lines 28–32: rewrite the comment to list only the five kept strategies and drop the "remain available only for reproducibility" sentence.

```bash
./scripts/test-extraction-candidate-builds.sh && make build
```

Expected: both succeed.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor(extractor): retire five extraction candidate strategies

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: Record the retirement decision (ADR)

**Files:**
- Create: `docs/decisions/2026-07-28-extraction-strategy-retirement.md`

- [ ] **Step 1: Write the ADR** (match the style of `docs/decisions/2026-07-24-device-scoped-agent-provisioning.md` — check its header layout first and mirror it):

```markdown
# Extraction candidate strategy retirement

- Status: accepted
- Date: 2026-07-28

## Context

The extractor kept ten candidate strategies permanently alive. One ships
(selected at link time); the rest carried permanent test and lint cost with
no retirement mechanism, and the strategy count only grew.

## Decision

Keep five strategies: `source-clause-v1` (build default), `interaction-slim`,
`source-span-v1`, `source-span-v2` (active evals), and `claim-card-v2`
(newest experiment). Delete `current`, `evidence-fidelity-v1`,
`source-clause-implicit-state-v1`, `typed-2`, and `claim-card-v1` with their
strategy-specific prompts, decoders, and tests.

Standing policy: a strategy enters `candidate_strategies` with a stated
experiment goal and an eval exit condition. When the experiment concludes,
the strategy is either promoted to the build default or deleted in the same
change that records the conclusion. Git history is the archive; the table
holds no dormant entries.

## Consequences

- Retired strategy names now fail `make build` validation and runtime
  strategy resolution.
- Rolling episodes stored under retired protocol versions lose their warm
  prefix and are re-extracted on the next slice (verified: episode
  compatibility is an exact protocol-version match; saved content decoding
  only runs for compatible episodes).
- `rollingSystemPromptV2` and `rollingSystemPromptClaimCardV1` remain in the
  code as building blocks of `claim-card-v2`'s prompt bytes.
```

- [ ] **Step 2: Commit**

```bash
git add docs/decisions/2026-07-28-extraction-strategy-retirement.md
git commit -m "docs: record the extraction strategy retirement decision

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 6: Full verification

- [ ] **Step 1: Full gate run**

```bash
make lint && make coverage && go test ./internal/architecture/ -v && ./scripts/test-extraction-candidate-builds.sh
```

Expected: all pass; coverage gate ≥ 80%.

- [ ] **Step 2: Spec acceptance greps**

```bash
grep -rn 'llmwiki' internal/pagewiki --include='*.go' | grep -v _test | grep -v legacy_hydration.go ; echo "expect no output"
grep -rn 'typed-2\|claim-card-v1"\|evidence-fidelity-v1\|implicit-state-v1' --include='*.go' internal/ main.go Makefile scripts/ ; echo "expect no output"
```

- [ ] **Step 3: Report** — list the commits and any deviations from this plan.
