package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractionbudget"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
)

type Config struct {
	SliceEventLimit    int
	SliceTokenLimit    int
	SliceOverlap       int
	MaxSlicesPerJob    int
	TTLPolicy          teamnote.TTLPolicy
	Clock              teamnote.Clock
	NoteStore          teamnote.NoteStore
	Logger             *slog.Logger
	ExtractionObserver func(context.Context, ExtractionObservation)
}

type ExtractionStatus string

const (
	ExtractionCompleted   ExtractionStatus = "completed"
	ExtractionQuarantined ExtractionStatus = "quarantined"
	ExtractionFailed      ExtractionStatus = "failed"
	ExtractionTimedOut    ExtractionStatus = "timed_out"
	ExtractionCancelled   ExtractionStatus = "cancelled"
)

type ExtractionObservation struct {
	Actor        teamnote.Actor
	RunID        string
	StartedAt    time.Time
	CompletedAt  time.Time
	Status       ExtractionStatus
	InputEvents  int64
	Candidates   int64
	InputTokens  int64
	OutputTokens int64
}

type App struct {
	lake      *sessionlake.Lake
	extractor extractor.Extractor
	config    Config
	logger    *slog.Logger
}

func New(lake *sessionlake.Lake, candidateExtractor extractor.Extractor, config Config) (*App, error) {
	if lake == nil || candidateExtractor == nil {
		return nil, fmt.Errorf("create runtime: lake and extractor are required")
	}
	if config.SliceEventLimit <= 0 {
		config.SliceEventLimit = 25
	}
	if config.SliceTokenLimit <= 0 {
		config.SliceTokenLimit = 8192
	}
	if config.SliceOverlap < 0 {
		return nil, fmt.Errorf("create runtime: slice overlap cannot be negative")
	}
	if config.SliceOverlap == 0 {
		config.SliceOverlap = 3
	}
	if config.MaxSlicesPerJob <= 0 {
		config.MaxSlicesPerJob = extractionbudget.DefaultMaxSlicesPerJob
	}
	if config.TTLPolicy == nil {
		config.TTLPolicy = teamnote.DefaultTTLPolicy()
	}
	if config.Clock == nil {
		config.Clock = teamnote.SystemClock{}
	}
	if config.NoteStore == nil {
		config.NoteStore = teamnote.NewScopedLedgerStore(config.TTLPolicy, config.Clock)
	}
	if config.Logger == nil {
		config.Logger = observability.DiscardLogger()
	}
	return &App{lake: lake, extractor: candidateExtractor, config: config, logger: config.Logger}, nil
}

func (a *App) ObserveSession(ctx context.Context, batch teamnote.SessionBatch) (teamnote.IngestReceipt, error) {
	receipt, err := a.lake.Observe(ctx, batch)
	if err != nil {
		return teamnote.IngestReceipt{}, err
	}
	return receipt, nil
}

func (a *App) ProcessExtraction(ctx context.Context, actor teamnote.Actor, throughCursor int64, requireCurrent bool) (bool, error) {
	if requireCurrent {
		current, err := a.lake.IsCurrent(ctx, actor, throughCursor)
		if err != nil {
			return false, err
		}
		if !current {
			return false, nil
		}
	}
	policy := sessionlake.SlicePolicy{
		EventLimit: a.config.SliceEventLimit, TokenLimit: a.config.SliceTokenLimit,
		Overlap: a.config.SliceOverlap, ThroughSequence: throughCursor,
	}
	for range a.config.MaxSlicesPerJob {
		slice, sliceErr := a.lake.NextSlice(ctx, actor, policy)
		if sliceErr != nil {
			return false, fmt.Errorf("plan extraction: %w", sliceErr)
		}
		if len(slice.Events) == 0 {
			return false, nil
		}
		startedAt := time.Now()
		result, extractErr := a.extractor.Extract(ctx, slice)
		if extractErr != nil {
			a.observeExtraction(ctx, extractionObservation(
				actor, slice, result, startedAt, extractionFailureStatus(extractErr), "",
			))
			return false, fmt.Errorf("extract candidates: %w", extractErr)
		}
		result = a.filterAdmissible(result)
		runID := extractionRunID(slice.InputChecksum, result.ExtractionVersion)
		quarantined := false
		if applyErr := a.applyExtractionRun(ctx, slice, result); applyErr != nil {
			if !errors.Is(applyErr, teamnote.ErrExtractionRunQuarantined) {
				a.observeExtraction(ctx, extractionObservation(
					actor, slice, result, startedAt, extractionFailureStatus(applyErr), runID,
				))
				return false, applyErr
			}
			quarantined = true
			a.logger.WarnContext(ctx, "extraction run quarantined",
				"user_id", actor.UserID, "agent_id", actor.AgentID, "session_id", actor.SessionID,
				"from_sequence", slice.FromSequence, "to_sequence", slice.ToSequence,
				"error", applyErr,
			)
		}
		if commitErr := a.lake.CommitSlice(ctx, slice); commitErr != nil {
			a.observeExtraction(ctx, extractionObservation(
				actor, slice, result, startedAt, extractionFailureStatus(commitErr), runID,
			))
			return false, commitErr
		}
		attrs := []any{
			"user_id", actor.UserID, "agent_id", actor.AgentID, "session_id", actor.SessionID,
			"from_sequence", slice.FromSequence, "to_sequence", slice.ToSequence,
			"events", len(slice.Events), "candidates", len(result.Candidates),
			"rejections", len(result.Rejections), "quarantined", quarantined,
			"input_tokens", result.Usage.InputTokens, "output_tokens", result.Usage.OutputTokens,
			"prompt_cache_hit_tokens", result.Usage.PromptCacheHitTokens,
			"prompt_cache_miss_tokens", result.Usage.PromptCacheMissTokens,
			"duration_ms", time.Since(startedAt).Milliseconds(),
		}
		if result.Trace != nil {
			attrs = append(attrs,
				"claims", len(result.Trace.Claims),
				"state_decisions", len(result.Trace.StateDecisions),
				"claim_rejections", len(result.Trace.ClaimRejections),
				"decision_rejections", len(result.Trace.DecisionRejections),
			)
		}
		a.logger.InfoContext(ctx, "extraction slice completed", attrs...)
		status := ExtractionCompleted
		if quarantined {
			status = ExtractionQuarantined
		}
		a.observeExtraction(ctx, extractionObservation(actor, slice, result, startedAt, status, runID))
	}
	next, err := a.lake.NextSlice(ctx, actor, policy)
	if err != nil {
		return false, fmt.Errorf("check extraction backlog: %w", err)
	}
	return len(next.NewEventIDs) > 0, nil
}

