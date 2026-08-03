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
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	platformllm "github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/mocks"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

type llmUsageHandlerSuite struct {
	suite.Suite
	controller *gomock.Controller
	identity   *humanIdentityService
	llmUsage   *llmUsageService
	handler    *handler.Handler
}

func TestLLMUsageHandlerSuite(t *testing.T) {
	suite.Run(t, new(llmUsageHandlerSuite))
}

func (s *llmUsageHandlerSuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.identity = &humanIdentityService{principal: onprem.HumanPrincipal{
		UserID: "owner-user", MembershipID: "owner-membership", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive, ScopeID: onprem.LocalScopeID,
	}}
	s.llmUsage = &llmUsageService{}
	configured, err := handler.NewOnPrem(
		mocks.NewMockRuntime(s.controller), &credentialService{}, &memoryService{}, &channelService{},
		slog.New(slog.DiscardHandler),
		handler.WithHumanIdentity(s.identity, &oidcService{}, "/portal", false),
		handler.WithLLMUsage(s.llmUsage),
	)
	s.Require().NoError(err)
	s.handler = configured
}

func (s *llmUsageHandlerSuite) TestGetDefaultsToSevenDayWindow() {
	s.llmUsage.rows = []platformllm.LLMUsageRow{
		{
			Component: "wiki-editor", Model: "deepseek-chat", Calls: 2,
			InputTokens: 300, CacheHitTokens: 60, CacheMissTokens: 240, OutputTokens: 50,
		},
	}

	response := s.perform("/v1/llm-usage")

	s.Equal(consts.StatusOK, response.Code)
	s.JSONEq(`{"rows":[{"component":"wiki-editor","model":"deepseek-chat","calls":2,`+
		`"input_tokens":300,"cache_hit_tokens":60,"cache_miss_tokens":240,"output_tokens":50}]}`,
		response.Body.String())
	s.Equal(7*24*time.Hour, s.llmUsage.window)
}

func (s *llmUsageHandlerSuite) TestGetHonorsDaysQueryParameter() {
	response := s.perform("/v1/llm-usage?days=30")

	s.Equal(consts.StatusOK, response.Code)
	s.Equal(30*24*time.Hour, s.llmUsage.window)
}

func (s *llmUsageHandlerSuite) TestGetRejectsOutOfRangeDays() {
	response := s.perform("/v1/llm-usage?days=0")
	s.Equal(consts.StatusBadRequest, response.Code)
	s.Contains(response.Body.String(), `"code":"invalid_request"`)

	response = s.perform("/v1/llm-usage?days=400")
	s.Equal(consts.StatusBadRequest, response.Code)
	s.Contains(response.Body.String(), `"code":"invalid_request"`)
}

func (s *llmUsageHandlerSuite) TestGetFailureUsesStableInternalError() {
	s.llmUsage.err = errors.New("database unavailable")

	response := s.perform("/v1/llm-usage")

	s.Equal(consts.StatusInternalServerError, response.Code)
	s.JSONEq(
		`{"code":"internal_error","message":"the request could not be completed"}`,
		response.Body.String(),
	)
}

func (s *llmUsageHandlerSuite) TestGetRequiresConfiguredLLMUsage() {
	unconfigured, err := handler.NewOnPrem(
		mocks.NewMockRuntime(s.controller), &credentialService{}, &memoryService{}, &channelService{},
		slog.New(slog.DiscardHandler),
		handler.WithHumanIdentity(s.identity, &oidcService{}, "/portal", false),
	)
	s.Require().NoError(err)
	s.handler = unconfigured

	response := s.perform("/v1/llm-usage")
	s.Equal(consts.StatusNotImplemented, response.Code)
	s.Contains(response.Body.String(), `"code":"not_configured"`)
}

func (s *llmUsageHandlerSuite) perform(path string) *ut.ResponseRecorder {
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(s.handler))
	router.GeneratedRegister(hertz)
	headers := []ut.Header{
		{Key: "Content-Type", Value: "application/json"},
		{Key: "Cookie", Value: "tm_human_session=session; tm_csrf=csrf"},
	}
	body := "{}"
	requestBody := &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}
	return ut.PerformRequest(hertz.Engine, http.MethodGet, path, requestBody, headers...)
}

type llmUsageService struct {
	rows   []platformllm.LLMUsageRow
	window time.Duration
	err    error
}

func (f *llmUsageService) UsageSummary(
	_ context.Context, _ string, window time.Duration,
) ([]platformllm.LLMUsageRow, error) {
	f.window = window
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}
