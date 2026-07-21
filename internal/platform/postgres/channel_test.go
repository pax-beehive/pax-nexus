package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/suite"
)

type channelStoreSuite struct {
	suite.Suite
	store       *postgres.Store
	credentials *onprem.CredentialService
	channel     *onprem.ChannelService
	admin       onprem.Principal
	now         time.Time
	sequence    atomic.Int64
	idPrefix    string
}

func TestChannelStoreSuite(t *testing.T) {
	suite.Run(t, new(channelStoreSuite))
}

func (s *channelStoreSuite) SetupSuite() {
	store, err := postgres.Open(context.Background(), testDSN(s.T()))
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(context.Background()))
	s.store = store
	s.now = time.Now().UTC()
	s.idPrefix = uniqueCredentialValue("channel-envelope")
	adminKey := uniqueCredentialValue("channel-admin")
	credentials, err := onprem.NewCredentialService(store.Credentials(), onprem.CredentialConfig{
		AdminAPIKey: adminKey, RotationOverlap: time.Minute,
	}, onprem.WithClock(func() time.Time { return s.now }))
	s.Require().NoError(err)
	s.credentials = credentials
	s.admin, err = credentials.Authenticate(context.Background(), adminKey)
	s.Require().NoError(err)
	channel, err := onprem.NewChannelService(
		store.Channel(),
		onprem.WithChannelClock(func() time.Time { return s.now }),
		onprem.WithChannelIDSource(func() (string, error) {
			return fmt.Sprintf("%s-%d", s.idPrefix, s.sequence.Add(1)), nil
		}),
	)
	s.Require().NoError(err)
	s.channel = channel
}

func (s *channelStoreSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *channelStoreSuite) TestEnvelopeLifecycleIsAuthorizedAndIdempotent() {
	sender := s.issuePrincipal("sender")
	recipient := s.issuePrincipal("recipient")
	other := s.issuePrincipal("other")
	request := postgresChannelRequest(recipient.AgentID, uniqueCredentialValue("send"))

	created, err := s.channel.Send(context.Background(), sender, request)
	s.Require().NoError(err)
	s.Require().NoError(s.credentials.RevokeCredential(context.Background(), s.admin, recipient.CredentialID))
	replayed, err := s.channel.Send(context.Background(), sender, request)
	s.Require().NoError(err)
	s.Equal(created.ID, replayed.ID)

	conflict := request
	conflict.Message = "different"
	_, err = s.channel.Send(context.Background(), sender, conflict)
	s.Require().ErrorIs(err, onprem.ErrIdempotencyConflict)

	inbox, err := s.channel.List(context.Background(), recipient, onprem.ListEnvelopesFilter{
		Status: onprem.EnvelopeStatusPending,
	})
	s.Require().NoError(err)
	s.Require().Len(inbox, 1)
	s.Equal(created.ID, inbox[0].ID)
	outbox, err := s.channel.List(context.Background(), sender, onprem.ListEnvelopesFilter{
		Direction: onprem.EnvelopeDirectionSent,
	})
	s.Require().NoError(err)
	s.Require().Len(outbox, 1)
	s.Equal(created.ID, outbox[0].ID)

	_, err = s.channel.Get(context.Background(), other, created.ID)
	s.Require().ErrorIs(err, onprem.ErrEnvelopeNotFound)
	_, err = s.channel.Get(context.Background(), sender, created.ID)
	s.Require().ErrorIs(err, onprem.ErrEnvelopeNotFound)
	got, err := s.channel.Get(context.Background(), recipient, created.ID)
	s.Require().NoError(err)
	s.Equal(created.ID, got.ID)
	_, err = s.channel.Accept(context.Background(), other, created.ID)
	s.Require().ErrorIs(err, onprem.ErrEnvelopeNotFound)

	var acceptedAt *time.Time
	var archivedAt *time.Time
	transitions := []struct {
		name       string
		apply      func() (onprem.ChannelEnvelope, error)
		wantStatus string
		wantError  error
		verifyTime func(onprem.ChannelEnvelope)
	}{
		{name: "accept pending", apply: func() (onprem.ChannelEnvelope, error) {
			return s.channel.Accept(context.Background(), recipient, created.ID)
		}, wantStatus: onprem.EnvelopeStatusAccepted, verifyTime: func(envelope onprem.ChannelEnvelope) {
			acceptedAt = envelope.AcceptedAt
		}},
		{name: "replay accept", apply: func() (onprem.ChannelEnvelope, error) {
			return s.channel.Accept(context.Background(), recipient, created.ID)
		}, wantStatus: onprem.EnvelopeStatusAccepted, verifyTime: func(envelope onprem.ChannelEnvelope) {
			s.Equal(acceptedAt, envelope.AcceptedAt)
		}},
		{name: "archive accepted", apply: func() (onprem.ChannelEnvelope, error) {
			return s.channel.Archive(context.Background(), recipient, created.ID)
		}, wantStatus: onprem.EnvelopeStatusArchived, verifyTime: func(envelope onprem.ChannelEnvelope) {
			archivedAt = envelope.ArchivedAt
		}},
		{name: "reject accept after archive", apply: func() (onprem.ChannelEnvelope, error) {
			return s.channel.Accept(context.Background(), recipient, created.ID)
		}, wantError: onprem.ErrEnvelopeState},
		{name: "replay archive", apply: func() (onprem.ChannelEnvelope, error) {
			return s.channel.Archive(context.Background(), recipient, created.ID)
		}, wantStatus: onprem.EnvelopeStatusArchived, verifyTime: func(envelope onprem.ChannelEnvelope) {
			s.Equal(archivedAt, envelope.ArchivedAt)
		}},
	}
	for _, transition := range transitions {
		s.Run(transition.name, func() {
			envelope, err := transition.apply()
			if transition.wantError != nil {
				s.ErrorIs(err, transition.wantError)
				return
			}
			s.Require().NoError(err)
			s.Equal(transition.wantStatus, envelope.Status)
			if transition.verifyTime != nil {
				transition.verifyTime(envelope)
			}
		})
	}
}

