# PageWiki LLM Planner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace deterministic heading-chunk page planning with an LLM planner that selects durable knowledge, rejects noise, and chooses create/update/skip per page, while keeping all deterministic evidence and publication layers authoritative.

**Architecture:** A new `LLMSessionPlanner` implements the existing `pagewiki.Planner` port with one chat call per source revision, validated deterministically before any brief reaches the service. `PageBrief` gains an `Evidence` field so `LLMSessionEditor` consumes planner-chosen quotes instead of re-running the heading chunker. `main.go` builds the LLM planner+editor pair when `LLMWIKI_ORGANIZER_MODE=openai|harness`.

**Tech Stack:** Go, `internal/llmwiki/workspace.ChatClient` (OpenAI-compatible DeepSeek client), `stretchr/testify/suite`, in-memory repository from `internal/pagewiki/memory`.

**Spec:** `docs/superpowers/specs/2026-07-27-pagewiki-llm-planner-design.md`

## Global Constraints

- Work happens on branch `feat/pagewiki-llm-planner` (worktree already exists; run all commands from the worktree root).
- Run tests with `go test ./internal/pagewiki/... ./internal/llmwiki/...` and the full gate `go build ./... && go test ./...` before the final commit of each task.
- Service invariant: planner must return between 1 and 8 briefs (`minPlannedPages`/`maxPlannedPages` in `internal/pagewiki/service.go:15-18`). Never return zero briefs.
- `ValidatePageBrief` (`internal/pagewiki/contracts.go:30`) requires: `create` → non-empty `ProposedSlug`+`ProposedTitle`+`TopicPath` (≤2 segments), no target IDs; `update` → `TargetPageID` present in catalog and `ExpectedBaseRevisionID == entry.CurrentRevisionID`; `source-only` → no target IDs.
- Citation invariant (`internal/pagewiki/service.go` `buildCitations`): every evidence quote must occur exactly once (`uniqueTextRange`) inside its event's content, and evidence events must be listed in `brief.EvidenceEventIDs`.
- All generated reader-facing text is English (existing editor prompt rule); code comments and identifiers follow existing file style.
- Do not modify: postgres repository, publication transaction, HTTP API, UI assets, `SessionDocumentEditor` behavior in `local` mode.

---

### Task 1: `PageBrief.Evidence` field, populated by the deterministic planner

`PageBrief` carries planner-selected exact quotes so editors no longer re-derive them. `SessionDocumentPlanner` fills it from its knowledge units, which keeps the existing `SessionDocumentPlanner`+`LLMSessionEditor` combination working after Task 2.

**Files:**
- Modify: `internal/pagewiki/types.go` (add field to `PageBrief`, around line 183)
- Modify: `internal/pagewiki/session_document.go` (`Plan`, around line 33)
- Test: `internal/pagewiki/session_document_test.go`

**Interfaces:**
- Produces: `PageBrief.Evidence []EvidenceQuoteDraft` — ordered quotes; `Evidence[i].EventID` pairs with `Evidence[i].ExactText`. Task 2 (editor) and Tasks 3-4 (LLM planner) rely on exactly this field name and type.

- [ ] **Step 1: Write the failing test**

