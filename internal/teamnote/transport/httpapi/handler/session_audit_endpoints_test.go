package handler_test

import (
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/audit"
	auditmocks "github.com/pax-beehive/pax-nexus/internal/audit/mocks"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/mocks"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

type sessionAuditHandlerSuite struct {
	suite.Suite
	controller *gomock.Controller
	identity   *humanIdentityService
	query      *auditmocks.MockQuery
	handler    *handler.Handler
}

func TestSessionAuditHandlerSuite(t *testing.T) {
	suite.Run(t, new(sessionAuditHandlerSuite))
}

func (s *sessionAuditHandlerSuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.identity = &humanIdentityService{principal: onprem.HumanPrincipal{
		UserID: "owner-user", MembershipID: "owner-membership", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive,
	}}
	s.query = auditmocks.NewMockQuery(s.controller)
	s.handler = s.newHandler(handler.WithSessionAudit(s.query))
}

func (s *sessionAuditHandlerSuite) newHandler(options ...handler.OnPremOption) *handler.Handler {
	base := []handler.OnPremOption{
		handler.WithAgentRegistry(&agentRegistryService{}),
		handler.WithHumanIdentity(s.identity, &oidcService{}, "/portal", false),
	}
	configured, err := handler.NewOnPrem(
		mocks.NewMockRuntime(s.controller), &credentialService{}, &memoryService{}, &channelService{},
		slog.New(slog.DiscardHandler), append(base, options...)...,
	)
	s.Require().NoError(err)
	return configured
}

func (s *sessionAuditHandlerSuite) perform(path string, headers ...ut.Header) *ut.ResponseRecorder {
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(s.handler))
	router.GeneratedRegister(hertz)
	allHeaders := append([]ut.Header{{Key: "Content-Type", Value: "application/json"}}, headers...)
	return ut.PerformRequest(hertz.Engine, http.MethodGet, path, nil, allHeaders...)
}

func (s *sessionAuditHandlerSuite) authenticated(path string) *ut.ResponseRecorder {
	return s.perform(path, ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
}

func (s *sessionAuditHandlerSuite) TestListsToolCallsWithFilters() {
	s.query.EXPECT().ListToolCalls(gomock.Any(), audit.ToolCallFilter{
		ScopeID: onprem.LocalScopeID, UserID: "user-1", AgentID: "agent-1", SessionID: "session-1",
		RiskLevel: "high", ApprovalState: audit.ApprovalApproved, Limit: 5,
	}).Return([]audit.ToolCall{{
		EventID: "event-1", UserID: "user-1", AgentID: "agent-1", SessionID: "session-1",
		CallID: "call-1", ToolName: "bash", InputSummary: "rm -rf /tmp/scratch",
		RiskLevel: audit.LevelCritical, RiskReasons: []audit.Reason{audit.ReasonDestructiveCommand},
		ApprovalState: audit.ApprovalApproved,
		OccurredAt:    time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC),
		CapturedAt:    time.Date(2026, time.July, 30, 10, 0, 1, 0, time.UTC),
	}}, nil)

	response := s.authenticated("/v1/admin/session-audit/tool-calls" +
		"?user_id=user-1&agent_id=agent-1&session_id=session-1&risk_level=high&approval_state=approved&limit=5")

	s.Equal(consts.StatusOK, response.Code)
	body := response.Body.String()
	s.Contains(body, `"event_id":"event-1"`)
	s.Contains(body, `"call_id":"call-1"`)
	s.Contains(body, `"tool_name":"bash"`)
	s.Contains(body, `"risk_level":"critical"`)
	s.Contains(body, `"risk_reasons":["destructive_command"]`)
	s.Contains(body, `"approval_state":"approved"`)
	s.Contains(body, `"occurred_at":"2026-07-30T10:00:00Z"`)
}

func (s *sessionAuditHandlerSuite) TestToolCallsDefaultsAndEmptyResult() {
	s.query.EXPECT().ListToolCalls(gomock.Any(), audit.ToolCallFilter{
		ScopeID: onprem.LocalScopeID, Limit: 50,
	}).Return(nil, nil)

	response := s.authenticated("/v1/admin/session-audit/tool-calls")

	s.Equal(consts.StatusOK, response.Code)
	s.JSONEq(`{"tool_calls":[]}`, response.Body.String())
}

func (s *sessionAuditHandlerSuite) TestRejectsInvalidToolCallFilters() {
	for _, path := range []string{
		"/v1/admin/session-audit/tool-calls?risk_level=bogus",
		"/v1/admin/session-audit/tool-calls?approval_state=maybe",
		"/v1/admin/session-audit/tool-calls?limit=0",
	} {
		s.Run(path, func() {
			response := s.authenticated(path)
			s.Equal(consts.StatusBadRequest, response.Code)
			s.JSONEq(`{"code":"invalid_request","message":"the request is invalid"}`, response.Body.String())
		})
	}
}

