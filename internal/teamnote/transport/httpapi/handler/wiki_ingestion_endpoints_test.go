package handler_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
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
		MembershipStatus: onprem.MembershipStatusActive,
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

	s.Equal(consts.StatusOK, response.Code)
	s.JSONEq(`{"auto_inject":true}`, response.Body.String())
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
	body := &ut.Body{Body: bytes.NewBufferString(`{}`), Len: 2}
	return ut.PerformRequest(hertz.Engine, method, path, body, headers...)
}

type wikiControlService struct {
	rebuilds   int
	rebuildErr error
}

func (s *wikiControlService) Status(context.Context, string) (sessionconsumer.Status, error) {
	return sessionconsumer.Status{}, nil
}

func (s *wikiControlService) SetAutoInject(
	_ context.Context,
	_ string,
	enabled bool,
) (sessionconsumer.Status, error) {
	return sessionconsumer.Status{AutoInject: enabled}, nil
}

func (s *wikiControlService) InjectSession(
	context.Context,
	string,
	string,
) (sessionconsumer.InjectResult, error) {
	return sessionconsumer.InjectResult{}, nil
}

func (s *wikiControlService) Rebuild(context.Context, string) (sessionconsumer.Status, error) {
	s.rebuilds++
	return sessionconsumer.Status{AutoInject: true}, s.rebuildErr
}
