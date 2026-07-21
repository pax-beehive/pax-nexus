package handler_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/recall"
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

type onPremHandlerSuite struct {
	suite.Suite
	controller  *gomock.Controller
	runtime     *mocks.MockRuntime
	credentials *credentialService
	memory      *memoryService
	handler     *handler.Handler
}

func TestOnPremHandlerSuite(t *testing.T) {
	suite.Run(t, new(onPremHandlerSuite))
}

func (s *onPremHandlerSuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.runtime = mocks.NewMockRuntime(s.controller)
	s.credentials = &credentialService{}
	s.memory = &memoryService{}
	configured, err := handler.NewOnPrem(
		s.runtime,
		s.credentials,
		s.memory,
		slog.New(slog.DiscardHandler),
	)
	s.Require().NoError(err)
	s.handler = configured
}

func (s *onPremHandlerSuite) TestCredentialLifecycleEndpoints() {
	created := perform(s.handler.CreateAgentEnrollment, http.MethodPost,
		`{"user_id":"owner","agent_id":"agent-1","expires_in_seconds":300}`, "admin")
	s.Equal(consts.StatusOK, created.Code)
	s.Contains(created.Body.String(), `"token":"tm_enroll_token"`)

	exchanged := perform(s.handler.ExchangeAgentEnrollment, http.MethodPost, `{"token":"tm_enroll_token"}`, "")
	s.Equal(consts.StatusOK, exchanged.Code)
	s.Contains(exchanged.Body.String(), `"api_key":"tm_key_agent"`)

	identity := perform(s.handler.GetAgentIdentity, http.MethodGet, "", "agent")
	s.Equal(consts.StatusOK, identity.Code)
	s.Contains(identity.Body.String(), `"agent_id":"agent-1"`)

	rotated := perform(s.handler.RotateAgentCredential, http.MethodPost, `{}`, "agent")
	s.Equal(consts.StatusOK, rotated.Code)
	s.Contains(rotated.Body.String(), `"api_key":"tm_key_rotated"`)

	revoked := performWithPath(s.handler.RevokeAgentCredential, http.MethodDelete, "", "admin", "credential_id", "credential-1")
	s.Equal(consts.StatusOK, revoked.Code)
	s.Equal("credential-1", s.credentials.revokedID)
}

func (s *onPremHandlerSuite) TestObservationBindsActorFromCredential() {
	s.runtime.EXPECT().ObserveSession(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, batch teamnote.SessionBatch) (teamnote.IngestReceipt, error) {
			scopeID, err := teamnote.ScopeFromContext(ctx)
			s.Require().NoError(err)
			s.Equal(onprem.LocalScopeID, scopeID)
			s.Require().Len(batch.Events, 1)
			s.Equal("owner", batch.Events[0].Actor.UserID)
			s.Equal("agent-1", batch.Events[0].Actor.AgentID)
			s.Equal("session-1", batch.Events[0].Actor.SessionID)
			return teamnote.IngestReceipt{Accepted: 1, Cursor: 1}, nil
		},
	)
	body := `{"session_id":"session-1","idempotency_key":"batch-1","events":[{"id":"event-1","sequence":1,"type":"assistant","content":"Release approved.","occurred_at":"2026-07-21T08:00:00Z"}],"complete":true}`

	response := perform(s.handler.ObserveBatch, http.MethodPost, body, "agent")

	s.Equal(consts.StatusOK, response.Code)
	s.Contains(response.Body.String(), `"accepted":1`)
	s.Contains(response.Body.String(), `"idempotency_key":"batch-1"`)
}

func (s *onPremHandlerSuite) TestMemorySearchAndGetBindPrincipal() {
	search := perform(s.handler.SearchMemory, http.MethodPost,
		`{"intent":"passive","session_id":"session-1","query":"release","token_budget":64}`, "agent")
	s.Equal(consts.StatusOK, search.Code)
	s.Equal("owner", s.memory.searchRequest.Actor.UserID)
	s.Equal("agent-1", s.memory.searchRequest.Actor.AgentID)
	s.Contains(search.Body.String(), `"disposition":"evidence"`)

	get := perform(s.handler.GetMemory, http.MethodPost,
		`{"session_id":"session-1","ref":"wiki:release"}`, "agent")
	s.Equal(consts.StatusOK, get.Code)
	s.Equal("agent-1", s.memory.getRequest.Actor.AgentID)
	s.Contains(get.Body.String(), `"text":"Full document"`)
}

func (s *onPremHandlerSuite) TestOnPremEndpointsEnforceAuthenticationAndPermission() {
	response := perform(s.handler.ObserveBatch, http.MethodPost, `{}`, "wrong")
	s.Equal(consts.StatusUnauthorized, response.Code)
	response = perform(s.handler.CreateAgentEnrollment, http.MethodPost,
		`{"user_id":"owner","agent_id":"agent-1"}`, "agent")
	s.Equal(consts.StatusForbidden, response.Code)
}

