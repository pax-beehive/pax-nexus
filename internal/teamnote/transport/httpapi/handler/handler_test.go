package handler_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/mocks"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

type handlerSuite struct {
	suite.Suite
	controller *gomock.Controller
	runtime    *mocks.MockRuntime
	handler    *handler.Handler
	logs       bytes.Buffer
}

func TestHandlerSuite(t *testing.T) {
	suite.Run(t, new(handlerSuite))
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	t.Parallel()
	controller := gomock.NewController(t)
	runtime := mocks.NewMockRuntime(controller)
	resolver := handler.StaticAPIKeys{"secret": "scope"}
	logger := slog.New(slog.DiscardHandler)
	tests := []struct {
		name     string
		runtime  teamnote.Runtime
		resolver handler.ScopeResolver
		logger   *slog.Logger
	}{
		{name: "runtime", resolver: resolver, logger: logger},
		{name: "resolver", runtime: runtime, logger: logger},
		{name: "logger", runtime: runtime, resolver: resolver},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			configured, err := handler.New(test.runtime, test.resolver, test.logger)
			require.Error(t, err)
			require.Nil(t, configured)
		})
	}
}

func TestGeneratedBridgesRequireAHandlerInstance(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		bridge app.HandlerFunc
	}{
		{name: "observe", bridge: handler.ObserveSession},
		{name: "recall", bridge: handler.RecallNotes},
		{name: "health", bridge: handler.Health},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := perform(test.bridge, http.MethodGet, "", "")
			require.Equal(t, consts.StatusInternalServerError, response.Code)
		})
	}
}

func (s *handlerSuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.runtime = mocks.NewMockRuntime(s.controller)
	logger := slog.New(slog.NewJSONHandler(&s.logs, nil))
	configured, err := handler.New(s.runtime, handler.StaticAPIKeys{"secret": "scope-1"}, logger)
	s.Require().NoError(err)
	s.handler = configured
}

func (s *handlerSuite) TestObserveSession() {
	s.runtime.EXPECT().ObserveSession(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, batch teamnote.SessionBatch) (teamnote.IngestReceipt, error) {
			scopeID, err := teamnote.ScopeFromContext(ctx)
			s.Require().NoError(err)
			s.Equal("scope-1", scopeID)
			s.Require().Len(batch.Events, 1)
			s.Equal("producer", batch.Events[0].Actor.AgentID)
			return teamnote.IngestReceipt{Accepted: 1, Cursor: 1, RunID: "run-1"}, nil
		},
	)
	body := `{"events":[{"id":"event-1","actor":{"user_id":"owner","agent_id":"producer","session_id":"session-1"},"sequence":1,"type":"assistant","content":"Tests fail.","task_ref":"release-42","occurred_at":"2026-07-14T12:00:00Z"}],"complete":true}`
	response := perform(s.handler.ObserveSession, http.MethodPost, body, "secret")
	s.Equal(consts.StatusOK, response.Code)
	s.Contains(response.Body.String(), `"accepted":1`)
	s.Contains(s.logs.String(), `"msg":"session batch observed"`)
	s.Contains(s.logs.String(), `"agent_id":"producer"`)
	s.Contains(s.logs.String(), `"run_id":"run-1"`)
}

func (s *handlerSuite) TestRecallNotes() {
	s.runtime.EXPECT().RecallNotes(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, request teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
			scopeID, err := teamnote.ScopeFromContext(ctx)
			s.Require().NoError(err)
			s.Equal("scope-1", scopeID)
			s.Equal("consumer", request.Actor.AgentID)
			s.Equal("When is the deadline?", request.Query)
			s.Equal(3, request.MaxItems)
			return teamnote.NoteEnvelope{
				Revision: "note:1", Items: []string{"[blocker] Tests fail."}, Tokens: 7,
				Details: []teamnote.RecalledNote{{
					NoteID: "note", Revision: 1, Text: "[blocker] Tests fail.",
					Relevance: 0.5, Certainty: teamnote.CertaintyUnresolved,
					Origin: teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"},
				}},
			}, nil
		},
	)
	body := `{"actor":{"user_id":"owner","agent_id":"consumer","session_id":"session-2"},"task_ref":"release-42","token_budget":256,"query":"When is the deadline?","max_items":3}`
	response := perform(s.handler.RecallNotes, http.MethodPost, body, "secret")
	s.Equal(consts.StatusOK, response.Code)
	s.Contains(response.Body.String(), "Tests fail")
	s.Contains(response.Body.String(), `"agent_id":"producer"`)
	s.Contains(response.Body.String(), `"session_id":"producer-session"`)
	s.Contains(response.Body.String(), `"relevance":0.5`)
	s.Contains(response.Body.String(), `"certainty":"unresolved"`)
	s.Contains(s.logs.String(), `"msg":"team notes recalled"`)
	s.Contains(s.logs.String(), `"notes":1`)
	s.Contains(s.logs.String(), `"tokens":7`)
}

