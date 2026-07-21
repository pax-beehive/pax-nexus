package onprem

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	EnvelopePayloadKnowledgeCapsule = "knowledge_capsule"
	EnvelopeStatusPending           = "pending"
	EnvelopeStatusAccepted          = "accepted"
	EnvelopeStatusArchived          = "archived"
	EnvelopeDirectionReceived       = "received"
	EnvelopeDirectionSent           = "sent"

	envelopeMessageLimit = 1000
	envelopePayloadLimit = 128 * 1024
	envelopeRouteLimit   = 256
)

type AgentIdentity struct {
	UserID  string
	AgentID string
}

type ChannelEnvelope struct {
	ID             string
	FromUserID     string
	FromAgentID    string
	ToUserID       string
	ToAgentID      string
	PayloadType    string
	PayloadJSON    json.RawMessage
	Message        string
	IdempotencyKey string
	Status         string
	CreatedAt      time.Time
	AcceptedAt     *time.Time
	ArchivedAt     *time.Time
}

type SendEnvelopeRequest struct {
	ToAgentID      string
	PayloadType    string
	PayloadJSON    json.RawMessage
	Message        string
	IdempotencyKey string
}

type ListEnvelopesFilter struct {
	Status    string
	Direction string
	Limit     int
	Cursor    string
}

type ChannelStore interface {
	ResolveActiveAgent(context.Context, string, time.Time) (AgentIdentity, error)
	FindChannelEnvelopeByIdempotency(context.Context, Principal, string) (ChannelEnvelope, bool, error)
	CreateChannelEnvelope(context.Context, ChannelEnvelope) (ChannelEnvelope, error)
	ListChannelEnvelopes(context.Context, Principal, ListEnvelopesFilter) ([]ChannelEnvelope, error)
	GetChannelEnvelope(context.Context, Principal, string) (ChannelEnvelope, error)
	AcceptChannelEnvelope(context.Context, Principal, string, time.Time) (ChannelEnvelope, error)
	ArchiveChannelEnvelope(context.Context, Principal, string, time.Time) (ChannelEnvelope, error)
}

type channelOptions struct {
	clock    func() time.Time
	idSource func() (string, error)
}

type ChannelOption func(*channelOptions)

func WithChannelClock(clock func() time.Time) ChannelOption {
	return func(options *channelOptions) { options.clock = clock }
}

func WithChannelIDSource(source func() (string, error)) ChannelOption {
	return func(options *channelOptions) { options.idSource = source }
}

type ChannelService struct {
	store    ChannelStore
	clock    func() time.Time
	idSource func() (string, error)
}

func NewChannelService(store ChannelStore, options ...ChannelOption) (*ChannelService, error) {
	if store == nil {
		return nil, fmt.Errorf("create on-prem channel service: store is required")
	}
	configured := channelOptions{clock: time.Now, idSource: randomEnvelopeID}
	for _, option := range options {
		option(&configured)
	}
	if configured.clock == nil || configured.idSource == nil {
		return nil, fmt.Errorf("create on-prem channel service: clock and ID source are required")
	}
	return &ChannelService{store: store, clock: configured.clock, idSource: configured.idSource}, nil
}

func (s *ChannelService) Send(
	ctx context.Context,
	principal Principal,
	request SendEnvelopeRequest,
) (ChannelEnvelope, error) {
	if err := validateChannelPrincipal(principal); err != nil {
		return ChannelEnvelope{}, err
	}
	request = normalizeSendEnvelopeRequest(request)
	if err := validateSendEnvelopeRequest(request); err != nil {
		return ChannelEnvelope{}, fmt.Errorf("%w: %w", ErrInvalidChannelRequest, err)
	}
	existing, found, err := s.store.FindChannelEnvelopeByIdempotency(ctx, principal, request.IdempotencyKey)
	if err != nil {
		return ChannelEnvelope{}, fmt.Errorf("find idempotent channel envelope: %w", err)
	}
	if found {
		if sameSendEnvelopeIntent(existing, request) {
			return existing, nil
		}
		return ChannelEnvelope{}, ErrIdempotencyConflict
	}
	recipient, err := s.store.ResolveActiveAgent(ctx, request.ToAgentID, s.clock().UTC())
	if err != nil {
		return ChannelEnvelope{}, fmt.Errorf("resolve channel recipient: %w", err)
	}
	envelopeID, err := s.idSource()
	if err != nil {
		return ChannelEnvelope{}, fmt.Errorf("create channel envelope ID: %w", err)
	}
	envelope := ChannelEnvelope{
		ID:         "tm_env_" + envelopeID,
		FromUserID: principal.UserID, FromAgentID: principal.AgentID,
		ToUserID: recipient.UserID, ToAgentID: recipient.AgentID,
		PayloadType: request.PayloadType, PayloadJSON: append(json.RawMessage(nil), request.PayloadJSON...),
		Message: request.Message, IdempotencyKey: request.IdempotencyKey,
		Status: EnvelopeStatusPending, CreatedAt: s.clock().UTC(),
	}
	created, err := s.store.CreateChannelEnvelope(ctx, envelope)
	if err != nil {
		return ChannelEnvelope{}, fmt.Errorf("create channel envelope: %w", err)
	}
	return created, nil
}

