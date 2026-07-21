package onprem_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/stretchr/testify/suite"
)

type channelServiceSuite struct {
	suite.Suite
	store     *channelStore
	service   *onprem.ChannelService
	now       time.Time
	principal onprem.Principal
}

func TestChannelServiceSuite(t *testing.T) {
	suite.Run(t, new(channelServiceSuite))
}

func (s *channelServiceSuite) SetupTest() {
	s.now = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	s.store = &channelStore{
		recipient: onprem.AgentIdentity{UserID: "recipient-user", AgentID: "recipient-agent"},
	}
	service, err := onprem.NewChannelService(
		s.store,
		onprem.WithChannelClock(func() time.Time { return s.now }),
		onprem.WithChannelIDSource(func() (string, error) { return "fixed", nil }),
	)
	s.Require().NoError(err)
	s.service = service
	s.principal = onprem.Principal{
		UserID: "sender-user", AgentID: "sender-agent", ScopeID: onprem.LocalScopeID,
	}
}

func (s *channelServiceSuite) TestSendBindsSenderAndResolvesRecipient() {
	envelope, err := s.service.Send(context.Background(), s.principal, validSendRequest())

	s.Require().NoError(err)
	s.Equal("tm_env_fixed", envelope.ID)
	s.Equal("sender-user", envelope.FromUserID)
	s.Equal("sender-agent", envelope.FromAgentID)
	s.Equal("recipient-user", envelope.ToUserID)
	s.Equal("recipient-agent", envelope.ToAgentID)
	s.Equal(onprem.EnvelopeStatusPending, envelope.Status)
	s.Equal(s.now, envelope.CreatedAt)
	s.Equal("recipient-agent", s.store.resolvedAgentID)
}

func (s *channelServiceSuite) TestSendReplaysBeforeResolvingRecipient() {
	request := validSendRequest()
	s.store.existing = &onprem.ChannelEnvelope{
		ID: "tm_env_existing", FromUserID: s.principal.UserID, FromAgentID: s.principal.AgentID,
		ToAgentID: request.ToAgentID, PayloadType: request.PayloadType, PayloadJSON: request.PayloadJSON,
		Message: request.Message, IdempotencyKey: request.IdempotencyKey,
	}

	envelope, err := s.service.Send(context.Background(), s.principal, request)

	s.Require().NoError(err)
	s.Equal("tm_env_existing", envelope.ID)
	s.Empty(s.store.resolvedAgentID)
}

func (s *channelServiceSuite) TestSendValidationMatrix() {
	tests := []struct {
		name   string
		mutate func(*onprem.SendEnvelopeRequest)
	}{
		{name: "target", mutate: func(request *onprem.SendEnvelopeRequest) { request.ToAgentID = "" }},
		{name: "idempotency", mutate: func(request *onprem.SendEnvelopeRequest) { request.IdempotencyKey = "" }},
		{name: "payload type", mutate: func(request *onprem.SendEnvelopeRequest) { request.PayloadType = "other" }},
		{name: "payload json", mutate: func(request *onprem.SendEnvelopeRequest) { request.PayloadJSON = json.RawMessage(`{`) }},
		{name: "schema", mutate: func(request *onprem.SendEnvelopeRequest) {
			request.PayloadJSON = json.RawMessage(`{"schema_version":"v3","capsule":{}}`)
		}},
		{name: "capsule", mutate: func(request *onprem.SendEnvelopeRequest) {
			request.PayloadJSON = json.RawMessage(`{"schema_version":"paxl.envelope_payload.knowledge_capsule.v2","capsule":{}}`)
		}},
		{name: "route value", mutate: func(request *onprem.SendEnvelopeRequest) {
			request.PayloadJSON = json.RawMessage(`{"schema_version":"paxl.envelope_payload.knowledge_capsule.v2","capsule":{"capsule_id":"kcap-1","source_session_id":"session-1","source_agent":"codex","keyword":"handoff","title":"Handoff","summary":"summary","content":"content","status":"active","truncated":false,"original_estimated_chars":7},"route":{"match_type":"project"}}`)
		}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			request := validSendRequest()
			test.mutate(&request)
			_, err := s.service.Send(context.Background(), s.principal, request)
			s.ErrorIs(err, onprem.ErrInvalidChannelRequest)
		})
	}
}

func (s *channelServiceSuite) TestSendAcceptsOptionalRouteAndUnicodeCharacterLimits() {
	request := validSendRequest()
	request.PayloadJSON = json.RawMessage(`{
		"schema_version":"paxl.envelope_payload.knowledge_capsule.v2",
		"capsule":{"capsule_id":"kcap-1","source_session_id":"session-1","source_agent":"codex","keyword":"handoff","title":"Handoff","summary":"summary","content":"content","status":"active","truncated":false,"original_estimated_chars":7}
	}`)
	request.Message = strings.Repeat("知", 1000)

	_, err := s.service.Send(context.Background(), s.principal, request)

	s.Require().NoError(err)
}

