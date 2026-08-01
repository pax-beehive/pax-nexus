package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
)

type registrySuite struct {
	suite.Suite
	root *Registry
}

func TestRegistry(t *testing.T) {
	suite.Run(t, new(registrySuite))
}

func (s *registrySuite) SetupTest() {
	root := s.T().TempDir()
	bundleRoot := filepath.Join(root, "one")
	s.Require().NoError(os.MkdirAll(filepath.Join(bundleRoot, "views"), 0o755))
	treeRoot := filepath.Join(
		bundleRoot,
		"artifacts",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"tree",
		"wiki",
	)
	s.Require().NoError(os.MkdirAll(treeRoot, 0o755))
	s.Require().NoError(os.WriteFile(
		filepath.Join(bundleRoot, "views", "wiki.html"),
		[]byte(`<html><a href="/">Wiki</a><a href="/wiki/caroline.md">Caroline</a></html>`),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(treeRoot, "index.md"),
		[]byte("# Wiki\n\n- [Caroline](caroline.md)\n"),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(treeRoot, "caroline.md"),
		[]byte("# Caroline\n\nProfile page.\n"),
		0o644,
	))
	sourceRoot := filepath.Join(filepath.Dir(treeRoot), "sources")
	s.Require().NoError(os.MkdirAll(sourceRoot, 0o755))
	s.Require().NoError(os.WriteFile(
		filepath.Join(sourceRoot, "session-1.md"),
		[]byte(
			"# Immutable Session Source\n\n"+
				"- Session: `conv-26:session_1`\n"+
				"- Turn range: `[0,18)`\n\n"+
				"## user\n\nHello.\n",
		),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(bundleRoot, "dataset-run.json"),
		[]byte(registryBundle),
		0o644,
	))
	registry, err := NewRegistry(root)
	s.Require().NoError(err)
	s.root = registry
}

func (s *registrySuite) TestLoadCatalogAndView() {
	catalog, err := s.root.Load(context.Background())
	s.Require().NoError(err)
	s.Len(catalog.Datasets, 1)
	s.Len(catalog.Solutions, 1)
	s.Len(catalog.Runs, 1)
	s.Len(catalog.Artifacts, 1)
	s.Len(catalog.Benchmarks, 3)
	s.Equal("locomo/train/conv-26", catalog.Datasets[0].ID)
	s.Equal("run-1", catalog.Runs[0].Detail.Run.ID)
	s.True(catalog.Benchmarks[0].Executed)

	content, contentType, err := s.root.OpenArtifactView(
		context.Background(),
		"artifact-1",
		"native",
		"",
	)
	s.Require().NoError(err)
	s.Contains(
		string(content),
		`href="/v1/knowledge-eval/artifacts/artifact-1/views/native?path=wiki/caroline.md"`,
	)
	s.Contains(contentType, "text/html")

	content, contentType, err = s.root.OpenArtifactView(
		context.Background(),
		"artifact-1",
		"native",
		"wiki/caroline.md",
	)
	s.Require().NoError(err)
	s.Contains(string(content), "<h1>Caroline</h1>")
	s.Contains(
		string(content),
		`href="/v1/knowledge-eval/artifacts/artifact-1/views/native"`,
	)
	s.Contains(contentType, "text/html")

	dataset, runIDs, err := s.root.GetDataset(
		context.Background(),
		"locomo",
		"train",
		"conv-26",
	)
	s.Require().NoError(err)
	s.Equal("artifact-1", dataset.SourceArtifact.Record.ArtifactID)
	s.Equal([]string{"run-1"}, runIDs)

	sessions, err := s.root.ListDatasetSessions(
		context.Background(),
		"locomo",
		"train",
		"conv-26",
	)
	s.Require().NoError(err)
	s.Require().Len(sessions, 1)
	s.Equal("conv-26:session_1", sessions[0].ID)
	s.Equal(18, sessions[0].Turns)

	content, contentType, err = s.root.OpenDatasetSession(
		context.Background(),
		"locomo",
		"train",
		"conv-26",
		"conv-26:session_1",
	)
	s.Require().NoError(err)
	s.Contains(string(content), "Immutable Session Source")
	s.Contains(contentType, "text/html")
}

func (s *registrySuite) TestCachesCatalogUntilTTLExpires() {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	registry, err := NewRegistry(
		s.root.root,
		WithCatalogCacheTTL(time.Minute),
		withRegistryClock(func() time.Time { return now }),
	)
	s.Require().NoError(err)

	first, err := registry.Load(context.Background())
	s.Require().NoError(err)
	s.Require().Len(first.Datasets, 1)
	s.Require().NoError(os.Remove(filepath.Join(s.root.root, "one", "dataset-run.json")))

	cached, err := registry.Load(context.Background())
	s.Require().NoError(err)
	s.Len(cached.Datasets, 1)

	now = now.Add(2 * time.Minute)
	refreshed, err := registry.Load(context.Background())
	s.Require().NoError(err)
	s.Empty(refreshed.Datasets)
}

