package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	llmwikidriver "github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/artifact/llmwiki"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/benchmark/qa"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/stretchr/testify/suite"
)

type DemoSuite struct {
	suite.Suite
	output string
}

func TestDemoSuite(t *testing.T) {
	suite.Run(t, new(DemoSuite))
}

func (s *DemoSuite) SetupTest() {
	s.output = s.T().TempDir()
	s.T().Cleanup(func() {
		s.Require().NoError(makeDirectoriesWritable(s.output))
	})
}

func (s *DemoSuite) TestGeneratesInspectableAcceptanceBundle() {
	bundle, err := Generate(context.Background(), s.output)
	s.Require().NoError(err)
	s.Len(bundle.Artifacts, 4)
	s.Len(bundle.Query.Runs, 4)
	s.Len(bundle.Checklist, 16)
	s.NotEmpty(bundle.Comparison)

	var answerDelta float64
	for _, delta := range bundle.Comparison {
		if delta.Metric == "answer_accuracy" {
			answerDelta = delta.Delta
		}
	}
	s.InDelta(1.0, answerDelta, 0.0001)
	for _, artifact := range bundle.Artifacts {
		s.NotEmpty(artifact.Views)
		for _, relative := range artifact.Views {
			_, err := os.Stat(filepath.Join(s.output, relative))
			s.Require().NoError(err)
		}
	}
	encoded, err := os.ReadFile(filepath.Join(s.output, "eval-lab-demo.json"))
	s.Require().NoError(err)
	s.NotContains(string(encoded), "file://")
	s.Contains(string(encoded), "artifact://sha256/")
	var persisted AcceptanceBundle
	s.Require().NoError(json.Unmarshal(encoded, &persisted))
	s.Equal(bundle.SchemaVersion, persisted.SchemaVersion)
}

func (s *DemoSuite) TestRequiresOutputDirectory() {
	_, err := Generate(context.Background(), "")
	s.Require().Error(err)
}

func (s *DemoSuite) TestRunsAnswerBlindSessionDatasetBaseline() {
	datasetRoot := filepath.Join(s.output, "dataset-input")
	s.Require().NoError(os.MkdirAll(datasetRoot, 0o755))
	ingest := filepath.Join(datasetRoot, "ingest.jsonl")
	queries := filepath.Join(datasetRoot, "query.jsonl")
	gold := filepath.Join(datasetRoot, "gold.jsonl")
	s.Require().NoError(os.WriteFile(ingest, []byte(
		`{"schema_version":"pax-session-dataset/v1","case_id":"case-1",`+
			`"source_kind":"long-running-conversation","participants":["A","B"],`+
			`"sessions":[{"session_id":"session-1","occurred_at":"2026-07-30T00:00:00Z",`+
			`"turns":[{"dia_id":"D1:1","speaker":"A","text":"The launch code is cedar.",`+
			`"role":"","content":"","blip_caption":"","img_url":null,"query":"","re-download":false}]}],`+
			`"trajectory_ids":[],"trajectory_store":""}`+"\n",
	), 0o644))
	s.Require().NoError(os.WriteFile(queries, []byte(
		`{"case_id":"case-1:qa:1","question":"What is the launch code?",`+
			`"schema_version":"pax-session-dataset/v1","source_case_id":"case-1"}`+"\n",
	), 0o644))
	s.Require().NoError(os.WriteFile(gold, []byte(
		`{"answer":"cedar","case_id":"case-1:qa:1","category":1,`+
			`"evidence_dialog_ids":["D1:1"],"schema_version":"pax-session-dataset/v1"}`+"\n",
	), 0o644))
	output := filepath.Join(s.output, "dataset-output")
	bundle, err := GenerateSessionDataset(context.Background(), SessionDatasetConfig{
		Dataset: "locomo", Partition: "train", CaseID: "case-1",
		IngestPath: ingest, QueryPath: queries, GoldPath: gold,
		QuestionLimit: 1, OutputDirectory: output, RunIDPrefix: "task-42",
	})
	s.Require().NoError(err)
	s.Equal("source-only-baseline", bundle.Mode)
	s.Equal("baseline_only", bundle.BuildStatus)
	s.Equal(1, bundle.Ingest.Sessions)
	s.Equal(1, bundle.Questions)
	s.Require().Len(bundle.Arms, 1)
	s.Equal("source-only", bundle.Arms[0].ID)
	s.Require().Len(bundle.Failures, 1)
	s.Equal(1, bundle.Failures[0].Artifact)
	s.Require().Len(bundle.Query.Runs, 1)
	s.Equal("completed", string(bundle.Query.Runs[0].Run.Status))
	s.Equal("run-task-42-locomo-case-1-source-only", bundle.Query.Runs[0].Run.ID)
	s.Require().FileExists(filepath.Join(output, "dataset-run.json"))
	s.Require().FileExists(filepath.Join(output, bundle.Artifact.Views["native"]))
}

