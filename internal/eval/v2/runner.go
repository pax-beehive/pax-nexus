package v2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/harness"
	"github.com/pax-beehive/pax-nexus/internal/eval/v2/memoryprobe"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

type Runner struct {
	store    Store
	executor Executor
	logger   *slog.Logger
	now      func() time.Time
}

type sharedProducerState struct {
	once   sync.Once
	result CommandResult
	output harness.AgentOutput
	err    error
}

var errInvalidAgentOutput = errors.New("invalid agent output")

func NewRunner(store Store, executor Executor, logger *slog.Logger) (*Runner, error) {
	if store == nil || executor == nil {
		return nil, fmt.Errorf("create eval runner: store and executor are required")
	}
	if logger == nil {
		logger = observability.DiscardLogger()
	}
	return &Runner{store: store, executor: executor, logger: logger, now: func() time.Time { return time.Now().UTC() }}, nil
}

func JudgeExistingRun(ctx context.Context, executor Executor, logger *slog.Logger, config Config, revision string, results []TrialResult) (RunRecord, []TrialResult, error) {
	if err := validateRunnerConfig(config); err != nil {
		return RunRecord{}, nil, fmt.Errorf("judge existing run: %w", err)
	}
	if executor == nil || config.Judge == nil {
		return RunRecord{}, nil, fmt.Errorf("judge existing run: executor and judge command are required")
	}
	if logger == nil {
		logger = observability.DiscardLogger()
	}
	run, err := buildRunRecord(config, revision)
	if err != nil {
		return RunRecord{}, nil, fmt.Errorf("judge existing run: %w", err)
	}
	judged := make([]TrialResult, len(results))
	copy(judged, results)
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range config.Run.Parallelism {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				result := &judged[index]
				if result.Status != "completed" || result.Judged {
					continue
				}
				artifactDir := filepath.Join(config.Run.OutputDir, "trials", result.CaseID, result.Arm)
				variables := map[string]string{
					"run_id": run.ID, "dataset": run.Dataset, "dataset_revision": run.DatasetRevision,
					"case_id": result.CaseID, "category": result.Category, "arm": result.Arm,
					"question": result.Question, "expected": result.Expected, "answer": result.Answer,
					"asking_user_id": result.AskingUserID, "answering_agent_id": result.AnsweringAgentID,
					"answerer_seed": result.AnswererSeed, "answerer_source_overlap": result.AnswererSourceOverlap,
					"output_dir": config.Run.OutputDir, "artifact_dir": artifactDir,
				}
				duration, judgment, judgeErr := (&Runner{executor: executor}).runJudge(ctx, *config.Judge, variables, artifactDir)
				result.JudgeDurationMS = duration.Milliseconds()
				if judgeErr != nil {
					result.JudgeError = judgeErr.Error()
					logger.ErrorContext(ctx, "eval judgment failed", "case_id", result.CaseID, "arm", result.Arm, "error", judgeErr)
					continue
				}
				result.Judged = true
				result.Correct = judgment.Correct
				result.JudgeAnswer = judgment.Text
				result.JudgeSessionID = judgment.SessionID
				result.JudgeInputTokens = judgment.InputTokens
				result.JudgeOutputTokens = judgment.OutputTokens
				result.JudgeCost = judgment.Cost
				result.JudgeError = ""
			}
		}()
	}
	for index := range judged {
		select {
		case jobs <- index:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return RunRecord{}, nil, fmt.Errorf("judge existing run: %w", ctx.Err())
		}
	}
	close(jobs)
	workers.Wait()
	if incomplete := countIncompleteJudgments(judged); incomplete > 0 {
		return run, judged, fmt.Errorf("judge existing run: %d completed trials remain unjudged", incomplete)
	}
	return run, judged, nil
}

// JudgeExistingRunDurable rejudges completed answers through the Store Attempt
// seam so the durable Trial projection and append-only ledger stay consistent.
func JudgeExistingRunDurable(
	ctx context.Context,
	store Store,
	executor Executor,
	logger *slog.Logger,
	config Config,
	revision string,
	results []TrialResult,
) (run RunRecord, judged []TrialResult, returnedErr error) {
	if store == nil {
		return RunRecord{}, nil, fmt.Errorf("judge existing run durably: store is required")
	}
	if err := validateRunnerConfig(config); err != nil {
		return RunRecord{}, nil, fmt.Errorf("judge existing run durably: %w", err)
	}
	if executor == nil || config.Judge == nil {
		return RunRecord{}, nil, fmt.Errorf("judge existing run durably: executor and judge command are required")
	}
	if logger == nil {
		logger = observability.DiscardLogger()
	}
	var err error
	run, err = buildRunRecord(config, revision)
	if err != nil {
		return RunRecord{}, nil, fmt.Errorf("judge existing run durably: %w", err)
	}
	acquired, err := store.Acquire(ctx, run.ID)
	if err != nil {
		return run, nil, fmt.Errorf("acquire durable rejudge run: %w", err)
	}
	if !acquired {
		return run, nil, fmt.Errorf("acquire durable rejudge run: run %q is already active", run.ID)
	}
	defer func() {
		if err := store.Release(context.Background(), run.ID); err != nil {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("release durable rejudge run: %w", err))
		}
	}()
	judged, latest, err := loadDurableRejudgeState(ctx, store, run, results)
	if err != nil {
		return run, nil, err
	}
	for index := range judged {
		result := &judged[index]
		if result.Status != "completed" || result.Judged {
			continue
		}
		if err := judgeExistingTrialDurably(ctx, store, executor, logger, run, *config.Judge, config.Run.OutputDir, latest, result); err != nil {
			returnedErr = errors.Join(returnedErr, err)
		}
	}
	returnedErr = errors.Join(returnedErr, exportDurableRejudgeAttempts(ctx, store, run.ID, config.Run.OutputDir))
	if incomplete := countIncompleteJudgments(judged); incomplete > 0 {
		returnedErr = errors.Join(returnedErr, fmt.Errorf("judge existing run durably: %d completed trials remain unjudged", incomplete))
	}
	return run, judged, returnedErr
}

