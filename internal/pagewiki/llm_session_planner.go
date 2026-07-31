package pagewiki

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

const (
	plannerMaxBriefs     = 8
	plannerMaxEventBytes = 16 << 10
	plannerAttempts      = 2
	planDegradedBriefKey = "plan-degraded"
	planEmptyBriefKey    = "source-only"
)

type LLMPlannerConfig struct {
	Client llm.ChatClient
	Model  string
	Logger *slog.Logger
}

// LLMSessionPlanner lets the model choose durable pages while deterministic
// code retains control of identity, evidence validity, and publication.
type LLMSessionPlanner struct {
	client llm.ChatClient
	model  string
	logger *slog.Logger
}

func NewLLMSessionPlanner(config LLMPlannerConfig) (*LLMSessionPlanner, error) {
	if config.Client == nil {
		return nil, errors.New("create Page Wiki LLM planner: client is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("create Page Wiki LLM planner: model is required")
	}
	logger := config.Logger
	if logger == nil {
		logger = observability.DiscardLogger()
	}
	return &LLMSessionPlanner{
		client: config.Client, model: strings.TrimSpace(config.Model), logger: logger,
	}, nil
}

type llmPlanEvent struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

type llmPlanPage struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
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
	var lastErr error
	for attempt := 0; attempt < plannerAttempts; attempt++ {
		response, err := p.client.Complete(ctx, llm.ChatRequest{
			Model: p.model,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: pageWikiPlannerPrompt + generationDirectivesPrompt(input.Directives)},
				{Role: "user", Content: string(payload)},
			},
		})
		if err != nil {
			lastErr = err
			continue
		}
		var decoded llmPlanResponse
		if err := json.Unmarshal(
			[]byte(trimJSONFence(response.Message.Content)),
			&decoded,
		); err != nil {
			lastErr = err
			continue
		}
		return acceptedBriefs(decoded, input), nil
	}
	p.logger.Warn(
		"Page Wiki plan degraded after all attempts failed",
		"source_revision_id", input.SourceRevision.ID,
		"error", lastErr,
	)
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
			Slug: page.Slug, Title: page.Title, Summary: page.Summary,
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
		if slug == "" || title == "" {
			return PageBrief{}, false
		}
		if page, found := catalogBySlug(input.PageCatalog, slug); found {
			return updateBrief(page, candidate, eventIDs, evidence), true
		}
		return PageBrief{
			Key: slug, Action: PageActionCreate,
			ProposedSlug: slug, ProposedTitle: title,
			ReaderGoal:       strings.TrimSpace(candidate.ReaderGoal),
			EvidenceEventIDs: eventIDs,
			Evidence:         evidence,
		}, true
	case "update":
		if page, found := catalogBySlug(input.PageCatalog, strings.TrimSpace(candidate.TargetSlug)); found {
			return updateBrief(page, candidate, eventIDs, evidence), true
		}
		return PageBrief{}, false
	default:
		return PageBrief{}, false
	}
}

func catalogBySlug(catalog PageCatalog, slug string) (PageCatalogEntry, bool) {
	for _, page := range catalog {
		if page.Slug == slug {
			return page, true
		}
	}
	return PageCatalogEntry{}, false
}

func updateBrief(
	page PageCatalogEntry,
	candidate llmPlanBrief,
	eventIDs []string,
	evidence []EvidenceQuoteDraft,
) PageBrief {
	return PageBrief{
		Key: page.Slug, Action: PageActionUpdate,
		TargetPageID:           page.ID,
		ExpectedBaseRevisionID: page.CurrentRevisionID,
		ReaderGoal:             strings.TrimSpace(candidate.ReaderGoal),
		EvidenceEventIDs:       eventIDs,
		Evidence:               evidence,
	}
}

func validEvidence(
	candidates []llmPlanEvidence,
	revision SourceRevision,
) ([]EvidenceQuoteDraft, []string) {
	events := eventIndex(revision.Events)
	evidence := make([]EvidenceQuoteDraft, 0, len(candidates))
	eventIDs := make([]string, 0, len(candidates))
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
		if overlapsAcceptedQuote(evidence, quote) {
			continue
		}
		evidence = append(evidence, EvidenceQuoteDraft{
			EventID: candidate.EventID, ExactText: quote,
		})
		if !containsString(eventIDs, candidate.EventID) {
			eventIDs = append(eventIDs, candidate.EventID)
		}
	}
	return evidence, eventIDs
}

func overlapsAcceptedQuote(accepted []EvidenceQuoteDraft, quote string) bool {
	for _, existing := range accepted {
		if existing.ExactText == quote ||
			strings.Contains(existing.ExactText, quote) ||
			strings.Contains(quote, existing.ExactText) {
			return true
		}
	}
	return false
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
You receive one JSON object: {"events":[{"id","content","truncated"}],"pages":[{"slug","title","summary"}]}.
Return exactly one JSON object and no Markdown fence:
{"briefs":[{"action":"create|update|skip_noise","target_slug":"existing page slug, update only","proposed_slug":"kebab-case, create only","proposed_title":"English title, create only","reader_goal":"one English sentence","evidence":[{"event_id":"...","exact_quote":"verbatim substring of that event's content"}]}]}

Keep only knowledge a teammate would still need in a month: decisions and
their rationale, architecture, conventions, durable project state, and
domain facts. Treat as noise and mark skip_noise: one-off session narratives
about what an agent did or tried in a single session; transient
operational or verification records such as release checks, approvals, and
test-run logs; content unrelated to the team's work; single bug-fix details
that establish no lasting convention; code diffs and fragments; JSON or log
output; tool transcripts; agent system or skill instructions; branch names;
and timestamps. When in doubt, skip.

Updating an existing page is the rule, not a preference: when any page in
pages covers the same subject or a parent subject, or the new evidence
continues that subject's story, the action MUST be update with that page's
slug. Creating a page whose subject overlaps an existing page is an error.
Group related evidence aggressively into one page; most sessions should
yield zero to two briefs. Judge subject overlap with each page's summary, not its title alone. Every exact_quote must be copied verbatim from
the event content and must genuinely support the page. Account for every
event with either a page brief or skip_noise. Return at most 8 briefs and
JSON only.`

var _ Planner = (*LLMSessionPlanner)(nil)
