package knowledgeeval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type KnowledgeEvalSuite struct {
	suite.Suite
	ctx context.Context
	now func() time.Time
}

func TestKnowledgeEvalSuite(t *testing.T) {
	suite.Run(t, new(KnowledgeEvalSuite))
}

func (s *KnowledgeEvalSuite) SetupTest() {
	s.ctx = context.Background()
	current := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		value := current
		current = current.Add(time.Second)
		return value
	}
}

func (s *KnowledgeEvalSuite) TestOpaqueRefValidation() {
	valid := testRef("fixture", "v1", "file:///tmp/fixture", []byte("fixture"))
	cases := []struct {
		name    string
		mutate  func(*OpaqueRef)
		wantErr string
	}{
		{name: "valid"},
		{name: "kind", mutate: func(ref *OpaqueRef) { ref.Kind = "" }, wantErr: "kind"},
		{name: "schema", mutate: func(ref *OpaqueRef) { ref.SchemaVersion = "" }, wantErr: "schema"},
		{name: "uri", mutate: func(ref *OpaqueRef) { ref.URI = "::" }, wantErr: "URI"},
		{name: "digest", mutate: func(ref *OpaqueRef) { ref.SHA256 = "bad" }, wantErr: "SHA-256"},
	}
	for _, testCase := range cases {
		s.Run(testCase.name, func() {
			ref := valid
			if testCase.mutate != nil {
				testCase.mutate(&ref)
			}
			err := ref.Validate()
			if testCase.wantErr == "" {
				s.Require().NoError(err)
				return
			}
			s.Require().ErrorContains(err, testCase.wantErr)
			s.Require().ErrorIs(err, ErrInvalidRecord)
		})
	}
}

func (s *KnowledgeEvalSuite) TestArtifactAndCapabilities() {
	record := ArtifactRecord{
		ArtifactID: "artifact-1", Kind: "wiki", WorldID: "world-1",
		GroupID: "group-1", CheckpointID: "checkpoint-1",
		Payload:   testRef("wiki", "v1", "file:///tmp/wiki", []byte("wiki")),
		CreatedAt: s.now(),
	}
	s.Require().NoError(record.Validate())
	record.GroupID = ""
	s.Require().ErrorIs(record.Validate(), ErrInvalidRecord)

	capabilities := CapabilitySet{{Name: SearchCapability, Version: "v1"}}
	s.Require().NoError(capabilities.Supports(Capability{Name: SearchCapability, Version: "v1"}))
	s.Require().ErrorIs(
		capabilities.Supports(Capability{Name: SearchCapability, Version: "v2"}),
		ErrCapabilityVersion,
	)
	s.Require().ErrorIs(
		capabilities.Supports(Capability{Name: GetCapability, Version: "v1"}),
		ErrCapabilityMissing,
	)
	s.Equal(capabilities, capabilities.Clone())
}

func (s *KnowledgeEvalSuite) TestMemoryRunLifecycleAndRetry() {
	store := NewMemoryRunStore(s.now)
	run := Run{ID: "run-1", WorldID: "world", GroupID: "group", CheckpointID: "cp"}
	s.Require().NoError(store.CreateRun(s.ctx, run))
	s.Require().ErrorIs(store.CreateRun(s.ctx, run), ErrConflict)

	trial := Trial{
		ID: "trial-1", RunID: run.ID, CaseID: "case",
		BenchmarkID: "quality", BenchmarkFingerprint: "quality:v1",
	}
	s.Require().NoError(store.AddTrial(s.ctx, trial))
	attempt, err := store.StartAttempt(s.ctx, trial.ID)
	s.Require().NoError(err)
	s.Require().NoError(store.AdvanceAttempt(s.ctx, attempt.ID, StageEvaluating, "evaluate"))
	s.Require().ErrorIs(
		store.AdvanceAttempt(s.ctx, attempt.ID, StageBuilding, "backward"),
		ErrInvalidTransition,
	)
	s.Require().NoError(store.FailAttempt(s.ctx, attempt.ID, StageEvaluating, errors.New("transient")))

	retry, err := store.StartAttempt(s.ctx, trial.ID)
	s.Require().NoError(err)
	s.Equal(2, retry.Number)
	s.Require().NoError(store.AdvanceAttempt(s.ctx, retry.ID, StageEvaluating, "retry"))
	result := BenchmarkResult{
		Status:  "completed",
		Metrics: []Metric{{Name: "score", Value: 1, Unit: "ratio"}},
	}
	s.Require().NoError(store.CompleteAttempt(s.ctx, retry.ID, result))

	ineligible := Trial{
		ID: "trial-2", RunID: run.ID, CaseID: "case",
		BenchmarkID: "tester", BenchmarkFingerprint: "tester:v1",
	}
	s.Require().NoError(store.AddTrial(s.ctx, ineligible))
	s.Require().NoError(store.MarkIneligible(s.ctx, ineligible.ID, "missing recall/search:v1"))
	s.Require().NoError(store.FinishRun(s.ctx, run.ID))

	detail, err := store.GetRun(s.ctx, run.ID)
	s.Require().NoError(err)
	s.Equal(RunStatusCompleted, detail.Run.Status)
	s.Len(detail.Trials, 2)
	s.Len(detail.Attempts, 2)
	s.GreaterOrEqual(len(detail.Events), 9)
	s.Equal(TrialStatusIneligible, detail.Trials[1].Status)
}

