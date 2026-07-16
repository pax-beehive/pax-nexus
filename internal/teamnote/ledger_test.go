package teamnote_test

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type ledgerSuite struct {
	suite.Suite
	clock  *fakeClock
	ledger *teamnote.Ledger
}

func TestLedgerSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(ledgerSuite))
}

func (s *ledgerSuite) SetupTest() {
	s.clock = &fakeClock{now: time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)}
	s.ledger = teamnote.NewLedger(teamnote.DefaultTTLPolicy(), s.clock)
}

func (s *ledgerSuite) TestGroundedNoteIsDeliveredOncePerRevisionAndSession() {
	evidence := producerEvent("event-1", "The release is blocked by failing integration tests.")
	candidate := teamnote.Candidate{
		ID: "candidate-1", Action: teamnote.ActionCreate, Kind: teamnote.KindBlocker,
		Subject: "release", Body: "Integration tests are failing.", TaskRef: "release-42",
		Origin: evidence.Actor, EvidenceEventIDs: []string{evidence.ID},
	}

	note, err := s.ledger.Apply(context.Background(), candidate, []teamnote.SessionEvent{evidence})
	s.Require().NoError(err)
	s.Require().Equal(1, note.Revision)
	s.Require().Equal(teamnote.StateActive, note.State)

	request := teamnote.RecallRequest{
		Actor:   teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "consumer-session"},
		TaskRef: "release-42", TokenBudget: 256,
	}
	first, err := s.ledger.Recall(context.Background(), request)
	s.Require().NoError(err)
	s.Require().Equal([]string{"[blocker certainty=unresolved] Integration tests are failing."}, first.Items)
	s.Require().Len(first.Details, 1)
	s.Equal(evidence.Actor, first.Details[0].Origin)
	s.Equal(note.ID, first.Details[0].NoteID)
	s.Equal(note.Revision, first.Details[0].Revision)
	s.Equal(teamnote.CertaintyUnresolved, first.Details[0].Certainty)

	second, err := s.ledger.Recall(context.Background(), request)
	s.Require().NoError(err)
	s.Require().Empty(second.Items)
}

func (s *ledgerSuite) TestUpdateRedeliversAndResolveStopsDelivery() {
	createEvidence := producerEvent("event-create", "The build is running.")
	created, err := s.ledger.Apply(context.Background(), teamnote.Candidate{
		ID: "candidate-create", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
		Subject: "build", Body: "The build is running.", TaskRef: "release-42",
		Origin: createEvidence.Actor, EvidenceEventIDs: []string{createEvidence.ID},
	}, []teamnote.SessionEvent{createEvidence})
	s.Require().NoError(err)

	request := consumerRecall("release-42", "consumer-session")
	_, err = s.ledger.Recall(context.Background(), request)
	s.Require().NoError(err)

	updateEvidence := producerEvent("event-update", "The build passed.")
	updated, err := s.ledger.Apply(context.Background(), teamnote.Candidate{
		ID: "candidate-update", Action: teamnote.ActionUpdate, Kind: teamnote.KindStatus,
		Subject: "build", Body: "The build passed.", TaskRef: "release-42",
		Origin: updateEvidence.Actor, EvidenceEventIDs: []string{updateEvidence.ID},
	}, []teamnote.SessionEvent{updateEvidence})
	s.Require().NoError(err)
	s.Require().Equal(created.ID, updated.ID)
	s.Require().Equal(2, updated.Revision)

	envelope, err := s.ledger.Recall(context.Background(), request)
	s.Require().NoError(err)
	s.Require().Equal([]string{"[status certainty=confirmed] The build passed."}, envelope.Items)

	resolveEvidence := producerEvent("event-resolve", "The build status is no longer active.")
	resolved, err := s.ledger.Apply(context.Background(), teamnote.Candidate{
		ID: "candidate-resolve", Action: teamnote.ActionResolve, Kind: teamnote.KindStatus,
		Subject: "build", Body: "The build completed.", TaskRef: "release-42",
		Origin: resolveEvidence.Actor, EvidenceEventIDs: []string{resolveEvidence.ID},
	}, []teamnote.SessionEvent{resolveEvidence})
	s.Require().NoError(err)
	s.Require().Equal(teamnote.StateResolved, resolved.State)

	envelope, err = s.ledger.Recall(context.Background(), consumerRecall("release-42", "new-session"))
	s.Require().NoError(err)
	s.Require().Empty(envelope.Items)
}

