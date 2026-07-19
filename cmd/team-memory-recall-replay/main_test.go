package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallreplay"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/stretchr/testify/suite"
)

type mainSuite struct {
	suite.Suite
}

func TestMainSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(mainSuite))
}

func (s *mainSuite) TestExportScopeResolverSupportsSharedEvalV3Scope() {
	resolve, prefix := exportScopeResolver("run-v3", "run-v3-groupmembench-Finance", "")

	s.Equal("run-v3-groupmembench-Finance", prefix)
	s.Equal("run-v3-groupmembench-Finance", resolve("multi_hop_1"))
	s.Equal("run-v3-groupmembench-Finance", resolve("knowledge_update_20"))
}

func (s *mainSuite) TestExportScopeResolverKeepsPerCaseEvalV2Scopes() {
	resolve, prefix := exportScopeResolver("run-v2", "", "-team-note-hybrid")

	s.Equal("run-v2-groupmembench-", prefix)
	s.Equal("run-v2-groupmembench-case-1-team-note-hybrid", resolve("case-1"))
}

func (s *mainSuite) TestRunReplayWritesRecallEvalV1Artifacts() {
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	fixturePath := filepath.Join(s.T().TempDir(), "fixture.json")
	outputDirectory := filepath.Join(s.T().TempDir(), "output")
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Policy:        recallreplay.Policy{SemanticThreshold: 0.5, CandidateLimit: 16},
		Cases: []recallreplay.Case{{
			Fixture: stageeval.Fixture{
				CaseID: "case-1", SourceRevision: "6c79b2531a762ed4fa50dd359ed3e363f63c3c4c640a5151a4174577d9dbc596",
				RecallContext: stageeval.RecallContext{ConsumerUserID: "user", Query: "release ready", TokenBudget: 128},
				RequiredAtoms: []stageeval.Atom{{ID: "ready", Patterns: []string{"(?i)release is ready"}}},
			},
			ScopeID: "scope", Actor: recallreplay.Actor{UserID: "user", AgentID: "agent", SessionID: "session"},
			ObservationTime: now,
			QueryTimezone:   "UTC",
			ExtractionItems: []stageeval.Item{{ID: "note-1", Text: "Release is ready.", EvidenceEventIDs: []string{"event-1"}}},
			Candidates: []recallreplay.Candidate{{
				ID: "note-1", Kind: "status", Subject: "release", Body: "Release is ready.",
				Origin:           recallreplay.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"},
				EvidenceEventIDs: []string{"event-1"}, Revision: 1,
				CreatedAt: now, UpdatedAt: now, SourceOccurredAt: now, LexicalScore: 1,
			}},
		}},
	}
	s.Require().NoError(recallreplay.WriteFixtureSet(fixturePath, set))
	var stdout bytes.Buffer

	s.Require().NoError(runReplay(fixturePath, 0.5, 16, false, false, outputDirectory, &stdout))
	for _, name := range []string{"replay-results.jsonl", "replay-summary.json", "recall-loss-ledger.jsonl"} {
		_, err := os.Stat(filepath.Join(outputDirectory, name))
		s.Require().NoError(err, name)
	}
	ledger, err := os.ReadFile(filepath.Join(outputDirectory, "recall-loss-ledger.jsonl"))
	s.Require().NoError(err)
	s.Contains(string(ledger), `"atom_id":"ready"`)
}
