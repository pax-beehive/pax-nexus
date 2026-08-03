package handler_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/mocks"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

type wikiSettingsHandlerSuite struct {
	suite.Suite
	controller   *gomock.Controller
	identity     *humanIdentityService
	wikiSettings *wikiSettingsService
	handler      *handler.Handler
}

func TestWikiSettingsHandlerSuite(t *testing.T) {
	suite.Run(t, new(wikiSettingsHandlerSuite))
}

func (s *wikiSettingsHandlerSuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.identity = &humanIdentityService{principal: onprem.HumanPrincipal{
		UserID: "owner-user", MembershipID: "owner-membership", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive, ScopeID: onprem.LocalScopeID,
	}}
	s.wikiSettings = &wikiSettingsService{}
	configured, err := handler.NewOnPrem(
		mocks.NewMockRuntime(s.controller), &credentialService{}, &memoryService{}, &channelService{},
		slog.New(slog.DiscardHandler),
		handler.WithHumanIdentity(s.identity, &oidcService{}, "/portal", false),
		handler.WithWikiSettings(s.wikiSettings),
	)
	s.Require().NoError(err)
	s.handler = configured
}

func (s *wikiSettingsHandlerSuite) TestGetReturnsDefaults() {
	response := s.perform(http.MethodGet, "/v1/wiki/settings", true, "")

	s.Equal(consts.StatusOK, response.Code)
	s.JSONEq(`{"language":"","custom_instructions":""}`, response.Body.String())
}

func (s *wikiSettingsHandlerSuite) TestPutStoresAndEchoesSettings() {
	body := `{"language":"简体中文","custom_instructions":"prefer tables"}`

	response := s.perform(http.MethodPut, "/v1/wiki/settings", true, body)

	s.Equal(consts.StatusOK, response.Code)
	s.JSONEq(body, response.Body.String())
	s.Equal(pagewiki.GenerationDirectives{Language: "简体中文", CustomInstructions: "prefer tables"}, s.wikiSettings.stored)
}

func (s *wikiSettingsHandlerSuite) TestRequestsCarryThePrincipalsScope() {
	s.identity.principal.ScopeID = "other-scope"

	getResponse := s.perform(http.MethodGet, "/v1/wiki/settings", true, "")
	s.Equal(consts.StatusOK, getResponse.Code)
	s.Equal("other-scope", s.wikiSettings.getScope)

	putResponse := s.perform(http.MethodPut, "/v1/wiki/settings", true,
		`{"language":"","custom_instructions":""}`)
	s.Equal(consts.StatusOK, putResponse.Code)
	s.Equal("other-scope", s.wikiSettings.setScope)
}

func (s *wikiSettingsHandlerSuite) TestPutRejectsOverLongLanguage() {
	body := `{"language":"` + strings.Repeat("a", 65) + `","custom_instructions":""}`

	response := s.perform(http.MethodPut, "/v1/wiki/settings", true, body)

	s.Equal(consts.StatusBadRequest, response.Code)
	s.Contains(response.Body.String(), `"code":"invalid_request"`)
}

func (s *wikiSettingsHandlerSuite) TestSettingsRequireMembershipAndCSRF() {
	s.identity.principal.MembershipStatus = onprem.MembershipStatusSuspended
	response := s.perform(http.MethodGet, "/v1/wiki/settings", false, "")
	s.Equal(consts.StatusForbidden, response.Code)
	s.Contains(response.Body.String(), `"code":"membership_required"`)

	s.identity.principal.MembershipStatus = onprem.MembershipStatusActive
	response = s.perform(http.MethodPut, "/v1/wiki/settings", false,
		`{"language":"","custom_instructions":""}`)
	s.Equal(consts.StatusForbidden, response.Code)
	s.Contains(response.Body.String(), `"code":"csrf_invalid"`)
}

func (s *wikiSettingsHandlerSuite) TestRoutesRequireConfiguredWikiSettings() {
	unconfigured, err := handler.NewOnPrem(
		mocks.NewMockRuntime(s.controller), &credentialService{}, &memoryService{}, &channelService{},
		slog.New(slog.DiscardHandler),
		handler.WithHumanIdentity(s.identity, &oidcService{}, "/portal", false),
	)
	s.Require().NoError(err)
	s.handler = unconfigured

	getResponse := s.perform(http.MethodGet, "/v1/wiki/settings", true, "")
	s.Equal(consts.StatusNotImplemented, getResponse.Code)
	s.Contains(getResponse.Body.String(), `"code":"not_configured"`)

	putResponse := s.perform(http.MethodPut, "/v1/wiki/settings", true,
		`{"language":"","custom_instructions":""}`)
	s.Equal(consts.StatusNotImplemented, putResponse.Code)
	s.Contains(putResponse.Body.String(), `"code":"not_configured"`)
}

func (s *wikiSettingsHandlerSuite) perform(method, path string, csrf bool, body string) *ut.ResponseRecorder {
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
	if body == "" {
		body = "{}"
	}
	requestBody := &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}
	return ut.PerformRequest(hertz.Engine, method, path, requestBody, headers...)
}

type wikiSettingsService struct {
	stored   pagewiki.GenerationDirectives
	err      error
	getScope string
	setScope string
}

func (f *wikiSettingsService) GenerationSettings(_ context.Context, scopeID string) (pagewiki.GenerationDirectives, error) {
	f.getScope = scopeID
	return f.stored, f.err
}

func (f *wikiSettingsService) SetGenerationSettings(
	_ context.Context, scopeID string, d pagewiki.GenerationDirectives,
) (pagewiki.GenerationDirectives, error) {
	f.setScope = scopeID
	if f.err != nil {
		return pagewiki.GenerationDirectives{}, f.err
	}
	valid, err := pagewiki.ValidateGenerationDirectives(d)
	if err != nil {
		return pagewiki.GenerationDirectives{}, err
	}
	f.stored = valid
	return valid, nil
}
