package effecteval_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/llmwiki/effecteval"
	"github.com/stretchr/testify/suite"
)

type evalSuite struct {
	suite.Suite
	root    string
	fixture string
}

func TestEvalSuite(t *testing.T) {
	suite.Run(t, new(evalSuite))
}

func (s *evalSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.T().Cleanup(func() {
		err := os.Chmod(filepath.Join(s.root, "sources"), 0o755)
		if err != nil && !os.IsNotExist(err) {
			s.NoError(err)
		}
	})
	s.fixture = filepath.Join(s.T().TempDir(), "fixture.json")
	fixture := effecteval.Fixture{
		SchemaVersion: effecteval.SchemaVersion,
		ID:            "test-eval",
		Cases: []effecteval.Case{
			{
				ID: "case-one", Category: "fact",
				Sessions: []effecteval.Session{{
					ID: "session-one",
					Events: []effecteval.Event{{
						ID: "evidence-one", Role: "user",
						Content: "The local demo uses SQLite.",
					}},
				}},
				Evaluator: effecteval.Evaluator{
					Question:           "Which database?",
					AnswerPatterns:     []string{"(?i)uses SQLite"},
					SupportingEventIDs: []string{"evidence-one"},
				},
			},
			{
				ID: "case-two", Category: "preference",
				Sessions: []effecteval.Session{{
					ID: "session-two",
					Events: []effecteval.Event{{
						ID: "evidence-two", Role: "assistant",
						Content: "Prefer small accepted slices.",
					}},
				}},
				Evaluator: effecteval.Evaluator{
					Question:           "Which delivery style?",
					AnswerPatterns:     []string{"(?i)small accepted slices"},
					SupportingEventIDs: []string{"evidence-two"},
				},
			},
		},
	}
	encoded, err := json.Marshal(fixture)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(s.fixture, encoded, 0o600))
}

func (s *evalSuite) TestPreparesOnlySourcesThenScoresPrivateEvaluatorData() {
	prepared, err := effecteval.Prepare(context.Background(), s.fixture, s.root)
	s.Require().NoError(err)
	s.Equal("test-eval", prepared.DatasetID)
	s.Equal(2, prepared.Cases)
	s.Equal(2, prepared.Sources)

	workspaceFiles := s.walkRelative(s.root)
	s.NotContains(workspaceFiles, "eval.json")
	s.NotContains(workspaceFiles, "fixture.json")
	for _, relative := range workspaceFiles {
		content, readErr := os.ReadFile(filepath.Join(s.root, relative))
		if readErr == nil {
			s.NotContains(string(content), "Which database?")
			s.NotContains(string(content), "Which delivery style?")
		}
	}

	manifest, err := effecteval.LoadSourceMap(s.root)
	s.Require().NoError(err)
	first := manifest["evidence-one"]
	page := "# Evaluation Knowledge\n\nThe local demo uses SQLite " +
		"([source](../../" + first.SourcePath + "#" + first.Anchor + ")).\n"
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.root, "wiki/index.md"),
		[]byte("# Wiki\n\n- [Knowledge](pages/knowledge.md)\n"),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.root, "wiki/pages/knowledge.md"),
		[]byte(page),
		0o644,
	))

	report, err := effecteval.Score(s.fixture, s.root)
	s.Require().NoError(err)
	s.Equal(2, report.Cases)
	s.Equal(2, report.RawSourceHits)
	s.Equal(1, report.WikiHits)
	s.Equal(1, report.GroundedWikiHits)
	s.Greater(report.RawSourceBytes, report.WikiBytes)
	s.Require().Len(report.Results, 2)
	s.True(report.Results[0].RawSourceHit)
	s.True(report.Results[0].WikiHit)
	s.True(report.Results[0].Grounded)
	s.False(report.Results[1].WikiHit)
}

func (s *evalSuite) TestRejectsInvalidFixtureAndMissingEvidence() {
	s.Require().NoError(os.WriteFile(s.fixture, []byte(`{"schema_version":"wrong"}`), 0o600))
	_, err := effecteval.Prepare(context.Background(), s.fixture, s.root)
	s.Require().ErrorContains(err, "schema")

	bad := effecteval.Fixture{
		SchemaVersion: effecteval.SchemaVersion,
		ID:            "bad",
		Cases: []effecteval.Case{{
			ID: "bad-case",
			Sessions: []effecteval.Session{{
				ID: "bad-session",
				Events: []effecteval.Event{{
					ID: "event", Role: "system", Content: "unsupported",
				}},
			}},
		}},
	}
	encoded, marshalErr := json.Marshal(bad)
	s.Require().NoError(marshalErr)
	s.Require().NoError(os.WriteFile(s.fixture, encoded, 0o600))
	_, err = effecteval.Prepare(context.Background(), s.fixture, s.root)
	s.Require().ErrorContains(err, "unsupported role")
}

func (s *evalSuite) walkRelative(root string) []string {
	s.T().Helper()
	var result []string
	s.Require().NoError(filepath.Walk(root, func(
		target string,
		info os.FileInfo,
		err error,
	) error {
		if err != nil || info.IsDir() {
			return err
		}
		relative, relErr := filepath.Rel(root, target)
		if relErr != nil {
			return relErr
		}
		result = append(result, filepath.ToSlash(relative))
		return nil
	}))
	return result
}