func (s *registrySuite) TestLoadsPreparedDatasetFamiliesAndUnrunGroups() {
	preparedRoot := s.writePreparedCatalog()
	registry, err := NewRegistry(s.root.root, WithPreparedRoot(preparedRoot))
	s.Require().NoError(err)

	catalog, err := registry.Load(context.Background())
	s.Require().NoError(err)
	s.Require().Len(catalog.Families, 1)
	family := catalog.Families[0]
	s.Equal("locomo", family.ID)
	s.Equal("LoCoMo", family.Name)
	s.Equal("revision-1", family.Revision)
	s.Equal("conversation", family.GroupKind)
	s.Equal(3, family.GroupCount)
	s.Equal(1, family.RunGroupCount)
	s.Equal(1, family.RunCount)
	s.Equal(1, family.ArtifactCount)
	s.Equal(
		[]DatasetPartition{
			{Name: "holdout", GroupCount: 1},
			{Name: "train", GroupCount: 2, RunGroupCount: 1},
		},
		family.Partitions,
	)

	s.Require().Len(catalog.Datasets, 3)
	var unrun Dataset
	for _, group := range catalog.Datasets {
		if group.CaseID == "conv-44" {
			unrun = group
		}
	}
	s.Equal("not_run", unrun.Status)
	s.Equal("conversation", unrun.GroupKind)
	s.Equal("long-running-conversation", unrun.SourceKind)
	s.Equal(2, unrun.Sessions)
	s.Equal(3, unrun.Turns)
	s.Equal(1, unrun.Questions)
	s.Equal([]string{"conv-44"}, unrun.CaseIDs)

	group, runIDs, err := registry.GetDataset(
		context.Background(),
		"locomo",
		"train",
		"conv-44",
	)
	s.Require().NoError(err)
	s.Equal("not_run", group.Status)
	s.Empty(runIDs)
}

func (s *registrySuite) TestGroupsSharedTrajectoryCasesByEnvironment() {
	preparedRoot := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(
		filepath.Join(preparedRoot, "manifests"),
		0o755,
	))
	s.Require().NoError(os.MkdirAll(
		filepath.Join(preparedRoot, "train", "longmemeval-v2", "maintainer"),
		0o755,
	))
	s.Require().NoError(os.MkdirAll(
		filepath.Join(preparedRoot, "train", "longmemeval-v2", "reader"),
		0o755,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(preparedRoot, "manifests", "longmemeval-v2.json"),
		[]byte(`{"dataset":"LongMemEval-V2 Small","revision":"revision-2","license":"Apache-2.0"}`),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(
			preparedRoot,
			"train",
			"longmemeval-v2",
			"maintainer",
			"ingest.jsonl",
		),
		[]byte(
			`{"schema_version":"pax-session-dataset/v1","case_id":"question-a","source_kind":"agent-trajectory-haystack","trajectory_ids":["one","two"],"trajectory_store":"trajectories.jsonl"}`+"\n"+
				`{"schema_version":"pax-session-dataset/v1","case_id":"question-b","source_kind":"agent-trajectory-haystack","trajectory_ids":["one","two"],"trajectory_store":"trajectories.jsonl"}`+"\n"+
				`{"schema_version":"pax-session-dataset/v1","case_id":"question-c","source_kind":"agent-trajectory-haystack","trajectory_ids":["three"],"trajectory_store":"trajectories.jsonl"}`+"\n",
		),
		0o644,
	))
	s.Require().NoError(os.MkdirAll(
		filepath.Join(preparedRoot, "holdout", "longmemeval-v2", "maintainer"),
		0o755,
	))
	s.Require().NoError(os.MkdirAll(
		filepath.Join(preparedRoot, "holdout", "longmemeval-v2", "reader"),
		0o755,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(
			preparedRoot,
			"holdout",
			"longmemeval-v2",
			"maintainer",
			"ingest.jsonl",
		),
		[]byte(
			`{"schema_version":"pax-session-dataset/v1","case_id":"question-d","source_kind":"agent-trajectory-haystack","trajectory_ids":["one","two"],"trajectory_store":"trajectories.jsonl"}`+"\n"+
				`{"schema_version":"pax-session-dataset/v1","case_id":"question-e","source_kind":"agent-trajectory-haystack","trajectory_ids":["three"],"trajectory_store":"trajectories.jsonl"}`+"\n",
		),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(
			preparedRoot,
			"holdout",
			"longmemeval-v2",
			"reader",
			"query.jsonl",
		),
		[]byte(
			`{"case_id":"question-d","question":"D?"}`+"\n"+
				`{"case_id":"question-e","question":"E?"}`+"\n",
		),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(
			preparedRoot,
			"train",
			"longmemeval-v2",
			"reader",
			"query.jsonl",
		),
		[]byte(
			`{"case_id":"question-a","question":"A?"}`+"\n"+
				`{"case_id":"question-b","question":"B?"}`+"\n"+
				`{"case_id":"question-c","question":"C?"}`+"\n",
		),
		0o644,
	))
	registry, err := NewRegistry(s.root.root, WithPreparedRoot(preparedRoot))
	s.Require().NoError(err)

	catalog, err := registry.Load(context.Background())
	s.Require().NoError(err)
	var family DatasetFamily
	var shared Dataset
	for _, item := range catalog.Families {
		if item.ID == "longmemeval-v2" {
			family = item
		}
	}
	for _, item := range catalog.Datasets {
		if item.Name == "longmemeval-v2" &&
			item.Partition == "train" &&
			len(item.CaseIDs) == 2 {
			shared = item
		}
	}
	s.Equal(2, family.GroupCount)
	s.Equal("environment", shared.GroupKind)
	s.Equal("not_run", shared.Status)
	s.Equal(2, shared.Trajectories)
	s.Equal(2, shared.EvaluationCases)
	s.Equal([]string{"question-a", "question-b"}, shared.CaseIDs)
}