func (s *sessionAuditHandlerSuite) TestListsFindingsWithFilters() {
	s.query.EXPECT().ListFindings(gomock.Any(), audit.FindingFilter{
		ScopeID: onprem.LocalScopeID, UserID: "user-1", AgentID: "agent-1", SessionID: "session-1",
		Kind: audit.FindingHighRiskUnapproved, Severity: string(audit.LevelHigh), Limit: 7,
	}).Return([]audit.Finding{{
		FindingID: 42, UserID: "user-1", AgentID: "agent-1", SessionID: "session-1",
		Kind: audit.FindingHighRiskUnapproved, Severity: string(audit.LevelHigh),
		Summary: "high risk tool call without approval", EvidenceEventIDs: []string{"event-1"},
		CreatedAt: time.Date(2026, time.July, 30, 11, 0, 0, 0, time.UTC),
	}}, nil)

	response := s.authenticated("/v1/admin/session-audit/findings" +
		"?user_id=user-1&agent_id=agent-1&session_id=session-1&kind=high_risk_unapproved&severity=high&limit=7")

	s.Equal(consts.StatusOK, response.Code)
	body := response.Body.String()
	s.Contains(body, `"finding_id":42`)
	s.Contains(body, `"kind":"high_risk_unapproved"`)
	s.Contains(body, `"severity":"high"`)
	s.Contains(body, `"evidence_event_ids":["event-1"]`)
	s.Contains(body, `"created_at":"2026-07-30T11:00:00Z"`)
}

func (s *sessionAuditHandlerSuite) TestRejectsInvalidFindingFilters() {
	for _, path := range []string{
		"/v1/admin/session-audit/findings?kind=bogus",
		"/v1/admin/session-audit/findings?severity=bogus",
	} {
		s.Run(path, func() {
			response := s.authenticated(path)
			s.Equal(consts.StatusBadRequest, response.Code)
			s.JSONEq(`{"code":"invalid_request","message":"the request is invalid"}`, response.Body.String())
		})
	}
}

func (s *sessionAuditHandlerSuite) TestListsActivityWithDayRange() {
	s.query.EXPECT().GetActivityDaily(gomock.Any(), audit.ActivityFilter{
		ScopeID: onprem.LocalScopeID, UserID: "user-1", AgentID: "agent-1",
		FromDay: time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC),
		ToDay:   time.Date(2026, time.July, 31, 0, 0, 0, 0, time.UTC),
		Limit:   10,
	}).Return([]audit.ActivityDaily{{
		UserID: "user-1", AgentID: "agent-1",
		Day:        time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC),
		EventCount: 12, ToolCallCount: 5, HighRiskCount: 2, SessionCount: 3,
		ToolBreakdown: map[string]int64{"bash": 4, "read": 1},
	}}, nil)

	response := s.authenticated("/v1/admin/session-audit/activity" +
		"?user_id=user-1&agent_id=agent-1&from_day=2026-07-01&to_day=2026-07-31&limit=10")

	s.Equal(consts.StatusOK, response.Code)
	body := response.Body.String()
	s.Contains(body, `"day":"2026-07-30"`)
	s.Contains(body, `"event_count":12`)
	s.Contains(body, `"tool_call_count":5`)
	s.Contains(body, `"high_risk_count":2`)
	s.Contains(body, `"session_count":3`)
	s.Contains(body, `"tool_breakdown":{"bash":4,"read":1}`)
}

func (s *sessionAuditHandlerSuite) TestActivityLeavesDayRangeOpen() {
	s.query.EXPECT().GetActivityDaily(gomock.Any(), audit.ActivityFilter{
		ScopeID: onprem.LocalScopeID, Limit: 50,
	}).Return(nil, nil)

	response := s.authenticated("/v1/admin/session-audit/activity")

	s.Equal(consts.StatusOK, response.Code)
	s.JSONEq(`{"activity":[]}`, response.Body.String())
}

func (s *sessionAuditHandlerSuite) TestRejectsInvalidActivityDays() {
	for _, path := range []string{
		"/v1/admin/session-audit/activity?from_day=2026-13-01",
		"/v1/admin/session-audit/activity?to_day=not-a-day",
	} {
		s.Run(path, func() {
			response := s.authenticated(path)
			s.Equal(consts.StatusBadRequest, response.Code)
			s.JSONEq(`{"code":"invalid_request","message":"the request is invalid"}`, response.Body.String())
		})
	}
}

func (s *sessionAuditHandlerSuite) TestRequiresAuthentication() {
	for _, path := range []string{
		"/v1/admin/session-audit/tool-calls",
		"/v1/admin/session-audit/findings",
		"/v1/admin/session-audit/activity",
	} {
		s.Run(path, func() {
			response := s.perform(path)
			s.Equal(consts.StatusUnauthorized, response.Code)
			s.JSONEq(`{"code":"unauthorized","message":"authentication is required"}`, response.Body.String())
		})
	}
}

func (s *sessionAuditHandlerSuite) TestRequiresActiveMembership() {
	s.identity.principal.MembershipStatus = onprem.MembershipStatusSuspended

	response := s.authenticated("/v1/admin/session-audit/tool-calls")

	s.Equal(consts.StatusForbidden, response.Code)
	s.JSONEq(`{"code":"membership_required","message":"an active membership is required"}`, response.Body.String())
}

func (s *sessionAuditHandlerSuite) TestAnswersNotConfiguredWithoutQuery() {
	s.handler = s.newHandler()

	response := s.authenticated("/v1/admin/session-audit/tool-calls")

	s.Equal(consts.StatusNotImplemented, response.Code)
	s.JSONEq(`{"code":"not_configured","message":"session audit is not configured"}`, response.Body.String())
}

func (s *sessionAuditHandlerSuite) TestMapsStoreFailureToInternalError() {
	s.query.EXPECT().ListToolCalls(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("postgres is down"))

	response := s.authenticated("/v1/admin/session-audit/tool-calls")

	s.Equal(consts.StatusInternalServerError, response.Code)
	s.JSONEq(`{"code":"internal_error","message":"the request could not be completed"}`, response.Body.String())
}
