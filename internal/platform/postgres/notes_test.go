package postgres_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type noteStoreSuite struct {
	suite.Suite
	store *postgres.Store
	clock *noteClock
}

type noteClock struct {
	now time.Time
}

func (c *noteClock) Now() time.Time {
	return c.now
}

func TestNoteStoreSuite(t *testing.T) {
	suite.Run(t, new(noteStoreSuite))
}

func (s *noteStoreSuite) SetupSuite() {
	dsn := testDSN(s.T())
	store, err := postgres.Open(context.Background(), dsn)
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(context.Background()))
	s.store = store
}

func (s *noteStoreSuite) SetupTest() {
	s.clock = &noteClock{now: time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)}
}

func (s *noteStoreSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *noteStoreSuite) TestLifecycleDeliveryAndPersistence() {
	ctx := context.Background()
	scopeID := uniqueScope("note-lifecycle")
	producer := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
	consumer := teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "consumer-session"}
	firstEvent := event("note-event-1", producer, 1)
	s.appendEvents(ctx, scopeID, firstEvent)
	notes := s.newNoteStore()

	created, err := notes.ApplyCandidate(ctx, scopeID, "run-1", candidate("candidate-1", teamnote.ActionCreate, "work started", producer, firstEvent.ID), []teamnote.SessionEvent{firstEvent})
	s.Require().NoError(err)
	s.Equal(1, created.Revision)
	s.Equal(teamnote.StateActive, created.State)

	request := teamnote.RecallRequest{Actor: consumer, TaskRef: "task-1", TokenBudget: 100}
	first, err := notes.RecallNotes(ctx, scopeID, request)
	s.Require().NoError(err)
	s.Equal([]string{"[status certainty=confirmed] work started"}, first.Items)
	s.Require().Len(first.Details, 1)
	s.Equal(producer, first.Details[0].Origin)
	s.Equal(created.ID, first.Details[0].NoteID)
	s.Equal(created.Revision, first.Details[0].Revision)

	duplicate, err := notes.RecallNotes(ctx, scopeID, request)
	s.Require().NoError(err)
	s.Empty(duplicate.Items)

	restarted := s.newNoteStore()
	request.Actor.SessionID = "consumer-session-after-restart"
	afterRestart, err := restarted.RecallNotes(ctx, scopeID, request)
	s.Require().NoError(err)
	s.Equal(first.Items, afterRestart.Items)

	secondEvent := event("note-event-2", producer, 2)
	s.appendEvents(ctx, scopeID, secondEvent)
	updated, err := restarted.ApplyCandidate(ctx, scopeID, "run-2", candidate("candidate-2", teamnote.ActionUpdate, "work finished", producer, secondEvent.ID), []teamnote.SessionEvent{secondEvent})
	s.Require().NoError(err)
	s.Equal(created.ID, updated.ID)
	s.Equal(2, updated.Revision)

	refreshed, err := restarted.RecallNotes(ctx, scopeID, request)
	s.Require().NoError(err)
	s.Equal([]string{"[status certainty=confirmed] work finished"}, refreshed.Items)

	thirdEvent := event("note-event-3", producer, 3)
	s.appendEvents(ctx, scopeID, thirdEvent)
	resolved, err := restarted.ApplyCandidate(ctx, scopeID, "run-3", candidate("candidate-3", teamnote.ActionResolve, "resolved", producer, thirdEvent.ID), []teamnote.SessionEvent{thirdEvent})
	s.Require().NoError(err)
	s.Equal(teamnote.StateResolved, resolved.State)

	request.Actor.SessionID = "new-consumer-session"
	afterResolve, err := restarted.RecallNotes(ctx, scopeID, request)
	s.Require().NoError(err)
	s.Empty(afterResolve.Items)
}