func (s *registrySuite) TestErrors() {
	_, err := NewRegistry(filepath.Join(s.T().TempDir(), "missing"))
	s.Require().Error(err)
	fileRoot := filepath.Join(s.T().TempDir(), "file")
	s.Require().NoError(os.WriteFile(fileRoot, []byte("x"), 0o644))
	_, err = NewRegistry(fileRoot)
	s.Require().Error(err)

	_, _, err = s.root.OpenArtifactView(context.Background(), "missing", "native", "")
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)
	_, _, err = s.root.OpenArtifactView(context.Background(), "artifact-1", "missing", "")
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)
	_, _, err = s.root.OpenArtifactView(
		context.Background(),
		"artifact-1",
		"native",
		"wiki/missing.md",
	)
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)
	_, _, err = s.root.OpenArtifactView(
		context.Background(),
		"artifact-1",
		"native",
		"../AGENTS.md",
	)
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, _, err = s.root.GetDataset(context.Background(), "missing", "train", "case")
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)
	_, _, err = s.root.OpenDatasetSession(
		context.Background(),
		"locomo",
		"train",
		"conv-26",
		"missing",
	)
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.root.Load(cancelled)
	s.Require().ErrorIs(err, context.Canceled)

	_, err = NewRegistry(s.root.root, WithCatalogCacheTTL(-time.Second))
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = NewRegistry(s.root.root, withRegistryClock(nil))
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
}

func (s *registrySuite) TestRejectsMalformedBundle() {
	root := s.T().TempDir()
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, "dataset-run.json"),
		[]byte(`{"dataset":"locomo"}`),
		0o644,
	))
	registry, err := NewRegistry(root)
	s.Require().NoError(err)
	_, err = registry.Load(context.Background())
	s.ErrorIs(err, knowledgeeval.ErrInvalidRecord)
}

func (s *registrySuite) TestRejectsInvalidPreparedRootAndRecords() {
	_, err := NewRegistry(s.root.root, WithPreparedRoot(
		filepath.Join(s.T().TempDir(), "missing"),
	))
	s.Require().Error(err)

	preparedRoot := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(
		filepath.Join(preparedRoot, "manifests"),
		0o755,
	))
	s.Require().NoError(os.MkdirAll(
		filepath.Join(preparedRoot, "train", "broken", "maintainer"),
		0o755,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(preparedRoot, "manifests", "broken.json"),
		[]byte(`{"dataset":"Broken"}`),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(preparedRoot, "train", "broken", "maintainer", "ingest.jsonl"),
		[]byte(`{"case_id":`),
		0o644,
	))
	registry, err := NewRegistry(s.root.root, WithPreparedRoot(preparedRoot))
	s.Require().NoError(err)
	_, err = registry.Load(context.Background())
	s.Require().Error(err)
}