func (s *channelStoreSuite) TestSendRequiresAnActiveTargetAgent() {
	sender := s.issuePrincipal("sender-target")
	request := postgresChannelRequest(uniqueCredentialValue("missing-agent"), uniqueCredentialValue("missing"))
	_, err := s.channel.Send(context.Background(), sender, request)
	s.Require().ErrorIs(err, onprem.ErrTargetAgentNotFound)
}

func (s *channelStoreSuite) issuePrincipal(label string) onprem.Principal {
	return s.issuePrincipalWithAgent(
		uniqueCredentialValue(label+"-user"),
		uniqueCredentialValue(label+"-agent"),
	)
}

func (s *channelStoreSuite) issuePrincipalWithAgent(userID, agentID string) onprem.Principal {
	enrollment, err := s.credentials.CreateEnrollment(context.Background(), s.admin, onprem.EnrollmentRequest{
		UserID: userID, AgentID: agentID,
		Permissions: []onprem.Permission{onprem.PermissionChannelSend, onprem.PermissionChannelReceive},
	})
	s.Require().NoError(err)
	issued, err := s.credentials.ExchangeEnrollment(context.Background(), enrollment.Token)
	s.Require().NoError(err)
	principal, err := s.credentials.Authenticate(context.Background(), issued.APIKey)
	s.Require().NoError(err)
	return principal
}

func postgresChannelRequest(toAgentID, idempotencyKey string) onprem.SendEnvelopeRequest {
	return onprem.SendEnvelopeRequest{
		ToAgentID: toAgentID, PayloadType: onprem.EnvelopePayloadKnowledgeCapsule,
		PayloadJSON: json.RawMessage(`{
			"schema_version":"paxl.envelope_payload.knowledge_capsule.v2",
			"capsule":{"capsule_id":"kcap-postgres","source_session_id":"session-1","source_agent":"codex","keyword":"handoff","title":"Handoff","summary":"summary","content":"handoff","status":"active","truncated":false,"original_estimated_chars":7},
			"route":{"match_type":"any"}
		}`),
		Message: "review", IdempotencyKey: idempotencyKey,
	}
}