func (s *noteStoreSuite) TestReplayScopeAudienceAndTTL() {
	ctx := context.Background()
	scopeID := uniqueScope("note-policy")
	producer := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
	evidence := event("policy-event", producer, 1)
	s.appendEvents(ctx, scopeID, evidence)
	notes := s.newNoteStore()
	candidate := candidate("policy-candidate", teamnote.ActionCreate, "restricted", producer, evidence.ID)
	candidate.AudienceAgentIDs = []string{"allowed-agent"}

	created, err := notes.ApplyCandidate(ctx, scopeID, "policy-run", candidate, []teamnote.SessionEvent{evidence})
	s.Require().NoError(err)
	replayed, err := notes.ApplyCandidate(ctx, scopeID, "policy-run", candidate, []teamnote.SessionEvent{evidence})
	s.Require().NoError(err)
	s.Equal(created, replayed)

	tests := []struct {
		name    string
		scopeID string
		agentID string
		want    int
	}{
		{name: "allowed", scopeID: scopeID, agentID: "allowed-agent", want: 1},
		{name: "wrong audience", scopeID: scopeID, agentID: "other-agent"},
		{name: "wrong scope", scopeID: uniqueScope("other"), agentID: "allowed-agent"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			envelope, recallErr := notes.RecallNotes(ctx, test.scopeID, teamnote.RecallRequest{
				Actor:   teamnote.Actor{UserID: "owner", AgentID: test.agentID, SessionID: test.name},
				TaskRef: "task-1", TokenBudget: 100,
			})
			s.Require().NoError(recallErr)
			s.Len(envelope.Items, test.want)
		})
	}

	s.clock.now = created.SoftExpiresAt
	expired, err := notes.RecallNotes(ctx, scopeID, teamnote.RecallRequest{
		Actor:   teamnote.Actor{UserID: "owner", AgentID: "allowed-agent", SessionID: "after-ttl"},
		TaskRef: "task-1", TokenBudget: 100,
	})
	s.Require().NoError(err)
	s.Empty(expired.Items)
}

func (s *noteStoreSuite) TestConcurrentAdmissionAndDeliveryAreSerialized() {
	ctx := context.Background()
	scopeID := uniqueScope("note-concurrency")
	producer := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
	events := make([]teamnote.SessionEvent, 6)
	for index := range events {
		events[index] = event(fmt.Sprintf("concurrent-event-%d", index), producer, int64(index+1))
	}
	s.appendEvents(ctx, scopeID, events...)
	notes := s.newNoteStore()

	errorsByCandidate := make(chan error, len(events))
	var writers sync.WaitGroup
	for index, evidence := range events {
		writers.Add(1)
		go func(index int, evidence teamnote.SessionEvent) {
			defer writers.Done()
			_, err := notes.ApplyCandidate(ctx, scopeID, fmt.Sprintf("concurrent-run-%d", index), candidate(
				fmt.Sprintf("concurrent-candidate-%d", index), teamnote.ActionCreate,
				fmt.Sprintf("revision-%d", index), producer, evidence.ID,
			), []teamnote.SessionEvent{evidence})
			errorsByCandidate <- err
		}(index, evidence)
	}
	writers.Wait()
	close(errorsByCandidate)
	for err := range errorsByCandidate {
		s.Require().NoError(err)
	}

	request := teamnote.RecallRequest{
		Actor:   teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "shared-session"},
		TaskRef: "task-1", TokenBudget: 100,
	}
	itemsByRecall := make(chan int, len(events))
	errorsByRecall := make(chan error, len(events))
	var readers sync.WaitGroup
	for range events {
		readers.Add(1)
		go func() {
			defer readers.Done()
			envelope, err := notes.RecallNotes(ctx, scopeID, request)
			itemsByRecall <- len(envelope.Items)
			errorsByRecall <- err
		}()
	}
	readers.Wait()
	close(itemsByRecall)
	close(errorsByRecall)
	totalItems := 0
	for count := range itemsByRecall {
		totalItems += count
	}
	for err := range errorsByRecall {
		s.Require().NoError(err)
	}
	s.Equal(1, totalItems)
}