func (s *registrySuite) TestRejectsInvalidDatasetSourceMetadata() {
	_, err := parseDatasetSession("sources/invalid.md", []byte("# Missing metadata"))
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)

	_, err = artifactTreeRoot(Artifact{Record: knowledgeeval.ArtifactRecord{
		ArtifactID: "artifact-invalid",
		Payload: knowledgeeval.OpaqueRef{
			SHA256: "not-a-digest",
		},
	}})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)

	_, err = parseDatasetSession(
		"sources/reversed.md",
		[]byte("- Session: `session`\n- Turn range: `[2,1)`\n"),
	)
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)

	_, err = artifactTreeRoot(Artifact{
		bundleDirectory: s.T().TempDir(),
		Record: knowledgeeval.ArtifactRecord{
			ArtifactID: "artifact-missing",
			Payload: knowledgeeval.OpaqueRef{
				SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		},
	})
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)
}

func (s *registrySuite) writePreparedCatalog() string {
	preparedRoot := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(
		filepath.Join(preparedRoot, "manifests"),
		0o755,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(preparedRoot, "manifests", "locomo.json"),
		[]byte(`{"dataset":"LoCoMo","revision":"revision-1","license":"CC-BY-NC-4.0"}`),
		0o644,
	))
	partitions := map[string]string{
		"train": `{"schema_version":"pax-session-dataset/v1","case_id":"conv-26","source_kind":"long-running-conversation","sessions":[{"session_id":"conv-26:session_1","turns":[{"dia_id":"D1:1"}]}]}` + "\n" +
			`{"schema_version":"pax-session-dataset/v1","case_id":"conv-44","source_kind":"long-running-conversation","sessions":[{"session_id":"conv-44:session_1","turns":[{"dia_id":"D1:1"}]},{"session_id":"conv-44:session_2","turns":[{"dia_id":"D2:1"},{"dia_id":"D2:2"}]}]}` + "\n",
		"holdout": `{"schema_version":"pax-session-dataset/v1","case_id":"conv-30","source_kind":"long-running-conversation","sessions":[{"session_id":"conv-30:session_1","turns":[{"dia_id":"D1:1"}]}]}` + "\n",
	}
	queries := map[string]string{
		"train": `{"case_id":"conv-26:qa:1","source_case_id":"conv-26","question":"One?"}` + "\n" +
			`{"case_id":"conv-44:qa:1","source_case_id":"conv-44","question":"Two?"}` + "\n",
		"holdout": `{"case_id":"conv-30:qa:1","source_case_id":"conv-30","question":"Three?"}` + "\n",
	}
	for partition, ingest := range partitions {
		root := filepath.Join(preparedRoot, partition, "locomo")
		s.Require().NoError(os.MkdirAll(
			filepath.Join(root, "maintainer"),
			0o755,
		))
		s.Require().NoError(os.MkdirAll(
			filepath.Join(root, "reader"),
			0o755,
		))
		s.Require().NoError(os.WriteFile(
			filepath.Join(root, "maintainer", "ingest.jsonl"),
			[]byte(ingest),
			0o644,
		))
		s.Require().NoError(os.WriteFile(
			filepath.Join(root, "reader", "query.jsonl"),
			[]byte(queries[partition]),
			0o644,
		))
	}
	return preparedRoot
}

const registryBundle = `{
  "schema_version":"v1",
  "generated_at":"2026-07-30T13:00:41Z",
  "dataset":"locomo",
  "partition":"train",
  "case_id":"conv-26",
  "build_status":"completed",
  "ingest":{"sessions":19,"turns":419,"sources":19},
  "questions":5,
  "arms":[{
    "id":"maintained",
    "role":"candidate",
    "build_status":"completed",
    "run_id":"run-1",
    "artifact":{
      "product":"maintained",
      "artifact":{
        "artifact_id":"artifact-1",
        "kind":"llmwiki-workspace",
        "world_id":"locomo",
        "group_id":"locomo-conv-26",
        "checkpoint_id":"train-conv-26",
        "payload":{"kind":"workspace","schema_version":"v1","uri":"artifact://one","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
        "provenance":{"builder_id":"maintainer","builder_version":"v1","code_revision":"rev","config_digest":"","seed":0},
        "created_at":"2026-07-30T13:00:20Z"
      },
      "views":{"native":"views/wiki.html"}
    }
  }],
  "query":{
    "schema_version":"v1",
    "generated_at":"2026-07-30T13:00:40Z",
    "runs":[{
      "run":{"id":"run-1","world_id":"locomo","group_id":"locomo-conv-26","checkpoint_id":"train-conv-26","artifact_id":"artifact-1","status":"completed","created_at":"2026-07-30T13:00:20Z"},
      "trials":[{"id":"trial-1","run_id":"run-1","case_id":"case","benchmark_id":"knowledge-search-get-qa","benchmark_fingerprint":"qa:v1","status":"completed","result":{"status":"passed","metrics":[{"name":"answer_accuracy","value":1,"unit":"ratio"}],"case_results":[]}}],
      "attempts":[],
      "events":[]
    }]
  },
  "failures":[]
}`
