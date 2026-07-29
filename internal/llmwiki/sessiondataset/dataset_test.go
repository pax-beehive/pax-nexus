package sessiondataset_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/llmwiki/sessiondataset"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	"github.com/stretchr/testify/suite"
)

type datasetSuite struct {
	suite.Suite
	root   string
	ingest string
}

func TestDatasetSuite(t *testing.T) {
	suite.Run(t, new(datasetSuite))
}

func (s *datasetSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.T().Cleanup(func() {
		err := os.Chmod(filepath.Join(s.root, "sources"), 0o755)
		if err != nil && !os.IsNotExist(err) {
			s.NoError(err)
		}
	})
	s.ingest = filepath.Join(s.T().TempDir(), "ingest.jsonl")
	content := `{"schema_version":"pax-session-dataset/v1","case_id":"conversation-one","source_kind":"long-running-conversation","participants":["Alex","Blair"],"sessions":[{"session_id":"conversation-one:session-1","occurred_at":"1:56 pm on 8 May, 2023","turns":[{"dia_id":"D1:1","speaker":"Alex","text":"I started a garden."},{"dia_id":"D1:2","speaker":"Blair","text":"Try growing basil."}]},{"session_id":"conversation-one:session-2","occurred_at":"9:55 am on 22 October, 2023","turns":[{"dia_id":"D2:1","speaker":"Alex","text":"The basil is thriving."}]}]}
{"schema_version":"pax-session-dataset/v1","case_id":"memory-one","source_kind":"chat-session-history","sessions":[{"session_id":"memory-session","occurred_at":"2023/05/20 (Sat) 07:46","turns":[{"role":"user","content":"Remember the blue folder."},{"role":"assistant","content":"I will remember it."}]}]}
`
	s.Require().NoError(os.WriteFile(s.ingest, []byte(content), 0o600))
}

func (s *datasetSuite) TestBuildsOneWorldIncrementallyWithoutEvaluatorData() {
	first, err := sessiondataset.Build(context.Background(), sessiondataset.Config{
		IngestPath: s.ingest,
		Root:       s.root,
		CaseID:     "conversation-one",
		Start:      0,
		End:        1,
	})
	s.Require().NoError(err)
	s.Equal("conversation-one", first.CaseID)
	s.Equal("long-running-conversation", first.SourceKind)
	s.Equal(1, first.Sessions)
	s.Equal(2, first.Turns)
	s.Equal(1, first.Sources)

	manifest, err := sessiondataset.SourceMap(s.root)
	s.Require().NoError(err)
	s.Contains(manifest, "D1:1")
	s.Contains(manifest, "D1:2")
	s.NotContains(manifest, "D2:1")
	source, err := os.ReadFile(filepath.Join(
		s.root,
		manifest["D1:1"].SourcePath,
	))
	s.Require().NoError(err)
	s.Contains(string(source), "Alex: I started a garden.")
	s.Contains(string(source), "Blair: Try growing basil.")
	s.NotContains(string(source), "question")
	s.NotContains(string(source), "answer")

	second, err := sessiondataset.Build(context.Background(), sessiondataset.Config{
		IngestPath: s.ingest,
		Root:       s.root,
		CaseID:     "conversation-one",
		Start:      1,
		End:        2,
	})
	s.Require().NoError(err)
	s.Equal(1, second.Sessions)
	s.Equal(1, second.Turns)

	manifest, err = sessiondataset.SourceMap(s.root)
	s.Require().NoError(err)
	s.Contains(manifest, "D1:1")
	s.Contains(manifest, "D2:1")
	s.Len(manifest, 3)
	s.True(workspace.Validate(s.root).Valid)
}

func (s *datasetSuite) TestBuildsLongMemEvalCaseWithStableEvidenceIDs() {
	result, err := sessiondataset.Build(context.Background(), sessiondataset.Config{
		IngestPath: s.ingest,
		Root:       s.root,
		CaseID:     "memory-one",
		Start:      0,
		End:        1,
	})
	s.Require().NoError(err)
	s.Equal(2, result.Turns)

	manifest, err := sessiondataset.SourceMap(s.root)
	s.Require().NoError(err)
	s.Contains(manifest, "memory-session:turn:0")
	s.Contains(manifest, "memory-session:turn:1")
}

