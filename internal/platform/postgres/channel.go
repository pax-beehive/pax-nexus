package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
)

type ChannelStore struct {
	pool *pgxpool.Pool
}

type channelRowScanner interface {
	Scan(...any) error
}

func (s *ChannelStore) ResolveActiveAgent(
	ctx context.Context,
	agentID string,
	now time.Time,
) (onprem.AgentIdentity, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT memberships.user_id
		FROM onprem_agents agents
		JOIN onprem_memberships memberships ON memberships.membership_id = agents.owner_membership_id
		JOIN onprem_users users ON users.user_id = memberships.user_id
		JOIN agent_credentials credentials
		  ON credentials.agent_id = agents.agent_id
		 AND credentials.owner_membership_id = agents.owner_membership_id
		WHERE agents.agent_id = $1
		  AND agents.status = 'active' AND memberships.status = 'active'
		  AND users.identity_status IN ('active', 'unclaimed')
		  AND credentials.revoked_at IS NULL
		  AND (credentials.expires_at IS NULL OR credentials.expires_at > $2)
		  AND $3 = ANY(credentials.permissions)
		ORDER BY memberships.user_id
		LIMIT 2
	`, agentID, now, string(onprem.PermissionChannelReceive))
	if err != nil {
		return onprem.AgentIdentity{}, fmt.Errorf("resolve postgres channel agent: %w", err)
	}
	defer rows.Close()
	var userIDs []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return onprem.AgentIdentity{}, fmt.Errorf("scan postgres channel agent: %w", err)
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		return onprem.AgentIdentity{}, fmt.Errorf("iterate postgres channel agents: %w", err)
	}
	switch len(userIDs) {
	case 0:
		return onprem.AgentIdentity{}, onprem.ErrTargetAgentNotFound
	case 1:
		return onprem.AgentIdentity{UserID: userIDs[0], AgentID: agentID}, nil
	default:
		return onprem.AgentIdentity{}, onprem.ErrAgentIdentityConflict
	}
}

func (s *ChannelStore) FindChannelEnvelopeByIdempotency(
	ctx context.Context,
	principal onprem.Principal,
	idempotencyKey string,
) (onprem.ChannelEnvelope, bool, error) {
	envelope, err := scanChannelEnvelope(s.pool.QueryRow(ctx, `
		SELECT envelope_id, from_user_id, from_agent_id, to_user_id, to_agent_id,
		       payload_type, payload_json, message, idempotency_key, status,
		       created_at, accepted_at, archived_at
		FROM onprem_channel_envelopes
		WHERE from_user_id = $1 AND from_agent_id = $2 AND idempotency_key = $3
	`, principal.UserID, principal.AgentID, idempotencyKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.ChannelEnvelope{}, false, nil
	}
	if err != nil {
		return onprem.ChannelEnvelope{}, false, fmt.Errorf("find idempotent postgres channel envelope: %w", err)
	}
	return envelope, true, nil
}

func (s *ChannelStore) CreateChannelEnvelope(
	ctx context.Context,
	envelope onprem.ChannelEnvelope,
) (onprem.ChannelEnvelope, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO onprem_channel_envelopes (
			envelope_id, from_user_id, from_agent_id, to_user_id, to_agent_id,
			payload_type, payload_json, message, idempotency_key, status, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (from_user_id, from_agent_id, idempotency_key) DO NOTHING
		RETURNING envelope_id, from_user_id, from_agent_id, to_user_id, to_agent_id,
		          payload_type, payload_json, message, idempotency_key, status,
		          created_at, accepted_at, archived_at
	`, envelope.ID, envelope.FromUserID, envelope.FromAgentID, envelope.ToUserID, envelope.ToAgentID,
		envelope.PayloadType, envelope.PayloadJSON, envelope.Message, envelope.IdempotencyKey,
		envelope.Status, envelope.CreatedAt)
	created, err := scanChannelEnvelope(row)
	if err == nil {
		return created, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return onprem.ChannelEnvelope{}, fmt.Errorf("create postgres channel envelope: %w", err)
	}
	existing, err := scanChannelEnvelope(s.pool.QueryRow(ctx, `
		SELECT envelope_id, from_user_id, from_agent_id, to_user_id, to_agent_id,
		       payload_type, payload_json, message, idempotency_key, status,
		       created_at, accepted_at, archived_at
		FROM onprem_channel_envelopes
		WHERE from_user_id = $1 AND from_agent_id = $2 AND idempotency_key = $3
	`, envelope.FromUserID, envelope.FromAgentID, envelope.IdempotencyKey))
	if err != nil {
		return onprem.ChannelEnvelope{}, fmt.Errorf("read idempotent postgres channel envelope: %w", err)
	}
	if !sameChannelEnvelopeIntent(existing, envelope) {
		return onprem.ChannelEnvelope{}, onprem.ErrIdempotencyConflict
	}
	return existing, nil
}