func (s *DemoSuite) TestComparesSourceOnlyAndMaintainerArms() {
	ingest, queries, gold := s.writeDatasetFixture()
	output := filepath.Join(s.output, "comparison-output")
	bundle, err := GenerateSessionDataset(context.Background(), SessionDatasetConfig{
		Dataset: "locomo", Partition: "train", CaseID: "case-1",
		IngestPath: ingest, QueryPath: queries, GoldPath: gold,
		QuestionLimit: 1, OutputDirectory: output, CodeRevision: "revision-3",
		Maintainer: &llmwikidriver.AgentBuilderConfig{
			Model: "test-model", MaxRounds: 2,
			Client: &demoChatClient{response: llm.ChatResponse{
				Message: llm.ChatMessage{Role: "assistant", Content: "Done."},
			}},
		},
	})
	s.Require().NoError(err)
	s.Equal("builder-comparison", bundle.Mode)
	s.Equal("completed", bundle.BuildStatus)
	s.Empty(bundle.Blocker)
	s.Require().Len(bundle.Arms, 2)
	s.Require().Len(bundle.Query.Runs, 2)
	s.NotEmpty(bundle.Comparison)
	s.Require().NotNil(bundle.Arms[1].Artifact)
	s.Equal(
		"llmwiki-maintainer",
		bundle.Arms[1].Artifact.Artifact.Provenance.BuilderID,
	)
	maintainedRun := bundle.Query.Runs[1]
	s.Equal("test-model", maintainedRun.Run.Metadata["model"])
	s.Require().Len(bundle.Failures, 2)
}

func (s *DemoSuite) TestReusesMaintainedArtifactForOnlyAdditionalQuestions() {
	ingest, queries, gold := s.writeDatasetFixture()
	buildOutput := filepath.Join(s.output, "reuse-build")
	built, err := GenerateSessionDataset(context.Background(), SessionDatasetConfig{
		Dataset: "locomo", Partition: "train", CaseID: "case-1",
		IngestPath: ingest, QueryPath: queries, GoldPath: gold,
		QuestionLimit: 1, OutputDirectory: buildOutput, CodeRevision: "revision-3",
		Maintainer: &llmwikidriver.AgentBuilderConfig{
			Model: "test-model", MaxRounds: 2,
			Client: &demoChatClient{response: llm.ChatResponse{
				Message: llm.ChatMessage{Role: "assistant", Content: "Done."},
			}},
		},
	})
	s.Require().NoError(err)
	s.Require().Len(built.Arms, 2)
	s.Require().NotNil(built.Arms[1].Artifact)
	maintained := built.Arms[1].Artifact.Artifact
	maintainedPath := filepath.Join(
		buildOutput,
		"artifacts",
		maintained.Payload.SHA256,
		"tree",
	)
	s.Require().NoError(appendFile(queries,
		`{"case_id":"case-1:qa:2","question":"Repeat the launch code?",`+
			`"schema_version":"pax-session-dataset/v1","source_case_id":"case-1"}`+"\n",
	))
	s.Require().NoError(appendFile(gold,
		`{"answer":"cedar","case_id":"case-1:qa:2","category":1,`+
			`"evidence_dialog_ids":["D1:1"],"schema_version":"pax-session-dataset/v1"}`+"\n",
	))

	evaluationOutput := filepath.Join(s.output, "reuse-evaluation")
	evaluated, err := GenerateSessionDataset(context.Background(), SessionDatasetConfig{
		Dataset: "locomo", Partition: "train", CaseID: "case-1",
		IngestPath: ingest, QueryPath: queries, GoldPath: gold,
		QuestionOffset: 1, QuestionLimit: 1,
		OutputDirectory: evaluationOutput, RunIDPrefix: "task-more",
		ReuseArtifact: &maintained, ReuseArtifactPath: maintainedPath,
	})
	s.Require().NoError(err)
	s.Equal("artifact-evaluation", evaluated.Mode)
	s.Equal(1, evaluated.QuestionOffset)
	s.Equal(2, evaluated.CumulativeQuestions)
	s.Require().Len(evaluated.Arms, 1)
	s.Equal(maintained.ArtifactID, evaluated.Arms[0].Artifact.Artifact.ArtifactID)
	s.Require().Len(evaluated.Query.Runs, 1)
	s.Require().Len(evaluated.Query.Runs[0].Trials, 1)
	s.Equal("knowledge-search-get-qa", evaluated.Query.Runs[0].Trials[0].BenchmarkID)
	s.Equal("case-1:qa:2", evaluated.Query.Runs[0].Trials[0].Result.CaseResults[0].CaseID)
	s.Equal("true", evaluated.Query.Runs[0].Run.Metadata["reused_artifact"])
}