func loadDurableRejudgeState(
	ctx context.Context,
	store Store,
	run RunRecord,
	requested []TrialResult,
) ([]TrialResult, map[string]TrialAttempt, error) {
	if err := store.Initialize(ctx, run, nil); err != nil {
		return nil, nil, fmt.Errorf("verify durable rejudge Run: %w", err)
	}
	durableResults, err := store.Results(ctx, run.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("load durable rejudge Trials: %w", err)
	}
	durableByTrial := make(map[string]TrialResult, len(durableResults))
	for _, result := range durableResults {
		durableByTrial[result.CaseID+"\x00"+result.Arm] = result
	}
	judged := make([]TrialResult, 0, len(requested))
	for _, selection := range requested {
		durable, exists := durableByTrial[selection.CaseID+"\x00"+selection.Arm]
		if !exists {
			return nil, nil, fmt.Errorf("load durable rejudge Trial %s/%s: result is missing", selection.CaseID, selection.Arm)
		}
		judged = append(judged, durable)
	}
	attempts, err := store.Attempts(ctx, run.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("load durable rejudge Attempts: %w", err)
	}
	return judged, latestTrialAttempts(attempts), nil
}

func exportDurableRejudgeAttempts(ctx context.Context, store Store, runID, outputDirectory string) error {
	attempts, err := store.Attempts(ctx, runID)
	if err != nil {
		return fmt.Errorf("reload durable rejudge Attempts: %w", err)
	}
	return ExportTrialAttempts(outputDirectory, attempts)
}

func judgeExistingTrialDurably(
	ctx context.Context,
	store Store,
	executor Executor,
	logger *slog.Logger,
	run RunRecord,
	judge CommandSpec,
	outputDirectory string,
	latest map[string]TrialAttempt,
	result *TrialResult,
) error {
	identity := result.CaseID + "\x00" + result.Arm
	previous, exists := latest[identity]
	if !exists {
		return fmt.Errorf("rejudge Trial %s/%s: prior Attempt is required", result.CaseID, result.Arm)
	}
	key := TrialKey{RunID: run.ID, CaseID: result.CaseID, Arm: result.Arm}
	attempt, claimed, err := store.ClaimRejudge(ctx, key)
	if err != nil {
		return fmt.Errorf("claim rejudge Trial %s/%s: %w", result.CaseID, result.Arm, err)
	}
	if !claimed {
		return fmt.Errorf("claim rejudge Trial %s/%s: completed Trial is unavailable", result.CaseID, result.Arm)
	}
	reference := attemptArtifactReference(attempt)
	if err := store.UpdateAttempt(ctx, attempt, TrialStageJudge, map[string]string{"artifact_dir": reference}); err != nil {
		failure := fmt.Errorf("initialize rejudge Attempt %s/%s/%d: %w", result.CaseID, result.Arm, attempt.Number, err)
		return failClaimedRejudge(ctx, store, attempt, result, TrialStageArtifactPublish, failure)
	}
	artifactDirectory := filepath.Join(outputDirectory, reference)
	if err := copyAttemptArtifact(
		filepath.Join(outputDirectory, previous.ArtifactRefs["artifact_dir"], "consumer.jsonl"),
		filepath.Join(artifactDirectory, "consumer.jsonl"),
	); err != nil {
		failure := fmt.Errorf("copy rejudge consumer evidence for %s/%s: %w", result.CaseID, result.Arm, err)
		return failClaimedRejudge(ctx, store, attempt, result, TrialStageArtifactPublish, failure)
	}
	variables := judgeExistingVariables(run, *result, outputDirectory, artifactDirectory)
	duration, judgment, judgeErr := (&Runner{executor: executor}).runJudge(ctx, judge, variables, artifactDirectory)
	result.JudgeDurationMS = duration.Milliseconds()
	completed := time.Now().UTC()
	result.CompletedAt = completed
	if judgeErr != nil {
		result.JudgeError = judgeErr.Error()
		result.FailureStage = TrialStageJudge
		result.FailureClass = classifyFailure(judgeErr)
		result.Error = judgeErr.Error()
		result.Status = "completed"
		if err := store.Complete(ctx, attempt, *result); err != nil {
			return errors.Join(fmt.Errorf("run durable rejudge %s/%s: %w", result.CaseID, result.Arm, judgeErr), fmt.Errorf("persist failed rejudge Attempt: %w", err))
		}
		logger.ErrorContext(ctx, "eval rejudgment failed", "case_id", result.CaseID, "arm", result.Arm, "error", judgeErr)
		return fmt.Errorf("run durable rejudge %s/%s: %w", result.CaseID, result.Arm, judgeErr)
	}
	result.Judged = true
	result.Status = "completed"
	result.Correct = judgment.Correct
	result.JudgeAnswer = judgment.Text
	result.JudgeSessionID = judgment.SessionID
	result.JudgeInputTokens = judgment.InputTokens
	result.JudgeOutputTokens = judgment.OutputTokens
	result.JudgeCost = judgment.Cost
	result.JudgeError = ""
	result.FailureStage = ""
	result.FailureClass = ""
	result.Error = ""
	if err := store.Complete(ctx, attempt, *result); err != nil {
		return fmt.Errorf("complete rejudge Attempt %s/%s/%d: %w", result.CaseID, result.Arm, attempt.Number, err)
	}
	latest[identity] = TrialAttempt{TrialAttemptHandle: attempt, Status: "completed", Stage: TrialStageCompleted, ArtifactRefs: map[string]string{"artifact_dir": reference}, CompletedAt: &completed}
	return nil
}