Append to `internal/pagewiki/session_document_test.go` (match the file's existing test style — if it uses a suite, add a suite method; the assertions stay the same):

```go
func TestSessionDocumentPlannerCarriesEvidenceQuotes(t *testing.T) {
	raw := "## Wiki 数据模型\n页面使用 immutable revision。"
	briefs, err := pagewiki.SessionDocumentPlanner{}.Plan(
		context.Background(),
		pagewiki.PlanInput{SourceRevision: pagewiki.SourceRevision{
			ID:  "source-revision-1",
			Raw: []byte(raw),
			Events: []pagewiki.SourceEvent{{
				ID: "event-1", StartByte: 0, EndByte: len(raw),
			}},
		}},
	)
	require.NoError(t, err)
	require.Len(t, briefs, 1)
	require.Len(t, briefs[0].Evidence, 1)
	require.Equal(t, "event-1", briefs[0].Evidence[0].EventID)
	require.Equal(t, "页面使用 immutable revision。", briefs[0].Evidence[0].ExactText)
}
```

If the test file is in package `pagewiki_test`, keep the `pagewiki.` qualifiers; if it is in package `pagewiki`, drop them. Check the first line of the existing file first.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagewiki/ -run TestSessionDocumentPlannerCarriesEvidenceQuotes -v`
Expected: FAIL — `briefs[0].Evidence` is empty (field does not exist yet → compile error first; add the field in Step 3).

- [ ] **Step 3: Implement**

In `internal/pagewiki/types.go`, add to `PageBrief` after `EvidenceEventIDs`:

```go
type PageBrief struct {
	Key                    string
	Action                 PageAction
	TargetPageID           string
	ExpectedBaseRevisionID string
	ProposedSlug           string
	ProposedTitle          string
	ReaderGoal             string
	TopicPath              []string
	EvidenceEventIDs       []string
	Evidence               []EvidenceQuoteDraft
	RelatedPages           []RelatedPage
}
```

In `internal/pagewiki/session_document.go` `Plan`, when building each brief from a unit, add the evidence pairs:

```go
		evidence := make([]EvidenceQuoteDraft, 0, len(unit.quotes))
		for index, quote := range unit.quotes {
			evidence = append(evidence, EvidenceQuoteDraft{
				EventID: unit.eventIDs[index], ExactText: quote,
			})
		}
		brief := PageBrief{
			Key: unit.key, Action: PageActionCreate,
			ProposedSlug: unit.slug, ProposedTitle: unit.title,
			ReaderGoal:       "Understand the durable knowledge supported by Session Lake evidence.",
			TopicPath:        unit.topicPath,
			EvidenceEventIDs: uniqueStrings(unit.eventIDs),
			Evidence:         evidence,
		}
```

(Keep the existing update-matching block below it unchanged; it only clears slug/title/topic, not evidence.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pagewiki/ -v`
Expected: all PASS, including the new test.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/types.go internal/pagewiki/session_document.go internal/pagewiki/session_document_test.go
git commit -m "feat(pagewiki): carry planner evidence quotes on the page brief"
```

---

### Task 2: `LLMSessionEditor` consumes brief evidence

The editor stops calling `knowledgeUnitForBrief` (which re-runs the heading chunker) and reads topic, slug, quotes, and event IDs from the brief.

**Files:**
- Modify: `internal/pagewiki/llm_session_editor.go`
- Test: `internal/pagewiki/llm_session_editor_test.go`

**Interfaces:**
- Consumes: `PageBrief.Evidence` from Task 1.
- Produces: `LLMSessionEditor.Edit` behavior relied on by Task 6 — topic sent to the model is `Brief.ProposedTitle` (create) or `CurrentPage.Title` (update); draft slug is `Brief.ProposedSlug` (create) or `CurrentPage.Slug` (update); citations and the `Source evidence` section come from `Brief.Evidence` in order.

- [ ] **Step 1: Write the failing test**

Append to `internal/pagewiki/llm_session_editor_test.go`:

```go
func (s *llmSessionEditorSuite) TestUsesBriefEvidenceInsteadOfHeadingChunks() {
	client := &wikiChatClient{responses: []string{
		`{"title":"Release Policy","summary":"How the team ships releases.","sections":[{"key":"policy","heading":"Policy","markdown":"Releases ship weekly after the validation gate passes."}]}`,
	}}
	editor, err := pagewiki.NewLLMSessionEditor(pagewiki.LLMEditorConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)
	raw := "noise before ## Fake Heading\nreal decision: releases ship weekly."
	draft, err := editor.Edit(context.Background(), pagewiki.EditInput{
		SourceRevision: pagewiki.SourceRevision{
			ID:  "source-revision-1",
			Raw: []byte(raw),
			Events: []pagewiki.SourceEvent{{
				ID: "event-1", StartByte: 0, EndByte: len(raw),
			}},
		},
		Brief: pagewiki.PageBrief{
			Key: "release-policy", Action: pagewiki.PageActionCreate,
			ProposedSlug: "release-policy", ProposedTitle: "Release Policy",
			ReaderGoal:       "Understand the release cadence.",
			TopicPath:        []string{"Engineering", "Runtime"},
			EvidenceEventIDs: []string{"event-1"},
			Evidence: []pagewiki.EvidenceQuoteDraft{{
				EventID: "event-1", ExactText: "real decision: releases ship weekly.",
			}},
		},
	})
	s.Require().NoError(err)
	s.Equal("release-policy", draft.Slug)
	s.Require().Len(draft.Citations, 1)
	s.Equal("real decision: releases ship weekly.", draft.Citations[0].ExactText)
	s.Equal("event-1", draft.Citations[0].Evidence[0].EventID)
	s.Require().Len(client.requests, 1)
	s.Contains(client.requests[0].Messages[1].Content, "Release Policy")
	s.Contains(client.requests[0].Messages[1].Content, "real decision: releases ship weekly.")
	s.NotContains(client.requests[0].Messages[1].Content, "Fake Heading")
}
```

The final `NotContains` proves the heading chunker no longer feeds the editor: with the old `knowledgeUnitForBrief` path this brief key finds no unit and `Edit` errors with `brief "release-policy" is unavailable`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagewiki/ -run TestLLMSessionEditorSuite -v`
Expected: FAIL — `write Page Wiki with LLM: brief "release-policy" is unavailable`.

- [ ] **Step 3: Implement**

In `internal/pagewiki/llm_session_editor.go`:

1. Delete `knowledgeUnitForBrief` entirely.
2. Rewrite `Edit`:

```go
func (e *LLMSessionEditor) Edit(ctx context.Context, input EditInput) (PageDraft, error) {
	topic := strings.TrimSpace(input.Brief.ProposedTitle)
	if input.CurrentPage != nil {
		topic = input.CurrentPage.Title
	}
	quotes := make([]string, 0, len(input.Brief.Evidence))
	for _, evidence := range input.Brief.Evidence {
		quotes = append(quotes, evidence.ExactText)
	}
	payload, err := json.Marshal(llmEditRequest{
		Topic:        topic,
		ReaderGoal:   input.Brief.ReaderGoal,
		CurrentTitle: currentTitle(input),
		CurrentText:  currentText(input),
		Evidence:     quotes,
	})
	if err != nil {
		return PageDraft{}, fmt.Errorf("encode Page Wiki LLM request: %w", err)
	}
	response, err := e.client.Complete(ctx, workspace.ChatRequest{
		Model: e.model,
		Messages: []workspace.ChatMessage{
			{Role: "system", Content: pageWikiEnglishEditorPrompt},
			{Role: "user", Content: string(payload)},
		},
	})
	if err != nil {
		return PageDraft{}, fmt.Errorf("write Page Wiki with LLM: %w", err)
	}
	var generated llmEditResponse
	if err := json.Unmarshal([]byte(trimJSONFence(response.Message.Content)), &generated); err != nil {
		return PageDraft{}, fmt.Errorf("decode Page Wiki LLM response: %w", err)
	}
	return generatedDraft(input, generated)
}
```

3. Change `generatedDraft` signature to `generatedDraft(input EditInput, generated llmEditResponse) (PageDraft, error)` and replace every `unit` usage:
   - `slug := input.Brief.ProposedSlug` / `if input.CurrentPage != nil { slug = input.CurrentPage.Slug }`
   - the evidence/citation loop iterates `input.Brief.Evidence`:

```go
	evidenceMarkdown := make([]string, 0, len(input.Brief.Evidence))
	citations := make([]CitationDraft, 0, len(input.Brief.Evidence))
	for _, evidence := range input.Brief.Evidence {
		evidenceMarkdown = append(evidenceMarkdown, evidence.ExactText)
		citations = append(citations, CitationDraft{
			SectionKey: "source-evidence",
			ExactText:  evidence.ExactText,
			Evidence: []EvidenceQuoteDraft{{
				EventID: evidence.EventID, ExactText: evidence.ExactText,
			}},
		})
	}
```

Everything else in `generatedDraft` (section building, related-knowledge links) stays as is.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pagewiki/ -v`
Expected: all PASS. The pre-existing `TestWritesEnglishPagesWithDeterministicEvidenceAndXanaduLinks` must still pass — it now works because Task 1 made `SessionDocumentPlanner` fill `Evidence`. The pre-existing malformed-response test still passes because the editor no longer fails on an unknown brief key (it proceeds to the LLM call and fails on decode, same assertion).

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/llm_session_editor.go internal/pagewiki/llm_session_editor_test.go
git commit -m "feat(pagewiki): drive the LLM editor from planner-chosen evidence"
```

---

### Task 3: `LLMSessionPlanner` happy path

New planner: one chat call, strict JSON decode, mapping to validated `PageBrief` values.

**Files:**
- Create: `internal/pagewiki/llm_session_planner.go`
- Test: `internal/pagewiki/llm_session_planner_test.go`

**Interfaces:**
- Consumes: `workspace.ChatClient` (`Complete(context.Context, workspace.ChatRequest) (workspace.ChatResponse, error)`), `trimJSONFence` (same package, `llm_session_editor.go`), `nonSlugCharacter` regexp (same package, `session_document.go`).
- Produces: `NewLLMSessionPlanner(config LLMPlannerConfig) (*LLMSessionPlanner, error)` with `LLMPlannerConfig{Client workspace.ChatClient; Model string}`; `(*LLMSessionPlanner).Plan(context.Context, PlanInput) ([]PageBrief, error)`. Task 5 wires it; Task 6 exercises it end-to-end.

- [ ] **Step 1: Write the failing test**

Create `internal/pagewiki/llm_session_planner_test.go` (package `pagewiki_test`; reuse `wikiChatClient` from `llm_session_editor_test.go` — same package, already visible):

```go
package pagewiki_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/stretchr/testify/suite"
)

type llmSessionPlannerSuite struct {
	suite.Suite
}

func TestLLMSessionPlannerSuite(t *testing.T) {
	suite.Run(t, new(llmSessionPlannerSuite))
}

func plannerRevision() pagewiki.SourceRevision {
	raw := "decision: releases ship weekly.diff --git a/main.go b/main.go"
	return pagewiki.SourceRevision{
		ID:  "source-revision-1",
		Raw: []byte(raw),
		Events: []pagewiki.SourceEvent{
			{ID: "event-1", StartByte: 0, EndByte: 30},
			{ID: "event-2", StartByte: 30, EndByte: len(raw)},
		},
	}
}

func (s *llmSessionPlannerSuite) TestPlansCreateUpdateAndDropsNoise() {
	client := &wikiChatClient{responses: []string{`{"briefs":[
		{"action":"create","proposed_slug":"Release  Policy","proposed_title":"Release Policy",
		 "reader_goal":"Understand the release cadence.","topic_path":["Engineering","Runtime"],
		 "evidence":[{"event_id":"event-1","exact_quote":"releases ship weekly"}]},
		{"action":"update","target_slug":"existing-page",
		 "reader_goal":"Refresh the existing page.","topic_path":["Engineering"],
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]},
		{"action":"skip_noise","reader_goal":"code diff",
		 "evidence":[{"event_id":"event-2","exact_quote":"diff --git"}]}
	]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
		PageCatalog: pagewiki.PageCatalog{{
			ID: "page-1", Slug: "existing-page", Title: "Existing Page",
			CurrentRevisionID: "revision-1",
		}},
	})

	s.Require().NoError(err)
	s.Require().Len(briefs, 2)

	create := briefs[0]
	s.Equal(pagewiki.PageActionCreate, create.Action)
	s.Equal("release-policy", create.ProposedSlug)
	s.Equal("release-policy", create.Key)
	s.Equal("Release Policy", create.ProposedTitle)
	s.Equal([]string{"Engineering", "Runtime"}, create.TopicPath)
	s.Equal([]string{"event-1"}, create.EvidenceEventIDs)
	s.Require().Len(create.Evidence, 1)
	s.Equal("releases ship weekly", create.Evidence[0].ExactText)

	update := briefs[1]
	s.Equal(pagewiki.PageActionUpdate, update.Action)
	s.Equal("page-1", update.TargetPageID)
	s.Equal("revision-1", update.ExpectedBaseRevisionID)
	s.Equal("existing-page", update.Key)
	s.Empty(update.ProposedSlug)
	s.Empty(update.TopicPath)

	s.Require().Len(client.requests, 1)
	s.Equal("test-model", client.requests[0].Model)
	s.Contains(client.requests[0].Messages[1].Content, "event-1")
	s.Contains(client.requests[0].Messages[1].Content, "existing-page")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagewiki/ -run TestLLMSessionPlannerSuite -v`
Expected: compile FAIL — `NewLLMSessionPlanner` undefined.

- [ ] **Step 3: Implement**

Create `internal/pagewiki/llm_session_planner.go`:

```go
package pagewiki

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
)

