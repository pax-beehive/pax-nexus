package demo

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
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
		QuestionLimit: 1, OutputDirectory: output,
	})
	s.Require().NoError(err)
	s.Equal("source-only-baseline", bundle.Mode)
	s.Equal("blocked", bundle.BuildStatus)
	s.Equal(1, bundle.Ingest.Sessions)
	s.Equal(1, bundle.Questions)
	s.Require().Len(bundle.Query.Runs, 1)
	s.Equal("completed", string(bundle.Query.Runs[0].Run.Status))
	s.Require().FileExists(filepath.Join(output, "dataset-run.json"))
	s.Require().FileExists(filepath.Join(output, bundle.Artifact.Views["native"]))
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
	_, err = loadQACases(queries, gold, "case", 1)
	s.Require().ErrorContains(err, "no gold answer")
	_, err = loadQACases(queries, gold, "other", 1)
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)

	_, err = GenerateSessionDataset(context.Background(), SessionDatasetConfig{
		Dataset: "dataset", Partition: "train", CaseID: "case",
		IngestPath: records, QueryPath: queries, GoldPath: gold,
		OutputDirectory: s.output,
	})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
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