func (s *DemoSuite) TestReportsRoundLimitAsAwaitingContinuation() {
	ingest, queries, gold := s.writeDatasetFixture()
	output := filepath.Join(s.output, "round-limit-output")
	bundle, err := GenerateSessionDataset(context.Background(), SessionDatasetConfig{
		Dataset: "locomo", Partition: "train", CaseID: "case-1",
		IngestPath: ingest, QueryPath: queries, GoldPath: gold,
		QuestionLimit: 1, OutputDirectory: output,
		Maintainer: &llmwikidriver.AgentBuilderConfig{
			Model: "test-model", MaxRounds: 1,
			Client: &demoChatClient{response: llm.ChatResponse{Message: llm.ChatMessage{
				Role: "assistant", ToolCalls: []llm.ToolCall{{
					ID: "write-incomplete", Type: "function", Function: llm.ToolFunction{
						Name: "write_file",
						Arguments: `{"path":"wiki/people/person.md",` +
							`"content":"# Person\n\n[Missing](missing.md)\n"}`,
					},
				}},
			}}},
		},
	})
	s.Require().NoError(err)
	s.Equal("needs_more_rounds", bundle.BuildStatus)
	s.Require().Len(bundle.Arms, 2)
	s.Equal("needs_more_rounds", bundle.Arms[1].BuildStatus)
	s.NotNil(bundle.Arms[1].FailurePayload)
}

func (s *DemoSuite) TestValidatesSessionDatasetInput() {
	_, err := GenerateSessionDataset(context.Background(), SessionDatasetConfig{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = datasetSessionCount(filepath.Join(s.output, "missing.jsonl"), "case")
	s.Require().Error(err)

	records := filepath.Join(s.output, "records.jsonl")
	s.Require().NoError(os.WriteFile(
		records,
		[]byte(`{"case_id":"other","sessions":[{}]}`+"\n"),
		0o644,
	))
	_, err = datasetSessionCount(records, "case")
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)

	queries := filepath.Join(s.output, "queries.jsonl")
	gold := filepath.Join(s.output, "gold.jsonl")
	s.Require().NoError(os.WriteFile(
		queries,
		[]byte(`{"case_id":"case:1","question":"Question","source_case_id":"case"}`+"\n"),
		0o644,
	))
	s.Require().NoError(os.WriteFile(gold, []byte{}, 0o644))
	_, err = loadQACases(queries, gold, "case", 0, 1)
	s.Require().ErrorContains(err, "no gold answer")
	_, err = loadQACases(queries, gold, "other", 0, 1)
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)

	s.Require().NoError(os.WriteFile(
		queries,
		[]byte(`{"case_id":"case","question":"Question"}`+"\n"),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		gold,
		[]byte(`{"case_id":"case","answer":"Answer","category":2}`+"\n"),
		0o644,
	))
	longMemEvalCases, err := loadQACases(queries, gold, "case", 0, 1)
	s.Require().NoError(err)
	s.Require().Len(longMemEvalCases, 1)
	s.Equal("case", longMemEvalCases[0].ID)
	s.Equal("2", longMemEvalCases[0].DatasetCategory)

	_, err = GenerateSessionDataset(context.Background(), SessionDatasetConfig{
		Dataset: "dataset", Partition: "train", CaseID: "case",
		IngestPath: records, QueryPath: queries, GoldPath: gold,
		OutputDirectory: s.output,
	})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
}