func failClaimedRejudge(
	ctx context.Context,
	store Store,
	attempt TrialAttemptHandle,
	result *TrialResult,
	stage TrialStage,
	failure error,
) error {
	completed := time.Now().UTC()
	result.Status = "failed"
	result.CompletedAt = completed
	result.FailureStage = stage
	result.FailureClass = classifyFailure(failure)
	result.Error = failure.Error()
	if err := store.Fail(ctx, attempt, *result); err != nil {
		return errors.Join(failure, fmt.Errorf("persist failed rejudge Attempt: %w", err))
	}
	return failure
}

func judgeExistingVariables(run RunRecord, result TrialResult, outputDirectory, artifactDirectory string) map[string]string {
	return map[string]string{
		"run_id": run.ID, "dataset": run.Dataset, "dataset_revision": run.DatasetRevision,
		"case_id": result.CaseID, "category": result.Category, "arm": result.Arm,
		"question": result.Question, "expected": result.Expected, "answer": result.Answer,
		"asking_user_id": result.AskingUserID, "answering_agent_id": result.AnsweringAgentID,
		"answerer_seed": result.AnswererSeed, "answerer_source_overlap": result.AnswererSourceOverlap,
		"output_dir": outputDirectory, "artifact_dir": artifactDirectory,
	}
}

func latestTrialAttempts(attempts []TrialAttempt) map[string]TrialAttempt {
	latest := make(map[string]TrialAttempt, len(attempts))
	for _, attempt := range attempts {
		key := attempt.CaseID + "\x00" + attempt.Arm
		if current, exists := latest[key]; !exists || attempt.Number > current.Number {
			latest[key] = attempt
		}
	}
	return latest
}