func (s *handlerSuite) TestRuntimeErrorIsLoggedButNotExposed() {
	s.runtime.EXPECT().RecallNotes(gomock.Any(), gomock.Any()).Return(
		teamnote.NoteEnvelope{}, errors.New("database password leaked"),
	)
	body := `{"actor":{"user_id":"owner","agent_id":"consumer","session_id":"session-2"},"token_budget":256}`
	response := perform(s.handler.RecallNotes, http.MethodPost, body, "secret")
	s.Equal(consts.StatusUnprocessableEntity, response.Code)
	s.Equal("recall notes", response.Body.String())
	s.NotContains(response.Body.String(), "password")
	s.Contains(s.logs.String(), `"msg":"recall notes failed"`)
	s.Contains(s.logs.String(), "database password leaked")
}

func (s *handlerSuite) TestUnauthorizedRequestIsRejected() {
	body := `{"events":[],"complete":true}`
	response := perform(s.handler.ObserveSession, http.MethodPost, body, "wrong")
	s.Equal(consts.StatusUnauthorized, response.Code)
}

func (s *handlerSuite) TestMalformedRequestsAreRejected() {
	for _, test := range []struct {
		name     string
		endpoint app.HandlerFunc
	}{
		{name: "observe", endpoint: s.handler.ObserveSession},
		{name: "recall", endpoint: s.handler.RecallNotes},
	} {
		s.Run(test.name, func() {
			response := perform(test.endpoint, http.MethodPost, `{`, "secret")
			s.Equal(consts.StatusBadRequest, response.Code)
		})
	}
}

func (s *handlerSuite) TestHealth() {
	response := perform(s.handler.Health, http.MethodGet, "", "")
	s.Equal(consts.StatusOK, response.Code)
	s.Contains(response.Body.String(), `"status":"ok"`)
}

func (s *handlerSuite) TestInstanceMiddlewareRoutesGeneratedBridge() {
	s.runtime.EXPECT().RecallNotes(gomock.Any(), gomock.Any()).Return(teamnote.NoteEnvelope{}, nil)
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(s.handler))
	router.GeneratedRegister(hertz)
	body := `{"actor":{"user_id":"owner","agent_id":"consumer","session_id":"session-2"},"token_budget":64}`

	response := ut.PerformRequest(hertz.Engine, http.MethodPost, "/v1/notes/recall", &ut.Body{
		Body: bytes.NewBufferString(body), Len: len(body),
	}, ut.Header{Key: "Content-Type", Value: "application/json"}, ut.Header{Key: "Authorization", Value: "Bearer secret"})

	s.Equal(consts.StatusOK, response.Code)
}

func perform(handlerFunction app.HandlerFunc, method, body, apiKey string) *ut.ResponseRecorder {
	engine := route.NewEngine(config.NewOptions([]config.Option{}))
	engine.Handle(method, "/", handlerFunction)
	headers := []ut.Header{{Key: "Content-Type", Value: "application/json"}}
	if apiKey != "" {
		headers = append(headers, ut.Header{Key: "Authorization", Value: "Bearer " + apiKey})
	}
	var requestBody *ut.Body
	if body != "" {
		requestBody = &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}
	}
	return ut.PerformRequest(engine, method, "/", requestBody, headers...)
}
