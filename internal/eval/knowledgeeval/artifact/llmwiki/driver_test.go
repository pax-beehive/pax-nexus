package llmwiki

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	"github.com/stretchr/testify/suite"
)

type DriverSuite struct {
	suite.Suite
	ctx       context.Context
	store     *knowledgeeval.ArtifactStore
	driver    *Driver
	builder   *DirectoryBuilder
	now       func() time.Time
	workspace string
	storeRoot string
}

func TestDriverSuite(t *testing.T) {
	suite.Run(t, new(DriverSuite))
}

func (s *DriverSuite) SetupTest() {
	s.ctx = context.Background()
	fixed := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }
	var err error
	s.storeRoot = s.T().TempDir()
	s.registerWritableDirectoryCleanup(s.storeRoot)
	s.store, err = knowledgeeval.NewArtifactStore(s.storeRoot)
	s.Require().NoError(err)
	s.driver, err = NewDriver(s.store, s.now)
	s.Require().NoError(err)
	s.builder, err = NewDirectoryBuilder(s.store, s.now, "revision-1")
	s.Require().NoError(err)
	s.workspace = s.validWorkspace("A local-first Wiki uses immutable Sources.")
}

func (s *DriverSuite) TestBuildOpenProjectSearchGetAndNavigate() {
	artifact := s.buildArtifact(s.workspace, nil, "checkpoint-1")
	opened, err := s.driver.Open(s.ctx, artifact)
	s.Require().NoError(err)
	s.Equal(artifact.ArtifactID, opened.ID())
	s.Require().NoError(opened.Capabilities().Supports(knowledgeeval.Capability{
		Name: knowledgeeval.SearchCapability, Version: "v1",
	}))

	projector, ok := opened.(knowledgeeval.Projector)
	s.Require().True(ok)
	projected, err := projector.Project(s.ctx, knowledgeeval.ProjectionRequest{
		Name: knowledgeeval.WikiCorpusCapability, Version: "v1",
	})
	s.Require().NoError(err)
	encoded, err := s.store.OpenBytes(s.ctx, projected.Payload)
	s.Require().NoError(err)
	var corpus knowledgeeval.WikiCorpus
	s.Require().NoError(json.Unmarshal(encoded, &corpus))
	s.Require().Len(corpus.Documents, 1)
	s.Equal("Knowledge Eval", corpus.Documents[0].Title)
	s.NotEmpty(corpus.Documents[0].Citations)

	searcher, ok := opened.(knowledgeeval.Searcher)
	s.Require().True(ok)
	response, err := searcher.Search(s.ctx, knowledgeeval.SearchRequest{
		Query: "immutable Sources", MaxItems: 3, TokenBudget: 1000,
	})
	s.Require().NoError(err)
	s.Require().Len(response.Hits, 1)
	s.Equal("wiki/index.md", response.Hits[0].Ref)
	s.NotEmpty(response.Trace)

	getter, ok := opened.(knowledgeeval.Getter)
	s.Require().True(ok)
	document, err := getter.Get(s.ctx, knowledgeeval.GetRequest{Ref: response.Hits[0].Ref})
	s.Require().NoError(err)
	s.Contains(document.Text, "immutable Sources")
	_, err = getter.Get(s.ctx, knowledgeeval.GetRequest{Ref: "missing"})
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)

	navigator, ok := opened.(knowledgeeval.Navigator)
	s.Require().True(ok)
	navigation, err := navigator.Navigate(s.ctx, knowledgeeval.NavigateRequest{})
	s.Require().NoError(err)
	s.Require().Len(navigation.Roots, 1)
	s.Equal("Knowledge Eval", navigation.Roots[0].Title)

	_, err = searcher.Search(s.ctx, knowledgeeval.SearchRequest{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = projector.Project(s.ctx, knowledgeeval.ProjectionRequest{Name: "other", Version: "v1"})
	s.Require().ErrorIs(err, knowledgeeval.ErrCapabilityMissing)
}

func (s *DriverSuite) TestRendersAllViewsIncludingDiff() {
	base := s.buildArtifact(s.workspace, nil, "checkpoint-1")
	for _, kind := range []string{"native", "canonical", "raw"} {
		s.Run(kind, func() {
			view, err := s.driver.RenderView(s.ctx, knowledgeeval.ArtifactViewRequest{
				Artifact: base, Kind: kind,
			})
			s.Require().NoError(err)
			s.Equal(kind, view.Kind)
			content, err := s.store.OpenBytes(s.ctx, view.Payload)
			s.Require().NoError(err)
			s.Contains(string(content), "<")
		})
	}

	updatedRoot := s.validWorkspace(
		"A local-first Wiki uses immutable Sources and expected-base CAS.",
	)
	updated := s.buildArtifact(updatedRoot, &base, "checkpoint-2")
	diff, err := s.driver.RenderView(s.ctx, knowledgeeval.ArtifactViewRequest{
		Artifact: updated, BaseArtifact: &base, Kind: "diff",
	})
	s.Require().NoError(err)
	content, err := s.store.OpenBytes(s.ctx, diff.Payload)
	s.Require().NoError(err)
	s.Contains(string(content), "modified")
	s.Contains(string(content), "index.md")

	_, err = s.driver.RenderView(s.ctx, knowledgeeval.ArtifactViewRequest{
		Artifact: updated, Kind: "diff",
	})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = s.driver.RenderView(s.ctx, knowledgeeval.ArtifactViewRequest{
		Artifact: updated, Kind: "unknown",
	})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
}

func (s *DriverSuite) TestRejectsWrongSchemaAndInvalidWorkspace() {
	artifact := s.buildArtifact(s.workspace, nil, "checkpoint-1")
	artifact.Payload.SchemaVersion = "other"
	_, err := s.driver.Open(s.ctx, artifact)
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)

	invalid := s.T().TempDir()
	s.Require().NoError(os.Mkdir(filepath.Join(invalid, "wiki"), 0o755))
	s.Require().NoError(os.WriteFile(
		filepath.Join(invalid, "wiki", "index.md"),
		[]byte("# Invalid"),
		0o644,
	))
	ref, err := s.store.PutDirectory(s.ctx, ArtifactKind, ArtifactSchema, invalid)
	s.Require().NoError(err)
	artifact.Payload = ref
	artifact.ArtifactID = "invalid"
	_, err = s.driver.Open(s.ctx, artifact)
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
}