func (s *ChannelService) List(
	ctx context.Context,
	principal Principal,
	filter ListEnvelopesFilter,
) ([]ChannelEnvelope, error) {
	if err := validateChannelPrincipal(principal); err != nil {
		return nil, err
	}
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Direction = strings.TrimSpace(filter.Direction)
	if filter.Direction == "" {
		filter.Direction = EnvelopeDirectionReceived
	}
	if filter.Direction != EnvelopeDirectionReceived && filter.Direction != EnvelopeDirectionSent {
		return nil, fmt.Errorf("%w: list channel envelopes: unsupported direction %q", ErrInvalidChannelRequest, filter.Direction)
	}
	if filter.Status != "" && !validEnvelopeStatus(filter.Status) {
		return nil, fmt.Errorf("%w: list channel envelopes: unsupported status %q", ErrInvalidChannelRequest, filter.Status)
	}
	return s.store.ListChannelEnvelopes(ctx, principal, filter)
}

func (s *ChannelService) Get(ctx context.Context, principal Principal, envelopeID string) (ChannelEnvelope, error) {
	if err := validateChannelPrincipal(principal); err != nil {
		return ChannelEnvelope{}, err
	}
	if strings.TrimSpace(envelopeID) == "" {
		return ChannelEnvelope{}, fmt.Errorf("%w: get channel envelope: envelope ID is required", ErrInvalidChannelRequest)
	}
	return s.store.GetChannelEnvelope(ctx, principal, strings.TrimSpace(envelopeID))
}

func (s *ChannelService) Accept(ctx context.Context, principal Principal, envelopeID string) (ChannelEnvelope, error) {
	if err := validateChannelPrincipal(principal); err != nil {
		return ChannelEnvelope{}, err
	}
	if strings.TrimSpace(envelopeID) == "" {
		return ChannelEnvelope{}, fmt.Errorf("%w: accept channel envelope: envelope ID is required", ErrInvalidChannelRequest)
	}
	return s.store.AcceptChannelEnvelope(ctx, principal, strings.TrimSpace(envelopeID), s.clock().UTC())
}

func (s *ChannelService) Archive(ctx context.Context, principal Principal, envelopeID string) (ChannelEnvelope, error) {
	if err := validateChannelPrincipal(principal); err != nil {
		return ChannelEnvelope{}, err
	}
	if strings.TrimSpace(envelopeID) == "" {
		return ChannelEnvelope{}, fmt.Errorf("%w: archive channel envelope: envelope ID is required", ErrInvalidChannelRequest)
	}
	return s.store.ArchiveChannelEnvelope(ctx, principal, strings.TrimSpace(envelopeID), s.clock().UTC())
}

func normalizeSendEnvelopeRequest(request SendEnvelopeRequest) SendEnvelopeRequest {
	request.ToAgentID = strings.TrimSpace(request.ToAgentID)
	request.PayloadType = strings.TrimSpace(request.PayloadType)
	request.Message = strings.TrimSpace(request.Message)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.PayloadJSON = bytes.TrimSpace(request.PayloadJSON)
	return request
}

func validateSendEnvelopeRequest(request SendEnvelopeRequest) error {
	if request.ToAgentID == "" {
		return fmt.Errorf("send channel envelope: to_agent_id is required")
	}
	if request.IdempotencyKey == "" || utf8.RuneCountInString(request.IdempotencyKey) > envelopeRouteLimit {
		return fmt.Errorf("send channel envelope: idempotency_key is required and must not exceed %d characters", envelopeRouteLimit)
	}
	if request.PayloadType != EnvelopePayloadKnowledgeCapsule {
		return fmt.Errorf("send channel envelope: unsupported payload_type %q", request.PayloadType)
	}
	if len(request.PayloadJSON) == 0 || !json.Valid(request.PayloadJSON) {
		return fmt.Errorf("send channel envelope: payload_json must be valid JSON")
	}
	if len(request.PayloadJSON) > envelopePayloadLimit {
		return fmt.Errorf("send channel envelope: payload_json exceeds %d bytes", envelopePayloadLimit)
	}
	if utf8.RuneCountInString(request.Message) > envelopeMessageLimit {
		return fmt.Errorf("send channel envelope: message exceeds %d characters", envelopeMessageLimit)
	}
	if err := validateKnowledgeCapsulePayload(request.PayloadJSON); err != nil {
		return fmt.Errorf("send channel envelope: %w", err)
	}
	return nil
}

