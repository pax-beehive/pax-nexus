package pagewiki

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

const (
	plannerMaxBriefs       = 8
	plannerMaxEventBytes   = 16 << 10
	plannerAttempts        = 2
	plannerMaxRelatedPages = 4
	planDegradedBriefKey   = "plan-degraded"
	planEmptyBriefKey      = "source-only"
	plannerTitleMaxWords   = 9
	plannerTitleMaxRunes   = 80
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
	RelatedSlugs  []string          `json:"related_slugs,omitempty"`
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
	accepted := make([]plannedBrief, 0, plannerMaxBriefs)
	seenKeys := make(map[string]struct{})
	for _, candidate := range decoded.Briefs {
		if len(accepted) >= plannerMaxBriefs {
			break
		}
		brief, accepted_ok := acceptBrief(candidate, input)
		if !accepted_ok {
			continue
		}
		if _, exists := seenKeys[brief.Key]; exists {
			continue
		}
		seenKeys[brief.Key] = struct{}{}
		accepted = append(accepted, plannedBrief{brief: brief, relatedSlugs: candidate.RelatedSlugs})
	}
	if len(accepted) == 0 {
		return []PageBrief{sourceOnlyBrief(planEmptyBriefKey, input.SourceRevision)}
	}
	targets := relatedTargets(accepted, input)
	briefs := make([]PageBrief, 0, len(accepted))
	for _, planned := range accepted {
		planned.brief.RelatedPages = resolveRelatedPages(planned, targets)
		briefs = append(briefs, planned.brief)
	}
	return briefs
}

// plannedBrief carries the planner's raw related_slugs alongside the accepted
// brief; slugs resolve to page IDs only after every brief is accepted, since a
// brief may link to a sibling created later in the same run.
type plannedBrief struct {
	brief        PageBrief
	relatedSlugs []string
}

// relatedTargets maps every linkable slug to its page identity: existing
// catalog pages by their real IDs, and accepted create briefs by the stable
// page ID the service assigns at publication (see loadTargetPages).
func relatedTargets(accepted []plannedBrief, input PlanInput) map[string]RelatedPage {
	targets := make(map[string]RelatedPage, len(input.PageCatalog)+len(accepted))
	for _, page := range input.PageCatalog {
		targets[page.Slug] = RelatedPage{ID: page.ID, Title: page.Title}
	}
	for _, planned := range accepted {
		if planned.brief.Action != PageActionCreate {
			continue
		}
		targets[planned.brief.ProposedSlug] = RelatedPage{
			ID:    stableID("page", input.SourceRevision.ID, planned.brief.Key),
			Title: planned.brief.ProposedTitle,
		}
	}
	return targets
}

func resolveRelatedPages(planned plannedBrief, targets map[string]RelatedPage) []RelatedPage {
	related := make([]RelatedPage, 0, len(planned.relatedSlugs))
	for _, slug := range planned.relatedSlugs {
		if len(related) >= plannerMaxRelatedPages {
			break
		}
		// A page never links to itself; Key holds the page's own slug for both
		// create and update briefs.
		if slug == planned.brief.Key {
			continue
		}
		target, found := targets[slug]
		if !found {
			continue
		}
		duplicate := false
		for _, existing := range related {
			if existing.ID == target.ID {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		related = append(related, target)
	}
	return related
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
		title := normalizeProposedTitle(candidate.ProposedTitle)
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

// normalizeProposedTitle trims the title and strips trailing periods; it
// returns "" for titles long enough to read as sentences, which drops the
// brief the same way an empty title does. The bounds are looser than the
// prompt's five-word rule on purpose: the prompt shapes, the guard only
// catches egregious sentence-titles.
func normalizeProposedTitle(raw string) string {
	title := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(raw), ".。"))
	if utf8.RuneCountInString(title) > plannerTitleMaxRunes ||
		len(strings.Fields(title)) > plannerTitleMaxWords {
		return ""
	}
	return title
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
{"briefs":[{"action":"create|update|skip_noise","target_slug":"existing page slug, update only","proposed_slug":"kebab-case, create only","proposed_title":"English title, create only","reader_goal":"one English sentence","related_slugs":["up to 3 slugs this page durably relates to"],"evidence":[{"event_id":"...","exact_quote":"verbatim substring of that event's content"}]}]}

Keep only knowledge a teammate would still need in a month: decisions and
their rationale, architecture, conventions, durable project state, and
domain facts. Treat as noise and mark skip_noise: one-off session narratives
about what an agent did or tried in a single session; transient
operational or verification records such as release checks, approvals, and
test-run logs; content unrelated to the team's work; single bug-fix details
that establish no lasting convention; code diffs and fragments; JSON or log
output; tool transcripts; agent system or skill instructions; branch names;
and timestamps. When in doubt, skip.

A page's subject is a durable concept — a component, subsystem, decision,
convention, or domain fact — never an activity, task, fix, or session.
Before proposing a create, name the concept the evidence is about: the
page is about that concept, and this session's events are only new
evidence for it. proposed_title is a concise noun phrase naming that
concept — at most five words, never a sentence, no trailing period, and
never opening with a verb or gerund such as "Fixing", "Adding",
"Improving", or "Updating". "Xanadu Links" is a title; "Fixing xanadu
links on the planner path" is not.

Updating an existing page is the rule, not a preference: when any page in
pages covers the same subject or a parent subject, or the new evidence
continues that subject's story, the action MUST be update with that page's
slug. Creating a page whose subject overlaps an existing page is an error.
Group related evidence aggressively into one page; most sessions should
yield zero to two briefs. Judge subject overlap with each page's summary, not its title alone. Every exact_quote must be copied verbatim from
the event content and must genuinely support the page. Account for every
event with either a page brief or skip_noise. Return at most 8 briefs and
JSON only.

related_slugs names pages a reader of this page should also open: slugs
from pages or from another brief's proposed_slug in the same response.
Link only durable, direct relationships — prerequisite, sequel, same
subsystem — never same-session coincidence, and never the page's own slug.
Omit the field when nothing qualifies.`

var _ Planner = (*LLMSessionPlanner)(nil)