func (s *DriverSuite) validWorkspace(statement string) string {
	root := s.T().TempDir()
	s.registerWritableDirectoryCleanup(root)
	exported := workspace.SessionExport{
		SchemaVersion: workspace.PaxmSessionSchema,
		Agent:         "codex", SessionID: "session-1", Workspace: "/repo",
		Turns: []workspace.SessionTurn{{
			ID: "turn-1", User: "How should the Wiki work?",
			Assistant: statement, CreatedAt: s.now(),
		}},
	}
	encoded, err := json.Marshal(exported)
	s.Require().NoError(err)
	result, err := workspace.Build(s.ctx, workspace.BuildConfig{
		Root: root,
		ReadSession: func(context.Context, string) ([]byte, error) {
			return encoded, nil
		},
	}, workspace.BuildRequest{SessionID: "session-1", TurnStart: 0, TurnEnd: 1})
	s.Require().NoError(err)
	index := "---\ntype: portal\n---\n\n# Knowledge Eval\n\n" +
		"This page explains how the evaluation workspace is organized.\n\n## Architecture\n\n" + statement +
		" [Source](../" + result.Source.Path + "#" + result.Source.Anchors[1].ID + ")\n"
	s.Require().NoError(os.WriteFile(filepath.Join(root, "wiki", "index.md"), []byte(index), 0o644))
	report := workspace.Validate(root)
	s.Require().True(report.Valid, report.String())
	return root
}

func (s *DriverSuite) registerWritableDirectoryCleanup(root string) {
	s.T().Cleanup(func() {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() {
				return nil
			}
			return os.Chmod(path, 0o755)
		})
		s.Require().NoError(err)
	})
}

func (s *DriverSuite) buildArtifact(
	root string,
	base *knowledgeeval.ArtifactRecord,
	checkpoint string,
) knowledgeeval.ArtifactRecord {
	input, err := s.store.PutDirectory(s.ctx, "benchmark-build-input", "v1", root)
	s.Require().NoError(err)
	hidden, err := s.store.PutBytes(s.ctx, "benchmark-eval-input", "v1", []byte("{}"))
	s.Require().NoError(err)
	artifact, err := s.builder.Build(s.ctx, knowledgeeval.BuildRequest{
		Group: knowledgeeval.BenchmarkGroup{
			GroupID: "group", WorldID: "world", CheckpointID: checkpoint,
			BuildInput: input, EvaluationInput: hidden,
		},
		BaseArtifact: base,
	})
	s.Require().NoError(err)
	return artifact
}
