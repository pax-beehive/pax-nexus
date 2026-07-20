package teamnote_test

import (
	"fmt"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type extractionRunSuite struct {
	suite.Suite
}

func (s *extractionRunSuite) TestQuarantineOnlyDeterministicAdmissionFailures() {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "invalid candidate", err: teamnote.ErrInvalidCandidate, want: true},
		{name: "missing evidence", err: fmt.Errorf("admit: %w", teamnote.ErrMissingEvidence), want: true},
		{name: "missing target", err: teamnote.ErrNoteNotFound, want: true},
		{name: "context", err: fmt.Errorf("store unavailable"), want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.Equal(test.want, teamnote.ShouldQuarantineExtractionRun(test.err))
		})
	}
}

func (s *extractionRunSuite) TestPrepareRejectsInconsistentRunMembers() {
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "session"}
	other := teamnote.Actor{UserID: "owner", AgentID: "other", SessionID: "other-session"}
	tests := []struct {
		name string
		run  teamnote.ExtractionRun
		want error
	}{
		{
			name: "duplicate candidate",
			run: teamnote.ExtractionRun{Actor: actor, Candidates: []teamnote.Candidate{
				{ID: "duplicate", Origin: actor}, {ID: "duplicate", Origin: actor},
			}},
			want: teamnote.ErrInvalidCandidate,
		},
		{
			name: "candidate actor",
			run:  teamnote.ExtractionRun{Actor: actor, Candidates: []teamnote.Candidate{{ID: "candidate", Origin: other}}},
			want: teamnote.ErrInvalidCandidate,
		},
		{
			name: "evidence actor",
			run:  teamnote.ExtractionRun{Actor: actor, Evidence: []teamnote.SessionEvent{{ID: "event", Actor: other}}},
			want: teamnote.ErrMissingEvidence,
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := teamnote.PrepareExtractionRun(test.run)
			s.Require().ErrorIs(err, test.want)
		})
	}
}

func TestExtractionRunSuite(t *testing.T) {
	suite.Run(t, new(extractionRunSuite))
}

func (s *extractionRunSuite) TestPrepareBindsCandidateBatchToRunIdentity() {
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "session"}
	run, err := teamnote.PrepareExtractionRun(teamnote.ExtractionRun{
		ID: "run-1", Actor: actor,
		Candidates: []teamnote.Candidate{{ID: "candidate-1", Origin: actor}},
	})

	s.Require().NoError(err)
	s.NotEmpty(run.CandidateChecksum)

	replayed, err := teamnote.PrepareExtractionRun(run)
	s.Require().NoError(err)
	s.Equal(run.CandidateChecksum, replayed.CandidateChecksum)
}

func (s *extractionRunSuite) TestValidateDurableRunRequiresPersistenceIdentity() {
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "session"}
	valid, err := teamnote.PrepareExtractionRun(teamnote.ExtractionRun{
		ID: "run-1", Actor: actor, FromSequence: 1, ToSequence: 1,
		InputChecksum: "input", Candidates: []teamnote.Candidate{{ID: "candidate-1", Origin: actor}},
	})
	s.Require().NoError(err)

	tests := []struct {
		name    string
		scopeID string
		mutate  func(*teamnote.ExtractionRun)
	}{
		{name: "scope", scopeID: " ", mutate: func(*teamnote.ExtractionRun) {}},
		{name: "run id", scopeID: "scope", mutate: func(run *teamnote.ExtractionRun) { run.ID = "" }},
		{name: "actor", scopeID: "scope", mutate: func(run *teamnote.ExtractionRun) { run.Actor.AgentID = "" }},
		{name: "input", scopeID: "scope", mutate: func(run *teamnote.ExtractionRun) { run.InputChecksum = "" }},
		{name: "range", scopeID: "scope", mutate: func(run *teamnote.ExtractionRun) { run.FromSequence = 0 }},
		{name: "usage", scopeID: "scope", mutate: func(run *teamnote.ExtractionRun) { run.InputTokens = -1 }},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			changed := valid
			test.mutate(&changed)
			s.Error(teamnote.ValidateDurableExtractionRun(test.scopeID, changed))
		})
	}
	s.NoError(teamnote.ValidateDurableExtractionRun("scope", valid))
}

func (s *extractionRunSuite) TestReplayUsesStableInputIdentityAndDurableStatus() {
	actor := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "session"}
	stored := teamnote.ExtractionRun{
		ID: "run-1", Actor: actor, FromSequence: 1, ToSequence: 2,
		InputChecksum: "input", CandidateChecksum: "candidate-one",
		Model: "model", PromptVersion: "prompt", InputTokens: 10, OutputTokens: 2,
	}
	recomputed := stored
	recomputed.CandidateChecksum = "candidate-two"
	recomputed.InputTokens = 0
	recomputed.OutputTokens = 0
	s.Require().NoError(teamnote.ValidateExtractionRunReplay(stored, recomputed, teamnote.ExtractionRunCompleted, ""))

	changed := recomputed
	changed.PromptVersion = "other-prompt"
	s.Require().ErrorIs(
		teamnote.ValidateExtractionRunReplay(stored, changed, teamnote.ExtractionRunCompleted, ""),
		teamnote.ErrExtractionRunConflict,
	)
	s.Require().ErrorIs(
		teamnote.ValidateExtractionRunReplay(stored, recomputed, teamnote.ExtractionRunQuarantined, "invalid candidate"),
		teamnote.ErrExtractionRunQuarantined,
	)
	s.Require().ErrorIs(
		teamnote.ValidateExtractionRunReplay(stored, recomputed, teamnote.ExtractionRunProcessing, ""),
		teamnote.ErrExtractionRunConflict,
	)
}