func (s *onPremHandlerSuite) TestLegacyEndpointsAreDisabledInOnPremMode() {
	response := perform(s.handler.RecallNotes, http.MethodPost,
		`{"actor":{"user_id":"spoofed","agent_id":"spoofed","session_id":"session"},"token_budget":64}`, "legacy")

	s.Equal(consts.StatusUnauthorized, response.Code)
}

func (s *onPremHandlerSuite) TestAuthenticationStoreFailureIsNotReportedAsBadCredentials() {
	s.credentials.authErr = errors.New("credential store unavailable")

	response := perform(s.handler.GetAgentIdentity, http.MethodGet, "", "agent")

	s.Equal(consts.StatusInternalServerError, response.Code)
}

func (s *onPremHandlerSuite) TestGeneratedRoutesDispatchToConfiguredOnPremHandler() {
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(s.handler))
	router.GeneratedRegister(hertz)

	response := ut.PerformRequest(hertz.Engine, http.MethodGet, "/v1/agent-identity", nil,
		ut.Header{Key: "Authorization", Value: "Bearer agent"})

	s.Equal(consts.StatusOK, response.Code)
	s.Contains(response.Body.String(), `"credential_id":"credential-1"`)
}

type credentialService struct {
	revokedID string
	authErr   error
}

func (s *credentialService) Authenticate(_ context.Context, apiKey string) (onprem.Principal, error) {
	if s.authErr != nil {
		return onprem.Principal{}, s.authErr
	}
	switch apiKey {
	case "admin":
		return onprem.Principal{ScopeID: onprem.LocalScopeID, Permissions: []onprem.Permission{onprem.PermissionAdmin}}, nil
	case "agent":
		return onprem.Principal{
			UserID: "owner", AgentID: "agent-1", ScopeID: onprem.LocalScopeID, CredentialID: "credential-1",
			Permissions: []onprem.Permission{onprem.PermissionObserve, onprem.PermissionSearch, onprem.PermissionGet},
		}, nil
	default:
		return onprem.Principal{}, onprem.ErrUnauthorized
	}
}

func (s *credentialService) CreateEnrollment(
	_ context.Context,
	_ onprem.Principal,
	request onprem.EnrollmentRequest,
) (onprem.Enrollment, error) {
	return onprem.Enrollment{
		ID: "enrollment-1", Token: "tm_enroll_token", ExpiresAt: time.Now().Add(request.ExpiresIn),
	}, nil
}

func (s *credentialService) ExchangeEnrollment(context.Context, string) (onprem.IssuedCredential, error) {
	return onprem.IssuedCredential{CredentialID: "credential-1", APIKey: "tm_key_agent"}, nil
}

func (s *credentialService) RotateCredential(context.Context, onprem.Principal) (onprem.IssuedCredential, error) {
	return onprem.IssuedCredential{CredentialID: "credential-2", APIKey: "tm_key_rotated"}, nil
}

func (s *credentialService) RevokeCredential(_ context.Context, _ onprem.Principal, credentialID string) error {
	s.revokedID = credentialID
	return nil
}

type memoryService struct {
	searchRequest recall.SearchRequest
	getRequest    recall.GetRequest
}

func (s *memoryService) Search(_ context.Context, request recall.SearchRequest) (recall.SearchResult, error) {
	s.searchRequest = request
	return recall.SearchResult{
		Hits:               []recall.MemoryHit{{Ref: "note:1", Text: "Release approved.", Disposition: recall.DispositionEvidence}},
		EvidenceSufficient: true,
		Trace: recall.Trace{
			TeamNote:   recall.PathTrace{Status: recall.PathCompleted},
			WikiHint:   recall.PathTrace{Status: recall.PathSkipped},
			WikiSearch: recall.PathTrace{Status: recall.PathSkipped},
		},
	}, nil
}

func (s *memoryService) Get(_ context.Context, request recall.GetRequest) (recall.MemoryDocument, error) {
	s.getRequest = request
	return recall.MemoryDocument{Ref: request.Ref, Text: "Full document"}, nil
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

func performWithPath(
	handlerFunction app.HandlerFunc,
	method, body, apiKey, key, value string,
) *ut.ResponseRecorder {
	engine := route.NewEngine(config.NewOptions([]config.Option{}))
	engine.Handle(method, "/:"+key, handlerFunction)
	headers := []ut.Header{{Key: "Content-Type", Value: "application/json"}, {Key: "Authorization", Value: "Bearer " + apiKey}}
	var requestBody *ut.Body
	if body != "" {
		requestBody = &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}
	}
	return ut.PerformRequest(engine, method, "/"+value, requestBody, headers...)
}