func extractionObservation(
	actor teamnote.Actor,
	slice sessionlake.Slice,
	result extractor.Result,
	startedAt time.Time,
	status ExtractionStatus,
	runID string,
) ExtractionObservation {
	return ExtractionObservation{
		Actor: actor, RunID: runID, StartedAt: startedAt.UTC(), CompletedAt: time.Now().UTC(),
		Status: status, InputEvents: int64(len(slice.Events)), Candidates: int64(len(result.Candidates)),
		InputTokens: int64(result.Usage.InputTokens), OutputTokens: int64(result.Usage.OutputTokens),
	}
}

func extractionFailureStatus(err error) ExtractionStatus {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ExtractionTimedOut
	case errors.Is(err, context.Canceled):
		return ExtractionCancelled
	default:
		return ExtractionFailed
	}
}

func (a *App) observeExtraction(ctx context.Context, observation ExtractionObservation) {
	if a.config.ExtractionObserver != nil {
		a.config.ExtractionObserver(ctx, observation)
	}
}

func (a *App) RecallNotes(ctx context.Context, request teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
	scopeID, err := teamnote.ScopeFromContext(ctx)
	if err != nil {
		return teamnote.NoteEnvelope{}, fmt.Errorf("recall notes: %w", err)
	}
	return a.config.NoteStore.RecallNotes(ctx, scopeID, request)
}

func (a *App) applyExtractionRun(ctx context.Context, slice sessionlake.Slice, result extractor.Result) error {
	scopeID, err := teamnote.ScopeFromContext(ctx)
	if err != nil {
		return fmt.Errorf("apply extraction run: %w", err)
	}
	run := teamnote.ExtractionRun{
		ID: extractionRunID(slice.InputChecksum, result.ExtractionVersion), Actor: slice.Actor,
		FromSequence: slice.FromSequence, ToSequence: slice.ToSequence,
		InputChecksum: slice.InputChecksum, Model: result.Model, PromptVersion: result.PromptVersion,
		InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens,
		Candidates: result.Candidates, TransitionAuthorities: result.TransitionAuthorities,
		Evidence: slice.Events, Rejections: result.Rejections,
	}
	if _, err := a.config.NoteStore.ApplyExtractionRun(ctx, scopeID, run); err != nil {
		return fmt.Errorf("apply extraction run %q: %w", run.ID, err)
	}
	return nil
}

func extractionRunID(inputChecksum string, extractionVersion string) string {
	if extractionVersion == "" || extractionVersion == extractor.ExtractionVersionV1 {
		return inputChecksum
	}
	return inputChecksum + ":" + extractionVersion
}

// filterAdmissible drops candidates that can never pass deterministic
// admission, so one malformed candidate cannot poison its siblings or wedge
// the stream. Rejections are attached to the run for provenance.
func (a *App) filterAdmissible(result extractor.Result) extractor.Result {
	if len(result.Candidates) == 0 {
		return result
	}
	kept := make([]teamnote.Candidate, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		if err := teamnote.ValidateCandidate(candidate, a.config.TTLPolicy); err != nil {
			result.Rejections = append(result.Rejections, teamnote.CandidateRejection{Candidate: candidate, Reason: err.Error()})
			continue
		}
		kept = append(kept, candidate)
	}
	result.Candidates = kept
	return result
}