const (
	plannerMaxBriefs     = 8
	plannerMaxEventBytes = 16 << 10
	plannerAttempts      = 2
	planDegradedBriefKey = "plan-degraded"
	planEmptyBriefKey    = "source-only"
)

type LLMPlannerConfig struct {
	Client workspace.ChatClient
	Model  string
}

// LLMSessionPlanner lets the model choose durable pages while deterministic
// code retains control of identity, evidence validity, and publication.
type LLMSessionPlanner struct {
	client workspace.ChatClient
	model  string
}

func NewLLMSessionPlanner(config LLMPlannerConfig) (*LLMSessionPlanner, error) {
	if config.Client == nil {
		return nil, errors.New("create Page Wiki LLM planner: client is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("create Page Wiki LLM planner: model is required")
	}
	return &LLMSessionPlanner{
		client: config.Client, model: strings.TrimSpace(config.Model),
	}, nil
}

type llmPlanEvent struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

type llmPlanPage struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

type llmPlanRequest struct {
	Events []llmPlanEvent `json:"events"`
	Pages  []llmPlanPage  `json:"pages"`
}

type llmPlanEvidence struct {
	EventID    string `json:"event_id"`
	ExactQuote string `json:"exact_quote"`
}

type llmPlanBrief struct {
	Action        string            `json:"action"`
	TargetSlug    string            `json:"target_slug"`
	ProposedSlug  string            `json:"proposed_slug"`
	ProposedTitle string            `json:"proposed_title"`
	ReaderGoal    string            `json:"reader_goal"`
	TopicPath     []string          `json:"topic_path"`
	Evidence      []llmPlanEvidence `json:"evidence"`
}

type llmPlanResponse struct {
	Briefs []llmPlanBrief `json:"briefs"`
}

func (p *LLMSessionPlanner) Plan(
	ctx context.Context,
	input PlanInput,
) ([]PageBrief, error) {
	payload, err := json.Marshal(planRequest(input))
	if err != nil {
		return nil, fmt.Errorf("encode Page Wiki plan request: %w", err)
	}
	for attempt := 0; attempt < plannerAttempts; attempt++ {
		response, err := p.client.Complete(ctx, workspace.ChatRequest{
			Model: p.model,
			Messages: []workspace.ChatMessage{
				{Role: "system", Content: pageWikiPlannerPrompt},
				{Role: "user", Content: string(payload)},
			},
		})
		if err != nil {
			continue
		}
		var decoded llmPlanResponse
		if err := json.Unmarshal(
			[]byte(trimJSONFence(response.Message.Content)),
			&decoded,
		); err != nil {
			continue
		}
		return acceptedBriefs(decoded, input), nil
	}
	return []PageBrief{sourceOnlyBrief(planDegradedBriefKey, input.SourceRevision)}, nil
}

func planRequest(input PlanInput) llmPlanRequest {
	request := llmPlanRequest{
		Events: make([]llmPlanEvent, 0, len(input.SourceRevision.Events)),
		Pages:  make([]llmPlanPage, 0, len(input.PageCatalog)),
	}
	for _, event := range input.SourceRevision.Events {
		content := string(input.SourceRevision.Raw[event.StartByte:event.EndByte])
		truncated := false
		if len(content) > plannerMaxEventBytes {
			content = content[:plannerMaxEventBytes]
			truncated = true
		}
		request.Events = append(request.Events, llmPlanEvent{
			ID: event.ID, Content: content, Truncated: truncated,
		})
	}
	for _, page := range input.PageCatalog {
		request.Pages = append(request.Pages, llmPlanPage{
			Slug: page.Slug, Title: page.Title,
		})
	}
	return request
}

func acceptedBriefs(decoded llmPlanResponse, input PlanInput) []PageBrief {
	briefs := make([]PageBrief, 0, plannerMaxBriefs)
	seenKeys := make(map[string]struct{})
	for _, candidate := range decoded.Briefs {
		if len(briefs) >= plannerMaxBriefs {
			break
		}
		brief, accepted := acceptBrief(candidate, input)
		if !accepted {
			continue
		}
		if _, exists := seenKeys[brief.Key]; exists {
			continue
		}
		seenKeys[brief.Key] = struct{}{}
		briefs = append(briefs, brief)
	}
	if len(briefs) == 0 {
		return []PageBrief{sourceOnlyBrief(planEmptyBriefKey, input.SourceRevision)}
	}
	return briefs
}

func acceptBrief(candidate llmPlanBrief, input PlanInput) (PageBrief, bool) {
	evidence, eventIDs := validEvidence(candidate.Evidence, input.SourceRevision)
	if len(evidence) == 0 {
		return PageBrief{}, false
	}
	switch candidate.Action {
	case "create":
		slug := strings.Trim(nonSlugCharacter.ReplaceAllString(
			strings.ToLower(candidate.ProposedSlug), "-",
		), "-")
		title := strings.TrimSpace(candidate.ProposedTitle)
		topicPath := trimmedTopicPath(candidate.TopicPath)
		if slug == "" || title == "" || len(topicPath) == 0 {
			return PageBrief{}, false
		}
		return PageBrief{
			Key: slug, Action: PageActionCreate,
			ProposedSlug: slug, ProposedTitle: title,
			ReaderGoal:       strings.TrimSpace(candidate.ReaderGoal),
			TopicPath:        topicPath,
			EvidenceEventIDs: eventIDs,
			Evidence:         evidence,
		}, true
	case "update":
		for _, page := range input.PageCatalog {
			if page.Slug != strings.TrimSpace(candidate.TargetSlug) {
				continue
			}
			return PageBrief{
				Key: page.Slug, Action: PageActionUpdate,
				TargetPageID:           page.ID,
				ExpectedBaseRevisionID: page.CurrentRevisionID,
				ReaderGoal:             strings.TrimSpace(candidate.ReaderGoal),
				EvidenceEventIDs:       eventIDs,
				Evidence:               evidence,
			}, true
		}
		return PageBrief{}, false
	default:
		return PageBrief{}, false
	}
}

func validEvidence(
	candidates []llmPlanEvidence,
	revision SourceRevision,
) ([]EvidenceQuoteDraft, []string) {
	events := eventIndex(revision.Events)
	evidence := make([]EvidenceQuoteDraft, 0, len(candidates))
	eventIDs := make([]string, 0, len(candidates))
	seenQuotes := make(map[string]struct{})
	for _, candidate := range candidates {
		event, found := events[candidate.EventID]
		if !found {
			continue
		}
		quote := candidate.ExactQuote
		if strings.TrimSpace(quote) == "" {
			continue
		}
		content := string(revision.Raw[event.StartByte:event.EndByte])
		if strings.Count(content, quote) != 1 {
			continue
		}
		dedupeKey := candidate.EventID + "\x00" + quote
		if _, exists := seenQuotes[dedupeKey]; exists {
			continue
		}
		seenQuotes[dedupeKey] = struct{}{}
		evidence = append(evidence, EvidenceQuoteDraft{
			EventID: candidate.EventID, ExactText: quote,
		})
		if !containsString(eventIDs, candidate.EventID) {
			eventIDs = append(eventIDs, candidate.EventID)
		}
	}
	return evidence, eventIDs
}

func trimmedTopicPath(values []string) []string {
	result := make([]string, 0, 2)
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
		if len(result) == 2 {
			break
		}
	}
	return result
}

