package handler_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/deployment/saas"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/mocks"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

// teamLifecycle is the handwritten fake TeamLifecycle, mirroring the
// humanIdentityService fake style in this package's tests.
type teamLifecycle struct {
	created            saas.Team
	createErr          error
	summaries          []saas.TeamSummary
	listErr            error
	switched           onprem.HumanPrincipal
	switchErr          error
	idempotencyKey     string
	createdName        string
	switchedTeamID     string
	listTeamsCallCount int
}

func (s *teamLifecycle) CreateTeam(
	_ context.Context,
	_ onprem.HumanPrincipal,
	name string,
	idempotencyKey string,
) (saas.Team, error) {
	s.createdName = name
	s.idempotencyKey = idempotencyKey
	if s.createErr != nil {
		return saas.Team{}, s.createErr
	}
	return s.created, nil
}

func (s *teamLifecycle) ListTeams(context.Context, onprem.HumanPrincipal) ([]saas.TeamSummary, error) {
	s.listTeamsCallCount++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.summaries, nil
}

func (s *teamLifecycle) SwitchTeam(
	_ context.Context,
	_ onprem.HumanPrincipal,
	teamID string,
) (onprem.HumanPrincipal, error) {
	s.switchedTeamID = teamID
	if s.switchErr != nil {
		return onprem.HumanPrincipal{}, s.switchErr
	}
	return s.switched, nil
}

type teamEndpointsSuite struct {
	suite.Suite
	controller *gomock.Controller
	identity   *humanIdentityService
	teams      *teamLifecycle
}

func TestTeamEndpointsSuite(t *testing.T) {
	suite.Run(t, new(teamEndpointsSuite))
}

func (s *teamEndpointsSuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.identity = &humanIdentityService{principal: onprem.HumanPrincipal{
		UserID: "owner-user", MembershipID: "owner-membership", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive, Email: "owner@example.com", EmailVerified: true,
		ScopeID: "team_alpha", SessionID: "session",
	}}
	s.teams = &teamLifecycle{
		created: saas.Team{
			TeamID: "team_alpha", Name: "Acme Corp", Slug: "acme-corp",
			CreatedByUserID: "owner-user", CreatedAt: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
			ResourceVersion: 1,
		},
		summaries: []saas.TeamSummary{
			{Team: saas.Team{TeamID: "team_alpha", Name: "Acme Corp", Slug: "acme-corp"},
				Role: onprem.RoleOwner, MembershipID: "owner-membership"},
			{Team: saas.Team{TeamID: "team_beta", Name: "Side Project", Slug: "side-project"},
				Role: onprem.RoleMember, MembershipID: "beta-membership"},
		},
		switched: onprem.HumanPrincipal{
			UserID: "owner-user", MembershipID: "beta-membership", Role: onprem.RoleMember,
			MembershipStatus: onprem.MembershipStatusActive, Email: "owner@example.com",
			EmailVerified: true, ScopeID: "team_beta", SessionID: "session",
		},
	}
}

func (s *teamEndpointsSuite) newHandler(withTeams bool) *handler.Handler {
	options := []handler.OnPremOption{
		handler.WithAgentRegistry(&agentRegistryService{}),
		handler.WithHumanIdentity(s.identity, &oidcService{}, "/portal", false),
	}
	if withTeams {
		options = append(options, handler.WithTeams(s.teams))
	}
	configured, err := handler.NewOnPrem(
		mocks.NewMockRuntime(s.controller), &credentialService{}, &memoryService{}, &channelService{},
		slog.New(slog.DiscardHandler), options...,
	)
	s.Require().NoError(err)
	return configured
}

func (s *teamEndpointsSuite) perform(
	configured *handler.Handler,
	method string,
	path string,
	body string,
	headers ...ut.Header,
) *ut.ResponseRecorder {
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(configured))
	router.GeneratedRegister(hertz)
	allHeaders := append([]ut.Header{{Key: "Content-Type", Value: "application/json"}}, headers...)
	var requestBody *ut.Body
	if body != "" {
		requestBody = &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}
	}
	return ut.PerformRequest(hertz.Engine, method, path, requestBody, allHeaders...)
}