func (s *noteStoreSuite) TestTemporalRevisionAndQueryRankingPersist() {
	ctx := context.Background()
	scopeID := uniqueScope("note-temporal")
	producer := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
	generalEvent := event("temporal-general", producer, 1)
	firstEvent := event("temporal-first", producer, 2)
	secondEvent := event("temporal-second", producer, 3)
	s.appendEvents(ctx, scopeID, generalEvent, firstEvent, secondEvent)
	notes := s.newNoteStore()

	general := candidate("temporal-general-candidate", teamnote.ActionCreate, "A release blocker is open.", producer, generalEvent.ID)
	general.Kind = teamnote.KindBlocker
	general.Subject = "release blocker"
	_, err := notes.ApplyCandidate(ctx, scopeID, "temporal-run-general", general, []teamnote.SessionEvent{generalEvent})
	s.Require().NoError(err)

	first := candidate("temporal-first-candidate", teamnote.ActionCreate, "Validation owners must confirm by July 15.", producer, firstEvent.ID)
	first.Subject = "validation owner confirmation"
	first.ValidAt = timestampPointer(time.Date(2026, time.July, 10, 0, 0, 0, 0, time.UTC))
	created, err := notes.ApplyCandidate(ctx, scopeID, "temporal-run-first", first, []teamnote.SessionEvent{firstEvent})
	s.Require().NoError(err)

	second := candidate("temporal-second-candidate", teamnote.ActionUpdate, "Validation owners must confirm by July 18.", producer, secondEvent.ID)
	second.Subject = first.Subject
	second.ValidAt = timestampPointer(time.Date(2026, time.July, 12, 0, 0, 0, 0, time.UTC))
	updated, err := notes.ApplyCandidate(ctx, scopeID, "temporal-run-second", second, []teamnote.SessionEvent{secondEvent})
	s.Require().NoError(err)
	s.Equal(created.ID, updated.ID)

	envelope, err := notes.RecallNotes(ctx, scopeID, teamnote.RecallRequest{
		Actor:   teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "temporal-query"},
		TaskRef: "task-1", TokenBudget: 256, Query: "By which date should validation owners confirm?", MaxItems: 1,
	})
	s.Require().NoError(err)
	s.Require().Len(envelope.Items, 1)
	s.Equal("[status certainty=confirmed] Validation owners must confirm by July 18. [valid: 2026-07-12T00:00:00Z to present]", envelope.Items[0])
	s.Greater(envelope.Details[0].Relevance, 0.0)
	s.Equal(teamnote.CertaintyConfirmed, envelope.Details[0].Certainty)

	var invalidAt, expiredAt *time.Time
	err = s.store.Pool().QueryRow(ctx, `
SELECT invalid_at, expired_at FROM note_revisions
WHERE scope_id = $1 AND note_id = $2 AND revision = 1`, scopeID, created.ID).Scan(&invalidAt, &expiredAt)
	s.Require().NoError(err)
	s.Require().NotNil(invalidAt)
	s.Require().NotNil(expiredAt)
	s.Equal(second.ValidAt.UTC(), invalidAt.UTC())
}

func (s *noteStoreSuite) TestRelatedFactIsComposedForRecall() {
	ctx := context.Background()
	scopeID := uniqueScope("note-related")
	producer := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
	postingEvent := event("related-posting", producer, 1)
	reviewEvent := event("related-review", producer, 2)
	s.appendEvents(ctx, scopeID, postingEvent, reviewEvent)
	notes := s.newNoteStore()

	posting := candidate("related-posting-candidate", teamnote.ActionCreate,
		"User_7 must post final Ops rows by 2025-07-17.", producer, postingEvent.ID)
	posting.Subject = "posting final Ops rows"
	_, err := notes.ApplyCandidate(ctx, scopeID, "related-posting-run", posting, []teamnote.SessionEvent{postingEvent})
	s.Require().NoError(err)

	review := candidate("related-review-candidate", teamnote.ActionCreate,
		"Legal reviews the provisional rows after User_7 posts them.", producer, reviewEvent.ID)
	review.Kind = teamnote.KindHandoff
	review.Subject = "Legal review of provisional rows"
	review.RelatedSubjects = []string{"posting final Ops rows"}
	_, err = notes.ApplyCandidate(ctx, scopeID, "related-review-run", review, []teamnote.SessionEvent{reviewEvent})
	s.Require().NoError(err)

	envelope, err := notes.RecallNotes(ctx, scopeID, teamnote.RecallRequest{
		Actor:   teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "related-query"},
		TaskRef: "task-1", TokenBudget: 256, Query: "When should Legal review provisional rows?",
	})
	s.Require().NoError(err)
	s.Require().NotEmpty(envelope.Items)
	s.Contains(envelope.Items[0], "Legal reviews the provisional rows")
	s.Contains(envelope.Items[0], "related: [certainty=confirmed] posting final Ops rows: User_7 must post final Ops rows by 2025-07-17")
}

func (s *noteStoreSuite) newNoteStore() *postgres.NoteStore {
	store, err := postgres.NewNoteStore(s.store, teamnote.DefaultTTLPolicy(), s.clock)
	s.Require().NoError(err)
	return store
}

func (s *noteStoreSuite) appendEvents(ctx context.Context, scopeID string, events ...teamnote.SessionEvent) {
	_, err := s.store.AppendSession(ctx, scopeID, teamnote.SessionBatch{Events: events})
	s.Require().NoError(err)
}

func candidate(id string, action teamnote.CandidateAction, body string, actor teamnote.Actor, evidenceID string) teamnote.Candidate {
	return teamnote.Candidate{
		ID: id, Action: action, Kind: teamnote.KindStatus, Subject: "delivery",
		Body: body, TaskRef: "task-1", Origin: actor, EvidenceEventIDs: []string{evidenceID},
	}
}

func timestampPointer(value time.Time) *time.Time {
	return &value
}
