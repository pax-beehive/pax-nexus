package handler_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/explorer"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/mocks"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

type explorerHandlerSuite struct {
	suite.Suite
	controller *gomock.Controller
	identity   *humanIdentityService
	explorer   *explorerLifecycle
	handler    *handler.Handler
}

func TestExplorerHandlerSuite(t *testing.T) {
	suite.Run(t, new(explorerHandlerSuite))
}

func (s *explorerHandlerSuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.identity = &humanIdentityService{principal: onprem.HumanPrincipal{
		UserID: "owner-user", MembershipID: "owner-membership", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive,
	}}
	s.explorer = &explorerLifecycle{now: time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)}
	configured, err := handler.NewOnPrem(
		mocks.NewMockRuntime(s.controller), &credentialService{}, &memoryService{}, &channelService{},
		slog.New(slog.DiscardHandler),
		handler.WithAgentRegistry(&agentRegistryService{}),
		handler.WithHumanIdentity(s.identity, &oidcService{}, "/portal", false),
		handler.WithExplorer(s.explorer),
	)
	s.Require().NoError(err)
	s.handler = configured
}

func (s *explorerHandlerSuite) TestOwnerReadsExplorerViewsThroughGeneratedRoutes() {
	list := s.perform("/v1/admin/team-notes?q=release&kind=blocker&limit=1")
	s.Equal(consts.StatusOK, list.Code)
	s.Contains(list.Body.String(), `"note_id":"note-1"`)
	s.Contains(list.Body.String(), `"next_cursor":`)
	s.Equal("release", s.explorer.filter.Query)
	s.Equal("blocker", s.explorer.filter.Kind)

	detail := s.perform("/v1/admin/team-notes/note-1")
	s.Equal(consts.StatusOK, detail.Code)
	s.Contains(detail.Body.String(), `"body":"Waiting for approval"`)
	s.Contains(detail.Body.String(), `"content":"Source event content"`)
	s.Contains(detail.Body.String(), `"subject":"Release candidate"`)
	s.Contains(detail.Body.String(), `"run_id":"run-1"`)
	s.Contains(detail.Body.String(), `"observation_id":41`)
	s.Contains(detail.Body.String(), `"rejection_reasons":[]`)
	s.Contains(detail.Body.String(), `"budget_drop_reasons":[]`)
	s.Contains(detail.Body.String(), `"hard_gate_failures":[]`)
	s.Contains(detail.Body.String(), `"related_subjects":[]`)
	s.Contains(detail.Body.String(), `"audience_agent_ids":[]`)
	s.NotContains(detail.Body.String(), "secret query")

	extraction := s.perform("/v1/admin/diagnostics/extractions/run-1")
	s.Equal(consts.StatusOK, extraction.Code)
	s.Contains(extraction.Body.String(), `"admission_status":"rejected"`)
	s.Contains(extraction.Body.String(), `"rejection_reason":"missing_evidence"`)

	channel := s.perform("/v1/admin/diagnostics/channels/envelope-1")
	s.Equal(consts.StatusOK, channel.Code)
	s.Contains(channel.Body.String(), `"content":"Capsule content"`)
	s.Contains(channel.Body.String(), `"payload_status":"decoded"`)
	s.NotContains(channel.Body.String(), "idempotency")
	s.NotContains(channel.Body.String(), "secret-key")
}

func (s *explorerHandlerSuite) TestExplorerRejectsAdminAndMember() {
	for _, role := range []onprem.Role{onprem.RoleAdmin, onprem.RoleMember} {
		s.Run(string(role), func() {
			s.identity.principal.Role = role
			response := s.perform("/v1/admin/team-notes")
			s.Equal(consts.StatusForbidden, response.Code)
			s.Contains(response.Body.String(), `"code":"forbidden"`)
		})
	}
}

func (s *explorerHandlerSuite) TestExplorerUsesStableValidationAndNotFoundErrors() {
	invalid := s.perform("/v1/admin/team-notes?limit=101")
	s.Equal(consts.StatusBadRequest, invalid.Code)
	s.JSONEq(`{"code":"invalid_request","message":"the request is invalid"}`, invalid.Body.String())

	s.explorer.err = explorer.ErrNotFound
	missing := s.perform("/v1/admin/team-notes/missing")
	s.Equal(consts.StatusNotFound, missing.Code)
	s.JSONEq(`{"code":"explorer_not_found","message":"the requested resource was not found"}`, missing.Body.String())
}

func (s *explorerHandlerSuite) perform(path string) *ut.ResponseRecorder {
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(s.handler))
	router.GeneratedRegister(hertz)
	return ut.PerformRequest(hertz.Engine, http.MethodGet, path, nil,
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
}

type explorerLifecycle struct {
	now    time.Time
	filter explorer.TeamNoteFilter
	err    error

	// noteMix* back NoteMix, the Overview endpoint's live-note breakdown.
	// calls counts invocations so tests can assert the source was never
	// reached (e.g. the Overview 403 path).
	noteMixCalls  int
	noteMixAt     time.Time
	noteMixResult []explorer.NoteKindCount
	noteMixErr    error

	// noteMixBlock, when set, makes NoteMix hang on its context instead of
	// returning — it exercises the Overview endpoint's per-source timeout: a
	// hung source must degrade once its bounded context expires rather than
	// stalling the whole request.
	noteMixBlock bool

	// countExpiring* back CountExpiringNotes, the Overview endpoint's
	// expiring-soon tile. calls counts invocations so tests can assert the
	// source was never reached (e.g. the Overview 403 path).
	countExpiringCalls  int
	countExpiringAt     time.Time
	countExpiringWithin time.Duration
	countExpiringResult int64
	countExpiringErr    error
}

