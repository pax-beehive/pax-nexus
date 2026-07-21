package recall

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

type Config struct {
	EnablePassiveWikiHint bool
}

type Router struct {
	teamNote TeamNotePath
	wiki     WikiPath
	config   Config
}

func NewRouter(teamNote TeamNotePath, wiki WikiPath, config Config) (*Router, error) {
	if teamNote == nil {
		return nil, fmt.Errorf("create recall router: team note path is required")
	}
	if config.EnablePassiveWikiHint && wiki == nil {
		return nil, fmt.Errorf("create recall router: enabled wiki hint path is required")
	}
	return &Router{teamNote: teamNote, wiki: wiki, config: config}, nil
}

func (r *Router) Search(ctx context.Context, request SearchRequest) (SearchResult, error) {
	if err := validateSearch(request); err != nil {
		return SearchResult{}, err
	}
	if request.Intent == IntentActive {
		return r.searchWiki(ctx, request)
	}
	return r.searchPassive(ctx, request)
}

func (r *Router) Get(ctx context.Context, request GetRequest) (MemoryDocument, error) {
	if strings.TrimSpace(request.Ref) == "" {
		return MemoryDocument{}, fmt.Errorf("get memory document: ref is required")
	}
	if r.wiki == nil {
		return MemoryDocument{}, fmt.Errorf("get memory document: wiki path is unavailable")
	}
	document, err := r.wiki.Get(ctx, request)
	if err != nil {
		return MemoryDocument{}, fmt.Errorf("get wiki document: %w", err)
	}
	return document, nil
}

func validateSearch(request SearchRequest) error {
	if request.Intent != IntentPassive && request.Intent != IntentActive {
		return fmt.Errorf("search memory: unsupported intent %q", request.Intent)
	}
	if strings.TrimSpace(request.Query) == "" || request.TokenBudget <= 0 || request.MaxItems < 0 {
		return fmt.Errorf("search memory: query and positive token budget are required")
	}
	if request.Intent == IntentActive && request.Source != SourceLLMWiki {
		return fmt.Errorf("search memory: active search requires llm_wiki source")
	}
	return nil
}

func (r *Router) searchWiki(ctx context.Context, request SearchRequest) (SearchResult, error) {
	if r.wiki == nil {
		return SearchResult{}, fmt.Errorf("search memory: wiki path is unavailable")
	}
	startedAt := time.Now()
	hits, err := r.wiki.Search(ctx, request)
	trace := PathTrace{DurationMS: time.Since(startedAt).Milliseconds(), Candidates: len(hits)}
	if err != nil {
		trace.Status, trace.Error = pathFailure(ctx, err)
		return SearchResult{Trace: Trace{TeamNote: skipped("active_wiki_search"), WikiHint: skipped("active_wiki_search"), WikiSearch: trace}},
			fmt.Errorf("search wiki memory: %w", err)
	}
	trace.Status = PathCompleted
	for index := range hits {
		hits[index].Disposition = DispositionReference
	}
	return SearchResult{Hits: hits, Trace: Trace{
		TeamNote: skipped("active_wiki_search"), WikiHint: skipped("active_wiki_search"), WikiSearch: trace,
	}}, nil
}

type teamOutcome struct {
	envelope teamnote.NoteEnvelope
	err      error
	duration time.Duration
}

type hintOutcome struct {
	hit      MemoryHit
	err      error
	duration time.Duration
}

func (r *Router) searchPassive(ctx context.Context, request SearchRequest) (SearchResult, error) {
	if !r.config.EnablePassiveWikiHint {
		return r.searchTeamNoteOnly(ctx, request)
	}
	searchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	teamResults := make(chan teamOutcome, 1)
	hintResults := make(chan hintOutcome, 1)
	go r.runTeamNote(searchCtx, request, teamResults)
	go r.runWikiHint(searchCtx, request, hintResults)

	var hint *hintOutcome
	for {
		select {
		case current := <-hintResults:
			hint = &current
			hintResults = nil
		case team := <-teamResults:
			return r.finishPassive(ctx, cancel, request, team, hint, hintResults)
		case <-ctx.Done():
			cancel()
			result := SearchResult{Trace: Trace{
				TeamNote: timedOut(ctx.Err()), WikiHint: timedOut(ctx.Err()), WikiSearch: skipped("passive_search"),
			}}
			if hint == nil && hintResults != nil {
				select {
				case current := <-hintResults:
					hint = &current
				default:
				}
			}
			if hint != nil {
				result = composeHint(result, *hint, request.TokenBudget)
				if len(result.Hits) > 0 {
					return result, nil
				}
			}
			return result, fmt.Errorf("search passive memory: %w", ctx.Err())
		}
	}
}

func (r *Router) finishPassive(
	ctx context.Context,
	cancel context.CancelFunc,
	request SearchRequest,
	team teamOutcome,
	hint *hintOutcome,
	hintResults <-chan hintOutcome,
) (SearchResult, error) {
	result := teamResult(team)
	result.Trace.WikiSearch = skipped("passive_search")
	if team.err != nil {
		cancel()
		result.Trace.WikiHint = cancelled("team_note_failed")
		return result, fmt.Errorf("search team note memory: %w", team.err)
	}
	if team.envelope.Decision.EvidenceSufficient {
		cancel()
		result.Trace.EarlyReturn = true
		if hint == nil {
			result.Trace.WikiHint = cancelled("sufficient_team_note_evidence")
		} else {
			result.Trace.WikiHint = completedHintTrace(*hint, "discarded_sufficient_team_note_evidence")
		}
		return result, nil
	}
	if hint == nil {
		select {
		case current := <-hintResults:
			hint = &current
		case <-ctx.Done():
			cancel()
			result.Trace.WikiHint = timedOut(ctx.Err())
			return result, nil
		}
	}
	return composeHint(result, *hint, request.TokenBudget), nil
}

