package v2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/harness"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

type Runner struct {
	store    Store
	executor Executor
	logger   *slog.Logger
	now      func() time.Time
}

func NewRunner(store Store, executor Executor, logger *slog.Logger) (*Runner, error) {
	if store == nil || executor == nil {
		return nil, fmt.Errorf("create eval runner: store and executor are required")
	}
	if logger == nil {
		logger = observability.DiscardLogger()
	}
	return &Runner{store: store, executor: executor, logger: logger, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (r *Runner) Run(ctx context.Context, config Config, cases []Case, revision string) (run RunRecord, results []TrialResult, returnedErr error) {
	if err := config.Validate(); err != nil {
		return RunRecord{}, nil, err
	}
	if len(cases) == 0 {
		return RunRecord{}, nil, fmt.Errorf("run eval: cases are required")
	}
	runtimeValues, err := config.ResolveRuntime(os.Getenv)
	if err != nil {
		return RunRecord{}, nil, err
	}
	hash, err := config.HashWithRuntime(runtimeValues)
	if err != nil {
		return RunRecord{}, nil, err
	}
	run = RunRecord{ID: config.Run.ID, Dataset: config.Run.Dataset, DatasetRevision: revision, ConfigHash: hash, Config: config, Runtime: runtimeValues}
	acquired, err := r.store.Acquire(ctx, run.ID)
	if err != nil {
		return RunRecord{}, nil, fmt.Errorf("acquire eval run: %w", err)
	}
	if !acquired {
		return RunRecord{}, nil, fmt.Errorf("acquire eval run: run %q is already active", run.ID)
	}
	defer func() {
		if err := r.store.Release(context.Background(), run.ID); err != nil {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("release eval run: %w", err))
		}
	}()
	trials := trialKeys(run.ID, cases, config.Arms)
	if err := r.store.Initialize(ctx, run, trials); err != nil {
		return RunRecord{}, nil, fmt.Errorf("initialize eval run: %w", err)
	}
	if err := r.store.ResetRunning(ctx, run.ID); err != nil {
		return RunRecord{}, nil, fmt.Errorf("reset interrupted eval trials: %w", err)
	}
	if err := os.MkdirAll(config.Run.OutputDir, 0o755); err != nil {
		return RunRecord{}, nil, fmt.Errorf("create eval output directory: %w", err)
	}
	lifecycleVariables := map[string]string{
		"run_id": run.ID, "dataset": run.Dataset, "dataset_revision": run.DatasetRevision,
		"manifest": config.Run.Manifest, "output_dir": config.Run.OutputDir,
	}
	if config.BeforeRun != nil {
		if _, err := r.executor.Execute(ctx, *config.BeforeRun, lifecycleVariables, filepath.Join(config.Run.OutputDir, "before-run.log"), filepath.Join(config.Run.OutputDir, "before-run.stderr.log")); err != nil {
			return RunRecord{}, nil, fmt.Errorf("prepare eval run: %w", err)
		}
	}
	if config.AfterRun != nil {
		defer func() {
			teardownCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if _, err := r.executor.Execute(teardownCtx, *config.AfterRun, lifecycleVariables, filepath.Join(config.Run.OutputDir, "after-run.log"), filepath.Join(config.Run.OutputDir, "after-run.stderr.log")); err != nil {
				r.logger.Error("tear down eval run", "error", err)
				returnedErr = errors.Join(returnedErr, fmt.Errorf("tear down eval run: %w", err))
			}
		}()
	}
	timeout, err := config.Timeout()
	if err != nil {
		return RunRecord{}, nil, err
	}
	if err := r.runTrials(ctx, run, cases, config, timeout); err != nil {
		return RunRecord{}, nil, err
	}
	results, err = r.store.Results(ctx, run.ID)
	if err != nil {
		return RunRecord{}, nil, fmt.Errorf("load eval results: %w", err)
	}
	results = HydrateResults(results, cases)
	if err := r.store.Finish(ctx, run.ID); err != nil {
		return RunRecord{}, nil, fmt.Errorf("finish eval run: %w", err)
	}
	r.logger.InfoContext(ctx, "eval v2 completed", "run_id", run.ID, "trials", len(results), "output_dir", config.Run.OutputDir)
	return run, results, nil
}

func HydrateResults(results []TrialResult, cases []Case) []TrialResult {
	byID := make(map[string]Case, len(cases))
	for _, evalCase := range cases {
		byID[evalCase.ID] = evalCase
	}
	hydrated := append([]TrialResult(nil), results...)
	for index := range hydrated {
		evalCase, ok := byID[hydrated[index].CaseID]
		if !ok {
			continue
		}
		if hydrated[index].Question == "" {
			hydrated[index].Question = evalCase.Question
		}
		if hydrated[index].Expected == "" {
			hydrated[index].Expected = evalCase.Expected
		}
		if hydrated[index].Category == "" {
			hydrated[index].Category = evalCase.Category
		}
		if hydrated[index].AskingUserID == "" {
			hydrated[index].AskingUserID = evalCase.AskingUserID
		}
	}
	return hydrated
}

func (r *Runner) runTrials(ctx context.Context, run RunRecord, cases []Case, config Config, timeout time.Duration) error {
	type job struct {
		evalCase Case
		arm      ArmConfig
	}
	jobs := make(chan job)
	errCh := make(chan error, 1)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	for range config.Run.Parallelism {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for current := range jobs {
				if err := r.runTrial(workerCtx, run, current.evalCase, current.arm, config.RetryFailed, timeout, config.Run.OutputDir); err != nil {
					select {
					case errCh <- err:
						cancel()
					default:
					}
					return
				}
			}
		}()
	}
sendLoop:
	for _, evalCase := range cases {
		for _, arm := range config.Arms {
			select {
			case jobs <- job{evalCase: evalCase, arm: arm}:
			case <-workerCtx.Done():
				break sendLoop
			}
		}
	}
	close(jobs)
	workers.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return ctx.Err()
	}
}