func (s *channelServiceSuite) TestListNormalizesAndValidatesFilters() {
	_, err := s.service.List(context.Background(), s.principal, onprem.ListEnvelopesFilter{})
	s.Require().NoError(err)
	s.Equal(onprem.EnvelopeDirectionReceived, s.store.filter.Direction)

	_, err = s.service.List(context.Background(), s.principal, onprem.ListEnvelopesFilter{Direction: "broadcast"})
	s.Require().ErrorIs(err, onprem.ErrInvalidChannelRequest)

	_, err = s.service.List(context.Background(), s.principal, onprem.ListEnvelopesFilter{Status: "unknown"})
	s.Require().ErrorIs(err, onprem.ErrInvalidChannelRequest)
}

func (s *channelServiceSuite) TestGetAcceptAndArchiveDelegateBoundPrincipal() {
	_, err := s.service.Get(context.Background(), s.principal, "env-1")
	s.Require().NoError(err)
	_, err = s.service.Accept(context.Background(), s.principal, "env-1")
	s.Require().NoError(err)
	_, err = s.service.Archive(context.Background(), s.principal, "env-1")
	s.Require().NoError(err)
	s.Equal([]string{"get:env-1", "accept:env-1", "archive:env-1"}, s.store.operations)
}

func validSendRequest() onprem.SendEnvelopeRequest {
	return onprem.SendEnvelopeRequest{
		ToAgentID: "recipient-agent", PayloadType: onprem.EnvelopePayloadKnowledgeCapsule,
		PayloadJSON: json.RawMessage(`{
			"schema_version":"paxl.envelope_payload.knowledge_capsule.v2",
			"capsule":{"capsule_id":"kcap-1","source_session_id":"session-1","source_agent":"codex","keyword":"handoff","title":"Handoff","summary":"summary","content":"handoff","status":"active","truncated":false,"original_estimated_chars":7},
			"route":{"match_type":"project","match_value":"team-memory","target_agent":"codex"}
		}`),
		Message: "review", IdempotencyKey: "send-1",
	}
}

type channelStore struct {
	recipient       onprem.AgentIdentity
	existing        *onprem.ChannelEnvelope
	resolvedAgentID string
	created         onprem.ChannelEnvelope
	filter          onprem.ListEnvelopesFilter
	operations      []string
}

func (s *channelStore) FindChannelEnvelopeByIdempotency(
	_ context.Context,
	_ onprem.Principal,
	_ string,
) (onprem.ChannelEnvelope, bool, error) {
	if s.existing != nil {
		return *s.existing, true, nil
	}
	return onprem.ChannelEnvelope{}, false, nil
}

func (s *channelStore) ResolveActiveAgent(
	_ context.Context,
	agentID string,
	_ time.Time,
) (onprem.AgentIdentity, error) {
	s.resolvedAgentID = agentID
	return s.recipient, nil
}

func (s *channelStore) CreateChannelEnvelope(
	_ context.Context,
	envelope onprem.ChannelEnvelope,
) (onprem.ChannelEnvelope, error) {
	s.created = envelope
	return envelope, nil
}

func (s *channelStore) ListChannelEnvelopes(
	_ context.Context,
	_ onprem.Principal,
	filter onprem.ListEnvelopesFilter,
) ([]onprem.ChannelEnvelope, error) {
	s.filter = filter
	return []onprem.ChannelEnvelope{}, nil
}

func (s *channelStore) GetChannelEnvelope(
	_ context.Context,
	_ onprem.Principal,
	envelopeID string,
) (onprem.ChannelEnvelope, error) {
	s.operations = append(s.operations, "get:"+envelopeID)
	return onprem.ChannelEnvelope{ID: envelopeID}, nil
}

func (s *channelStore) AcceptChannelEnvelope(
	_ context.Context,
	_ onprem.Principal,
	envelopeID string,
	_ time.Time,
) (onprem.ChannelEnvelope, error) {
	s.operations = append(s.operations, "accept:"+envelopeID)
	return onprem.ChannelEnvelope{ID: envelopeID, Status: onprem.EnvelopeStatusAccepted}, nil
}

func (s *channelStore) ArchiveChannelEnvelope(
	_ context.Context,
	_ onprem.Principal,
	envelopeID string,
	_ time.Time,
) (onprem.ChannelEnvelope, error) {
	s.operations = append(s.operations, "archive:"+envelopeID)
	return onprem.ChannelEnvelope{ID: envelopeID, Status: onprem.EnvelopeStatusArchived}, nil
}
