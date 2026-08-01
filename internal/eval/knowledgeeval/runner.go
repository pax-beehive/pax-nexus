package knowledgeeval

import (
	"context"
	"fmt"
	"time"
)

type Runner struct {
	store RunStore
	now   func() time.Time
}

func NewRunner(store RunStore, now func() time.Time) (*Runner, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: run store is required", ErrInvalidRecord)
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Runner{store: store, now: now}, nil
}

func (r *Runner) Evaluate(
	ctx context.Context,
	run Run,
	subject Subject,
	benchmarks []BenchmarkAdapter,
) (RunDetail, error) {
	if err := r.store.CreateRun(ctx, run); err != nil {
		return RunDetail{}, fmt.Errorf("create eval run: %w", err)
	}
	plans := PlanTrials(run.ID, subject, benchmarks)
	for index, plan := range plans {
		benchmark := benchmarks[index]
		trial := Trial{
			ID:                   plan.TrialID,
			RunID:                run.ID,
			CaseID:               run.GroupID,
			BenchmarkID:          plan.Benchmark.ID,
			BenchmarkFingerprint: plan.Benchmark.Fingerprint(),
		}
		if err := r.store.AddTrial(ctx, trial); err != nil {
			return RunDetail{}, fmt.Errorf("add eval trial %s: %w", trial.ID, err)
		}
		if !plan.Eligible {
			if err := r.store.MarkIneligible(ctx, trial.ID, plan.IneligibleReason); err != nil {
				return RunDetail{}, fmt.Errorf("mark eval trial %s ineligible: %w", trial.ID, err)
			}
			continue
		}
		attempt, err := r.store.StartAttempt(ctx, trial.ID)
		if err != nil {
			return RunDetail{}, fmt.Errorf("start eval attempt %s: %w", trial.ID, err)
		}
		if err := r.store.AdvanceAttempt(
			ctx,
			attempt.ID,
			StageEvaluating,
			"benchmark evaluation started",
		); err != nil {
			return RunDetail{}, fmt.Errorf("advance eval attempt %s: %w", attempt.ID, err)
		}
		result, runErr := benchmark.Run(ctx, subject)
		if runErr != nil {
			if err := r.store.FailAttempt(ctx, attempt.ID, StageEvaluating, runErr); err != nil {
				return RunDetail{}, fmt.Errorf("fail eval attempt %s: %w", attempt.ID, err)
			}
			continue
		}
		if err := r.store.CompleteAttempt(ctx, attempt.ID, result); err != nil {
			return RunDetail{}, fmt.Errorf("complete eval attempt %s: %w", attempt.ID, err)
		}
	}
	if err := r.store.FinishRun(ctx, run.ID); err != nil {
		return RunDetail{}, fmt.Errorf("finish eval run: %w", err)
	}
	detail, err := r.store.GetRun(ctx, run.ID)
	if err != nil {
		return RunDetail{}, fmt.Errorf("load completed eval run: %w", err)
	}
	return detail, nil
}
