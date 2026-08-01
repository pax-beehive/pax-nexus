package experimenttask

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	llmwikidriver "github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/artifact/llmwiki"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/benchmark/qa"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/demo"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

var safePathSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type SessionExecutorConfig struct {
	PreparedRoot string
	ResultRoot   string
	APIKey       string
	BaseURL      string
	CodeRevision string
	Instruction  string
}

type SessionExecutor struct {
	preparedRoot string
	resultRoot   string
	apiKey       string
	baseURL      string
	codeRevision string
	instruction  string
}

func NewSessionExecutor(config SessionExecutorConfig) (*SessionExecutor, error) {
	preparedRoot, err := filepath.Abs(config.PreparedRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve prepared root: %w", err)
	}
	resultRoot, err := filepath.Abs(config.ResultRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve result root: %w", err)
	}
	for name, root := range map[string]string{
		"prepared root": preparedRoot,
		"result root":   resultRoot,
	} {
		if strings.TrimSpace(root) == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
	}
	if config.BaseURL == "" {
		config.BaseURL = "https://api.deepseek.com"
	}
	if config.CodeRevision == "" {
		config.CodeRevision = "working-tree"
	}
	if config.Instruction == "" {
		config.Instruction = workspace.DefaultMaintenanceInstruction
	}
	return &SessionExecutor{
		preparedRoot: preparedRoot, resultRoot: resultRoot,
		apiKey: config.APIKey, baseURL: config.BaseURL,
		codeRevision: config.CodeRevision, instruction: config.Instruction,
	}, nil
}

func (e *SessionExecutor) Execute(
	ctx context.Context,
	taskID string,
	request Request,
) (ExecutionResult, error) {
	if err := validateExecuteRequest(taskID, request); err != nil {
		return ExecutionResult{}, err
	}
	base := filepath.Join(e.preparedRoot, request.Partition, request.Dataset)
	outputDirectory := filepath.Join(e.resultRoot, "tasks", taskID)
	config := demo.SessionDatasetConfig{
		Dataset: request.Dataset, Partition: request.Partition,
		CaseID: request.GroupID, RunIDPrefix: taskID,
		IngestPath:    filepath.Join(base, "maintainer", "ingest.jsonl"),
		QueryPath:     filepath.Join(base, "reader", "query.jsonl"),
		GoldPath:      filepath.Join(base, "evaluator", "gold.jsonl"),
		QuestionLimit: request.QuestionLimit, QuestionOffset: request.QuestionOffset,
		OutputDirectory: outputDirectory,
		CodeRevision:    e.codeRevision,
	}
	if request.Mode == ModeMaintainer {
		if err := e.configureMaintainer(&config, request); err != nil {
			return ExecutionResult{}, err
		}
	}
	bundle, err := demo.GenerateSessionDataset(ctx, config)
	result := executionResult(taskID, bundle)
	if err != nil {
		return result, fmt.Errorf("run session dataset task: %w", err)
	}
	if err := validateExecutionBundle(request, bundle); err != nil {
		return result, err
	}
	return result, nil
}

func validateExecuteRequest(taskID string, request Request) error {
	for name, value := range map[string]string{
		"task ID": taskID, "dataset": request.Dataset,
		"partition": request.Partition, "group ID": request.GroupID,
	} {
		if !safePathSegment.MatchString(value) {
			return fmt.Errorf("run task: invalid %s %q", name, value)
		}
	}
	if request.ContinueFromTaskID != "" && !safePathSegment.MatchString(request.ContinueFromTaskID) {
		return fmt.Errorf(
			"run task: invalid continuation task ID %q",
			request.ContinueFromTaskID,
		)
	}
	if request.ReuseArtifactFromTaskID != "" &&
		!safePathSegment.MatchString(request.ReuseArtifactFromTaskID) {
		return fmt.Errorf(
			"run task: invalid reused artifact task ID %q",
			request.ReuseArtifactFromTaskID,
		)
	}
	return nil
}

func (e *SessionExecutor) configureMaintainer(
	config *demo.SessionDatasetConfig,
	request Request,
) error {
	if strings.TrimSpace(e.apiKey) == "" {
		return fmt.Errorf("run task: DEEPSEEK_API_KEY is not configured")
	}
	client := llm.NewDeepSeekClient(llm.DeepSeekConfig{
		BaseURL: e.baseURL,
		APIKey:  e.apiKey,
	})
	reader, err := qa.NewChatReader(client, request.ReaderModel)
	if err != nil {
		return fmt.Errorf("configure task QA reader: %w", err)
	}
	judge, err := qa.NewSemanticAnswerJudge(client, request.ReaderModel)
	if err != nil {
		return fmt.Errorf("configure task semantic answer judge: %w", err)
	}
	config.Reader = reader
	config.Judge = judge
	if request.ReuseArtifactFromTaskID != "" {
		artifact, artifactPath, err := e.reusableArtifact(request.ReuseArtifactFromTaskID)
		if err != nil {
			return err
		}
		config.ReuseArtifact = &artifact
		config.ReuseArtifactPath = artifactPath
	} else {
		config.Maintainer = &llmwikidriver.AgentBuilderConfig{
			Model: request.Model, MaxRounds: request.MaxRounds,
			Instruction: e.instruction, Client: client,
		}
	}
	if request.ContinueFromTaskID != "" {
		resumePath, err := e.failureWorkspacePath(request.ContinueFromTaskID)
		if err != nil {
			return err
		}
		config.MaintainerResumePath = resumePath
	}
	return nil
}