func (s *ChannelStore) ListChannelEnvelopes(
	ctx context.Context,
	principal onprem.Principal,
	filter onprem.ListEnvelopesFilter,
) ([]onprem.ChannelEnvelope, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `
		SELECT envelope_id, from_user_id, from_agent_id, to_user_id, to_agent_id,
		       payload_type, payload_json, message, idempotency_key, status,
		       created_at, accepted_at, archived_at
		FROM onprem_channel_envelopes
		WHERE `
	args := []any{principal.UserID, principal.AgentID}
	switch filter.Direction {
	case onprem.EnvelopeDirectionSent:
		query += "from_user_id = $1 AND from_agent_id = $2"
	default:
		query += "to_user_id = $1 AND to_agent_id = $2"
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		query += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if filter.Cursor != "" {
		args = append(args, filter.Cursor)
		placeholder := len(args)
		query += fmt.Sprintf(` AND (created_at, envelope_id) < (
			SELECT created_at, envelope_id FROM onprem_channel_envelopes WHERE envelope_id = $%d
		)`, placeholder)
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY created_at DESC, envelope_id DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list postgres channel envelopes: %w", err)
	}
	defer rows.Close()
	envelopes := make([]onprem.ChannelEnvelope, 0)
	for rows.Next() {
		envelope, err := scanChannelEnvelope(rows)
		if err != nil {
			return nil, fmt.Errorf("scan listed postgres channel envelope: %w", err)
		}
		envelopes = append(envelopes, envelope)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres channel envelopes: %w", err)
	}
	return envelopes, nil
}

func (s *ChannelStore) GetChannelEnvelope(
	ctx context.Context,
	principal onprem.Principal,
	envelopeID string,
) (onprem.ChannelEnvelope, error) {
	return s.getRecipientChannelEnvelope(ctx, principal, envelopeID)
}

func (s *ChannelStore) AcceptChannelEnvelope(
	ctx context.Context,
	principal onprem.Principal,
	envelopeID string,
	acceptedAt time.Time,
) (onprem.ChannelEnvelope, error) {
	envelope, err := scanChannelEnvelope(s.pool.QueryRow(ctx, `
		UPDATE onprem_channel_envelopes
		SET status = $4, accepted_at = $5
		WHERE envelope_id = $1 AND to_user_id = $2 AND to_agent_id = $3 AND status = $6
		RETURNING envelope_id, from_user_id, from_agent_id, to_user_id, to_agent_id,
		          payload_type, payload_json, message, idempotency_key, status,
		          created_at, accepted_at, archived_at
	`, envelopeID, principal.UserID, principal.AgentID, onprem.EnvelopeStatusAccepted,
		acceptedAt, onprem.EnvelopeStatusPending))
	if err == nil {
		return envelope, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return onprem.ChannelEnvelope{}, fmt.Errorf("accept postgres channel envelope: %w", err)
	}
	existing, err := s.getRecipientChannelEnvelope(ctx, principal, envelopeID)
	if err != nil {
		return onprem.ChannelEnvelope{}, err
	}
	if existing.Status == onprem.EnvelopeStatusAccepted {
		return existing, nil
	}
	return onprem.ChannelEnvelope{}, onprem.ErrEnvelopeState
}

func (s *ChannelStore) ArchiveChannelEnvelope(
	ctx context.Context,
	principal onprem.Principal,
	envelopeID string,
	archivedAt time.Time,
) (onprem.ChannelEnvelope, error) {
	envelope, err := scanChannelEnvelope(s.pool.QueryRow(ctx, `
		UPDATE onprem_channel_envelopes
		SET status = $4, archived_at = $5
		WHERE envelope_id = $1 AND to_user_id = $2 AND to_agent_id = $3
		  AND status IN ($6, $7)
		RETURNING envelope_id, from_user_id, from_agent_id, to_user_id, to_agent_id,
		          payload_type, payload_json, message, idempotency_key, status,
		          created_at, accepted_at, archived_at
	`, envelopeID, principal.UserID, principal.AgentID, onprem.EnvelopeStatusArchived,
		archivedAt, onprem.EnvelopeStatusPending, onprem.EnvelopeStatusAccepted))
	if err == nil {
		return envelope, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return onprem.ChannelEnvelope{}, fmt.Errorf("archive postgres channel envelope: %w", err)
	}
	existing, err := s.getRecipientChannelEnvelope(ctx, principal, envelopeID)
	if err != nil {
		return onprem.ChannelEnvelope{}, err
	}
	if existing.Status == onprem.EnvelopeStatusArchived {
		return existing, nil
	}
	return onprem.ChannelEnvelope{}, onprem.ErrEnvelopeState
}

func (s *ChannelStore) getRecipientChannelEnvelope(
	ctx context.Context,
	principal onprem.Principal,
	envelopeID string,
) (onprem.ChannelEnvelope, error) {
	envelope, err := scanChannelEnvelope(s.pool.QueryRow(ctx, `
		SELECT envelope_id, from_user_id, from_agent_id, to_user_id, to_agent_id,
		       payload_type, payload_json, message, idempotency_key, status,
		       created_at, accepted_at, archived_at
		FROM onprem_channel_envelopes
		WHERE envelope_id = $1 AND to_user_id = $2 AND to_agent_id = $3
	`, envelopeID, principal.UserID, principal.AgentID))
	return mapChannelEnvelopeRead(envelope, err)
}

func scanChannelEnvelope(row channelRowScanner) (onprem.ChannelEnvelope, error) {
	var envelope onprem.ChannelEnvelope
	var payload []byte
	err := row.Scan(
		&envelope.ID, &envelope.FromUserID, &envelope.FromAgentID,
		&envelope.ToUserID, &envelope.ToAgentID, &envelope.PayloadType,
		&payload, &envelope.Message, &envelope.IdempotencyKey, &envelope.Status,
		&envelope.CreatedAt, &envelope.AcceptedAt, &envelope.ArchivedAt,
	)
	envelope.PayloadJSON = append(json.RawMessage(nil), payload...)
	return envelope, err
}

func mapChannelEnvelopeRead(envelope onprem.ChannelEnvelope, err error) (onprem.ChannelEnvelope, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return onprem.ChannelEnvelope{}, onprem.ErrEnvelopeNotFound
	}
	if err != nil {
		return onprem.ChannelEnvelope{}, fmt.Errorf("read postgres channel envelope: %w", err)
	}
	return envelope, nil
}

func sameChannelEnvelopeIntent(left, right onprem.ChannelEnvelope) bool {
	if left.FromUserID != right.FromUserID || left.FromAgentID != right.FromAgentID ||
		left.ToUserID != right.ToUserID || left.ToAgentID != right.ToAgentID ||
		left.PayloadType != right.PayloadType || left.Message != right.Message {
		return false
	}
	var leftPayload any
	var rightPayload any
	if json.Unmarshal(left.PayloadJSON, &leftPayload) != nil || json.Unmarshal(right.PayloadJSON, &rightPayload) != nil {
		return false
	}
	return reflect.DeepEqual(leftPayload, rightPayload)
}
