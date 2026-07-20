package stagecapture_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/stagecapture"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

const sourceRevision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type CaptureSuite struct {
	suite.Suite
	store *postgres.Store
	dsn   string
}

func TestCaptureSuite(t *testing.T) {
	suite.Run(t, new(CaptureSuite))
}

func (s *CaptureSuite) SetupSuite() {
	s.dsn = os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if s.dsn == "" {
		s.T().Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is required")
	}
	store, err := postgres.Open(context.Background(), s.dsn)
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(context.Background()))
	s.store = store
}

func (s *CaptureSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *CaptureSuite) TestCaptureExportsAdmittedNotesAndExactRecallEnvelope() {
	ctx := context.Background()
	scopeID := fmt.Sprintf("stage-capture-%d", time.Now().UnixNano())
	producer := teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"}
	consumer := teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "consumer-session"}
	event := session.SessionEvent{
		ID: "event-1", Actor: producer, Sequence: 1, Type: "message", Content: "Ops Lead owns rollback evidence.",
		Visibility: "team_note_eligible", OccurredAt: time.Now().UTC(), CapturedAt: time.Now().UTC(),
	}
	_, err := s.store.Sessions().AppendSession(ctx, scopeID, session.SessionBatch{Events: []session.SessionEvent{event}, Complete: true})
	s.Require().NoError(err)
	notes, err := postgres.NewNoteStore(s.store, teamnote.DefaultTTLPolicy(), teamnote.SystemClock{}, postgres.RetrievalConfig{})
	s.Require().NoError(err)
	_, err = notes.ApplyExtractionRun(ctx, scopeID, teamnote.ExtractionRun{
		ID: "run-1", Actor: producer, FromSequence: 1, ToSequence: 1, InputChecksum: "input",
		Model: "extractor-model", PromptVersion: "prompt-v1", InputTokens: 10, OutputTokens: 4,
		Candidates: []teamnote.Candidate{{
			ID: "candidate-1", Action: teamnote.ActionCreate, Kind: teamnote.KindHandoff,
			Subject: "rollback evidence", Body: "Ops Lead owns rollback evidence.", Origin: producer,
			EvidenceEventIDs: []string{event.ID},
		}},
		Evidence: []session.SessionEvent{event},
	})
	s.Require().NoError(err)
	request := teamnote.RecallRequest{Actor: consumer, Query: "Who owns rollback evidence?", TokenBudget: 128, MaxItems: 3}
	envelope, err := notes.RecallNotes(ctx, scopeID, request)
	s.Require().NoError(err)
	s.Require().Len(envelope.Items, 1)
	secondRequest := request
	secondRequest.Query = "focused rollback evidence owner"
	secondEnvelope, err := notes.RecallNotes(ctx, scopeID, secondRequest)
	s.Require().NoError(err)
	s.Empty(secondEnvelope.Items)

	observer, err := stagecapture.Open(ctx, s.dsn)
	s.Require().NoError(err)
	defer observer.Close()
	fixture := stageeval.Fixture{
		CaseID: "case-1", SourceRevision: sourceRevision,
		RecallContext: stageeval.RecallContext{
			ConsumerUserID: consumer.UserID, ConsumerAgentID: consumer.AgentID,
			ConsumerSessionID: consumer.SessionID, Query: request.Query, TokenBudget: request.TokenBudget,
		},
		RequiredAtoms: []stageeval.Atom{{ID: "owner", Patterns: []string{"Ops Lead"}}},
	}
	extraction, recall, err := observer.Capture(ctx, stagecapture.Target{
		Fixture: fixture, ScopeID: scopeID, RecipientAgentID: consumer.AgentID, RecipientSessionID: consumer.SessionID,
	})

	s.Require().NoError(err)
	s.Require().Len(extraction.Items, 1)
	s.Equal("Ops Lead owns rollback evidence.", extraction.Items[0].Text)
	s.Equal([]string{event.ID}, extraction.Items[0].EvidenceEventIDs)
	s.Equal("extractor-model", extraction.Provenance["model"])
	s.Require().Len(recall.Items, 1)
	s.Equal(envelope.Items[0], recall.Items[0].Text)
	s.Equal(extraction.Items[0].ID, recall.Items[0].ID)
	s.Equal("2", recall.Provenance["recall_count"])
	var traces []teamnote.RecallTrace
	s.Require().NoError(json.Unmarshal([]byte(recall.Provenance["recall_traces"]), &traces))
	s.Require().Len(traces, 2)
	s.Equal(1, traces[0].Candidates)
	s.Equal(1, traces[0].PlannedNotes)

	resolvedEvent := session.SessionEvent{
		ID: "event-2", Actor: producer, Sequence: 2, Type: "message", Content: "Rollback evidence is no longer current.",
		Visibility: "team_note_eligible", OccurredAt: time.Now().UTC(), CapturedAt: time.Now().UTC(),
	}
	_, err = s.store.Sessions().AppendSession(ctx, scopeID, session.SessionBatch{Events: []session.SessionEvent{resolvedEvent}, Complete: true})
	s.Require().NoError(err)
	_, err = notes.ApplyExtractionRun(ctx, scopeID, teamnote.ExtractionRun{
		ID: "run-2", Actor: producer, FromSequence: 2, ToSequence: 2, InputChecksum: "resolve-input",
		Candidates: []teamnote.Candidate{{
			ID: "candidate-2", Action: teamnote.ActionResolve, Kind: teamnote.KindHandoff,
			Subject: "rollback evidence", Body: "resolved", Origin: producer,
			EvidenceEventIDs: []string{resolvedEvent.ID},
		}},
		Evidence: []session.SessionEvent{resolvedEvent},
	})
	s.Require().NoError(err)
	extraction, _, err = observer.Capture(ctx, stagecapture.Target{
		Fixture: fixture, ScopeID: scopeID, RecipientAgentID: consumer.AgentID, RecipientSessionID: consumer.SessionID,
	})
	s.Require().NoError(err)
	s.Require().Len(extraction.Items, 1)
	s.Equal("Ops Lead owns rollback evidence.", extraction.Items[0].Text)
}

