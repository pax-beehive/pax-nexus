package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
)

type Config struct {
	SliceEventLimit int
	SliceTokenLimit int
	SliceOverlap    int
	MaxSlicesPerJob int
	TTLPolicy       teamnote.TTLPolicy
	Clock           teamnote.Clock
	NoteStore       teamnote.NoteStore
	Logger          *slog.Logger
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
		config.MaxSlicesPerJob = 4
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
			return false, fmt.Errorf("extract candidates: %w", extractErr)
		}
		if applyErr := a.applyCandidates(ctx, slice.InputChecksum, result.Candidates, slice.Events); applyErr != nil {
			return false, applyErr
		}
		if commitErr := a.lake.CommitSlice(ctx, slice); commitErr != nil {
			return false, commitErr
		}
		a.logger.InfoContext(ctx, "extraction slice completed",
			"user_id", actor.UserID, "agent_id", actor.AgentID, "session_id", actor.SessionID,
			"from_sequence", slice.FromSequence, "to_sequence", slice.ToSequence,
			"events", len(slice.Events), "candidates", len(result.Candidates),
			"input_tokens", result.Usage.InputTokens, "output_tokens", result.Usage.OutputTokens,
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
	}
	next, err := a.lake.NextSlice(ctx, actor, policy)
	if err != nil {
		return false, fmt.Errorf("check extraction backlog: %w", err)
	}
	return len(next.NewEventIDs) > 0, nil
}

func (a *App) RecallNotes(ctx context.Context, request teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
	scopeID, err := teamnote.ScopeFromContext(ctx)
	if err != nil {
		return teamnote.NoteEnvelope{}, fmt.Errorf("recall notes: %w", err)
	}
	return a.config.NoteStore.RecallNotes(ctx, scopeID, request)
}

func (a *App) applyCandidates(ctx context.Context, runID string, candidates []teamnote.Candidate, evidence []teamnote.SessionEvent) error {
	scopeID, err := teamnote.ScopeFromContext(ctx)
	if err != nil {
		return fmt.Errorf("apply candidates: %w", err)
	}
	for _, candidate := range candidates {
		if _, err := a.config.NoteStore.ApplyCandidate(ctx, scopeID, runID, candidate, evidence); err != nil {
			return fmt.Errorf("apply candidate %q: %w", candidate.ID, err)
		}
	}
	return nil
}