func (s *ledgerSuite) TestSoftTTLExpiresNotes() {
	evidence := producerEvent("event-ttl", "The deployment is running.")
	_, err := s.ledger.Apply(context.Background(), teamnote.Candidate{
		ID: "candidate-ttl", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
		Subject: "deployment", Body: "The deployment is running.", TaskRef: "release-42",
		Origin: evidence.Actor, EvidenceEventIDs: []string{evidence.ID},
	}, []teamnote.SessionEvent{evidence})
	s.Require().NoError(err)

	s.clock.now = s.clock.now.Add(24 * time.Hour)
	envelope, err := s.ledger.Recall(context.Background(), consumerRecall("release-42", "after-expiry"))
	s.Require().NoError(err)
	s.Require().Empty(envelope.Items)
}

func (s *ledgerSuite) TestAdmissionRejectsUnsafeCandidates() {
	evidence := producerEvent("event-safe", "The build passed.")
	privateEvidence := evidence
	privateEvidence.ID = "event-private"
	privateEvidence.Visibility = "private"
	otherSessionEvidence := evidence
	otherSessionEvidence.ID = "event-other-session"
	otherSessionEvidence.Actor.SessionID = "other-session"
	otherTaskEvidence := evidence
	otherTaskEvidence.ID = "event-other-task"
	otherTaskEvidence.TaskRef = "other-task"
	tests := []struct {
		name      string
		candidate teamnote.Candidate
		events    []teamnote.SessionEvent
		wantError error
	}{
		{
			name: "missing evidence",
			candidate: teamnote.Candidate{
				ID: "missing", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
				Subject: "build", Body: "The build passed.", Origin: evidence.Actor,
			},
			wantError: teamnote.ErrMissingEvidence,
		},
		{
			name: "forged speaker",
			candidate: teamnote.Candidate{
				ID: "forged", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
				Subject: "build", Body: "The build passed.",
				Origin:           teamnote.Actor{UserID: "other", AgentID: "other", SessionID: "session"},
				EvidenceEventIDs: []string{evidence.ID},
			},
			events: []teamnote.SessionEvent{evidence}, wantError: teamnote.ErrMissingEvidence,
		},
		{
			name: "unsupported kind",
			candidate: teamnote.Candidate{
				ID: "unsupported", Action: teamnote.ActionCreate, Kind: "approval",
				Subject: "release", Body: "Approved.", Origin: evidence.Actor,
				EvidenceEventIDs: []string{evidence.ID},
			},
			events: []teamnote.SessionEvent{evidence}, wantError: teamnote.ErrInvalidCandidate,
		},
		{
			name: "invalid validity window",
			candidate: teamnote.Candidate{
				ID: "invalid-window", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
				Subject: "build", Body: "The build passed.", Origin: evidence.Actor,
				ValidAt:          timePointer(time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)),
				InvalidAt:        timePointer(time.Date(2026, time.July, 14, 0, 0, 0, 0, time.UTC)),
				EvidenceEventIDs: []string{evidence.ID},
			},
			events: []teamnote.SessionEvent{evidence}, wantError: teamnote.ErrInvalidCandidate,
		},
		{
			name: "private evidence",
			candidate: teamnote.Candidate{
				ID: "private", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
				Subject: "build", Body: "The build passed.", Origin: privateEvidence.Actor,
				EvidenceEventIDs: []string{privateEvidence.ID},
			},
			events: []teamnote.SessionEvent{privateEvidence}, wantError: teamnote.ErrMissingEvidence,
		},
		{
			name: "cross session evidence",
			candidate: teamnote.Candidate{
				ID: "cross-session", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
				Subject: "build", Body: "The build passed.", Origin: evidence.Actor,
				EvidenceEventIDs: []string{otherSessionEvidence.ID},
			},
			events: []teamnote.SessionEvent{otherSessionEvidence}, wantError: teamnote.ErrMissingEvidence,
		},
		{
			name: "task scope mismatch",
			candidate: teamnote.Candidate{
				ID: "task-mismatch", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
				Subject: "build", Body: "The build passed.", TaskRef: "release-42", Origin: evidence.Actor,
				EvidenceEventIDs: []string{otherTaskEvidence.ID},
			},
			events: []teamnote.SessionEvent{otherTaskEvidence}, wantError: teamnote.ErrMissingEvidence,
		},
		{
			name: "omitted task scope",
			candidate: teamnote.Candidate{
				ID: "task-omitted", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
				Subject: "build", Body: "The build passed.", Origin: evidence.Actor,
				EvidenceEventIDs: []string{evidence.ID},
			},
			events: []teamnote.SessionEvent{evidence}, wantError: teamnote.ErrMissingEvidence,
		},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := s.ledger.Apply(context.Background(), test.candidate, test.events)
			s.Require().ErrorIs(err, test.wantError)
		})
	}
}