func sourceOnlyBrief(key string, revision SourceRevision) PageBrief {
	eventIDs := make([]string, 0, len(revision.Events))
	for _, event := range revision.Events {
		eventIDs = append(eventIDs, event.ID)
	}
	return PageBrief{
		Key: key, Action: PageActionSourceOnly, EvidenceEventIDs: eventIDs,
	}
}

const pageWikiPlannerPrompt = `You are the maintenance planner of a durable, evidence-backed team Wiki.
You receive one JSON object: {"events":[{"id","content","truncated"}],"pages":[{"slug","title"}]}.
Return exactly one JSON object and no Markdown fence:
{"briefs":[{"action":"create|update|skip_noise","target_slug":"existing page slug, update only","proposed_slug":"kebab-case, create only","proposed_title":"English title, create only","reader_goal":"one English sentence","topic_path":["Area","Subarea"],"evidence":[{"event_id":"...","exact_quote":"verbatim substring of that event's content"}]}]}

Select only knowledge that stays durable for the team: decisions, designs,
conventions, project state, and domain facts. Treat as noise and mark
skip_noise: code diffs and fragments, JSON or log output, tool transcripts,
agent system or skill instructions, branch names, timestamps, and one-off
conversation. When in doubt, skip. Prefer updating an existing page over
creating a near-duplicate; use action update with that page's slug from
pages. Group related evidence into one page instead of one page per
fragment. Every exact_quote must be copied verbatim from the event content
and must genuinely support the page. topic_path has at most two segments,
for example ["Engineering","Runtime"]. Account for every event with either
a page brief or skip_noise. Return at most 8 briefs and JSON only.`