func (s *DemoSuite) TestClassifiesGoldAnswerKinds() {
	tests := []struct {
		name   string
		gold   goldRecord
		expect qa.AnswerKind
	}{
		{name: "abstention", gold: goldRecord{Abstention: true}, expect: qa.AnswerUnanswerable},
		{name: "unanswerable type", gold: goldRecord{QuestionType: "unanswerable"}, expect: qa.AnswerUnanswerable},
		{name: "list", gold: goldRecord{Answer: []any{"red", "blue"}}, expect: qa.AnswerList},
		{name: "numeric evaluator", gold: goldRecord{EvalFunction: "number_match"}, expect: qa.AnswerNumeric},
		{name: "temporal question", gold: goldRecord{QuestionType: "temporal-reasoning"}, expect: qa.AnswerTemporal},
		{name: "fact default", gold: goldRecord{Answer: "local-first"}, expect: qa.AnswerFact},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			s.Equal(test.expect, answerKindForGold(test.gold))
		})
	}

	s.Equal(
		[]string{"session:turn:1"},
		supportRefsForGold(goldRecord{EvidenceTurnIDs: []string{"session:turn:1"}}),
	)
}

func (s *DemoSuite) TestReportsFilesystemAndDependencyErrors() {
	fileTarget := filepath.Join(s.output, "file")
	s.Require().NoError(os.WriteFile(fileTarget, []byte("x"), 0o644))
	_, err := Generate(context.Background(), fileTarget)
	s.Require().Error(err)

	blockedViews := filepath.Join(s.output, "blocked-views")
	s.Require().NoError(os.Mkdir(blockedViews, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(blockedViews, "views"), []byte("x"), 0o644))
	_, err = Generate(context.Background(), blockedViews)
	s.Require().Error(err)

	blockedArtifacts := filepath.Join(s.output, "blocked-artifacts")
	s.Require().NoError(os.Mkdir(blockedArtifacts, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(blockedArtifacts, "artifacts"), []byte("x"), 0o644))
	_, err = Generate(context.Background(), blockedArtifacts)
	s.Require().Error(err)

	now := func() time.Time { return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC) }
	_, _, _, err = generateLLMWiki(context.Background(), s.output, nil, now)
	s.Require().Error(err)
	_, _, _, err = generatePageWiki(context.Background(), s.output, nil, now)
	s.Require().Error(err)
	_, _, _, err = generateTeamNote(context.Background(), s.output, nil, now)
	s.Require().Error(err)
	_, err = wikiBenchmarks(nil)
	s.Require().Error(err)
	_, err = exportQuery(context.Background(), nil, now)
	s.Require().Error(err)

	viewlessOutput := filepath.Join(s.output, "viewless")
	store, err := knowledgeeval.NewArtifactStore(filepath.Join(viewlessOutput, "store"))
	s.Require().NoError(err)
	_, _, _, err = generateTeamNote(context.Background(), viewlessOutput, store, now)
	s.Require().Error(err)

	ingest, queries, gold := s.writeDatasetFixture()
	_, err = GenerateSessionDataset(context.Background(), SessionDatasetConfig{
		Dataset: "locomo", Partition: "train", CaseID: "case-1",
		IngestPath: ingest, QueryPath: queries, GoldPath: gold,
		QuestionLimit: 1, OutputDirectory: filepath.Join(s.output, "missing-resume"),
		Maintainer: &llmwikidriver.AgentBuilderConfig{
			Client: &demoChatClient{},
		},
		MaintainerResumePath: filepath.Join(s.output, "does-not-exist"),
	})
	s.Require().ErrorContains(err, "import failed maintainer workspace")
}