func (s *ledgerSuite) TestStatusIdentityPreservesDifferentAgentReports() {
	firstEvidence := producerEvent("event-first-agent", "The rollout is ready for review.")
	secondEvidence := producerEvent("event-second-agent", "The rollout is blocked on approval.")
	secondEvidence.Actor.AgentID = "second-producer"
	secondEvidence.Actor.SessionID = "second-session"

	first, err := s.ledger.Apply(context.Background(), teamnote.Candidate{
		ID: "candidate-first-agent", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
		Subject: "rollout", Body: "The rollout is ready for review.", TaskRef: "release-42",
		Origin: firstEvidence.Actor, EvidenceEventIDs: []string{firstEvidence.ID},
	}, []teamnote.SessionEvent{firstEvidence})
	s.Require().NoError(err)
	second, err := s.ledger.Apply(context.Background(), teamnote.Candidate{
		ID: "candidate-second-agent", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
		Subject: "rollout", Body: "The rollout is blocked on approval.", TaskRef: "release-42",
		Origin: secondEvidence.Actor, EvidenceEventIDs: []string{secondEvidence.ID},
	}, []teamnote.SessionEvent{secondEvidence})
	s.Require().NoError(err)
	s.NotEqual(first.ID, second.ID)

	envelope, err := s.ledger.Recall(context.Background(), consumerRecall("release-42", "status-conflict-consumer"))
	s.Require().NoError(err)
	s.Len(envelope.Items, 2)
	s.Equal([]string{
		"[status certainty=confirmed] The rollout is ready for review.",
		"[status certainty=confirmed] The rollout is blocked on approval.",
	}, envelope.Items)
}

func (s *ledgerSuite) TestExtractionRunRejectsMismatchedReplay() {
	evidence := producerEvent("event-run-replay", "The rollout is ready.")
	run := teamnote.ExtractionRun{
		ID: "run-replay", Actor: evidence.Actor, FromSequence: 1, ToSequence: 1,
		InputChecksum: "checksum-one", Model: "extractor-model", PromptVersion: "prompt-v1",
		Candidates: []teamnote.Candidate{{
			ID: "candidate-run-replay", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
			Subject: "rollout", Body: "The rollout is ready.", TaskRef: "release-42",
			Origin: evidence.Actor, EvidenceEventIDs: []string{evidence.ID},
		}},
		Evidence: []teamnote.SessionEvent{evidence},
	}
	_, err := s.ledger.ApplyRun(context.Background(), run)
	s.Require().NoError(err)

	run.InputChecksum = "checksum-two"
	_, err = s.ledger.ApplyRun(context.Background(), run)
	s.Require().ErrorIs(err, teamnote.ErrExtractionRunConflict)

	run.InputChecksum = "checksum-one"
	run.Candidates[0].Body = "The rollout changed after the durable result."
	_, err = s.ledger.ApplyRun(context.Background(), run)
	s.Require().ErrorIs(err, teamnote.ErrExtractionRunConflict)
}

