package handler_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/sessionconsumer"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/mocks"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

type wikiIngestionHandlerSuite struct {
	suite.Suite
	controller  *gomock.Controller
	identity    *humanIdentityService
	wikiControl *wikiControlService
	handler     *handler.Handler
}

func TestWikiIngestionHandlerSuite(t *testing.T) {
	suite.Run(t, new(wikiIngestionHandlerSuite))
}

func (s *wikiIngestionHandlerSuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.identity = &humanIdentityService{principal: onprem.HumanPrincipal{
		UserID: "owner-user", MembershipID: "owner-membership", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive, ScopeID: onprem.LocalScopeID,
	}}
	s.wikiControl = &wikiControlService{}
	configured, err := handler.NewOnPrem(
		mocks.NewMockRuntime(s.controller), &credentialService{}, &memoryService{}, &channelService{},
		slog.New(slog.DiscardHandler),
		handler.WithHumanIdentity(s.identity, &oidcService{}, "/portal", false),
		handler.WithWikiControl(s.wikiControl),
	)
	s.Require().NoError(err)
	s.handler = configured
}

func (s *wikiIngestionHandlerSuite) TestOwnerConfirmsRebuildThroughGeneratedRoute() {
	response := s.perform(http.MethodPost, "/v1/wiki/rebuild", true)

	s.Equal(consts.StatusAccepted, response.Code)
	s.JSONEq(`{"auto_inject":true,"rebuild_state":"queued"}`, response.Body.String())
	s.Equal(1, s.wikiControl.rebuilds)
}

func (s *wikiIngestionHandlerSuite) TestRebuildRequiresOwnerAndCSRF() {
	s.identity.principal.Role = onprem.RoleMember
	response := s.perform(http.MethodPost, "/v1/wiki/rebuild", true)
	s.Equal(consts.StatusForbidden, response.Code)
	s.Contains(response.Body.String(), `"code":"forbidden"`)
	s.Zero(s.wikiControl.rebuilds)

	s.identity.principal.Role = onprem.RoleOwner
	response = s.perform(http.MethodPost, "/v1/wiki/rebuild", false)
	s.Equal(consts.StatusForbidden, response.Code)
	s.Contains(response.Body.String(), `"code":"csrf_invalid"`)
	s.Zero(s.wikiControl.rebuilds)
}

func (s *wikiIngestionHandlerSuite) TestRebuildFailureUsesStableInternalError() {
	s.wikiControl.rebuildErr = errors.New("database unavailable")

	response := s.perform(http.MethodPost, "/v1/wiki/rebuild", true)

	s.Equal(consts.StatusInternalServerError, response.Code)
	s.JSONEq(
		`{"code":"internal_error","message":"the request could not be completed"}`,
		response.Body.String(),
	)
}

func (s *wikiIngestionHandlerSuite) TestStatusIncludesProgressWhenAvailable() {
	processed := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	s.wikiControl.status = sessionconsumer.Status{
		AutoInject: true,
		Progress:   &sessionconsumer.Progress{PendingSessions: 3, LastProcessedAt: &processed},
	}

	response := s.perform(http.MethodGet, "/v1/wiki/ingestion", false)

	s.Equal(consts.StatusOK, response.Code)
	s.JSONEq(
		`{"auto_inject":true,"pending_sessions":3,"last_processed_at":"2026-07-29T08:00:00Z",`+
			`"rebuild_state":"idle"}`,
		response.Body.String(),
	)
}

func (s *wikiIngestionHandlerSuite) TestStatusOmitsProgressWhenUnavailable() {
	s.wikiControl.status = sessionconsumer.Status{AutoInject: true}

	response := s.perform(http.MethodGet, "/v1/wiki/ingestion", false)

	s.Equal(consts.StatusOK, response.Code)
	s.JSONEq(`{"auto_inject":true,"rebuild_state":"idle"}`, response.Body.String())
}

func (s *wikiIngestionHandlerSuite) TestUpdateIngestionExposesRebuildState() {
	response := s.performWithBody(http.MethodPut, "/v1/wiki/ingestion", true, `{"auto_inject":true}`)

	s.Equal(consts.StatusOK, response.Code)
	s.JSONEq(`{"auto_inject":true,"rebuild_state":"idle"}`, response.Body.String())
}

