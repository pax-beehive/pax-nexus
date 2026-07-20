package bm25_test

import (
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/bm25"
	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/stretchr/testify/suite"
)

type rawSuite struct{ suite.Suite }

func TestRawSuite(t *testing.T) { suite.Run(t, new(rawSuite)) }

func (s *rawSuite) TestRecallFiltersIneligibleAndPacksRankedEvidence() {
	result, err := bm25.RecallRaw([]session.SessionBatch{{Complete: true, Events: []session.SessionEvent{
		event("one", "User_1", "s1", "Treasury rollback owner is Ops.", "team_note_eligible", 1),
		event("two", "User_2", "s2", "The customer lunch menu changed.", "private", 2),
		event("three", "User_3", "s3", "Ops owns rollback evidence and confirms the cutoff.", "team_note_eligible", 3),
	}}}, bm25.RawQuery{Text: "Who owns rollback evidence?", CandidateLimit: 4, TokenBudget: 20, ChunkEvents: 1})

	s.Require().NoError(err)
	s.Equal(3, result.SourceEvents)
	s.Equal(2, result.EligibleEvents)
	s.Equal(2, result.Candidates)
	s.Equal(1, result.Selected)
	s.Equal([]string{"three"}, result.SelectedEventIDs)
	s.Contains(result.Context, "Ops owns rollback evidence")
	s.NotContains(result.Context, "customer lunch")
	s.Equal(1, result.BudgetDrops)
	s.Len(result.CandidateTrace, 2)
	s.True(result.CandidateTrace[0].Selected)
	s.Equal("token_budget", result.CandidateTrace[1].Rejection)
}

func event(id, userID, sessionID, content, visibility string, second int) session.SessionEvent {
	return session.SessionEvent{
		ID: id, Actor: session.Actor{UserID: userID, AgentID: "agent-" + userID, SessionID: sessionID},
		Sequence: int64(second), Content: content, Visibility: visibility,
		OccurredAt: time.Unix(int64(second), 0).UTC(), Metadata: map[string]string{"role": "Reviewer"},
	}
}
