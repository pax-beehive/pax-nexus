package v2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
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
	if err := r.store.Finish(ctx, run.ID); err != nil {
		return RunRecord{}, nil, fmt.Errorf("finish eval run: %w", err)
	}
	r.logger.InfoContext(ctx, "eval v2 completed", "run_id", run.ID, "trials", len(results), "output_dir", config.Run.OutputDir)
	return run, results, nil
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
	claimed, err := r.store.Claim(ctx, key, config.RetryFailed, config.MaxAttempts())
	if err != nil {
		return fmt.Errorf("claim eval trial %s/%s: %w", evalCase.ID, arm.Name, err)
	}
	if !claimed {
		return nil
	}
	started := r.now()
	trialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, runErr := r.executeTrial(trialCtx, run, evalCase, arm, config.SharedProducer, shared, config.Run.OutputDir, started)
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

func (r *Runner) executeTrial(
	ctx context.Context,
	run RunRecord,
	evalCase Case,
	arm ArmConfig,
	sharedSpec *CommandSpec,
	shared *sharedProducerState,
	outputDir string,
	started time.Time,
) (TrialResult, error) {
	variables := trialVariables(run, evalCase, arm.Name, outputDir)
	artifactDir := variables["artifact_dir"]
	durations := [3]time.Duration{}
	partial := TrialResult{
		RunID: run.ID, Dataset: run.Dataset, DatasetRevision: run.DatasetRevision,
		CaseID: evalCase.ID, Category: evalCase.Category, Question: evalCase.Question, Arm: arm.Name, AskingUserID: evalCase.AskingUserID,
		Expected: evalCase.Expected, Status: "running", CostScope: "opencode_reported", StartedAt: started,
	}
	var producerOutput harness.AgentOutput
	var ingestResult memoryprobe.IngestResult
	if arm.Ingest != nil {
		producerDuration, ingestDuration, observedIngest, err := r.runSharedIngest(ctx, run, evalCase, arm, sharedSpec, shared, variables, artifactDir, outputDir)
		durations[0], durations[1] = producerDuration, ingestDuration
		partial.ProducerDurationMS = durations[0].Milliseconds()
		if err != nil {
			return partial, err
		}
		ingestResult = observedIngest
		partial = withMemoryIngest(partial, ingestResult)
		partial.ReadinessDurationMS = durations[1].Milliseconds()
	}
	if arm.Producer != nil {
		var err error
		durations[0], producerOutput, err = r.runLegacyProducer(ctx, *arm.Producer, variables, artifactDir)
		partial = withProducerUsage(partial, producerOutput)
		partial.ProducerDurationMS = durations[0].Milliseconds()
		if err != nil {
			return partial, err
		}
	}
	if arm.AfterProducer != nil {
		readinessDuration, err := r.waitForMemory(ctx, *arm.AfterProducer, variables, artifactDir)
		if err != nil {
			return partial, err
		}
		durations[1] += readinessDuration
		partial.ReadinessDurationMS = durations[1].Milliseconds()
	}
	var output harness.AgentOutput
	var executeErr error
	durations[2], output, executeErr = r.runConsumer(ctx, arm.Consumer, variables, artifactDir)
	partial = withConsumerUsage(partial, output)
	partial.ConsumerDurationMS = durations[2].Milliseconds()
	if executeErr != nil {
		return partial, executeErr
	}
	return withMemoryIngest(ScoreResult(run, evalCase, arm.Name, producerOutput, output, started, durations), ingestResult), nil
}

func (r *Runner) runSharedIngest(
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
	if sharedSpec == nil || shared == nil {
		return 0, 0, memoryprobe.IngestResult{}, fmt.Errorf("run shared producer: shared producer is not configured")
	}
	shared.once.Do(func() {
		shared.result, shared.output, shared.err = r.executeSharedProducer(ctx, run, evalCase, *sharedSpec, outputDir)
	})
	if shared.err != nil {
		return shared.result.Duration, 0, memoryprobe.IngestResult{}, shared.err
	}
	result, err := r.executor.Execute(ctx, *arm.Ingest, variables, filepath.Join(artifactDir, "ingest.log"), filepath.Join(artifactDir, "ingest.stderr.log"))
	if err != nil {
		return shared.result.Duration, result.Duration, memoryprobe.IngestResult{}, fmt.Errorf("ingest shared producer transcript: %w", err)
	}
	var ingest memoryprobe.IngestResult
	if err := json.Unmarshal(bytes.TrimSpace(result.Output), &ingest); err != nil {
		return shared.result.Duration, result.Duration, memoryprobe.IngestResult{}, fmt.Errorf("decode memory ingest result: %w", err)
	}
	if ingest.Provider == "" {
		return shared.result.Duration, result.Duration, memoryprobe.IngestResult{}, fmt.Errorf("decode memory ingest result: provider is required")
	}
	return shared.result.Duration, result.Duration, ingest, nil
}

func withMemoryIngest(result TrialResult, ingest memoryprobe.IngestResult) TrialResult {
	result.MemoryIngestProvider = ingest.Provider
	result.MemoryIngestAccepted = ingest.Accepted
	result.MemoryIngestDuplicate = ingest.Duplicate
	result.MemoryIngestCreated = ingest.Created
	result.MemoryIngestUpdated = ingest.Updated
	result.MemoryIngestDeleted = ingest.Deleted
	result.MemoryIngestNoOp = ingest.NoOp
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

func (r *Runner) runConsumer(ctx context.Context, spec CommandSpec, variables map[string]string, artifactDir string) (time.Duration, harness.AgentOutput, error) {
	result, executeErr := r.executor.Execute(ctx, spec, variables, filepath.Join(artifactDir, "consumer.jsonl"), filepath.Join(artifactDir, "consumer.stderr.log"))
	output, parseErr := parseAgentOutput(result, "consumer")
	if executeErr != nil {
		return result.Duration, output, fmt.Errorf("run consumer: %w", executeErr)
	}
	if parseErr != nil {
		return result.Duration, output, parseErr
	}
	return result.Duration, output, nil
}

func parseAgentOutput(result CommandResult, label string) (harness.AgentOutput, error) {
	if len(result.Output) == 0 {
		return harness.AgentOutput{}, fmt.Errorf("parse %s output: OpenCode output contains no text", label)
	}
	output, err := harness.ParseOpenCodeJSON(bytes.NewReader(result.Output))
	if err != nil {
		return output, fmt.Errorf("parse %s output: %w", label, err)
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
	sharedArtifactDir := filepath.Join(outputDir, "trials", evalCase.ID, "shared")
	return map[string]string{
		"run_id": run.ID, "dataset": run.Dataset, "dataset_revision": run.DatasetRevision,
		"case_id": evalCase.ID, "category": evalCase.Category, "arm": arm,
		"question": evalCase.Question, "expected": evalCase.Expected, "user_id": evalCase.AskingUserID,
		"scope_id": evalCase.ScopeID, "producer_workspace": evalCase.ProducerWorkspace,
		"consumer_workspace": evalCase.ConsumerWorkspace, "artifact_dir": artifactDir,
		"shared_artifact_dir":  sharedArtifactDir,
		"shared_producer_text": filepath.Join(sharedArtifactDir, "producer.txt"),
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