var _ Planner = (*LLMSessionPlanner)(nil)
```

Note: `eventIndex` and `containsString` already exist in this package (`service.go` and `session_document.go`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pagewiki/ -run TestLLMSessionPlannerSuite -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/llm_session_planner.go internal/pagewiki/llm_session_planner_test.go
git commit -m "feat(pagewiki): add the LLM session planner"
```

---

### Task 4: Planner validation edge cases and degradation

Locks in the untrusted-output rules and the approved no-garbage degradation.

**Files:**
- Modify: `internal/pagewiki/llm_session_planner.go` (only if a test exposes a gap — the Task 3 implementation is expected to already satisfy these)
- Test: `internal/pagewiki/llm_session_planner_test.go`

**Interfaces:**
- Consumes: `NewLLMSessionPlanner`, `wikiChatClient` (append an `err` field, see Step 1).
- Produces: degradation contract relied on by operations: malformed output twice → single `source-only` brief with `Key == "plan-degraded"`; all briefs rejected → `Key == "source-only"`.

- [ ] **Step 1: Extend the scripted client to support errors**

In `internal/pagewiki/llm_session_editor_test.go`, extend `wikiChatClient`:

```go
type wikiChatClient struct {
	requests  []workspace.ChatRequest
	responses []string
	err       error
}

func (c *wikiChatClient) Complete(
	_ context.Context,
	request workspace.ChatRequest,
) (workspace.ChatResponse, error) {
	c.requests = append(c.requests, request)
	if c.err != nil {
		return workspace.ChatResponse{}, c.err
	}
	response := c.responses[0]
	c.responses = c.responses[1:]
	return workspace.ChatResponse{
		Message: workspace.ChatMessage{Role: "assistant", Content: response},
	}, nil
}
```