func (s *explorerLifecycle) NoteMix(
	ctx context.Context,
	_ onprem.HumanPrincipal,
	at time.Time,
) ([]explorer.NoteKindCount, error) {
	s.noteMixCalls++
	s.noteMixAt = at
	if s.noteMixBlock {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.noteMixErr != nil {
		return nil, s.noteMixErr
	}
	return s.noteMixResult, nil
}

func (s *explorerLifecycle) CountExpiringNotes(
	_ context.Context,
	_ onprem.HumanPrincipal,
	at time.Time,
	within time.Duration,
) (int64, error) {
	s.countExpiringCalls++
	s.countExpiringAt = at
	s.countExpiringWithin = within
	if s.countExpiringErr != nil {
		return 0, s.countExpiringErr
	}
	return s.countExpiringResult, nil
}

func (s *explorerLifecycle) ListTeamNotes(
	_ context.Context,
	_ onprem.HumanPrincipal,
	filter explorer.TeamNoteFilter,
) ([]explorer.TeamNoteSummary, error) {
	s.filter = filter
	if s.err != nil {
		return nil, s.err
	}
	return []explorer.TeamNoteSummary{
		explorerTestNote("note-1", s.now), explorerTestNote("note-0", s.now.Add(-time.Minute)),
	}, nil
}

func (s *explorerLifecycle) GetTeamNote(
	_ context.Context,
	_ onprem.HumanPrincipal,
	_ string,
) (explorer.TeamNoteDetail, error) {
	if s.err != nil {
		return explorer.TeamNoteDetail{}, s.err
	}
	run := explorerTestRun(s.now)
	return explorer.TeamNoteDetail{
		Note: explorer.TeamNote{
			TeamNoteSummary: explorerTestNote("note-1", s.now),
			Body:            "Waiting for approval", OriginUserID: "user", OriginSessionID: "session-source",
		},
		Revisions: []explorer.Revision{{
			Revision: 1, CandidateID: "candidate-1", Operation: "create",
			Body: "Waiting for approval", CreatedAt: s.now, Extraction: run,
			Candidate: explorer.Candidate{
				CandidateID: "candidate-1", Action: "create", Kind: "blocker",
				Subject: "Release candidate", Body: "Waiting for approval",
				OriginAgentID: "codex", EvidenceEventIDs: []string{"event-1"},
				AdmissionStatus: "admitted", CreatedAt: s.now,
				ResultingNoteID: "note-1",
			},
			Evidence: []explorer.SourceEvent{{
				EventID: "event-1", UserID: "user", AgentID: "codex",
				SessionID: "session-source", Sequence: 1, Type: "message",
				Content: "Source event content", OccurredAt: s.now, CapturedAt: s.now,
			}},
		}},
		RecallObservations: []explorer.RecallUse{{
			ObservationID: 41, RecipientAgentID: "claude",
			RecipientSessionID: "session-consumer", OccurredAt: s.now, Delivered: true,
		}},
	}, nil
}

func (s *explorerLifecycle) GetExtractionDiagnostic(
	_ context.Context,
	_ onprem.HumanPrincipal,
	_ string,
) (explorer.ExtractionDiagnostic, error) {
	if s.err != nil {
		return explorer.ExtractionDiagnostic{}, s.err
	}
	return explorer.ExtractionDiagnostic{
		Run: explorerTestRun(s.now),
		Candidates: []explorer.Candidate{{
			CandidateID: "candidate-1", Action: "create", Kind: "blocker",
			Subject: "Release", Body: "Rejected", OriginAgentID: "codex",
			AdmissionStatus: "rejected", RejectionReason: "missing_evidence", CreatedAt: s.now,
		}},
	}, nil
}

func (s *explorerLifecycle) GetChannelDiagnostic(
	_ context.Context,
	_ onprem.HumanPrincipal,
	_ string,
) (explorer.ChannelDiagnostic, error) {
	if s.err != nil {
		return explorer.ChannelDiagnostic{}, s.err
	}
	return explorer.ChannelDiagnostic{
		EnvelopeID: "envelope-1", FromAgentID: "codex", ToAgentID: "claude",
		Status: "pending", PayloadStatus: "decoded", CreatedAt: s.now,
		Capsule: explorer.KnowledgeCapsule{
			CapsuleID: "capsule-1", Title: "Release", Content: "Capsule content",
		},
	}, nil
}

func explorerTestNote(noteID string, updatedAt time.Time) explorer.TeamNoteSummary {
	return explorer.TeamNoteSummary{
		NoteID: noteID, Kind: "blocker", Subject: "Release", State: "active",
		OriginAgentID: "codex", Revision: 1, CreatedAt: updatedAt,
		UpdatedAt: updatedAt, SoftExpiresAt: updatedAt.Add(time.Hour),
		HardExpiresAt: updatedAt.Add(24 * time.Hour),
	}
}

func explorerTestRun(now time.Time) explorer.ExtractionRun {
	return explorer.ExtractionRun{
		RunID: "run-1", UserID: "user", AgentID: "codex",
		SessionID: "session-source", FromSequence: 1, ToSequence: 1,
		Model: "extractor", PromptVersion: "prompt-v1", Status: "completed",
		InputTokens: 20, OutputTokens: 5, CreatedAt: now,
	}
}
