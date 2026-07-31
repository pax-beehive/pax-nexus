package tester

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
)

const (
	BenchmarkID      = "knowledge-tester-agent"
	BenchmarkVersion = "v1"
)

type Step struct {
	Tool     string `json:"tool"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
}

type Task struct {
	ID    string `json:"id"`
	Steps []Step `json:"steps"`
}

type Config struct {
	Tasks    []Task `json:"tasks"`
	MaxSteps int    `json:"max_steps"`
}

type Adapter struct {
	store      *knowledgeeval.ArtifactStore
	config     Config
	descriptor knowledgeeval.BenchmarkDescriptor
}

type stepObservation struct {
	TaskID   string `json:"task_id"`
	Step     int    `json:"step"`
	Tool     string `json:"tool"`
	Input    string `json:"input"`
	Output   string `json:"output"`
	Expected string `json:"expected"`
	Passed   bool   `json:"passed"`
}

func NewAdapter(store *knowledgeeval.ArtifactStore, config Config) (*Adapter, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: artifact store is required", knowledgeeval.ErrInvalidRecord)
	}
	if len(config.Tasks) == 0 {
		return nil, fmt.Errorf("%w: tester tasks are required", knowledgeeval.ErrInvalidRecord)
	}
	if config.MaxSteps <= 0 {
		config.MaxSteps = 20
	}
	required := make(knowledgeeval.CapabilitySet, 0, 4)
	seen := make(map[string]struct{})
	for _, task := range config.Tasks {
		if strings.TrimSpace(task.ID) == "" || len(task.Steps) == 0 {
			return nil, fmt.Errorf("%w: tester task ID and steps are required", knowledgeeval.ErrInvalidRecord)
		}
		for _, step := range task.Steps {
			capability, err := capabilityForTool(step.Tool)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(step.Input) == "" || strings.TrimSpace(step.Expected) == "" {
				return nil, fmt.Errorf("%w: tester step input and expectation are required", knowledgeeval.ErrInvalidRecord)
			}
			if _, exists := seen[capability.Name]; !exists {
				required = append(required, capability)
				seen[capability.Name] = struct{}{}
			}
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode tester benchmark config: %w", err)
	}
	return &Adapter{
		store: store, config: config,
		descriptor: knowledgeeval.BenchmarkDescriptor{
			ID: BenchmarkID, Version: BenchmarkVersion,
			RequiredCapabilities: required,
			BundleDigest:         digest([]byte(BenchmarkID + ":" + BenchmarkVersion)),
			ConfigDigest:         digest(encoded),
		},
	}, nil
}

func (a *Adapter) Descriptor() knowledgeeval.BenchmarkDescriptor {
	return a.descriptor
}

func (a *Adapter) Run(
	ctx context.Context,
	subject knowledgeeval.Subject,
) (knowledgeeval.BenchmarkResult, error) {
	observations := make([]stepObservation, 0)
	cases := make([]knowledgeeval.CaseResult, 0, len(a.config.Tasks))
	passedTasks := 0
	for _, task := range a.config.Tasks {
		result, taskObservations, err := a.runTask(ctx, subject, task)
		if err != nil {
			return knowledgeeval.BenchmarkResult{}, err
		}
		observations = append(observations, taskObservations...)
		cases = append(cases, result)
		if result.Correct {
			passedTasks++
		}
	}
	metrics := []knowledgeeval.Metric{
		{Name: "task_success_rate", Value: ratio(passedTasks, len(cases)), Unit: "ratio"},
		{Name: "task_count", Value: float64(len(cases)), Unit: "count"},
		{Name: "tool_call_count", Value: float64(len(observations)), Unit: "count"},
	}
	return a.publish(ctx, passedTasks == len(cases), metrics, cases, observations)
}

func (a *Adapter) runTask(
	ctx context.Context,
	subject knowledgeeval.Subject,
	task Task,
) (knowledgeeval.CaseResult, []stepObservation, error) {
	observations := make([]stepObservation, 0, len(task.Steps))
	evidence := make([]string, 0, len(task.Steps))
	if len(task.Steps) > a.config.MaxSteps {
		return knowledgeeval.CaseResult{
			CaseID: task.ID, Status: "failed", Correct: false,
			FailureStage: "step_budget",
			Actual:       fmt.Sprintf("%d steps exceed budget %d", len(task.Steps), a.config.MaxSteps),
		}, observations, nil
	}
	for index, step := range task.Steps {
		output, err := executeTool(ctx, subject, step)
		if err != nil {
			return knowledgeeval.CaseResult{}, nil, fmt.Errorf(
				"execute tester task %s step %d: %w",
				task.ID,
				index+1,
				err,
			)
		}
		passed := containsNormalized(output, step.Expected)
		observations = append(observations, stepObservation{
			TaskID: task.ID, Step: index + 1, Tool: step.Tool,
			Input: step.Input, Output: output, Expected: step.Expected, Passed: passed,
		})
		evidence = append(evidence, fmt.Sprintf("%s:%s", step.Tool, output))
		if !passed {
			return knowledgeeval.CaseResult{
				CaseID: task.ID, Status: "failed", Correct: false,
				Expected: step.Expected, Actual: output, FailureStage: "agent_assertion",
				Evidence: evidence,
			}, observations, nil
		}
	}
	return knowledgeeval.CaseResult{
		CaseID: task.ID, Status: "passed", Correct: true, Evidence: evidence,
	}, observations, nil
}

func executeTool(
	ctx context.Context,
	subject knowledgeeval.Subject,
	step Step,
) (string, error) {
	switch step.Tool {
	case "search":
		searcher, ok := subject.(knowledgeeval.Searcher)
		if !ok {
			return "", fmt.Errorf("%w: Search", knowledgeeval.ErrCapabilityMissing)
		}
		response, err := searcher.Search(ctx, knowledgeeval.SearchRequest{
			Query: step.Input, MaxItems: 5, TokenBudget: 2000,
		})
		if err != nil {
			return "", err
		}
		parts := make([]string, 0, len(response.Hits))
		for _, hit := range response.Hits {
			parts = append(parts, hit.Ref+" "+hit.Text)
		}
		return strings.Join(parts, "\n"), nil
	case "get":
		getter, ok := subject.(knowledgeeval.Getter)
		if !ok {
			return "", fmt.Errorf("%w: Get", knowledgeeval.ErrCapabilityMissing)
		}
		response, err := getter.Get(ctx, knowledgeeval.GetRequest{Ref: step.Input})
		if err != nil {
			return "", err
		}
		return response.Ref + " " + response.Text, nil
	case "navigate":
		navigator, ok := subject.(knowledgeeval.Navigator)
		if !ok {
			return "", fmt.Errorf("%w: Navigate", knowledgeeval.ErrCapabilityMissing)
		}
		response, err := navigator.Navigate(ctx, knowledgeeval.NavigateRequest{Ref: step.Input})
		if err != nil {
			return "", err
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			return "", fmt.Errorf("encode navigation response: %w", err)
		}
		return string(encoded), nil
	case "recall":
		recaller, ok := subject.(knowledgeeval.PassiveRecaller)
		if !ok {
			return "", fmt.Errorf("%w: PassiveRecall", knowledgeeval.ErrCapabilityMissing)
		}
		response, err := recaller.Recall(ctx, knowledgeeval.PassiveRecallRequest{
			Query: step.Input, MaxItems: 5, TokenBudget: 2000,
		})
		if err != nil {
			return "", err
		}
		parts := make([]string, 0, len(response.Items))
		for _, item := range response.Items {
			parts = append(parts, item.Ref+" "+item.Text)
		}
		return strings.Join(parts, "\n"), nil
	default:
		return "", fmt.Errorf("%w: tester tool %q", knowledgeeval.ErrInvalidRecord, step.Tool)
	}
}

func (a *Adapter) publish(
	ctx context.Context,
	passed bool,
	metrics []knowledgeeval.Metric,
	cases []knowledgeeval.CaseResult,
	observations []stepObservation,
) (knowledgeeval.BenchmarkResult, error) {
	encodedObservations, err := json.MarshalIndent(observations, "", "  ")
	if err != nil {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf("encode tester observations: %w", err)
	}
	observationRef, err := a.store.PutBytes(
		ctx, "benchmark-observations", "pax.knowledge-eval.tester.v1", encodedObservations,
	)
	if err != nil {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf("store tester observations: %w", err)
	}
	report := struct {
		Benchmark string                     `json:"benchmark"`
		Metrics   []knowledgeeval.Metric     `json:"metrics"`
		Cases     []knowledgeeval.CaseResult `json:"cases"`
	}{Benchmark: BenchmarkID, Metrics: metrics, Cases: cases}
	encodedReport, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf("encode tester report: %w", err)
	}
	reportRef, err := a.store.PutBytes(
		ctx, "benchmark-report", "pax.knowledge-eval.report.v1", encodedReport,
	)
	if err != nil {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf("store tester report: %w", err)
	}
	status := "failed"
	if passed {
		status = "passed"
	}
	return knowledgeeval.BenchmarkResult{
		Status: status, Metrics: metrics, CaseResults: cases,
		Observations: observationRef, RawReport: reportRef,
	}, nil
}

func capabilityForTool(tool string) (knowledgeeval.Capability, error) {
	names := map[string]string{
		"search":   knowledgeeval.SearchCapability,
		"get":      knowledgeeval.GetCapability,
		"navigate": knowledgeeval.NavigateCapability,
		"recall":   knowledgeeval.PassiveRecallCapability,
	}
	name, exists := names[tool]
	if !exists {
		return knowledgeeval.Capability{}, fmt.Errorf(
			"%w: unsupported tester tool %q",
			knowledgeeval.ErrInvalidRecord,
			tool,
		)
	}
	return knowledgeeval.Capability{Name: name, Version: "v1"}, nil
}

func containsNormalized(haystack, needle string) bool {
	normalize := func(value string) string {
		return strings.Join(strings.Fields(strings.ToLower(value)), " ")
	}
	return strings.Contains(normalize(haystack), normalize(needle))
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