func (r *Runner) runTrial(ctx context.Context, run RunRecord, evalCase Case, arm ArmConfig, retryFailed bool, timeout time.Duration, outputDir string) error {
	key := TrialKey{RunID: run.ID, CaseID: evalCase.ID, Arm: arm.Name}
	claimed, err := r.store.Claim(ctx, key, retryFailed)
	if err != nil {
		return fmt.Errorf("claim eval trial %s/%s: %w", evalCase.ID, arm.Name, err)
	}
	if !claimed {
		return nil
	}
	started := r.now()
	trialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, runErr := r.executeTrial(trialCtx, run, evalCase, arm, outputDir, started)
	if runErr == nil {
		if err := r.store.Complete(ctx, result); err != nil {
			return fmt.Errorf("complete eval trial %s/%s: %w", evalCase.ID, arm.Name, err)
		}
		r.logger.InfoContext(ctx, "eval trial completed", "case_id", evalCase.ID, "arm", arm.Name, "token_f1", result.TokenF1)
		return nil
	}
	failed := failureResult(result, run, evalCase, arm.Name, started, runErr)
	if err := r.store.Fail(ctx, failed); err != nil {
		return errors.Join(fmt.Errorf("run eval trial %s/%s: %w", evalCase.ID, arm.Name, runErr), fmt.Errorf("persist eval failure: %w", err))
	}
	r.logger.ErrorContext(ctx, "eval trial failed", "case_id", evalCase.ID, "arm", arm.Name, "error", runErr)
	return nil
}