func (s *ledgerSuite) TestAudienceAndBudgetAreEnforced() {
	evidence := producerEvent("event-handoff", "Consumer A owns the handoff.")
	_, err := s.ledger.Apply(context.Background(), teamnote.Candidate{
		ID: "candidate-handoff", Action: teamnote.ActionCreate, Kind: teamnote.KindHandoff,
		Subject: "release", Body: "Continue the release.", TaskRef: "release-42",
		Origin: evidence.Actor, AudienceAgentIDs: []string{"consumer-a"},
		EvidenceEventIDs: []string{evidence.ID},
	}, []teamnote.SessionEvent{evidence})
	s.Require().NoError(err)

	wrongAudience := consumerRecall("release-42", "wrong-audience")
	wrongAudience.Actor.AgentID = "consumer-b"
	envelope, err := s.ledger.Recall(context.Background(), wrongAudience)
	s.Require().NoError(err)
	s.Require().Empty(envelope.Items)

	smallBudget := consumerRecall("release-42", "small-budget")
	smallBudget.Actor.AgentID = "consumer-a"
	smallBudget.TokenBudget = 1
	envelope, err = s.ledger.Recall(context.Background(), smallBudget)
	s.Require().NoError(err)
	s.Require().Empty(envelope.Items)
}

func (s *ledgerSuite) TestQueryPrioritizesRelevantCurrentFact() {
	generalEvidence := producerEvent("event-general", "The release has a blocker.")
	generalEvidence.TaskRef = ""
	_, err := s.ledger.Apply(context.Background(), teamnote.Candidate{
		ID: "candidate-general", Action: teamnote.ActionCreate, Kind: teamnote.KindBlocker,
		Subject: "release blocker", Body: "The release has a blocker.", Origin: generalEvidence.Actor,
		EvidenceEventIDs: []string{generalEvidence.ID}, SourceOccurredAt: generalEvidence.OccurredAt,
	}, []teamnote.SessionEvent{generalEvidence})
	s.Require().NoError(err)

	temporalEvidence := producerEvent("event-temporal", "The deadline moved to July 18.")
	temporalEvidence.TaskRef = ""
	temporalEvidence.OccurredAt = temporalEvidence.OccurredAt.Add(time.Minute)
	_, err = s.ledger.Apply(context.Background(), teamnote.Candidate{
		ID: "candidate-temporal", Action: teamnote.ActionUpdate, Kind: teamnote.KindStatus,
		Subject: "release readiness", Body: "The deadline moved to July 18.", Origin: temporalEvidence.Actor,
		EvidenceEventIDs: []string{temporalEvidence.ID}, SourceOccurredAt: temporalEvidence.OccurredAt,
		ValidAt: timePointer(time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)),
	}, []teamnote.SessionEvent{temporalEvidence})
	s.Require().NoError(err)

	request := consumerRecall("", "temporal-consumer")
	request.Query = "By which date should release readiness be complete?"
	envelope, err := s.ledger.Recall(context.Background(), request)
	s.Require().NoError(err)
	s.Require().Len(envelope.Items, 1)
	s.Equal("[status certainty=confirmed] The deadline moved to July 18. [valid: 2026-07-15T00:00:00Z to present]", envelope.Items[0])
	s.Greater(envelope.Details[0].Relevance, 0.0)
}