func (s *KnowledgeEvalSuite) TestMemoryRunStoreRejectsInvalidState() {
	store := NewMemoryRunStore(s.now)
	s.Require().ErrorIs(
		store.CreateRun(s.ctx, Run{ID: "missing"}),
		ErrInvalidRecord,
	)
	s.Require().ErrorIs(
		store.AddTrial(s.ctx, Trial{ID: "trial", RunID: "missing"}),
		ErrNotFound,
	)
	s.Require().ErrorIs(store.FinishRun(s.ctx, "missing"), ErrNotFound)

	run := Run{ID: "run", WorldID: "world", GroupID: "group", CheckpointID: "cp"}
	s.Require().NoError(store.CreateRun(s.ctx, run))
	s.Require().ErrorIs(store.FinishRun(s.ctx, run.ID), ErrInvalidTransition)
	_, err := store.GetRun(s.ctx, "missing")
	s.Require().ErrorIs(err, ErrNotFound)
}

func (s *KnowledgeEvalSuite) TestArtifactStoreBytesAndDirectory() {
	store, err := NewArtifactStore(s.T().TempDir())
	s.Require().NoError(err)

	ref, err := store.PutBytes(s.ctx, "report", "v1", []byte("hello"))
	s.Require().NoError(err)
	content, err := store.OpenBytes(s.ctx, ref)
	s.Require().NoError(err)
	s.Equal("hello", string(content))
	duplicate, err := store.PutBytes(s.ctx, "report", "v1", []byte("hello"))
	s.Require().NoError(err)
	s.Equal(ref.SHA256, duplicate.SHA256)

	source := s.T().TempDir()
	s.Require().NoError(os.Mkdir(filepath.Join(source, "wiki"), 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(source, "wiki", "index.md"), []byte("# Home"), 0o644))
	treeRef, err := store.PutDirectory(s.ctx, "llmwiki", "v1", source)
	s.Require().NoError(err)
	tree, err := store.OpenDirectory(s.ctx, treeRef)
	s.Require().NoError(err)
	s.FileExists(filepath.Join(tree, "wiki", "index.md"))

	s.Require().NoError(os.WriteFile(filepath.Join(tree, "wiki", "index.md"), []byte("tampered"), 0o644))
	_, err = store.OpenDirectory(s.ctx, treeRef)
	s.Require().ErrorIs(err, ErrInvalidRecord)
}

func (s *KnowledgeEvalSuite) TestArtifactStoreRejectsSymlinkAndEscape() {
	store, err := NewArtifactStore(s.T().TempDir())
	s.Require().NoError(err)
	source := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(source, "target"), []byte("x"), 0o644))
	s.Require().NoError(os.Symlink(filepath.Join(source, "target"), filepath.Join(source, "link")))
	_, err = store.PutDirectory(s.ctx, "tree", "v1", source)
	s.Require().ErrorIs(err, ErrInvalidRecord)

	escape := testRef("file", "v1", "file:///tmp/outside", []byte("outside"))
	_, err = store.OpenBytes(s.ctx, escape)
	s.Require().ErrorIs(err, ErrInvalidRecord)
}

func (s *KnowledgeEvalSuite) TestPlannerRunnerAndQuerySnapshot() {
	store := NewMemoryRunStore(s.now)
	runner, err := NewRunner(store, s.now)
	s.Require().NoError(err)
	subject := fakeSubject{
		id:           "subject",
		capabilities: CapabilitySet{{Name: SearchCapability, Version: "v1"}},
	}
	success := fakeBenchmark{
		descriptor: BenchmarkDescriptor{
			ID: "search-qa", Version: "v1", BundleDigest: "bundle", ConfigDigest: "config",
			RequiredCapabilities: CapabilitySet{{Name: SearchCapability, Version: "v1"}},
		},
		result: BenchmarkResult{
			Status:  "completed",
			Metrics: []Metric{{Name: "accuracy", Value: 1, Unit: "ratio"}},
		},
	}
	ineligible := fakeBenchmark{
		descriptor: BenchmarkDescriptor{
			ID: "get-qa", Version: "v1", BundleDigest: "bundle", ConfigDigest: "config",
			RequiredCapabilities: CapabilitySet{{Name: GetCapability, Version: "v1"}},
		},
	}
	failure := fakeBenchmark{
		descriptor: BenchmarkDescriptor{
			ID: "broken", Version: "v1", BundleDigest: "bundle", ConfigDigest: "config",
			RequiredCapabilities: CapabilitySet{{Name: SearchCapability, Version: "v1"}},
		},
		err: errors.New("judge unavailable"),
	}
	run := Run{
		ID: "run", WorldID: "world", GroupID: "group",
		CheckpointID: "cp", ArtifactID: "artifact",
	}
	detail, err := runner.Evaluate(
		s.ctx,
		run,
		subject,
		[]BenchmarkAdapter{success, ineligible, failure},
	)
	s.Require().NoError(err)
	s.Equal(RunStatusFailed, detail.Run.Status)
	s.Len(detail.Trials, 3)

	query, err := NewQueryService(store, s.now)
	s.Require().NoError(err)
	runs, err := query.ListRuns(s.ctx)
	s.Require().NoError(err)
	s.Len(runs, 1)
	loaded, err := query.GetRun(s.ctx, run.ID)
	s.Require().NoError(err)
	s.Equal(detail.Run.ID, loaded.Run.ID)
	var encoded bytes.Buffer
	s.Require().NoError(query.Export(s.ctx, &encoded))
	var snapshot QuerySnapshot
	s.Require().NoError(json.Unmarshal(encoded.Bytes(), &snapshot))
	s.Equal("pax.knowledge-eval.query.v1", snapshot.SchemaVersion)
	s.Len(snapshot.Runs, 1)
}