- [ ] **Step 2: Write the failing tests**

Append to `internal/pagewiki/llm_session_planner_test.go`:

```go
func (s *llmSessionPlannerSuite) TestDropsInvalidEvidenceAndBriefs() {
	client := &wikiChatClient{responses: []string{`{"briefs":[
		{"action":"create","proposed_slug":"ghost","proposed_title":"Ghost",
		 "topic_path":["Engineering"],
		 "evidence":[{"event_id":"missing-event","exact_quote":"decision:"}]},
		{"action":"create","proposed_slug":"ambiguous","proposed_title":"Ambiguous",
		 "topic_path":["Engineering"],
		 "evidence":[{"event_id":"event-1","exact_quote":"e"}]},
		{"action":"update","target_slug":"absent-page",
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]},
		{"action":"create","proposed_slug":"","proposed_title":"No Slug",
		 "topic_path":["Engineering"],
		 "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]}
	]}`}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	s.Require().NoError(err)
	s.Require().Len(briefs, 1)
	s.Equal("source-only", briefs[0].Key)
	s.Equal(pagewiki.PageActionSourceOnly, briefs[0].Action)
	s.Equal([]string{"event-1", "event-2"}, briefs[0].EvidenceEventIDs)
}

func (s *llmSessionPlannerSuite) TestRetriesOnceThenDegradesToSourceOnly() {
	client := &wikiChatClient{responses: []string{"not-json", "still-not-json"}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	s.Require().NoError(err)
	s.Len(client.requests, 2)
	s.Require().Len(briefs, 1)
	s.Equal("plan-degraded", briefs[0].Key)
	s.Equal(pagewiki.PageActionSourceOnly, briefs[0].Action)
}

func (s *llmSessionPlannerSuite) TestDegradesWhenTheModelIsUnreachable() {
	client := &wikiChatClient{err: context.DeadlineExceeded}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	s.Require().NoError(err)
	s.Len(client.requests, 2)
	s.Require().Len(briefs, 1)
	s.Equal("plan-degraded", briefs[0].Key)
}

func (s *llmSessionPlannerSuite) TestCapsAcceptedBriefsAtEight() {
	var body strings.Builder
	body.WriteString(`{"briefs":[`)
	for index := 0; index < 10; index++ {
		if index > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body,
			`{"action":"create","proposed_slug":"page-%d","proposed_title":"Page %d",
			  "topic_path":["Engineering"],
			  "evidence":[{"event_id":"event-1","exact_quote":"decision:"}]}`,
			index, index)
	}
	body.WriteString(`]}`)
	client := &wikiChatClient{responses: []string{body.String()}}
	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)

	briefs, err := planner.Plan(context.Background(), pagewiki.PlanInput{
		SourceRevision: plannerRevision(),
	})

	s.Require().NoError(err)
	s.Len(briefs, 8)
}
```

Add `"fmt"` and `"strings"` to the test file imports. Note `TestDropsInvalidEvidenceAndBriefs` rejects the second brief because the quote `"e"` occurs many times in the event content (`strings.Count != 1`).

- [ ] **Step 3: Run tests**

Run: `go test ./internal/pagewiki/ -run TestLLMSessionPlannerSuite -v`
Expected: PASS if the Task 3 implementation is complete; if any case fails, fix `llm_session_planner.go` until all pass — the tests are the contract, do not weaken them.

- [ ] **Step 4: Run the package suite**

Run: `go test ./internal/pagewiki/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pagewiki/llm_session_planner_test.go internal/pagewiki/llm_session_editor_test.go internal/pagewiki/llm_session_planner.go
git commit -m "test(pagewiki): pin planner validation and degradation behavior"
```