func copyAttemptArtifact(source, destination string) error {
	input, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if len(input) == 0 {
		return fmt.Errorf("source artifact is empty")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(destination, input, 0o600); err != nil {
		return err
	}
	return nil
}

func (r *Runner) Run(ctx context.Context, config Config, cases []Case, revision string) (run RunRecord, results []TrialResult, returnedErr error) {
	if err := validateRunnerConfig(config); err != nil {
		return RunRecord{}, nil, err
	}
	if len(cases) == 0 {
		return RunRecord{}, nil, fmt.Errorf("run eval: cases are required")
	}
	run, err := buildRunRecord(config, revision)
	if err != nil {
		return RunRecord{}, nil, err
	}
	acquired, err := r.store.Acquire(ctx, run.ID)
	if err != nil {
		return RunRecord{}, nil, fmt.Errorf("acquire eval run: %w", err)
	}
	if !acquired {
		return RunRecord{}, nil, fmt.Errorf("acquire eval run: run %q is already active", run.ID)
	}
	defer r.releaseRun(run.ID, &returnedErr)
	lifecycleVariables := map[string]string{
		"run_id": run.ID, "dataset": run.Dataset, "dataset_revision": run.DatasetRevision,
		"manifest": config.Run.Manifest, "output_dir": config.Run.OutputDir,
	}
	if err := r.prepareRun(ctx, run, cases, config, lifecycleVariables); err != nil {
		return RunRecord{}, nil, err
	}
	if config.AfterRun != nil {
		defer r.tearDownRun(config, lifecycleVariables, &returnedErr)
	}
	if err := r.preflightRun(ctx, run.ID, config, lifecycleVariables); err != nil {
		return RunRecord{}, nil, err
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
	attempts, err := r.store.Attempts(ctx, run.ID)
	if err != nil {
		return RunRecord{}, nil, fmt.Errorf("load eval trial attempts: %w", err)
	}
	if err := ExportTrialAttempts(config.Run.OutputDir, attempts); err != nil {
		return RunRecord{}, nil, err
	}
	if err := r.store.Finish(ctx, run.ID); err != nil {
		return RunRecord{}, nil, fmt.Errorf("finish eval run: %w", err)
	}
	if config.Judge != nil {
		if incomplete := countIncompleteJudgments(results); incomplete > 0 {
			return run, results, fmt.Errorf("finish eval run: %d completed trials remain unjudged", incomplete)
		}
	}
	r.logger.InfoContext(ctx, "eval completed", "protocol", config.Version, "run_id", run.ID, "trials", len(results), "output_dir", config.Run.OutputDir)
	return run, results, nil
}

func validateRunnerConfig(config Config) error {
	if config.Version != ConfigVersion && config.Version != "v3" && config.Version != "recall-v2" {
		return fmt.Errorf("validate eval runner config: unsupported version %q", config.Version)
	}
	return config.ValidateBase()
}

func buildRunRecord(config Config, revision string) (RunRecord, error) {
	runtimeValues, err := config.ResolveRuntime(os.Getenv)
	if err != nil {
		return RunRecord{}, err
	}
	hash, err := config.HashWithRuntime(runtimeValues)
	if err != nil {
		return RunRecord{}, err
	}
	return RunRecord{
		ID: config.Run.ID, Dataset: config.Run.Dataset, DatasetRevision: revision,
		ConfigHash: hash, Config: config, Runtime: runtimeValues,
	}, nil
}

func (r *Runner) prepareRun(ctx context.Context, run RunRecord, cases []Case, config Config, variables map[string]string) error {
	if err := r.store.Initialize(ctx, run, trialKeys(run.ID, cases, config.Arms)); err != nil {
		return fmt.Errorf("initialize eval run: %w", err)
	}
	if err := r.store.ResetRunning(ctx, run.ID); err != nil {
		return fmt.Errorf("reset interrupted eval trials: %w", err)
	}
	if err := os.MkdirAll(config.Run.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create eval output directory: %w", err)
	}
	if config.BeforeRun == nil {
		return nil
	}
	if _, err := r.executor.Execute(ctx, *config.BeforeRun, variables, filepath.Join(config.Run.OutputDir, "before-run.log"), filepath.Join(config.Run.OutputDir, "before-run.stderr.log")); err != nil {
		return fmt.Errorf("prepare eval run: %w", err)
	}
	return nil
}

func (r *Runner) preflightRun(ctx context.Context, runID string, config Config, variables map[string]string) error {
	runnable, err := r.store.HasRunnable(ctx, runID, config.RetryFailed, config.MaxAttempts())
	if err != nil {
		return fmt.Errorf("check runnable eval trials: %w", err)
	}
	if !runnable || config.Preflight == nil {
		return nil
	}
	if _, err := r.executor.Execute(ctx, *config.Preflight, variables, filepath.Join(config.Run.OutputDir, "preflight.log"), filepath.Join(config.Run.OutputDir, "preflight.stderr.log")); err != nil {
		return fmt.Errorf("preflight eval run: %w", err)
	}
	return nil
}

func (r *Runner) releaseRun(runID string, returnedErr *error) {
	if err := r.store.Release(context.Background(), runID); err != nil {
		*returnedErr = errors.Join(*returnedErr, fmt.Errorf("release eval run: %w", err))
	}
}

func (r *Runner) tearDownRun(config Config, variables map[string]string, returnedErr *error) {
	teardownCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := r.executor.Execute(teardownCtx, *config.AfterRun, variables, filepath.Join(config.Run.OutputDir, "after-run.log"), filepath.Join(config.Run.OutputDir, "after-run.stderr.log")); err != nil {
		r.logger.Error("tear down eval run", "error", err)
		*returnedErr = errors.Join(*returnedErr, fmt.Errorf("tear down eval run: %w", err))
	}
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
		hydrated[index] = withCaseIdentity(hydrated[index], evalCase)
	}
	return hydrated
}

func (r *Runner) runTrials(ctx context.Context, run RunRecord, cases []Case, config Config, timeout time.Duration) error {
	type job struct {
		evalCase Case
		arm      ArmConfig
		shared   *sharedProducerState
	}
	sharedByCase := make(map[string]*sharedProducerState, len(cases))
	if config.SharedProducer != nil {
		for _, evalCase := range cases {
			sharedByCase[evalCase.ID] = &sharedProducerState{}
		}
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
				if err := r.runTrial(workerCtx, run, current.evalCase, current.arm, config, timeout, current.shared); err != nil {
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
			case jobs <- job{evalCase: evalCase, arm: arm, shared: sharedByCase[evalCase.ID]}:
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

func (r *Runner) runTrial(ctx context.Context, run RunRecord, evalCase Case, arm ArmConfig, config Config, timeout time.Duration, shared *sharedProducerState) error {
	key := TrialKey{RunID: run.ID, CaseID: evalCase.ID, Arm: arm.Name}
	attempt, claimed, err := r.store.Claim(ctx, key, config.RetryFailed, config.MaxAttempts())
	if err != nil {
		return fmt.Errorf("claim eval trial %s/%s: %w", evalCase.ID, arm.Name, err)
	}
	if !claimed {
		return nil
	}
	started := r.now()
	trialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	artifactDir := attemptArtifactDirectory(config.Run.OutputDir, attempt)
	if err := r.store.UpdateAttempt(ctx, attempt, TrialStageAnswererSelection, map[string]string{"artifact_dir": attemptArtifactReference(attempt)}); err != nil {
		return fmt.Errorf("initialize eval trial attempt %s/%s/%d: %w", evalCase.ID, arm.Name, attempt.Number, err)
	}
	result, runErr := r.executeTrial(trialCtx, run, evalCase, arm, config.SharedProducer, config.Judge, shared, config.Run.OutputDir, artifactDir, started, func(stage TrialStage) error {
		return r.store.UpdateAttempt(ctx, attempt, stage, nil)
	})
	if runErr != nil && trialCtx.Err() != nil {
		runErr = errors.Join(runErr, trialCtx.Err())
	}
	if runErr == nil {
		if err := r.store.Complete(ctx, attempt, result); err != nil {
			return fmt.Errorf("complete eval trial %s/%s: %w", evalCase.ID, arm.Name, err)
		}
		r.logger.InfoContext(ctx, "eval trial completed", "case_id", evalCase.ID, "arm", arm.Name, "token_f1", result.TokenF1)
		return nil
	}
	failed := failureResult(result, run, evalCase, arm.Name, started, runErr)
	if err := r.store.Fail(ctx, attempt, failed); err != nil {
		return errors.Join(fmt.Errorf("run eval trial %s/%s: %w", evalCase.ID, arm.Name, runErr), fmt.Errorf("persist eval failure: %w", err))
	}
	r.logger.ErrorContext(ctx, "eval trial failed", "case_id", evalCase.ID, "arm", arm.Name, "error", runErr)
	return nil
}

func (r *Runner) executeTrial(
	ctx context.Context,
	run RunRecord,
	evalCase Case,
	arm ArmConfig,
	sharedSpec *CommandSpec,
	judgeSpec *CommandSpec,
	shared *sharedProducerState,
	outputDir string,
	artifactDir string,
	started time.Time,
	enterStage func(TrialStage) error,
) (TrialResult, error) {
	variables := trialVariablesAtArtifactDirectory(run, evalCase, arm.Name, outputDir, artifactDir)
	durations := [3]time.Duration{}
	partial := withCaseIdentity(TrialResult{
		RunID: run.ID, Dataset: run.Dataset, DatasetRevision: run.DatasetRevision,
		CaseID: evalCase.ID, Category: evalCase.Category, Question: evalCase.Question, Arm: arm.Name, AskingUserID: evalCase.AskingUserID,
		Expected: evalCase.Expected, Status: "running", CostScope: "opencode_reported", StartedAt: started,
	}, evalCase)
	if evalCase.AnsweringAgentID == "" && evalCase.AnswererSourceOverlap == "no_cross_agent_answerer_available" {
		return partial, stageError(TrialStageAnswererSelection, fmt.Errorf("select eval answerer: %s", evalCase.AnswererSourceOverlap))
	}
	var producerOutput harness.AgentOutput
	var ingestResult memoryprobe.IngestResult
	if arm.Ingest != nil {
		if err := enterStage(TrialStageMemoryIngest); err != nil {
			return partial, stageError(TrialStageMemoryIngest, fmt.Errorf("record eval attempt stage: %w", err))
		}
		producerDuration, ingestDuration, observedIngest, err := r.runMemoryIngest(ctx, run, evalCase, arm, sharedSpec, shared, variables, artifactDir, outputDir)
		durations[0], durations[1] = producerDuration, ingestDuration
		partial.ProducerDurationMS = durations[0].Milliseconds()
		if err != nil {
			return partial, stageError(TrialStageMemoryIngest, err)
		}
		ingestResult = observedIngest
		partial = withMemoryIngest(partial, ingestResult)
		partial.ReadinessDurationMS = durations[1].Milliseconds()
	}
	if arm.Producer != nil {
		if err := enterStage(TrialStageProducer); err != nil {
			return partial, stageError(TrialStageProducer, fmt.Errorf("record eval attempt stage: %w", err))
		}
		var err error
		durations[0], producerOutput, err = r.runLegacyProducer(ctx, *arm.Producer, variables, artifactDir)
		partial = withProducerUsage(partial, producerOutput)
		partial.ProducerDurationMS = durations[0].Milliseconds()
		if err != nil {
			return partial, stageError(TrialStageProducer, err)
		}
	}
	if arm.AfterProducer != nil {
		if err := enterStage(TrialStageReadiness); err != nil {
			return partial, stageError(TrialStageReadiness, fmt.Errorf("record eval attempt stage: %w", err))
		}
		readinessDuration, err := r.waitForMemory(ctx, *arm.AfterProducer, variables, artifactDir)
		if err != nil {
			return partial, stageError(TrialStageReadiness, err)
		}
		durations[1] += readinessDuration
		partial.ReadinessDurationMS = durations[1].Milliseconds()
	}
	var output harness.AgentOutput
	var recallDiagnostics recallDiagnostics
	var executeErr error
	if err := enterStage(TrialStageConsumer); err != nil {
		return partial, stageError(TrialStageConsumer, fmt.Errorf("record eval attempt stage: %w", err))
	}
	durations[2], output, recallDiagnostics, executeErr = r.runConsumer(ctx, arm.Consumer, variables, artifactDir)
	partial = withConsumerUsage(partial, output)
	partial = withRecallDiagnostics(partial, recallDiagnostics)
	partial.ConsumerDurationMS = durations[2].Milliseconds()
	if executeErr != nil {
		return partial, stageError(TrialStageConsumer, executeErr)
	}
	scored := withRecallDiagnostics(
		withMemoryIngest(ScoreResult(run, evalCase, arm.Name, producerOutput, output, started, durations), ingestResult),
		recallDiagnostics,
	)
	if judgeSpec == nil {
		return scored, nil
	}
	if err := enterStage(TrialStageJudge); err != nil {
		return scored, stageError(TrialStageJudge, fmt.Errorf("record eval attempt stage: %w", err))
	}
	variables["answer"] = output.Text
	judgeDuration, judgment, judgeErr := r.runJudge(ctx, *judgeSpec, variables, artifactDir)
	scored.JudgeDurationMS = judgeDuration.Milliseconds()
	if judgeErr != nil {
		scored.JudgeError = judgeErr.Error()
		scored.FailureStage = TrialStageJudge
		scored.FailureClass = classifyFailure(judgeErr)
		return scored, nil //nolint:nilerr // Judge infrastructure failures preserve the completed consumer result and fail the run through the completeness gate.
	}
	completed := time.Now().UTC()
	scored.Judged = true
	scored.Correct = judgment.Correct
	scored.JudgeAnswer = judgment.Text
	scored.JudgeSessionID = judgment.SessionID
	scored.JudgeInputTokens = judgment.InputTokens
	scored.JudgeOutputTokens = judgment.OutputTokens
	scored.JudgeCost = judgment.Cost
	scored.CompletedAt = completed
	scored.TotalDurationMS = completed.Sub(started).Milliseconds()
	return scored, nil
}

func countIncompleteJudgments(results []TrialResult) int {
	incomplete := 0
	for _, result := range results {
		if result.Status == "completed" && !result.Judged {
			incomplete++
		}
	}
	return incomplete
}

func (r *Runner) runMemoryIngest(
	ctx context.Context,
	run RunRecord,
	evalCase Case,
	arm ArmConfig,
	sharedSpec *CommandSpec,
	shared *sharedProducerState,
	variables map[string]string,
	artifactDir string,
	outputDir string,
) (time.Duration, time.Duration, memoryprobe.IngestResult, error) {
	producerDuration := time.Duration(0)
	if sharedSpec != nil {
		if shared == nil {
			return 0, 0, memoryprobe.IngestResult{}, fmt.Errorf("run shared producer: shared state is not configured")
		}
		shared.once.Do(func() {
			shared.result, shared.output, shared.err = r.executeSharedProducer(ctx, run, evalCase, *sharedSpec, outputDir)
		})
		producerDuration = shared.result.Duration
		if shared.err != nil {
			return producerDuration, 0, memoryprobe.IngestResult{}, shared.err
		}
	}
	result, err := r.executor.Execute(ctx, *arm.Ingest, variables, filepath.Join(artifactDir, "ingest.log"), filepath.Join(artifactDir, "ingest.stderr.log"))
	if err != nil {
		return producerDuration, result.Duration, memoryprobe.IngestResult{}, fmt.Errorf("ingest evaluation memory: %w", err)
	}
	var ingest memoryprobe.IngestResult
	if err := json.Unmarshal(bytes.TrimSpace(result.Output), &ingest); err != nil {
		return producerDuration, result.Duration, memoryprobe.IngestResult{}, fmt.Errorf("decode memory ingest result: %w", err)
	}
	if ingest.Provider == "" {
		return producerDuration, result.Duration, memoryprobe.IngestResult{}, fmt.Errorf("decode memory ingest result: provider is required")
	}
	return producerDuration, result.Duration, ingest, nil
}

func withMemoryIngest(result TrialResult, ingest memoryprobe.IngestResult) TrialResult {
	result.MemoryIngestProvider = ingest.Provider
	result.MemoryIngestAccepted = ingest.Accepted
	result.MemoryIngestDuplicate = ingest.Duplicate
	result.MemoryIngestCreated = ingest.Created
	result.MemoryIngestUpdated = ingest.Updated
	result.MemoryIngestDeleted = ingest.Deleted
	result.MemoryIngestNoOpKnown = ingest.NoOpKnown
	result.MemoryIngestNoOp = ingest.NoOp
	result.MemorySourceEvents = ingest.SourceEvents
	result.MemorySourceActors = ingest.SourceActors
	result.MemorySourceSessions = ingest.SourceSessions
	return result
}

func (r *Runner) runLegacyProducer(ctx context.Context, spec CommandSpec, variables map[string]string, artifactDir string) (time.Duration, harness.AgentOutput, error) {
	result, executeErr := r.executor.Execute(ctx, spec, variables, filepath.Join(artifactDir, "producer.jsonl"), filepath.Join(artifactDir, "producer.stderr.log"))
	output, parseErr := parseAgentOutput(result, "producer")
	if executeErr != nil {
		return result.Duration, output, fmt.Errorf("run producer: %w", executeErr)
	}
	if parseErr != nil {
		return result.Duration, output, parseErr
	}
	return result.Duration, output, nil
}

func (r *Runner) waitForMemory(ctx context.Context, spec CommandSpec, variables map[string]string, artifactDir string) (time.Duration, error) {
	result, err := r.executor.Execute(ctx, spec, variables, filepath.Join(artifactDir, "readiness.log"), filepath.Join(artifactDir, "readiness.stderr.log"))
	if err != nil {
		return result.Duration, fmt.Errorf("wait for memory: %w", err)
	}
	return result.Duration, nil
}

func (r *Runner) runConsumer(ctx context.Context, spec CommandSpec, variables map[string]string, artifactDir string) (time.Duration, harness.AgentOutput, recallDiagnostics, error) {
	stderrPath := filepath.Join(artifactDir, "consumer.stderr.log")
	result, executeErr := r.executor.Execute(ctx, spec, variables, filepath.Join(artifactDir, "consumer.jsonl"), stderrPath)
	output, parseErr := parseAgentOutput(result, "consumer")
	diagnostics, diagnosticsErr := loadRecallDiagnostics(stderrPath)
	if executeErr != nil {
		return result.Duration, output, diagnostics, fmt.Errorf("run consumer: %w", executeErr)
	}
	if parseErr != nil {
		return result.Duration, output, diagnostics, parseErr
	}
	if diagnosticsErr != nil {
		return result.Duration, output, diagnostics, diagnosticsErr
	}
	return result.Duration, output, diagnostics, nil
}

type recallDiagnostics struct {
	Observed       bool
	Success        bool
	ProviderCalls  int
	Providers      map[string]int
	Candidates     int
	Eligible       int
	Hits           int
	Inserted       int
	DurationMS     int64
	ActiveObserved bool
	ActiveSuccess  bool
	ActiveCalls    int
	ProviderType   string
}

type recallDiagnosticEvent struct {
	Kind                  string         `json:"kind"`
	Success               bool           `json:"success"`
	DurationMS            int64          `json:"duration_ms"`
	HitCount              int            `json:"hit_count"`
	InsertedCount         int            `json:"inserted_count"`
	CallCount             int            `json:"call_count"`
	ProviderType          string         `json:"provider_type"`
	ProviderRecalls       map[string]int `json:"provider_recalls"`
	ProviderHits          map[string]int `json:"provider_hits"`
	ProviderRecallDetails []struct {
		CandidateCount int `json:"candidate_count"`
		EligibleCount  int `json:"eligible_count"`
	} `json:"provider_recall_details"`
}

func loadRecallDiagnostics(path string) (recallDiagnostics, error) {
	input, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return recallDiagnostics{}, nil
	}
	if err != nil {
		return recallDiagnostics{}, fmt.Errorf("read consumer recall diagnostics: %w", err)
	}

	diagnostics := recallDiagnostics{Success: true}
	for line := range bytes.SplitSeq(input, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(line, &header) != nil || (header.Kind != "hook_recall" && header.Kind != "hook_active_recall" && header.Kind != "hook_provider_config") {
			continue
		}
		var event recallDiagnosticEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return diagnostics, fmt.Errorf("decode consumer recall diagnostics: %w", err)
		}
		if event.Kind == "hook_active_recall" {
			diagnostics.addActive(event)
			continue
		}
		if event.Kind == "hook_provider_config" {
			diagnostics.ProviderType = event.ProviderType
			continue
		}
		diagnostics.add(event)
	}
	return diagnostics, nil
}

func (diagnostics *recallDiagnostics) addActive(event recallDiagnosticEvent) {
	diagnostics.ActiveObserved = true
	diagnostics.ActiveSuccess = diagnostics.ActiveSuccess || event.Success
	diagnostics.ActiveCalls += event.CallCount
}

func (diagnostics *recallDiagnostics) add(event recallDiagnosticEvent) {
	diagnostics.Observed = true
	diagnostics.Success = diagnostics.Success && event.Success
	diagnostics.DurationMS += event.DurationMS
	diagnostics.ProviderCalls += sumDiagnosticCounts(event.ProviderRecalls)
	if diagnostics.Providers == nil {
		diagnostics.Providers = make(map[string]int)
	}
	for provider, count := range event.ProviderRecalls {
		diagnostics.Providers[provider] += count
	}
	diagnostics.Hits += event.HitCount
	if event.HitCount == 0 {
		diagnostics.Hits += sumDiagnosticCounts(event.ProviderHits)
	}
	diagnostics.Inserted += event.InsertedCount
	for _, detail := range event.ProviderRecallDetails {
		diagnostics.Candidates += detail.CandidateCount
		diagnostics.Eligible += detail.EligibleCount
	}
}

func sumDiagnosticCounts(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func withRecallDiagnostics(result TrialResult, diagnostics recallDiagnostics) TrialResult {
	result.MemoryRecallObserved = diagnostics.Observed
	result.MemoryRecallSuccess = diagnostics.Success
	result.MemoryRecallProviderCalls = diagnostics.ProviderCalls
	result.MemoryRecallProviders = maps.Clone(diagnostics.Providers)
	result.MemoryRecallProviderType = diagnostics.ProviderType
	result.MemoryRecallCandidates = diagnostics.Candidates
	result.MemoryRecallEligible = diagnostics.Eligible
	result.MemoryRecallHits = diagnostics.Hits
	result.MemoryContextItems = diagnostics.Inserted
	result.MemoryRecallDurationMS = diagnostics.DurationMS
	result.ActiveRecallObserved = diagnostics.ActiveObserved
	result.ActiveRecallSuccess = diagnostics.ActiveSuccess
	result.ActiveRecallCalls = diagnostics.ActiveCalls
	return result
}

func (r *Runner) runJudge(ctx context.Context, spec CommandSpec, variables map[string]string, artifactDir string) (time.Duration, harness.GroupMemBenchJudgment, error) {
	result, executeErr := r.executor.Execute(ctx, spec, variables, filepath.Join(artifactDir, "judge.jsonl"), filepath.Join(artifactDir, "judge.stderr.log"))
	output, parseErr := parseAgentOutput(result, "judge")
	if executeErr != nil {
		return result.Duration, harness.GroupMemBenchJudgment{}, fmt.Errorf("run judge: %w", executeErr)
	}
	if parseErr != nil {
		return result.Duration, harness.GroupMemBenchJudgment{}, parseErr
	}
	judgment, err := harness.ParseGroupMemBenchJudgment(output)
	if err != nil {
		return result.Duration, harness.GroupMemBenchJudgment{}, err
	}
	return result.Duration, judgment, nil
}

func parseAgentOutput(result CommandResult, label string) (harness.AgentOutput, error) {
	if len(result.Output) == 0 {
		return harness.AgentOutput{}, fmt.Errorf("%w: parse %s output: OpenCode output contains no text", errInvalidAgentOutput, label)
	}
	output, err := harness.ParseOpenCodeJSON(bytes.NewReader(result.Output))
	if err != nil {
		return output, fmt.Errorf("%w: parse %s output: %w", errInvalidAgentOutput, label, err)
	}
	return output, nil
}

func (r *Runner) executeSharedProducer(
	ctx context.Context,
	run RunRecord,
	evalCase Case,
	spec CommandSpec,
	outputDir string,
) (CommandResult, harness.AgentOutput, error) {
	artifactDir := filepath.Join(outputDir, "trials", evalCase.ID, "shared")
	stdoutPath := filepath.Join(artifactDir, "producer.jsonl")
	textPath := filepath.Join(artifactDir, "producer.txt")
	markerPath := filepath.Join(artifactDir, "producer.complete")
	commandMarkerPath := filepath.Join(artifactDir, "producer.command-success")
	cachedResult, cachedOutput, found, err := loadSharedProducer(stdoutPath, textPath, markerPath, commandMarkerPath)
	if err != nil {
		return CommandResult{}, harness.AgentOutput{}, err
	}
	if found {
		return cachedResult, cachedOutput, nil
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return CommandResult{}, harness.AgentOutput{}, fmt.Errorf("create shared producer artifact directory: %w", err)
	}
	if err := removeIfExists(commandMarkerPath); err != nil {
		return CommandResult{}, harness.AgentOutput{}, fmt.Errorf("clear incomplete shared producer command marker: %w", err)
	}
	variables := trialVariables(run, evalCase, "shared", outputDir)
	result, executeErr := r.executor.Execute(ctx, spec, variables, stdoutPath, filepath.Join(artifactDir, "producer.stderr.log"))
	var output harness.AgentOutput
	if len(result.Output) > 0 {
		var parseErr error
		output, parseErr = harness.ParseOpenCodeJSON(bytes.NewReader(result.Output))
		if parseErr != nil && executeErr == nil {
			return result, output, fmt.Errorf("parse shared producer output: %w", parseErr)
		}
	}
	if executeErr != nil {
		return result, output, fmt.Errorf("run shared producer: %w", executeErr)
	}
	if err := writeCommandOutput(commandMarkerPath, []byte("success\n")); err != nil {
		return result, output, fmt.Errorf("persist shared producer command marker: %w", err)
	}
	if output.Text == "" {
		return result, output, fmt.Errorf("parse shared producer output: OpenCode output contains no text")
	}
	if err := writeCommandOutput(markerPath, []byte("complete\n")); err != nil {
		return result, output, fmt.Errorf("persist shared producer completion marker: %w", err)
	}
	if err := writeCommandOutput(textPath, []byte(output.Text)); err != nil {
		return result, output, fmt.Errorf("persist shared producer transcript: %w", err)
	}
	return result, output, nil
}

func loadSharedProducer(stdoutPath, textPath, markerPath, commandMarkerPath string) (CommandResult, harness.AgentOutput, bool, error) {
	complete, err := fileExists(markerPath)
	if err != nil {
		return CommandResult{}, harness.AgentOutput{}, false, fmt.Errorf("inspect cached shared producer completion marker: %w", err)
	}
	commandSucceeded, err := fileExists(commandMarkerPath)
	if err != nil {
		return CommandResult{}, harness.AgentOutput{}, false, fmt.Errorf("inspect cached shared producer command marker: %w", err)
	}
	if !complete && !commandSucceeded {
		return CommandResult{}, harness.AgentOutput{}, false, nil
	}
	raw, err := os.ReadFile(stdoutPath)
	if err != nil {
		return CommandResult{}, harness.AgentOutput{}, false, fmt.Errorf("read marked shared producer output: %w", err)
	}
	output, err := harness.ParseOpenCodeJSON(bytes.NewReader(raw))
	if err != nil {
		if !complete {
			return CommandResult{}, harness.AgentOutput{}, false, nil
		}
		return CommandResult{}, harness.AgentOutput{}, false, fmt.Errorf("parse cached shared producer output: %w", err)
	}
	if output.Text == "" {
		if !complete {
			return CommandResult{}, harness.AgentOutput{}, false, nil
		}
		return CommandResult{}, harness.AgentOutput{}, false, fmt.Errorf("parse cached shared producer output: OpenCode output contains no text")
	}
	if !complete {
		if err := writeCommandOutput(markerPath, []byte("complete\n")); err != nil {
			return CommandResult{}, harness.AgentOutput{}, false, fmt.Errorf("finalize cached shared producer output: %w", err)
		}
	}
	text, err := os.ReadFile(textPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return CommandResult{}, harness.AgentOutput{}, false, fmt.Errorf("read cached shared producer transcript: %w", err)
	}
	if !bytes.Equal(text, []byte(output.Text)) {
		if err := writeCommandOutput(textPath, []byte(output.Text)); err != nil {
			return CommandResult{}, harness.AgentOutput{}, false, fmt.Errorf("repair cached shared producer transcript: %w", err)
		}
	}
	return CommandResult{Output: raw}, output, true, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
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
	return trialVariablesAtArtifactDirectory(run, evalCase, arm, outputDir, artifactDir)
}

func trialVariablesAtArtifactDirectory(run RunRecord, evalCase Case, arm, outputDir, artifactDir string) map[string]string {
	sharedArtifactDir := filepath.Join(outputDir, "trials", evalCase.ID, "shared")
	return map[string]string{
		"run_id": run.ID, "dataset": run.Dataset, "dataset_revision": run.DatasetRevision,
		"case_id": evalCase.ID, "category": evalCase.Category, "arm": arm,
		"question": evalCase.Question, "expected": evalCase.Expected, "user_id": evalCase.AskingUserID,
		"asking_user_id": evalCase.AskingUserID, "answering_agent_id": evalCase.AnsweringAgentID,
		"answerer_seed": evalCase.AnswererSeed, "answerer_source_overlap": evalCase.AnswererSourceOverlap,
		"knowledge_source_status": evalCase.KnowledgeSourceStatus, "temporal_mode": evalCase.TemporalMode,
		"strict_cross_agent": fmt.Sprintf("%t", evalCase.StrictCrossAgent),
		"scope_id":           evalCase.ScopeID, "producer_workspace": evalCase.ProducerWorkspace,
		"consumer_workspace": evalCase.ConsumerWorkspace, "output_dir": outputDir, "artifact_dir": artifactDir,
		"shared_artifact_dir":  sharedArtifactDir,
		"shared_producer_text": filepath.Join(sharedArtifactDir, "producer.txt"),
		"session_batches_file": filepath.Join(evalCase.ProducerWorkspace, "session-batches.json"),
	}
}

func attemptArtifactDirectory(outputDir string, attempt TrialAttemptHandle) string {
	return filepath.Join(outputDir, attemptArtifactReference(attempt))
}

func attemptArtifactReference(attempt TrialAttemptHandle) string {
	return filepath.Join("trials", attempt.CaseID, attempt.Arm, "attempts", fmt.Sprintf("%03d", attempt.Number))
}

type trialStageError struct {
	stage TrialStage
	err   error
}

func (e *trialStageError) Error() string { return e.err.Error() }
func (e *trialStageError) Unwrap() error { return e.err }

func stageError(stage TrialStage, err error) error {
	return &trialStageError{stage: stage, err: err}
}

func failureResult(partial TrialResult, run RunRecord, evalCase Case, arm string, started time.Time, err error) TrialResult {
	completed := time.Now().UTC()
	if partial.RunID == "" {
		partial = withCaseIdentity(TrialResult{
			RunID: run.ID, Dataset: run.Dataset, DatasetRevision: run.DatasetRevision,
			CaseID: evalCase.ID, Category: evalCase.Category, Question: evalCase.Question, Arm: arm, AskingUserID: evalCase.AskingUserID,
			Expected: evalCase.Expected, CostScope: "opencode_reported", StartedAt: started,
		}, evalCase)
	}
	partial.Status = "failed"
	partial.CompletedAt = completed
	partial.TotalDurationMS = completed.Sub(started).Milliseconds()
	partial.Error = err.Error()
	var staged *trialStageError
	if errors.As(err, &staged) {
		partial.FailureStage = staged.stage
	}
	partial.FailureClass = classifyFailure(err)
	return partial
}

func classifyFailure(err error) FailureClass {
	var exitErr *exec.ExitError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return FailureClassDeadline
	case errors.Is(err, context.Canceled):
		return FailureClassCanceled
	case errors.As(err, &exitErr):
		return FailureClassExit
	case errors.Is(err, errInvalidAgentOutput):
		return FailureClassInvalidOutput
	default:
		return FailureClassUnknown
	}
}

func withCaseIdentity(result TrialResult, evalCase Case) TrialResult {
	result.AnsweringAgentID = evalCase.AnsweringAgentID
	result.AnswererSeed = evalCase.AnswererSeed
	result.StrictCrossAgent = evalCase.StrictCrossAgent
	result.AnswererSourceOverlap = evalCase.AnswererSourceOverlap
	result.KnowledgeSourceStatus = evalCase.KnowledgeSourceStatus
	result.TemporalMode = evalCase.TemporalMode
	return result
}