func (s *ledgerSuite) TestQueryFiltersIrrelevantNotesAndEnforcesItemLimit() {
	tests := []struct {
		id      string
		subject string
		body    string
	}{
		{id: "candidate-cutover-date", subject: "cutover readiness date", body: "The cutover readiness date is July 28."},
		{id: "candidate-cutover-owner", subject: "cutover owner", body: "Finance owns cutover readiness."},
		{id: "candidate-adjacent-date", subject: "cutover downtime", body: "Cutover downtime is targeted for July 29."},
		{id: "candidate-unrelated", subject: "lunch menu", body: "Lunch is served at noon."},
	}
	for index, test := range tests {
		evidence := producerEvent("event-"+test.id, test.body)
		evidence.TaskRef = ""
		evidence.OccurredAt = evidence.OccurredAt.Add(time.Duration(index) * time.Minute)
		_, err := s.ledger.Apply(context.Background(), teamnote.Candidate{
			ID: test.id, Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
			Subject: test.subject, Body: test.body, Origin: evidence.Actor,
			EvidenceEventIDs: []string{evidence.ID}, SourceOccurredAt: evidence.OccurredAt,
		}, []teamnote.SessionEvent{evidence})
		s.Require().NoError(err)
	}

	request := consumerRecall("", "limited-consumer")
	request.Query = "What is the exact cutover readiness target date?"
	request.MaxItems = 1
	envelope, err := s.ledger.Recall(context.Background(), request)
	s.Require().NoError(err)
	s.Require().Len(envelope.Details, 1)
	s.Contains(envelope.Details[0].Text, "July 28")
	s.Greater(envelope.Details[0].Relevance, 0.0)
	s.Equal(teamnote.CertaintyConfirmed, envelope.Details[0].Certainty)

	request.Actor.SessionID = "unlimited-consumer"
	request.MaxItems = 10
	envelope, err = s.ledger.Recall(context.Background(), request)
	s.Require().NoError(err)
	s.Require().Len(envelope.Details, 1)
	s.Contains(envelope.Details[0].Text, "July 28")
}

func (s *ledgerSuite) TestFirstPersonQueryPrefersTheAskingUsersOwnSource() {
	otherEvidence := producerEvent("event-other-assignment", "The rollout assignment is to validate billing.")
	otherEvidence.TaskRef = ""
	otherEvidence.Actor.UserID = "User_7"
	otherEvidence.OccurredAt = otherEvidence.OccurredAt.Add(time.Minute)
	_, err := s.ledger.Apply(context.Background(), teamnote.Candidate{
		ID: "candidate-other-assignment", Action: teamnote.ActionCreate, Kind: teamnote.KindHandoff,
		Subject: "rollout assignment", Body: "The rollout assignment is to validate billing.",
		Origin: otherEvidence.Actor, EvidenceEventIDs: []string{otherEvidence.ID},
	}, []teamnote.SessionEvent{otherEvidence})
	s.Require().NoError(err)

	ownEvidence := producerEvent("event-own-assignment", "The rollout assignment is to verify exports.")
	ownEvidence.TaskRef = ""
	_, err = s.ledger.Apply(context.Background(), teamnote.Candidate{
		ID: "candidate-own-assignment", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
		Subject: "rollout assignment", Body: "The rollout assignment is to verify exports.",
		Origin: ownEvidence.Actor, EvidenceEventIDs: []string{ownEvidence.ID},
	}, []teamnote.SessionEvent{ownEvidence})
	s.Require().NoError(err)

	request := consumerRecall("", "identity-aware-consumer")
	request.Query = "What is my rollout assignment?"
	request.MaxItems = 1
	envelope, err := s.ledger.Recall(context.Background(), request)
	s.Require().NoError(err)
	s.Require().Len(envelope.Details, 1)
	s.Equal("owner", envelope.Details[0].Origin.UserID)
	s.Contains(envelope.Details[0].Text, "verify exports")
}