func (s *CaptureSuite) TestCapturePreservesSuccessfulZeroHitRecall() {
	ctx := context.Background()
	scopeID := fmt.Sprintf("stage-capture-empty-%d", time.Now().UnixNano())
	consumer := teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "empty-consumer-session"}
	notes, err := postgres.NewNoteStore(s.store, teamnote.DefaultTTLPolicy(), teamnote.SystemClock{}, postgres.RetrievalConfig{})
	s.Require().NoError(err)
	request := teamnote.RecallRequest{Actor: consumer, Query: "nothing should match", TokenBudget: 64}
	envelope, err := notes.RecallNotes(ctx, scopeID, request)
	s.Require().NoError(err)
	s.Empty(envelope.Items)

	observer, err := stagecapture.Open(ctx, s.dsn)
	s.Require().NoError(err)
	defer observer.Close()
	fixture := stageeval.Fixture{
		CaseID: "case-empty", SourceRevision: sourceRevision,
		RecallContext: stageeval.RecallContext{
			ConsumerUserID: consumer.UserID, ConsumerAgentID: consumer.AgentID,
			ConsumerSessionID: consumer.SessionID, Query: request.Query, TokenBudget: request.TokenBudget,
		},
		RequiredAtoms: []stageeval.Atom{{ID: "missing", Patterns: []string{"missing"}}},
	}
	_, recall, err := observer.Capture(ctx, stagecapture.Target{
		Fixture: fixture, ScopeID: scopeID, RecipientAgentID: consumer.AgentID, RecipientSessionID: consumer.SessionID,
	})

	s.Require().NoError(err)
	s.Empty(recall.Items)
	s.Empty(recall.Error)
}

func (s *CaptureSuite) TestCaptureReportsMissingRecallWithoutInventingQualityLoss() {
	ctx := context.Background()
	scopeID := fmt.Sprintf("stage-capture-missing-%d", time.Now().UnixNano())
	fixture := stageeval.Fixture{
		CaseID: "case-missing", SourceRevision: sourceRevision,
		RecallContext: stageeval.RecallContext{
			ConsumerUserID: "owner", ConsumerAgentID: "consumer", Query: "question", TokenBudget: 64,
		},
		RequiredAtoms: []stageeval.Atom{{ID: "missing", Patterns: []string{"missing"}}},
	}
	observer, err := stagecapture.Open(ctx, s.dsn)
	s.Require().NoError(err)
	defer observer.Close()
	extraction, recall, err := observer.Capture(ctx, stagecapture.Target{
		Fixture: fixture, ScopeID: scopeID, RecipientAgentID: "consumer", RecipientSessionID: "session",
	})

	s.Require().NoError(err)
	s.Contains(extraction.Error, "no matching successful")
	s.Contains(recall.Error, "no matching successful")
}