func (s *DemoSuite) TestReportsHelperErrors() {
	store, err := knowledgeeval.NewArtifactStore(filepath.Join(s.output, "helper-store"))
	s.Require().NoError(err)
	err = copyView(
		context.Background(),
		store,
		knowledgeeval.ArtifactViewRecord{Payload: knowledgeeval.OpaqueRef{}},
		filepath.Join(s.output, "missing.html"),
	)
	s.Require().Error(err)

	ref, err := store.PutBytes(context.Background(), "view", "v1", []byte("content"))
	s.Require().NoError(err)
	err = copyView(
		context.Background(),
		store,
		knowledgeeval.ArtifactViewRecord{Payload: ref},
		filepath.Join(s.output, "missing", "view.html"),
	)
	s.Require().Error(err)
	err = writeJSON(filepath.Join(s.output, "missing", "bundle.json"), map[string]string{"ok": "yes"})
	s.Require().Error(err)
	err = writeJSON(filepath.Join(s.output, "bad.json"), make(chan int))
	s.Require().Error(err)

	runStore := knowledgeeval.NewMemoryRunStore(time.Now)
	runner, err := knowledgeeval.NewRunner(runStore, time.Now)
	s.Require().NoError(err)
	_, err = evaluate(
		context.Background(),
		runner,
		time.Now,
		"invalid",
		knowledgeeval.ArtifactRecord{},
		bareSubject{},
		nil,
	)
	s.Require().Error(err)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, cleanup, err := buildWorkspace(cancelled, "statement", time.Now)
	if cleanup != nil {
		s.Require().NoError(cleanup())
	}
	s.Require().NoError(err)
}

type bareSubject struct{}

func (bareSubject) ID() string {
	return "bare"
}

func (bareSubject) Capabilities() knowledgeeval.CapabilitySet {
	return nil
}

func (s *DemoSuite) writeDatasetFixture() (string, string, string) {
	root := filepath.Join(s.output, "shared-dataset-input")
	s.Require().NoError(os.MkdirAll(root, 0o755))
	ingest := filepath.Join(root, "ingest.jsonl")
	queries := filepath.Join(root, "query.jsonl")
	gold := filepath.Join(root, "gold.jsonl")
	s.Require().NoError(os.WriteFile(ingest, []byte(
		`{"schema_version":"pax-session-dataset/v1","case_id":"case-1",`+
			`"source_kind":"long-running-conversation","participants":["A","B"],`+
			`"sessions":[{"session_id":"session-1","occurred_at":"2026-07-30T00:00:00Z",`+
			`"turns":[{"dia_id":"D1:1","speaker":"A","text":"The launch code is cedar.",`+
			`"role":"","content":"","blip_caption":"","img_url":null,"query":"","re-download":false}]}],`+
			`"trajectory_ids":[],"trajectory_store":""}`+"\n",
	), 0o644))
	s.Require().NoError(os.WriteFile(queries, []byte(
		`{"case_id":"case-1:qa:1","question":"What is the launch code?",`+
			`"schema_version":"pax-session-dataset/v1","source_case_id":"case-1"}`+"\n",
	), 0o644))
	s.Require().NoError(os.WriteFile(gold, []byte(
		`{"answer":"cedar","case_id":"case-1:qa:1","category":1,`+
			`"evidence_dialog_ids":["D1:1"],"schema_version":"pax-session-dataset/v1"}`+"\n",
	), 0o644))
	return ingest, queries, gold
}

type demoChatClient struct {
	response llm.ChatResponse
}

func appendFile(path, content string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return fmt.Errorf("append fixture: %w", errors.Join(err, closeErr))
		}
		return err
	}
	return file.Close()
}

func (c *demoChatClient) Complete(
	context.Context,
	llm.ChatRequest,
) (llm.ChatResponse, error) {
	return c.response, nil
}