---

### Task 5: Wire the LLM planner pair in `main.go`

`LLMWIKI_ORGANIZER_MODE=openai|harness` now builds the LLM planner AND the LLM editor; `local`/empty keeps the deterministic pair.

**Files:**
- Modify: `main.go` (`buildPageWikiHTTPHandler` around line 167-186, `buildPageWikiEditor` around line 202-229)

**Interfaces:**
- Consumes: `pagewiki.NewLLMSessionPlanner` / `pagewiki.LLMPlannerConfig` from Task 3.
- Produces: `buildPageWikiMaintainers(config applicationConfig) (pagewiki.Planner, pagewiki.Editor, error)`.

- [ ] **Step 1: Implement the wiring**

Replace `buildPageWikiEditor` with:

```go
func buildPageWikiMaintainers(
	config applicationConfig,
) (pagewiki.Planner, pagewiki.Editor, error) {
	switch strings.TrimSpace(config.llmwikiMode) {
	case "", "local":
		return pagewiki.SessionDocumentPlanner{}, pagewiki.SessionDocumentEditor{}, nil
	case "openai", "harness":
		if strings.TrimSpace(config.llmwikiBaseURL) == "" ||
			strings.TrimSpace(config.llmwikiAPIKey) == "" ||
			strings.TrimSpace(config.llmwikiModel) == "" {
			return nil, nil, errors.New(
				"initialize Page Wiki LLM maintainers: LLMWIKI_LLM_BASE_URL, " +
					"LLMWIKI_LLM_API_KEY, and LLMWIKI_LLM_MODEL are required",
			)
		}
		client := llmwikiworkspace.NewDeepSeekClient(llmwikiworkspace.DeepSeekConfig{
			BaseURL: config.llmwikiBaseURL,
			APIKey:  config.llmwikiAPIKey,
		})
		planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
			Client: client, Model: config.llmwikiModel,
		})
		if err != nil {
			return nil, nil, err
		}
		editor, err := pagewiki.NewLLMSessionEditor(pagewiki.LLMEditorConfig{
			Client: client, Model: config.llmwikiModel,
		})
		if err != nil {
			return nil, nil, err
		}
		return planner, editor, nil
	default:
		return nil, nil, fmt.Errorf(
			"initialize Page Wiki LLM maintainers: unsupported LLMWIKI_ORGANIZER_MODE %q",
			config.llmwikiMode,
		)
	}
}
```

In `buildPageWikiHTTPHandler`, replace:

```go
	editor, err := buildPageWikiEditor(config)
	if err != nil {
		return nil, nil, err
	}
	service := pagewiki.NewService(
		repository,
		pagewiki.SessionDocumentPlanner{},
		editor,
	)
```

with:

```go
	planner, editor, err := buildPageWikiMaintainers(config)
	if err != nil {
		return nil, nil, err
	}
	service := pagewiki.NewService(repository, planner, editor)
```

- [ ] **Step 2: Build and search for stale references**

Run: `go build ./... && grep -rn "buildPageWikiEditor" --include="*.go" .`
Expected: build succeeds; grep finds nothing (update `main_test.go` if it referenced the old function — mirror whatever assertion pattern it used, now against `buildPageWikiMaintainers`).

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat(pagewiki): wire the LLM planner and editor pair by organizer mode"
```

(Include `main_test.go` in the `git add` if Step 2 changed it.)

---

### Task 6: Acceptance test — a noisy session produces only genuine pages

End-to-end proof against the spec's core complaint: skill documents and code diffs stop becoming pages.

**Files:**
- Create: `internal/pagewiki/llm_plan_acceptance_test.go`

**Interfaces:**
- Consumes: `NewLLMSessionPlanner`, `NewLLMSessionEditor`, `memory.NewRepository()`, `pagewiki.NewService`.

- [ ] **Step 1: Write the failing test**

Create `internal/pagewiki/llm_plan_acceptance_test.go`:

```go
package pagewiki_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type llmPlanAcceptanceSuite struct {
	suite.Suite
}

func TestLLMPlanAcceptanceSuite(t *testing.T) {
	suite.Run(t, new(llmPlanAcceptanceSuite))
}