type knowledgeCapsulePayload struct {
	SchemaVersion string            `json:"schema_version"`
	Capsule       *knowledgeCapsule `json:"capsule"`
	Route         *capsuleRoute     `json:"route,omitempty"`
}

type knowledgeCapsule struct {
	CapsuleID              string  `json:"capsule_id"`
	SourceSessionID        string  `json:"source_session_id"`
	SourceAgent            string  `json:"source_agent"`
	Keyword                string  `json:"keyword"`
	Title                  string  `json:"title"`
	Summary                *string `json:"summary"`
	Content                *string `json:"content"`
	Status                 string  `json:"status"`
	Truncated              *bool   `json:"truncated"`
	OriginalEstimatedChars *int64  `json:"original_estimated_chars"`
}

type capsuleRoute struct {
	MatchType   string `json:"match_type"`
	MatchValue  string `json:"match_value,omitempty"`
	TargetAgent string `json:"target_agent,omitempty"`
}

func validateKnowledgeCapsulePayload(raw json.RawMessage) error {
	var payload knowledgeCapsulePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode knowledge capsule payload: %w", err)
	}
	if payload.Capsule == nil {
		return fmt.Errorf("knowledge capsule payload requires capsule")
	}
	if strings.TrimSpace(payload.SchemaVersion) != "paxl.envelope_payload.knowledge_capsule.v2" {
		return fmt.Errorf("unsupported knowledge capsule payload schema %q", payload.SchemaVersion)
	}
	if err := validateKnowledgeCapsule(payload.Capsule); err != nil {
		return err
	}
	if err := validateCapsuleRoute(payload.Route); err != nil {
		return err
	}
	return nil
}

func validateKnowledgeCapsule(capsule *knowledgeCapsule) error {
	required := map[string]string{
		"capsule_id": capsule.CapsuleID, "source_session_id": capsule.SourceSessionID,
		"source_agent": capsule.SourceAgent, "keyword": capsule.Keyword,
		"title": capsule.Title, "status": capsule.Status,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("knowledge capsule requires %s", field)
		}
	}
	if capsule.Summary == nil || capsule.Content == nil || capsule.Truncated == nil || capsule.OriginalEstimatedChars == nil {
		return fmt.Errorf("knowledge capsule requires summary, content, truncated, and original_estimated_chars")
	}
	if *capsule.OriginalEstimatedChars < 0 {
		return fmt.Errorf("knowledge capsule original_estimated_chars must not be negative")
	}
	return nil
}

func validateCapsuleRoute(route *capsuleRoute) error {
	if route == nil {
		return nil
	}
	route.MatchType = strings.TrimSpace(route.MatchType)
	route.MatchValue = strings.TrimSpace(route.MatchValue)
	switch route.MatchType {
	case "any":
		if route.MatchValue != "" {
			return fmt.Errorf("route match_value must be empty for any")
		}
	case "project", "keyword":
		if route.MatchValue == "" {
			return fmt.Errorf("route match_value is required")
		}
	default:
		return fmt.Errorf("unsupported route match_type %q", route.MatchType)
	}
	if utf8.RuneCountInString(route.MatchValue) > envelopeRouteLimit {
		return fmt.Errorf("route match_value exceeds %d characters", envelopeRouteLimit)
	}
	if utf8.RuneCountInString(strings.TrimSpace(route.TargetAgent)) > envelopeRouteLimit {
		return fmt.Errorf("route target_agent exceeds %d characters", envelopeRouteLimit)
	}
	return nil
}

func validateChannelPrincipal(principal Principal) error {
	if principal.ScopeID != LocalScopeID || strings.TrimSpace(principal.UserID) == "" || strings.TrimSpace(principal.AgentID) == "" {
		return ErrUnauthorized
	}
	return nil
}

func validEnvelopeStatus(status string) bool {
	return status == EnvelopeStatusPending || status == EnvelopeStatusAccepted || status == EnvelopeStatusArchived
}

func sameSendEnvelopeIntent(envelope ChannelEnvelope, request SendEnvelopeRequest) bool {
	if envelope.ToAgentID != request.ToAgentID || envelope.PayloadType != request.PayloadType || envelope.Message != request.Message {
		return false
	}
	var storedPayload any
	var requestedPayload any
	if json.Unmarshal(envelope.PayloadJSON, &storedPayload) != nil || json.Unmarshal(request.PayloadJSON, &requestedPayload) != nil {
		return false
	}
	return reflect.DeepEqual(storedPayload, requestedPayload)
}

func randomEnvelopeID() (string, error) {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("read random envelope ID: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