func (s *wikiIngestionHandlerSuite) TestIngestionStatusExposesRebuildFailure() {
	finished := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	s.wikiControl.status = sessionconsumer.Status{
		AutoInject: true,
		Rebuild: sessionconsumer.RebuildStatus{
			State:      sessionconsumer.RebuildFailed,
			Error:      "rebuild unavailable",
			FinishedAt: &finished,
		},
	}

	response := s.perform(http.MethodGet, "/v1/wiki/ingestion", false)

	s.Equal(consts.StatusOK, response.Code)
	s.JSONEq(
		`{"auto_inject":true,"rebuild_state":"failed","rebuild_error":"rebuild unavailable",`+
			`"last_rebuild_finished_at":"2026-08-01T10:00:00Z"}`,
		response.Body.String(),
	)
}

func (s *wikiIngestionHandlerSuite) TestRebuildForwardsParsedSinceCutoff() {
	response := s.performWithBody(http.MethodPost, "/v1/wiki/rebuild", true,
		`{"since":"2026-07-01T00:00:00Z"}`)

	s.Equal(consts.StatusAccepted, response.Code)
	s.Equal(1, s.wikiControl.rebuilds)
	s.Equal(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), s.wikiControl.since)
}

func (s *wikiIngestionHandlerSuite) TestRebuildAcceptsFractionalSecondSince() {
	response := s.performWithBody(http.MethodPost, "/v1/wiki/rebuild", true,
		`{"since":"2026-06-30T16:00:00.000Z"}`)

	s.Equal(consts.StatusAccepted, response.Code)
	s.Equal(1, s.wikiControl.rebuilds)
	s.Equal(time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC), s.wikiControl.since)
}

func (s *wikiIngestionHandlerSuite) TestRebuildRejectsMalformedSince() {
	response := s.performWithBody(http.MethodPost, "/v1/wiki/rebuild", true,
		`{"since":"yesterday"}`)

	s.Equal(consts.StatusBadRequest, response.Code)
	s.Equal(0, s.wikiControl.rebuilds)
}

func (s *wikiIngestionHandlerSuite) TestRebuildWithoutSincePassesZeroTime() {
	response := s.perform(http.MethodPost, "/v1/wiki/rebuild", true)

	s.Equal(consts.StatusAccepted, response.Code)
	s.Equal(1, s.wikiControl.rebuilds)
	s.True(s.wikiControl.since.IsZero())
}

// TestInjectWikiSessionValidatesAndMapsOutcomes covers the manual inject
// endpoint: a blank session_id answers 400 before touching the service,
// ErrSessionNotFound maps to 404, and success returns processed_streams for
// the principal's scope.
func (s *wikiIngestionHandlerSuite) TestInjectWikiSessionValidatesAndMapsOutcomes() {
	s.Run("blank session ID", func() {
		// A direct engine without the :session_id route parameter leaves
		// c.Param("session_id") blank, standing in for a blank path segment.
		engine := route.NewEngine(config.NewOptions([]config.Option{}))
		engine.Handle(http.MethodPost, "/", s.handler.InjectWikiSession)
		response := ut.PerformRequest(engine, http.MethodPost, "/", nil,
			ut.Header{Key: "Content-Type", Value: "application/json"},
			ut.Header{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"},
			ut.Header{Key: "X-CSRF-Token", Value: "csrf"},
		)

		s.Equal(consts.StatusBadRequest, response.Code)
		s.Contains(response.Body.String(), `"code":"invalid_request"`)
		s.Empty(s.wikiControl.injectedSession, "a blank session ID must not reach the service")
	})

	s.Run("session not found", func() {
		s.wikiControl.injectErr = sessionconsumer.ErrSessionNotFound
		defer func() { s.wikiControl.injectErr = nil }()

		response := s.performWithBody(http.MethodPost, "/v1/wiki/sessions/missing/inject", true, `{}`)

		s.Equal(consts.StatusNotFound, response.Code)
		s.Contains(response.Body.String(), `"code":"session_not_found"`)
	})

	s.Run("success returns processed streams", func() {
		s.wikiControl.injectResult = sessionconsumer.InjectResult{ProcessedStreams: 4}

		response := s.performWithBody(http.MethodPost, "/v1/wiki/sessions/session-1/inject", true, `{}`)

		s.Equal(consts.StatusOK, response.Code)
		s.JSONEq(`{"processed_streams":4}`, response.Body.String())
		s.Equal("session-1", s.wikiControl.injectedSession)
		s.Equal(onprem.LocalScopeID, s.wikiControl.injectedScope)
	})
}

// TestInjectWikiSessionAnswersNotImplementedWithoutWikiControl proves the
// endpoint degrades to 501 not_configured when no wiki control is wired.
func (s *wikiIngestionHandlerSuite) TestInjectWikiSessionAnswersNotImplementedWithoutWikiControl() {
	configured, err := handler.NewOnPrem(
		mocks.NewMockRuntime(s.controller), &credentialService{}, &memoryService{}, &channelService{},
		slog.New(slog.DiscardHandler),
		handler.WithHumanIdentity(s.identity, &oidcService{}, "/portal", false),
	)
	s.Require().NoError(err)

	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(configured))
	router.GeneratedRegister(hertz)
	body := `{}`
	response := ut.PerformRequest(hertz.Engine, http.MethodPost, "/v1/wiki/sessions/session-1/inject",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"},
		ut.Header{Key: "X-CSRF-Token", Value: "csrf"},
	)

	s.Equal(consts.StatusNotImplemented, response.Code)
	s.Contains(response.Body.String(), `"code":"not_configured"`)
}