func (s *KnowledgeEvalSuite) TestRegistryAndComparison() {
	registry := NewRegistry()
	builder := fakeBuilder{id: "builder"}
	driver := fakeDriver{id: "driver"}
	benchmark := fakeBenchmark{descriptor: BenchmarkDescriptor{ID: "quality", Version: "v1"}}
	s.Require().NoError(registry.RegisterBuilder(builder))
	s.Require().NoError(registry.RegisterArtifact(driver))
	s.Require().NoError(registry.RegisterBenchmark(benchmark))
	s.Require().ErrorIs(registry.RegisterBuilder(builder), ErrConflict)
	_, err := registry.Builder("builder")
	s.Require().NoError(err)
	_, err = registry.Artifact("driver")
	s.Require().NoError(err)
	_, err = registry.Benchmark("quality")
	s.Require().NoError(err)
	_, err = registry.Benchmark("missing")
	s.Require().ErrorIs(err, ErrNotFound)

	left := comparisonRun("run-left", "fingerprint", 0.5)
	right := comparisonRun("run-right", "fingerprint", 0.8)
	deltas := CompareRuns(left, right)
	s.Require().Len(deltas, 1)
	s.InDelta(0.3, deltas[0].Delta, 0.0001)
	s.True(deltas[0].Comparable)

	right.Trials[0].BenchmarkFingerprint = "changed"
	deltas = CompareRuns(left, right)
	s.Require().Len(deltas, 1)
	s.False(deltas[0].Comparable)
	s.Equal("benchmark fingerprint differs", deltas[0].Reason)
}

func testRef(kind, schema, uri string, content []byte) OpaqueRef {
	digest := sha256.Sum256(content)
	return OpaqueRef{
		Kind: kind, SchemaVersion: schema, URI: uri,
		SHA256: hex.EncodeToString(digest[:]),
	}
}

type fakeSubject struct {
	id           string
	capabilities CapabilitySet
}

func (s fakeSubject) ID() string {
	return s.id
}

func (s fakeSubject) Capabilities() CapabilitySet {
	return s.capabilities.Clone()
}

type fakeBenchmark struct {
	descriptor BenchmarkDescriptor
	result     BenchmarkResult
	err        error
}

func (b fakeBenchmark) Descriptor() BenchmarkDescriptor {
	return b.descriptor
}

func (b fakeBenchmark) Run(context.Context, Subject) (BenchmarkResult, error) {
	return b.result, b.err
}

type fakeBuilder struct {
	id string
}

func (b fakeBuilder) Descriptor() BuilderDescriptor {
	return BuilderDescriptor{ID: b.id, Version: "v1"}
}

func (fakeBuilder) Build(context.Context, BuildRequest) (ArtifactRecord, error) {
	return ArtifactRecord{}, nil
}

type fakeDriver struct {
	id string
}

func (d fakeDriver) Descriptor() ArtifactDriverDescriptor {
	return ArtifactDriverDescriptor{ID: d.id, Version: "v1"}
}

func (fakeDriver) Open(context.Context, ArtifactRecord) (Subject, error) {
	return fakeSubject{id: "subject"}, nil
}

func comparisonRun(runID, fingerprint string, score float64) RunDetail {
	result := BenchmarkResult{
		Status:  "completed",
		Metrics: []Metric{{Name: "accuracy", Value: score, Unit: "ratio"}},
	}
	return RunDetail{
		Run: Run{ID: runID},
		Trials: []Trial{{
			ID: "trial", CaseID: "case", BenchmarkID: "quality",
			BenchmarkFingerprint: fingerprint, Status: TrialStatusCompleted,
			Result: &result,
		}},
	}
}
