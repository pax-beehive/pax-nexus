package teamnote_test

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type scopedStoreSuite struct {
	suite.Suite
	store *teamnote.ScopedLedgerStore
}

func TestScopedStoreSuite(t *testing.T) {
	suite.Run(t, new(scopedStoreSuite))
}

func (s *scopedStoreSuite) SetupTest() {
	s.store = teamnote.NewScopedLedgerStore(teamnote.DefaultTTLPolicy(), &fakeClock{
		now: time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC),
	})
}

func (s *scopedStoreSuite) TestScopesHaveIndependentLedgers() {
	evidence := producerEvent("scoped-event", "Deployment is blocked.")
	candidate := teamnote.Candidate{
		ID: "scoped-candidate", Action: teamnote.ActionCreate, Kind: teamnote.KindBlocker,
		Subject: "deployment", Body: "Deployment is blocked.", TaskRef: "release-42",
		Origin: evidence.Actor, EvidenceEventIDs: []string{evidence.ID},
	}
	note, err := s.store.ApplyCandidate(context.Background(), "scope-a", "run-1", candidate, []teamnote.SessionEvent{evidence})
	s.Require().NoError(err)
	s.Equal(1, note.Revision)

	request := teamnote.RecallRequest{
		Actor:   teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "consumer-session"},
		TaskRef: "release-42", TokenBudget: 100,
	}
	visible, err := s.store.RecallNotes(context.Background(), "scope-a", request)
	s.Require().NoError(err)
	s.Len(visible.Items, 1)

	hidden, err := s.store.RecallNotes(context.Background(), "scope-b", request)
	s.Require().NoError(err)
	s.Empty(hidden.Items)
}

func (s *scopedStoreSuite) TestScopeContextRoundTripAndValidation() {
	ctx := teamnote.WithScope(context.Background(), "  team-a  ")
	scopeID, err := teamnote.ScopeFromContext(ctx)
	s.Require().NoError(err)
	s.Equal("team-a", scopeID)

	_, err = teamnote.ScopeFromContext(context.Background())
	s.Require().ErrorIs(err, teamnote.ErrMissingScope)
	_, err = teamnote.ScopeFromContext(teamnote.WithScope(context.Background(), "  "))
	s.Require().ErrorIs(err, teamnote.ErrMissingScope)
	s.False(teamnote.SystemClock{}.Now().IsZero())
}