func mutationHeaders() []ut.Header {
	return []ut.Header{
		{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"},
		{Key: "X-CSRF-Token", Value: "csrf"},
	}
}

func (s *teamEndpointsSuite) TestCreateTeam() {
	headers := append(mutationHeaders(), ut.Header{Key: "Idempotency-Key", Value: "key-1"})
	response := s.perform(s.newHandler(true), http.MethodPost, "/v1/teams", `{"name":"Acme Corp"}`, headers...)

	s.Equal(consts.StatusCreated, response.Code)
	s.Equal("Acme Corp", s.teams.createdName)
	s.Equal("key-1", s.teams.idempotencyKey, "the Idempotency-Key header passes through")
	body := response.Body.String()
	s.Contains(body, `"team_id":"team_alpha"`)
	s.Contains(body, `"slug":"acme-corp"`)
	s.Contains(body, `"resource_version":1`)
}

func (s *teamEndpointsSuite) TestCreateTeamRejections() {
	s.Run("unauthenticated", func() {
		// Valid CSRF pair but no session cookie: the mutation passes the
		// CSRF gate and fails authentication.
		response := s.perform(s.newHandler(true), http.MethodPost, "/v1/teams", `{"name":"Acme"}`,
			ut.Header{Key: "Cookie", Value: "tm_csrf=csrf"},
			ut.Header{Key: "X-CSRF-Token", Value: "csrf"})
		s.Equal(consts.StatusUnauthorized, response.Code)
	})
	s.Run("missing csrf", func() {
		response := s.perform(s.newHandler(true), http.MethodPost, "/v1/teams", `{"name":"Acme"}`,
			ut.Header{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"})
		s.Equal(consts.StatusForbidden, response.Code)
	})

	s.Run("slug conflict maps to 409", func() {
		s.teams.createErr = saas.ErrTeamSlugConflict
		response := s.perform(s.newHandler(true), http.MethodPost, "/v1/teams", `{"name":"Acme"}`, mutationHeaders()...)
		s.Equal(consts.StatusConflict, response.Code)
		s.JSONEq(`{"code":"team_slug_conflict","message":"the requested change conflicts with current state"}`,
			response.Body.String())
	})

	s.Run("teams not configured maps to 501", func() {
		response := s.perform(s.newHandler(false), http.MethodPost, "/v1/teams", `{"name":"Acme"}`, mutationHeaders()...)
		s.Equal(consts.StatusNotImplemented, response.Code)
		s.JSONEq(`{"code":"not_configured","message":"teams are not configured"}`, response.Body.String())
	})
}

func (s *teamEndpointsSuite) TestListTeams() {
	response := s.perform(s.newHandler(true), http.MethodGet, "/v1/teams", "",
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})

	s.Equal(consts.StatusOK, response.Code)
	s.JSONEq(`{"teams":[
		{"team_id":"team_alpha","name":"Acme Corp","slug":"acme-corp","role":"owner","membership_id":"owner-membership"},
		{"team_id":"team_beta","name":"Side Project","slug":"side-project","role":"member","membership_id":"beta-membership"}
	]}`, response.Body.String())

	unauthenticated := s.perform(s.newHandler(true), http.MethodGet, "/v1/teams", "")
	s.Equal(consts.StatusUnauthorized, unauthenticated.Code)
}

func (s *teamEndpointsSuite) TestSwitchCurrentTeam() {
	s.Run("returns the re-scoped principal with the team payload", func() {
		response := s.perform(s.newHandler(true), http.MethodPost, "/v1/me/current-team",
			`{"team_id":"team_beta"}`, mutationHeaders()...)

		s.Equal(consts.StatusOK, response.Code)
		s.Equal("team_beta", s.teams.switchedTeamID)
		body := response.Body.String()
		s.Contains(body, `"membership_id":"beta-membership"`)
		s.Contains(body, `"current_team_id":"team_beta"`)
		s.Contains(body, `"team_id":"team_alpha"`, "the response still carries the full team list")
	})

	s.Run("non-member maps to 403", func() {
		s.teams.switchErr = saas.ErrNotTeamMember
		response := s.perform(s.newHandler(true), http.MethodPost, "/v1/me/current-team",
			`{"team_id":"team_gamma"}`, mutationHeaders()...)
		s.Equal(consts.StatusForbidden, response.Code)
		s.JSONEq(`{"code":"not_team_member","message":"the operation is not permitted"}`, response.Body.String())
	})

	s.Run("unauthenticated", func() {
		// Valid CSRF pair but no session cookie: the mutation passes the
		// CSRF gate and fails authentication.
		response := s.perform(s.newHandler(true), http.MethodPost, "/v1/me/current-team",
			`{"team_id":"team_beta"}`,
			ut.Header{Key: "Cookie", Value: "tm_csrf=csrf"},
			ut.Header{Key: "X-CSRF-Token", Value: "csrf"})
		s.Equal(consts.StatusUnauthorized, response.Code)
	})
}

func (s *teamEndpointsSuite) TestNilChannelAnswersNotImplemented() {
	// The SaaS profile wires no channel lifecycle; the handler must build
	// without it and the channel endpoints answer 501 instead of panicking.
	configured, err := handler.NewOnPrem(
		mocks.NewMockRuntime(s.controller), &credentialService{}, &memoryService{}, nil,
		slog.New(slog.DiscardHandler),
		handler.WithAgentRegistry(&agentRegistryService{}),
		handler.WithHumanIdentity(s.identity, &oidcService{}, "/portal", false),
	)
	s.Require().NoError(err)

	response := s.perform(configured, http.MethodPost, "/v1/channel/envelopes",
		`{"recipient_agent_id":"agent","payload":{}}`,
		ut.Header{Key: "Authorization", Value: "Bearer agent"})
	s.Equal(consts.StatusNotImplemented, response.Code)
}

func (s *teamEndpointsSuite) TestHumanMeCarriesTeamsOnlyInTheSaaSProfile() {
	s.Run("saas profile populates teams and current team", func() {
		response := s.perform(s.newHandler(true), http.MethodGet, "/v1/me", "",
			ut.Header{Key: "Cookie", Value: "tm_human_session=session"})

		s.Equal(consts.StatusOK, response.Code)
		body := response.Body.String()
		s.Contains(body, `"current_team_id":"team_alpha"`)
		s.Contains(body, `"teams":[{"team_id":"team_alpha"`)
	})

	s.Run("on-prem profile keeps the response without team fields", func() {
		callsBefore := s.teams.listTeamsCallCount
		response := s.perform(s.newHandler(false), http.MethodGet, "/v1/me", "",
			ut.Header{Key: "Cookie", Value: "tm_human_session=session"})

		s.Equal(consts.StatusOK, response.Code)
		body := response.Body.String()
		s.NotContains(body, "teams")
		s.NotContains(body, "current_team_id")
		s.Contains(body, `"user_id":"owner-user"`)
		s.Equal(callsBefore, s.teams.listTeamsCallCount, "on-prem responses never consult the team lifecycle")
	})
}