func (s *wikiIngestionHandlerSuite) TestGeneratedRebuildRouteRequiresConfiguredRuntime() {
	hertz := server.New()
	router.GeneratedRegister(hertz)

	response := ut.PerformRequest(
		hertz.Engine,
		http.MethodPost,
		"/v1/wiki/rebuild",
		&ut.Body{Body: bytes.NewBufferString(`{}`), Len: 2},
	)

	s.Equal(consts.StatusInternalServerError, response.Code)
	s.Equal("runtime is not configured", response.Body.String())
}

func (s *wikiIngestionHandlerSuite) perform(method, path string, csrf bool) *ut.ResponseRecorder {
	return s.performWithBody(method, path, csrf, `{}`)
}

func (s *wikiIngestionHandlerSuite) performWithBody(
	method, path string,
	csrf bool,
	body string,
) *ut.ResponseRecorder {
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(s.handler))
	router.GeneratedRegister(hertz)
	headers := []ut.Header{
		{Key: "Content-Type", Value: "application/json"},
		{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"},
	}
	if csrf {
		headers = append(headers, ut.Header{Key: "X-CSRF-Token", Value: "csrf"})
	}
	payload := &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}
	return ut.PerformRequest(hertz.Engine, method, path, payload, headers...)
}

type wikiControlService struct {
	rebuilds   int
	rebuildErr error
	status     sessionconsumer.Status
	since      time.Time

	injectErr       error
	injectResult    sessionconsumer.InjectResult
	injectedScope   string
	injectedSession string
}

func (s *wikiControlService) Status(context.Context, string) (sessionconsumer.Status, error) {
	return s.status, nil
}

func (s *wikiControlService) SetAutoInject(
	_ context.Context,
	_ string,
	enabled bool,
) (sessionconsumer.Status, error) {
	return sessionconsumer.Status{AutoInject: enabled}, nil
}

func (s *wikiControlService) InjectSession(
	_ context.Context,
	scopeID string,
	sessionID string,
) (sessionconsumer.InjectResult, error) {
	s.injectedScope = scopeID
	s.injectedSession = sessionID
	if s.injectErr != nil {
		return sessionconsumer.InjectResult{}, s.injectErr
	}
	return s.injectResult, nil
}

func (s *wikiControlService) Rebuild(_ context.Context, _ string, since time.Time) (sessionconsumer.Status, error) {
	s.rebuilds++
	s.since = since
	return sessionconsumer.Status{
		AutoInject: true,
		Rebuild:    sessionconsumer.RebuildStatus{State: sessionconsumer.RebuildQueued},
	}, s.rebuildErr
}