func (s *datasetSuite) TestRejectsMixedWorldsLeaksAndInvalidRanges() {
	_, err := sessiondataset.Build(context.Background(), sessiondataset.Config{
		IngestPath: s.ingest,
		Root:       s.root,
		CaseID:     "missing",
		Start:      0,
		End:        1,
	})
	s.Require().ErrorContains(err, "not found")

	_, err = sessiondataset.Build(context.Background(), sessiondataset.Config{
		IngestPath: s.ingest,
		Root:       s.root,
		CaseID:     "conversation-one",
		Start:      0,
		End:        3,
	})
	s.Require().ErrorContains(err, "session range")

	leaked := filepath.Join(s.T().TempDir(), "leaked.jsonl")
	s.Require().NoError(os.WriteFile(
		leaked,
		[]byte(`{"schema_version":"pax-session-dataset/v1","case_id":"leaked","source_kind":"chat-session-history","answer":"secret","sessions":[]}`+"\n"),
		0o600,
	))
	_, err = sessiondataset.Build(context.Background(), sessiondataset.Config{
		IngestPath: leaked,
		Root:       s.root,
		CaseID:     "leaked",
		Start:      0,
		End:        1,
	})
	s.Require().ErrorContains(err, "unknown field")
}

func (s *datasetSuite) TestRejectsMalformedDatasetWorlds() {
	tests := []struct {
		name    string
		content string
		caseID  string
		end     int
		message string
	}{
		{
			name:    "wrong schema",
			content: `{"schema_version":"wrong","case_id":"case","source_kind":"chat-session-history","sessions":[]}`,
			caseID:  "case", end: 1, message: "unsupported",
		},
		{
			name:    "no sessions",
			content: `{"schema_version":"pax-session-dataset/v1","case_id":"case","source_kind":"chat-session-history","sessions":[]}`,
			caseID:  "case", end: 1, message: "no Sessions",
		},
		{
			name: "invalid timestamp",
			content: `{"schema_version":"pax-session-dataset/v1","case_id":"case","source_kind":"chat-session-history","sessions":[` +
				`{"session_id":"one","occurred_at":"yesterday-ish","turns":[{"role":"user","content":"fact"}]}]}`,
			caseID: "case", end: 1, message: "timestamp",
		},
		{
			name: "unsupported role",
			content: `{"schema_version":"pax-session-dataset/v1","case_id":"case","source_kind":"chat-session-history","sessions":[` +
				`{"session_id":"one","occurred_at":"2023-05-20T07:46:00Z","turns":[{"role":"system","content":"fact"}]}]}`,
			caseID: "case", end: 1, message: "unsupported role",
		},
		{
			name: "unknown participant",
			content: `{"schema_version":"pax-session-dataset/v1","case_id":"case","source_kind":"long-running-conversation","participants":["A","B"],"sessions":[` +
				`{"session_id":"one","turns":[{"dia_id":"D1","speaker":"C","text":"fact"}]}]}`,
			caseID: "case", end: 1, message: "unknown speaker",
		},
		{
			name: "trajectory adapter boundary",
			content: `{"schema_version":"pax-session-dataset/v1","case_id":"case","source_kind":"agent-trajectory-haystack","trajectory_ids":["one"],"trajectory_store":"store","sessions":[` +
				`{"session_id":"one","turns":[{"role":"user","content":"fact"}]}]}`,
			caseID: "case", end: 1, message: "trajectory adapter",
		},
		{
			name: "duplicate cases",
			content: `{"schema_version":"pax-session-dataset/v1","case_id":"case","source_kind":"chat-session-history","sessions":[{"session_id":"one","turns":[{"role":"user","content":"fact"}]}]}` + "\n" +
				`{"schema_version":"pax-session-dataset/v1","case_id":"case","source_kind":"chat-session-history","sessions":[{"session_id":"two","turns":[{"role":"user","content":"fact"}]}]}`,
			caseID: "case", end: 1, message: "duplicate dataset case",
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			ingest := filepath.Join(s.T().TempDir(), "ingest.jsonl")
			s.Require().NoError(os.WriteFile(
				ingest,
				[]byte(test.content+"\n"),
				0o600,
			))
			root := s.T().TempDir()
			_, err := sessiondataset.Build(
				context.Background(),
				sessiondataset.Config{
					IngestPath: ingest,
					Root:       root,
					CaseID:     test.caseID,
					Start:      0,
					End:        test.end,
				},
			)
			s.Require().ErrorContains(err, test.message)
		})
	}

	_, err := sessiondataset.Build(context.Background(), sessiondataset.Config{
		IngestPath: s.ingest,
		CaseID:     "conversation-one",
		Start:      0,
		End:        1,
	})
	s.Require().ErrorContains(err, "workspace root")
	_, err = sessiondataset.Build(context.Background(), sessiondataset.Config{
		IngestPath: filepath.Join(s.T().TempDir(), "missing.jsonl"),
		Root:       s.root,
		CaseID:     "case",
		Start:      0,
		End:        1,
	})
	s.Require().ErrorContains(err, "open maintainer ingest")
}