func executionResult(taskID string, bundle demo.SessionDatasetBundle) ExecutionResult {
	result := ExecutionResult{ResultPath: filepath.ToSlash(filepath.Join("tasks", taskID))}
	for _, arm := range bundle.Arms {
		if arm.RunID != "" {
			result.RunIDs = append(result.RunIDs, arm.RunID)
		}
		if arm.Artifact != nil && arm.Artifact.Artifact.ArtifactID != "" {
			result.ArtifactIDs = append(
				result.ArtifactIDs,
				arm.Artifact.Artifact.ArtifactID,
			)
		}
	}
	return result
}

func (e *SessionExecutor) reusableArtifact(
	taskID string,
) (knowledgeeval.ArtifactRecord, string, error) {
	bundlePath := filepath.Join(e.resultRoot, "tasks", taskID, "dataset-run.json")
	encoded, err := os.ReadFile(bundlePath)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, "", fmt.Errorf(
			"read reused artifact task bundle: %w",
			err,
		)
	}
	var bundle demo.SessionDatasetBundle
	if err := json.Unmarshal(encoded, &bundle); err != nil {
		return knowledgeeval.ArtifactRecord{}, "", fmt.Errorf(
			"decode reused artifact task bundle: %w",
			err,
		)
	}
	for _, arm := range bundle.Arms {
		if arm.ID != "maintained" || arm.Artifact == nil || arm.RunID == "" {
			continue
		}
		artifact := arm.Artifact.Artifact
		if err := artifact.Validate(); err != nil {
			return knowledgeeval.ArtifactRecord{}, "", fmt.Errorf(
				"validate reused artifact: %w",
				err,
			)
		}
		root := filepath.Join(
			e.resultRoot,
			"tasks",
			taskID,
			"artifacts",
			artifact.Payload.SHA256,
			"tree",
		)
		info, err := os.Stat(root)
		if err != nil {
			return knowledgeeval.ArtifactRecord{}, "", fmt.Errorf(
				"inspect reused artifact workspace: %w",
				err,
			)
		}
		if !info.IsDir() {
			return knowledgeeval.ArtifactRecord{}, "", fmt.Errorf(
				"%w: reused artifact workspace is not a directory",
				knowledgeeval.ErrInvalidRecord,
			)
		}
		return artifact, root, nil
	}
	return knowledgeeval.ArtifactRecord{}, "", fmt.Errorf(
		"%w: task %s has no completed maintained artifact",
		knowledgeeval.ErrNotFound,
		taskID,
	)
}

func (e *SessionExecutor) failureWorkspacePath(taskID string) (string, error) {
	bundlePath := filepath.Join(e.resultRoot, "tasks", taskID, "dataset-run.json")
	encoded, err := os.ReadFile(bundlePath)
	if err != nil {
		return "", fmt.Errorf("read resume task bundle: %w", err)
	}
	var bundle demo.SessionDatasetBundle
	if err := json.Unmarshal(encoded, &bundle); err != nil {
		return "", fmt.Errorf("decode resume task bundle: %w", err)
	}
	for _, arm := range bundle.Arms {
		if arm.ID != "maintained" || arm.FailurePayload == nil {
			continue
		}
		if err := arm.FailurePayload.Validate(); err != nil {
			return "", fmt.Errorf("validate resume failure payload: %w", err)
		}
		root := filepath.Join(
			e.resultRoot,
			"tasks",
			taskID,
			"artifacts",
			arm.FailurePayload.SHA256,
			"tree",
		)
		info, err := os.Stat(root)
		if err != nil {
			return "", fmt.Errorf("inspect resume failure workspace: %w", err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("resume failure workspace is not a directory")
		}
		return root, nil
	}
	return "", fmt.Errorf("resume task %s has no maintained failure payload", taskID)
}

func validateExecutionBundle(request Request, bundle demo.SessionDatasetBundle) error {
	if request.Mode != ModeMaintainer || bundle.BuildStatus == "completed" {
		return nil
	}
	blocker := strings.TrimSpace(bundle.Blocker)
	if blocker == "" {
		blocker = "maintainer arm did not complete"
	}
	if bundle.BuildStatus == StatusNeedsMoreRounds {
		return fmt.Errorf("%w: %s", ErrRoundLimitReached, blocker)
	}
	return fmt.Errorf(
		"run session dataset task: maintainer bundle is %s: %s",
		bundle.BuildStatus,
		blocker,
	)
}

func APIKeyConfigured() bool {
	return strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) != ""
}
