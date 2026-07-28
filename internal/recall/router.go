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
}

func NewRouter(teamNote TeamNotePath, wiki WikiPath, config Config) (*Router, error) {
	if teamNote == nil {
		return nil, fmt.Errorf("create recall router: team note path is required")
	}
	if config.EnablePassiveWikiHint && wiki == nil {
		return nil, fmt.Errorf("create recall router: enabled wiki hint path is required")
	}
	return &Router{teamNote: teamNote, wiki: wiki}, nil
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
	if request.Intent == IntentActive && request.Source != SourcePageWiki {
		return fmt.Errorf("search memory: active search requires pagewiki source")
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

type wikiOutcome struct {
	hits     []MemoryHit
	err      error
	duration time.Duration
}

func (r *Router) searchPassive(ctx context.Context, request SearchRequest) (SearchResult, error) {
	if r.wiki == nil {
		return r.searchTeamNoteOnly(ctx, request)
	}
	searchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	teamResults := make(chan teamOutcome, 1)
	wikiResults := make(chan wikiOutcome, 1)
	go r.runTeamNote(searchCtx, request, teamResults)
	go r.runPageWiki(searchCtx, request, wikiResults)

	var wiki *wikiOutcome
	for {
		select {
		case current := <-wikiResults:
			wiki = &current
			wikiResults = nil
		case team := <-teamResults:
			return r.finishPassive(ctx, cancel, request, team, wiki, wikiResults)
		case <-ctx.Done():
			cancel()
			result := SearchResult{Trace: Trace{
				TeamNote: timedOut(ctx.Err()), WikiHint: timedOut(ctx.Err()), WikiSearch: skipped("passive_search"),
			}}
			if wiki == nil && wikiResults != nil {
				select {
				case current := <-wikiResults:
					wiki = &current
				default:
				}
			}
			if wiki != nil {
				result = composePageWiki(result, *wiki, request)
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
	wiki *wikiOutcome,
	wikiResults <-chan wikiOutcome,
) (SearchResult, error) {
	result := teamResult(team)
	result.Trace.WikiHint = skipped("pagewiki_replaced")
	if team.err != nil {
		cancel()
		result.Trace.WikiSearch = cancelled("team_note_failed")
		return result, fmt.Errorf("search team note memory: %w", team.err)
	}
	if team.envelope.Decision.EvidenceSufficient {
		cancel()
		result.Trace.EarlyReturn = true
		if wiki == nil {
			result.Trace.WikiSearch = cancelled("sufficient_team_note_evidence")
		} else {
			result.Trace.WikiSearch = completedPageWikiTrace(
				*wiki,
				"discarded_sufficient_team_note_evidence",
			)
		}
		return result, nil
	}
	if wiki == nil {
		select {
		case current := <-wikiResults:
			wiki = &current
		case <-ctx.Done():
			cancel()
			result.Trace.WikiSearch = timedOut(ctx.Err())
			return result, nil
		}
	}
	return composePageWiki(result, *wiki, request), nil
}

func completedPageWikiTrace(outcome wikiOutcome, reason string) PathTrace {
	trace := PathTrace{DurationMS: outcome.duration.Milliseconds(), Reason: reason}
	if outcome.err != nil {
		trace.Status, trace.Error = pathFailure(context.Background(), outcome.err)
		return trace
	}
	trace.Status = PathCompleted
	trace.Candidates = len(outcome.hits)
	return trace
}

func (r *Router) searchTeamNoteOnly(ctx context.Context, request SearchRequest) (SearchResult, error) {
	startedAt := time.Now()
	envelope, err := r.teamNote.RecallNotes(ctx, teamNoteRequest(request))
	result := teamResult(teamOutcome{envelope: envelope, err: err, duration: time.Since(startedAt)})
	result.Trace.WikiHint = skipped("pagewiki_replaced")
	result.Trace.WikiSearch = skipped("unavailable")
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

func (r *Router) runPageWiki(ctx context.Context, request SearchRequest, results chan<- wikiOutcome) {
	startedAt := time.Now()
	hits, err := r.wiki.Search(ctx, request)
	results <- wikiOutcome{hits: hits, err: err, duration: time.Since(startedAt)}
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
		Trace: Trace{TeamNote: trace}, ObservationID: outcome.envelope.ObservationID,
	}
}

func recallReasonCodes(values []teamnote.RecallReasonCode) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func composePageWiki(
	result SearchResult,
	outcome wikiOutcome,
	request SearchRequest,
) SearchResult {
	trace := PathTrace{DurationMS: outcome.duration.Milliseconds()}
	if outcome.err != nil {
		trace.Status, trace.Error = pathFailure(context.Background(), outcome.err)
		result.Trace.WikiSearch = trace
		return result
	}
	trace.Status = PathCompleted
	trace.Candidates = len(outcome.hits)
	if len(outcome.hits) == 0 {
		trace.Reason = "empty_search"
		result.Trace.WikiSearch = trace
		return result
	}
	used := 0
	for _, hit := range result.Hits {
		used += hit.Tokens
	}
	for _, hit := range outcome.hits {
		if request.MaxItems > 0 && len(result.Hits) >= request.MaxItems {
			trace.BudgetDrops++
			continue
		}
		if hit.Tokens <= 0 {
			hit.Tokens = estimateTokens(hit.Text)
		}
		if used+hit.Tokens > request.TokenBudget {
			trace.BudgetDrops++
			continue
		}
		hit.Disposition = DispositionReference
		result.Hits = append(result.Hits, hit)
		used += hit.Tokens
	}
	if trace.BudgetDrops > 0 {
		trace.Reason = "shared_budget"
	}
	result.Trace.WikiSearch = trace
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