func (r *Runner) executeTrial(ctx context.Context, run RunRecord, evalCase Case, arm ArmConfig, outputDir string, started time.Time) (TrialResult, error) {
	variables := trialVariables(run, evalCase, arm.Name, outputDir)
	artifactDir := variables["artifact_dir"]
	durations := [3]time.Duration{}
	partial := TrialResult{
		RunID: run.ID, Dataset: run.Dataset, DatasetRevision: run.DatasetRevision,
		CaseID: evalCase.ID, Category: evalCase.Category, Question: evalCase.Question, Arm: arm.Name, AskingUserID: evalCase.AskingUserID,
		Expected: evalCase.Expected, Status: "running", CostScope: "opencode_reported", StartedAt: started,
	}
	var producerOutput harness.AgentOutput
	if arm.Producer != nil {
		result, executeErr := r.executor.Execute(ctx, *arm.Producer, variables, filepath.Join(artifactDir, "producer.jsonl"), filepath.Join(artifactDir, "producer.stderr.log"))
		durations[0] = result.Duration
		if len(result.Output) > 0 {
			var parseErr error
			producerOutput, parseErr = harness.ParseOpenCodeJSON(bytes.NewReader(result.Output))
			if parseErr != nil && executeErr == nil {
				return partial, fmt.Errorf("parse producer output: %w", parseErr)
			}
		}
		if executeErr == nil && len(result.Output) == 0 {
			return partial, fmt.Errorf("parse producer output: OpenCode output contains no text")
		}
		partial = withProducerUsage(partial, producerOutput)
		partial.ProducerDurationMS = durations[0].Milliseconds()
		if executeErr != nil {
			return partial, fmt.Errorf("run producer: %w", executeErr)
		}
	}
	if arm.AfterProducer != nil {
		result, err := r.executor.Execute(ctx, *arm.AfterProducer, variables, filepath.Join(artifactDir, "readiness.log"), filepath.Join(artifactDir, "readiness.stderr.log"))
		if err != nil {
			return partial, fmt.Errorf("wait for memory: %w", err)
		}
		durations[1] = result.Duration
		partial.ReadinessDurationMS = durations[1].Milliseconds()
	}
	consumer, executeErr := r.executor.Execute(ctx, arm.Consumer, variables, filepath.Join(artifactDir, "consumer.jsonl"), filepath.Join(artifactDir, "consumer.stderr.log"))
	durations[2] = consumer.Duration
	var output harness.AgentOutput
	if len(consumer.Output) > 0 {
		var parseErr error
		output, parseErr = harness.ParseOpenCodeJSON(bytes.NewReader(consumer.Output))
		if parseErr != nil && executeErr == nil {
			return partial, fmt.Errorf("parse consumer output: %w", parseErr)
		}
	}
	if executeErr == nil && len(consumer.Output) == 0 {
		return partial, fmt.Errorf("parse consumer output: OpenCode output contains no text")
	}
	partial = withConsumerUsage(partial, output)
	partial.ConsumerDurationMS = durations[2].Milliseconds()
	if executeErr != nil {
		return partial, fmt.Errorf("run consumer: %w", executeErr)
	}
	return ScoreResult(run, evalCase, arm.Name, producerOutput, output, started, durations), nil
}

func withProducerUsage(result TrialResult, output harness.AgentOutput) TrialResult {
	result.ProducerInputTokens = output.InputTokens
	result.ProducerOutputTokens = output.OutputTokens
	result.ProducerCost = output.Cost
	result.InputTokens += output.InputTokens
	result.OutputTokens += output.OutputTokens
	result.Cost += output.Cost
	return result
}

func withConsumerUsage(result TrialResult, output harness.AgentOutput) TrialResult {
	result.ConsumerInputTokens = output.InputTokens
	result.ConsumerOutputTokens = output.OutputTokens
	result.ConsumerCost = output.Cost
	result.InputTokens += output.InputTokens
	result.OutputTokens += output.OutputTokens
	result.Cost += output.Cost
	return result
}

func trialKeys(runID string, cases []Case, arms []ArmConfig) []TrialKey {
	result := make([]TrialKey, 0, len(cases)*len(arms))
	for _, evalCase := range cases {
		for _, arm := range arms {
			result = append(result, TrialKey{RunID: runID, CaseID: evalCase.ID, Arm: arm.Name})
		}
	}
	return result
}

func trialVariables(run RunRecord, evalCase Case, arm, outputDir string) map[string]string {
	artifactDir := filepath.Join(outputDir, "trials", evalCase.ID, arm)
	return map[string]string{
		"run_id": run.ID, "dataset": run.Dataset, "dataset_revision": run.DatasetRevision,
		"case_id": evalCase.ID, "category": evalCase.Category, "arm": arm,
		"question": evalCase.Question, "expected": evalCase.Expected, "user_id": evalCase.AskingUserID,
		"scope_id": evalCase.ScopeID, "producer_workspace": evalCase.ProducerWorkspace,
		"consumer_workspace": evalCase.ConsumerWorkspace, "artifact_dir": artifactDir,
	}
}

func failureResult(partial TrialResult, run RunRecord, evalCase Case, arm string, started time.Time, err error) TrialResult {
	completed := time.Now().UTC()
	if partial.RunID == "" {
		partial = TrialResult{
			RunID: run.ID, Dataset: run.Dataset, DatasetRevision: run.DatasetRevision,
			CaseID: evalCase.ID, Category: evalCase.Category, Question: evalCase.Question, Arm: arm, AskingUserID: evalCase.AskingUserID,
			Expected: evalCase.Expected, CostScope: "opencode_reported", StartedAt: started,
		}
	}
	partial.Status = "failed"
	partial.CompletedAt = completed
	partial.TotalDurationMS = completed.Sub(started).Milliseconds()
	partial.Error = err.Error()
	return partial
}