func completedHintTrace(outcome hintOutcome, reason string) PathTrace {
	trace := PathTrace{DurationMS: outcome.duration.Milliseconds(), Reason: reason}
	if outcome.err != nil {
		trace.Status, trace.Error = pathFailure(context.Background(), outcome.err)
		return trace
	}
	trace.Status = PathCompleted
	if strings.TrimSpace(outcome.hit.Text) != "" {
		trace.Candidates = 1
	}
	return trace
}

func (r *Router) searchTeamNoteOnly(ctx context.Context, request SearchRequest) (SearchResult, error) {
	startedAt := time.Now()
	envelope, err := r.teamNote.RecallNotes(ctx, teamNoteRequest(request))
	result := teamResult(teamOutcome{envelope: envelope, err: err, duration: time.Since(startedAt)})
	result.Trace.WikiHint = skipped("disabled")
	result.Trace.WikiSearch = skipped("passive_search")
	if err != nil {
		return result, fmt.Errorf("search team note memory: %w", err)
	}
	return result, nil
}

func (r *Router) runTeamNote(ctx context.Context, request SearchRequest, results chan<- teamOutcome) {
	startedAt := time.Now()
	envelope, err := r.teamNote.RecallNotes(ctx, teamNoteRequest(request))
	results <- teamOutcome{envelope: envelope, err: err, duration: time.Since(startedAt)}
}

func (r *Router) runWikiHint(ctx context.Context, request SearchRequest, results chan<- hintOutcome) {
	startedAt := time.Now()
	hit, err := r.wiki.Hint(ctx, request)
	results <- hintOutcome{hit: hit, err: err, duration: time.Since(startedAt)}
}

func teamNoteRequest(request SearchRequest) teamnote.RecallRequest {
	return teamnote.RecallRequest{
		Actor: request.Actor, TaskRef: request.TaskRef, ThreadRef: request.ThreadRef,
		TokenBudget: request.TokenBudget, Query: request.Query, MaxItems: request.MaxItems,
	}
}

func teamResult(outcome teamOutcome) SearchResult {
	trace := PathTrace{
		DurationMS: outcome.duration.Milliseconds(), Candidates: len(outcome.envelope.Details),
		ReasonCodes: recallReasonCodes(outcome.envelope.Decision.ReasonCodes),
	}
	if outcome.err != nil {
		trace.Status, trace.Error = pathFailure(context.Background(), outcome.err)
	} else {
		trace.Status = PathCompleted
	}
	hits := make([]MemoryHit, 0, max(len(outcome.envelope.Details), len(outcome.envelope.Items)))
	for index, detail := range outcome.envelope.Details {
		tokens := 0
		if index == 0 {
			tokens = outcome.envelope.Tokens
		}
		hits = append(hits, MemoryHit{
			Ref: detail.NoteID, Text: detail.Text, Score: detail.Relevance, Tokens: tokens,
			Disposition: DispositionEvidence, Metadata: map[string]string{"revision": fmt.Sprint(detail.Revision)},
		})
	}
	if len(hits) == 0 {
		for index, item := range outcome.envelope.Items {
			tokens := 0
			if index == 0 {
				tokens = outcome.envelope.Tokens
			}
			hits = append(hits, MemoryHit{Text: item, Tokens: tokens, Disposition: DispositionEvidence})
		}
	}
	return SearchResult{
		Hits: hits, EvidenceSufficient: outcome.envelope.Decision.EvidenceSufficient,
		Trace: Trace{TeamNote: trace},
	}
}

func recallReasonCodes(values []teamnote.RecallReasonCode) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func composeHint(result SearchResult, outcome hintOutcome, tokenBudget int) SearchResult {
	trace := PathTrace{DurationMS: outcome.duration.Milliseconds()}
	if outcome.err != nil {
		trace.Status, trace.Error = pathFailure(context.Background(), outcome.err)
		result.Trace.WikiHint = trace
		return result
	}
	trace.Status = PathCompleted
	if strings.TrimSpace(outcome.hit.Text) == "" {
		trace.Reason = "empty_hint"
		result.Trace.WikiHint = trace
		return result
	}
	outcome.hit.Disposition = DispositionHint
	if outcome.hit.Tokens <= 0 {
		outcome.hit.Tokens = estimateTokens(outcome.hit.Text)
	}
	used := 0
	for _, hit := range result.Hits {
		used += hit.Tokens
	}
	if used+outcome.hit.Tokens > tokenBudget {
		trace.BudgetDrops = 1
		trace.Reason = "shared_token_budget"
		result.Trace.WikiHint = trace
		return result
	}
	trace.Candidates = 1
	result.Hits = append(result.Hits, outcome.hit)
	result.Trace.WikiHint = trace
	return result
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len([]rune(text)) + 3) / 4
}

func pathFailure(ctx context.Context, err error) (PathStatus, string) {
	switch {
	case errors.Is(err, context.Canceled):
		return PathCancelled, err.Error()
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return PathTimedOut, err.Error()
	default:
		return PathFailed, err.Error()
	}
}

func skipped(reason string) PathTrace {
	return PathTrace{Status: PathSkipped, Reason: reason}
}

func cancelled(reason string) PathTrace {
	return PathTrace{Status: PathCancelled, Reason: reason}
}

func timedOut(err error) PathTrace {
	return PathTrace{Status: PathTimedOut, Error: err.Error()}
}