func (s *llmPlanAcceptanceSuite) TestNoisySessionProducesOnlyGenuinePages() {
	skillDoc := "## Checklist\nYou MUST create a task for each of these items."
	codeDiff := "diff --git a/main.go b/main.go\n+func added() {}"
	knowledge := "决定：team memory 的召回默认走 BM25，向量召回只做补充。"
	raw := skillDoc + codeDiff + knowledge

	planner, err := pagewiki.NewLLMSessionPlanner(pagewiki.LLMPlannerConfig{
		Client: &wikiChatClient{responses: []string{`{"briefs":[
			{"action":"skip_noise","reader_goal":"agent skill instructions",
			 "evidence":[{"event_id":"event-skill","exact_quote":"## Checklist"}]},
			{"action":"skip_noise","reader_goal":"code diff",
			 "evidence":[{"event_id":"event-diff","exact_quote":"diff --git"}]},
			{"action":"create","proposed_slug":"recall-strategy","proposed_title":"Recall Strategy",
			 "reader_goal":"Understand the default recall path.","topic_path":["Engineering","Retrieval"],
			 "evidence":[{"event_id":"event-knowledge","exact_quote":"召回默认走 BM25"}]}
		]}`}},
		Model: "test-model",
	})
	s.Require().NoError(err)
	editor, err := pagewiki.NewLLMSessionEditor(pagewiki.LLMEditorConfig{
		Client: &wikiChatClient{responses: []string{
			`{"title":"Recall Strategy","summary":"BM25 is the default recall path.","sections":[{"key":"decision","heading":"Decision","markdown":"The team defaults recall to BM25 and treats vector recall as a supplement."}]}`,
		}},
		Model: "test-model",
	})
	s.Require().NoError(err)
	repository := memory.NewRepository()
	service := pagewiki.NewService(repository, planner, editor)

	result, err := service.InjectSession(context.Background(), pagewiki.InjectSessionRequest{
		SourceID:       "session-noisy",
		IdempotencyKey: "session-noisy-injection",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{
			{ID: "event-skill", StartByte: 0, EndByte: len(skillDoc)},
			{ID: "event-diff", StartByte: len(skillDoc), EndByte: len(skillDoc) + len(codeDiff)},
			{ID: "event-knowledge", StartByte: len(skillDoc) + len(codeDiff), EndByte: len(raw)},
		},
	})

	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Len(result.Run.Targets, 1)
	s.Equal(pagewiki.PageActionCreate, result.Run.Targets[0].Action)

	page, err := repository.PageBySlug(context.Background(), "recall-strategy")
	s.Require().NoError(err)
	revision, err := repository.PageRevision(context.Background(), page.CurrentRevisionID)
	s.Require().NoError(err)
	s.Equal("Recall Strategy", revision.Title)
	s.Contains(revision.Markdown, "defaults recall to BM25")
	s.Contains(revision.Markdown, "召回默认走 BM25")
	s.Require().Len(revision.Citations, 1)
	s.Equal("event-knowledge", revision.Citations[0].SourceAnchors[0].EventID)

	navigation, err := repository.Navigation(context.Background())
	s.Require().NoError(err)
	s.Require().Len(navigation.Roots, 1)
	s.Equal("Engineering", navigation.Roots[0].Title)

	_, err = repository.PageBySlug(context.Background(), "knowledge-checklist")
	s.Require().ErrorIs(err, pagewiki.ErrNotFound)
}
```

- [ ] **Step 2: Run test to verify it passes (or expose integration gaps)**

Run: `go test ./internal/pagewiki/ -run TestLLMPlanAcceptanceSuite -v`
Expected: PASS. If it fails, the failure is a real integration bug between Tasks 1-4 (most likely a citation invariant); fix the implementation, never the invariant.

- [ ] **Step 3: Run the full suite and gofmt**

Run: `gofmt -l internal/pagewiki/ && go test ./...`
Expected: gofmt prints nothing; all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/pagewiki/llm_plan_acceptance_test.go
git commit -m "test(pagewiki): accept noisy sessions without publishing noise pages"
```

---

### Task 7: Manual deployment verification on the workstation

Not a code task — the operator checklist that closes the spec's manual acceptance criterion. Requires access to the workstation running the Team Memory Portal at `http://100.125.72.76:58080/`.

- [ ] **Step 1: Deploy the branch with LLM mode enabled**

On the workstation, set the environment before starting the server:

```bash
export LLMWIKI_ORGANIZER_MODE=openai
export LLMWIKI_LLM_BASE_URL=https://api.deepseek.com
export LLMWIKI_LLM_API_KEY=<key>
export LLMWIKI_LLM_MODEL=deepseek-v4-pro
```

- [ ] **Step 2: Reset the backlog (approved: wipe and re-inject)**

Truncate the pagewiki tables in the deployment database (page, page revision, citation, link, topic, placement, maintenance-run, and source tables owned by the pagewiki postgres repository — enumerate them via the migrations in `internal/pagewiki/postgres/`), then re-inject the sessions through the existing session consumer or `POST /sessions/inject`.

- [ ] **Step 3: Verify**

- `GET /v1/wiki/navigation`: no heading-fragment pages (nothing like `Checklist`, `+0x78`, `main...origin/main`).
- Open several pages: prose summaries (not "Maintained from N grounded Session Lake evidence item(s)"), citations resolve to real source anchors.
- Inject one session consisting only of noise: the maintenance run's single target is `source-only` and no page appears.

---

## Self-Review Notes

- Spec coverage: planner (Tasks 3-4), evidence contract + editor rewiring (Tasks 1-2), configuration pairing (Task 5), degradation policy (Task 4), acceptance criteria (Tasks 6-7), backlog wipe (Task 7). The spec's `plan-degraded` key is pinned by `TestRetriesOnceThenDegradesToSourceOnly`.
- Type consistency: `LLMPlannerConfig{Client, Model}` mirrors `LLMEditorConfig`; `Evidence []EvidenceQuoteDraft` is used identically in Tasks 1, 2, 3, and 6; `buildPageWikiMaintainers` returns `(pagewiki.Planner, pagewiki.Editor, error)` and is consumed in the same task.
- Known judgment call: `wikiChatClient` gains an `err` field in Task 4 rather than a new fake, because the planner and editor tests share one scripted client in package `pagewiki_test`.