func (s *ledgerSuite) TestRecallComposesOneHopRelatedFact() {
	postingEvidence := producerEvent("event-posting", "User_7 must post final Ops rows by 2025-07-17.")
	postingEvidence.TaskRef = ""
	_, err := s.ledger.Apply(context.Background(), teamnote.Candidate{
		ID: "candidate-posting", Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
		Subject: "posting final Ops rows", Body: "User_7 must post final Ops rows by 2025-07-17.",
		Origin: postingEvidence.Actor, EvidenceEventIDs: []string{postingEvidence.ID},
	}, []teamnote.SessionEvent{postingEvidence})
	s.Require().NoError(err)

	reviewEvidence := producerEvent("event-review", "Legal reviews the provisional rows after User_7 posts them.")
	reviewEvidence.TaskRef = ""
	reviewEvidence.OccurredAt = reviewEvidence.OccurredAt.Add(time.Minute)
	_, err = s.ledger.Apply(context.Background(), teamnote.Candidate{
		ID: "candidate-review", Action: teamnote.ActionCreate, Kind: teamnote.KindHandoff,
		Subject:         "Legal review of provisional rows",
		Body:            "Legal reviews the provisional rows after User_7 posts them.",
		RelatedSubjects: []string{"posting final Ops rows"}, Origin: reviewEvidence.Actor,
		EvidenceEventIDs: []string{reviewEvidence.ID},
	}, []teamnote.SessionEvent{reviewEvidence})
	s.Require().NoError(err)

	request := consumerRecall("", "related-consumer")
	request.Query = "When should Legal review the provisional rows?"
	envelope, err := s.ledger.Recall(context.Background(), request)
	s.Require().NoError(err)
	s.Require().NotEmpty(envelope.Items)
	s.Contains(envelope.Items[0], "Legal reviews the provisional rows")
	s.Contains(envelope.Items[0], "related: [certainty=confirmed] posting final Ops rows: User_7 must post final Ops rows by 2025-07-17")
	s.Equal(teamnote.CertaintyProposed, envelope.Details[0].Certainty)

	request.Actor.SessionID = "related-limited-consumer"
	request.MaxItems = 1
	envelope, err = s.ledger.Recall(context.Background(), request)
	s.Require().NoError(err)
	s.Require().Len(envelope.Items, 1)
	s.NotContains(envelope.Items[0], "related:")

	request.Actor.SessionID = "related-exact-consumer"
	request.Query = "What is the exact date for Legal review of provisional rows?"
	request.MaxItems = 5
	envelope, err = s.ledger.Recall(context.Background(), request)
	s.Require().NoError(err)
	s.Require().Len(envelope.Items, 1)
	s.NotContains(envelope.Items[0], "related:")
}

func (s *ledgerSuite) TestSemanticCandidatesPreservePrecisionChecks() {
	tests := []struct {
		name  string
		note  teamnote.Note
		query string
		want  bool
	}{
		{
			name: "semantic paraphrase", query: "What is stopping the launch?", want: true,
			note: teamnote.Note{Subject: "release approval", Body: "Release remains blocked pending legal approval."},
		},
		{
			name: "missing date slot", query: "When is the launch deadline?",
			note: teamnote.Note{Subject: "launch", Body: "Release remains blocked pending legal approval."},
		},
		{
			name: "exact query requires lexical evidence", query: "What is the exact launch code?",
			note: teamnote.Note{Subject: "launch", Body: "The launch code is ORBIT-731."},
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.Equal(test.want, teamnote.QuerySemanticallyRelevant(test.note, test.query))
		})
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func producerEvent(id, content string) teamnote.SessionEvent {
	return teamnote.SessionEvent{
		ID: id,
		Actor: teamnote.Actor{
			UserID: "owner", AgentID: "producer", SessionID: "producer-session",
		},
		Sequence: 1, Type: "assistant", Content: content, TaskRef: "release-42",
		Visibility: "team_note_eligible", OccurredAt: time.Date(2026, time.July, 14, 11, 59, 0, 0, time.UTC),
	}
}

func consumerRecall(taskRef, sessionID string) teamnote.RecallRequest {
	return teamnote.RecallRequest{
		Actor:   teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: sessionID},
		TaskRef: taskRef, TokenBudget: 256,
	}
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }
