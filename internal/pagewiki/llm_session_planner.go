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
