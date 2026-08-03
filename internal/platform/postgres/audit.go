package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/audit"
	"github.com/pax-beehive/pax-nexus/internal/session"
)

const (
	defaultAuditQueryLimit = 100
	maxAuditQueryLimit     = 500
)

// AuditStore implements both the session audit consumer Store and the
// audit.Query read seam against the shared pool.
type AuditStore struct {
	pool *pgxpool.Pool
}

func NewAuditStore(pool *pgxpool.Pool) (*AuditStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("create session audit store: pool is required")
	}
	return &AuditStore{pool: pool}, nil
}

func (s *AuditStore) PendingStreams(ctx context.Context) ([]audit.Stream, error) {
	rows, err := s.pool.Query(ctx, `
SELECT stream.scope_id, stream.user_id, stream.agent_id, stream.session_id, stream.last_sequence
FROM session_streams AS stream
LEFT JOIN session_processor_cursors AS cursor
  ON cursor.processor_name = $1
 AND cursor.processor_version = $2
 AND cursor.scope_id = stream.scope_id
 AND cursor.agent_id = stream.agent_id
 AND cursor.session_id = stream.session_id
WHERE stream.last_sequence > COALESCE(cursor.committed_sequence, 0)
  AND stream.source = 'agent-session'
  AND stream.agent_id <> ''
ORDER BY stream.updated_at
LIMIT 100`, audit.ProcessorName, audit.ProcessorVersion)
	if err != nil {
		return nil, fmt.Errorf("query session audit pending streams: %w", err)
	}
	defer rows.Close()
	streams := make([]audit.Stream, 0)
	for rows.Next() {
		var stream audit.Stream
		if err := rows.Scan(
			&stream.ScopeID,
			&stream.Actor.UserID,
			&stream.Actor.AgentID,
			&stream.Actor.SessionID,
			&stream.Head,
		); err != nil {
			return nil, fmt.Errorf("scan session audit pending stream: %w", err)
		}
		streams = append(streams, stream)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session audit pending streams: %w", err)
	}
	return streams, nil
}

func (s *AuditStore) SessionEvents(
	ctx context.Context,
	stream audit.Stream,
) ([]session.SessionEvent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT event_id, user_id, agent_id, session_id, sequence, event_type, content,
       task_ref, thread_ref, visibility, occurred_at, captured_at, extracted_at, metadata
FROM session_events
WHERE scope_id = $1 AND agent_id = $2 AND session_id = $3 AND sequence <= $4
ORDER BY sequence`, stream.ScopeID, stream.Actor.AgentID, stream.Actor.SessionID, stream.Head)
	if err != nil {
		return nil, fmt.Errorf("query session audit events: %w", err)
	}
	defer rows.Close()
	events := make([]session.SessionEvent, 0)
	for rows.Next() {
		var event session.SessionEvent
		var metadata []byte
		if err := rows.Scan(
			&event.ID, &event.Actor.UserID, &event.Actor.AgentID, &event.Actor.SessionID,
			&event.Sequence, &event.Type, &event.Content, &event.TaskRef, &event.ThreadRef,
			&event.Visibility, &event.OccurredAt, &event.CapturedAt, &event.ExtractedAt, &metadata,
		); err != nil {
			return nil, fmt.Errorf("scan session audit event: %w", err)
		}
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &event.Metadata); err != nil {
				return nil, fmt.Errorf("decode session audit event %q metadata: %w", event.ID, err)
			}
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session audit events: %w", err)
	}
	return events, nil
}

// ApplyBatch commits the tool-call rows, findings, activity deltas, and the
// cursor in one transaction; any failure rolls everything back so the stream
// is retried from its previous cursor.
func (s *AuditStore) ApplyBatch(ctx context.Context, batch audit.Batch) (resultErr error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin session audit batch transaction: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(context.WithoutCancel(ctx))
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr,
				fmt.Errorf("rollback session audit batch transaction: %w", rollbackErr))
		}
	}()
	if err := upsertAuditToolCalls(ctx, tx, batch); err != nil {
		return err
	}
	if err := applyAuditApprovalUpdates(ctx, tx, batch); err != nil {
		return err
	}
	if err := insertAuditFindings(ctx, tx, batch); err != nil {
		return err
	}
	if err := upsertAuditActivity(ctx, tx, batch); err != nil {
		return err
	}
	if err := advanceAuditCursor(ctx, tx, batch); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit session audit batch: %w", err)
	}
	return nil
}

func upsertAuditToolCalls(ctx context.Context, tx pgx.Tx, batch audit.Batch) error {
	for _, call := range batch.ToolCalls {
		reasons := call.RiskReasons
		if reasons == nil {
			reasons = []audit.Reason{}
		}
		reasonsJSON, err := json.Marshal(reasons)
		if err != nil {
			return fmt.Errorf("marshal audit risk reasons for event %q: %w", call.EventID, err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO audit_tool_calls (
    scope_id, event_id, user_id, agent_id, session_id, call_id, tool_name,
    input_summary, risk_level, risk_reasons, approval_state, occurred_at, captured_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (scope_id, event_id) DO UPDATE SET
    risk_level = EXCLUDED.risk_level,
    risk_reasons = EXCLUDED.risk_reasons,
    approval_state = CASE
        WHEN audit_tool_calls.approval_state = 'unknown' THEN EXCLUDED.approval_state
        ELSE audit_tool_calls.approval_state
    END`,
			batch.ScopeID, call.EventID, call.UserID, call.AgentID, call.SessionID,
			call.CallID, call.ToolName, call.InputSummary, string(call.RiskLevel),
			reasonsJSON, call.ApprovalState, call.OccurredAt, call.CapturedAt,
		); err != nil {
			return fmt.Errorf("upsert audit tool call %q: %w", call.EventID, err)
		}
	}
	return nil
}

// applyAuditApprovalUpdates upgrades stored tool-call rows still in unknown
// state when an approval arrives in a different batch than its tool_call.
func applyAuditApprovalUpdates(ctx context.Context, tx pgx.Tx, batch audit.Batch) error {
	for _, update := range batch.ApprovalUpdates {
		if _, err := tx.Exec(ctx, `
UPDATE audit_tool_calls
SET approval_state = $3
WHERE scope_id = $1 AND call_id = $2 AND approval_state = 'unknown'`,
			batch.ScopeID, update.CallID, update.State,
		); err != nil {
			return fmt.Errorf("apply audit approval update for call %q: %w", update.CallID, err)
		}
	}
	return nil
}

func insertAuditFindings(ctx context.Context, tx pgx.Tx, batch audit.Batch) error {
	for _, finding := range batch.Findings {
		evidence := finding.EvidenceEventIDs
		if evidence == nil {
			evidence = []string{}
		}
		evidenceJSON, err := json.Marshal(evidence)
		if err != nil {
			return fmt.Errorf("marshal audit finding %q evidence: %w", finding.Kind, err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO audit_findings (
    scope_id, user_id, agent_id, session_id, kind, severity, summary, evidence_event_ids
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			batch.ScopeID, finding.UserID, finding.AgentID, finding.SessionID,
			finding.Kind, finding.Severity, finding.Summary, evidenceJSON,
		); err != nil {
			return fmt.Errorf("insert audit finding %q: %w", finding.Kind, err)
		}
	}
	return nil
}

func upsertAuditActivity(ctx context.Context, tx pgx.Tx, batch audit.Batch) error {
	for _, delta := range batch.Activity {
		sessionIDs := delta.SessionIDs
		if sessionIDs == nil {
			sessionIDs = []string{}
		}
		sessionsJSON, err := json.Marshal(sessionIDs)
		if err != nil {
			return fmt.Errorf("marshal audit activity sessions for %q: %w", delta.UserID, err)
		}
		breakdown := delta.ToolBreakdown
		if breakdown == nil {
			breakdown = map[string]int64{}
		}
		breakdownJSON, err := json.Marshal(breakdown)
		if err != nil {
			return fmt.Errorf("marshal audit activity breakdown for %q: %w", delta.UserID, err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO audit_activity_daily (
    scope_id, user_id, agent_id, day, event_count, tool_call_count, high_risk_count,
    session_count, session_ids, tool_breakdown
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (scope_id, user_id, agent_id, day) DO UPDATE SET
    event_count = audit_activity_daily.event_count + EXCLUDED.event_count,
    tool_call_count = audit_activity_daily.tool_call_count + EXCLUDED.tool_call_count,
    high_risk_count = audit_activity_daily.high_risk_count + EXCLUDED.high_risk_count,
    session_ids = (
        SELECT COALESCE(jsonb_agg(DISTINCT elem), '[]'::jsonb)
        FROM jsonb_array_elements_text(
            audit_activity_daily.session_ids || EXCLUDED.session_ids
        ) AS elem
    ),
    session_count = (
        SELECT COUNT(DISTINCT elem)
        FROM jsonb_array_elements_text(
            audit_activity_daily.session_ids || EXCLUDED.session_ids
        ) AS elem
    ),
    tool_breakdown = (
        SELECT COALESCE(jsonb_object_agg(key, total), '{}'::jsonb)
        FROM (
            SELECT key, SUM(value::bigint) AS total
            FROM jsonb_each_text(
                audit_activity_daily.tool_breakdown || EXCLUDED.tool_breakdown
            )
            GROUP BY key
        ) AS merged
    ),
    updated_at = NOW()`,
			batch.ScopeID, delta.UserID, delta.AgentID, delta.Day,
			delta.EventCount, delta.ToolCallCount, delta.HighRiskCount,
			len(sessionIDs), sessionsJSON, breakdownJSON,
		); err != nil {
			return fmt.Errorf("upsert audit activity for %q: %w", delta.UserID, err)
		}
	}
	return nil
}

func advanceAuditCursor(ctx context.Context, tx pgx.Tx, batch audit.Batch) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO session_processor_cursors (
    processor_name, processor_version, scope_id, agent_id, session_id, committed_sequence
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (processor_name, processor_version, scope_id, agent_id, session_id)
DO UPDATE SET committed_sequence = GREATEST(
    session_processor_cursors.committed_sequence,
    EXCLUDED.committed_sequence
), updated_at = NOW()`,
		audit.ProcessorName, audit.ProcessorVersion, batch.ScopeID,
		batch.Actor.AgentID, batch.Actor.SessionID, batch.Cursor,
	); err != nil {
		return fmt.Errorf("advance session audit cursor: %w", err)
	}
	return nil
}

func (s *AuditStore) ListToolCalls(
	ctx context.Context,
	filter audit.ToolCallFilter,
) ([]audit.ToolCall, error) {
	conditions, args := make([]string, 0, 6), make([]any, 0, 7)
	conditions, args = appendAuditCondition(conditions, args, "scope_id", filter.ScopeID)
	conditions, args = appendAuditCondition(conditions, args, "user_id", filter.UserID)
	conditions, args = appendAuditCondition(conditions, args, "agent_id", filter.AgentID)
	conditions, args = appendAuditCondition(conditions, args, "session_id", filter.SessionID)
	conditions, args = appendAuditCondition(conditions, args, "risk_level", filter.RiskLevel)
	conditions, args = appendAuditCondition(conditions, args, "approval_state", filter.ApprovalState)
	args = append(args, normalizeAuditLimit(filter.Limit))
	query := fmt.Sprintf(`
SELECT scope_id, event_id, user_id, agent_id, session_id, call_id, tool_name,
       input_summary, risk_level, risk_reasons, approval_state, occurred_at, captured_at
FROM audit_tool_calls
%s
ORDER BY occurred_at DESC, event_id
LIMIT $%d`, auditWhereClause(conditions), len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit tool calls: %w", err)
	}
	defer rows.Close()
	calls := make([]audit.ToolCall, 0)
	for rows.Next() {
		var call audit.ToolCall
		var riskLevel string
		var reasons []byte
		if err := rows.Scan(
			&call.ScopeID, &call.EventID, &call.UserID, &call.AgentID, &call.SessionID,
			&call.CallID, &call.ToolName, &call.InputSummary, &riskLevel, &reasons,
			&call.ApprovalState, &call.OccurredAt, &call.CapturedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit tool call: %w", err)
		}
		call.RiskLevel = audit.Level(riskLevel)
		if err := json.Unmarshal(reasons, &call.RiskReasons); err != nil {
			return nil, fmt.Errorf("decode audit tool call %q risk reasons: %w", call.EventID, err)
		}
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit tool calls: %w", err)
	}
	return calls, nil
}

func (s *AuditStore) ListFindings(
	ctx context.Context,
	filter audit.FindingFilter,
) ([]audit.Finding, error) {
	conditions, args := make([]string, 0, 6), make([]any, 0, 7)
	conditions, args = appendAuditCondition(conditions, args, "scope_id", filter.ScopeID)
	conditions, args = appendAuditCondition(conditions, args, "user_id", filter.UserID)
	conditions, args = appendAuditCondition(conditions, args, "agent_id", filter.AgentID)
	conditions, args = appendAuditCondition(conditions, args, "session_id", filter.SessionID)
	conditions, args = appendAuditCondition(conditions, args, "kind", filter.Kind)
	conditions, args = appendAuditCondition(conditions, args, "severity", filter.Severity)
	args = append(args, normalizeAuditLimit(filter.Limit))
	query := fmt.Sprintf(`
SELECT finding_id, scope_id, user_id, agent_id, session_id, kind, severity,
       summary, evidence_event_ids, created_at
FROM audit_findings
%s
ORDER BY finding_id DESC
LIMIT $%d`, auditWhereClause(conditions), len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit findings: %w", err)
	}
	defer rows.Close()
	findings := make([]audit.Finding, 0)
	for rows.Next() {
		var finding audit.Finding
		var evidence []byte
		if err := rows.Scan(
			&finding.FindingID, &finding.ScopeID, &finding.UserID, &finding.AgentID,
			&finding.SessionID, &finding.Kind, &finding.Severity, &finding.Summary,
			&evidence, &finding.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit finding: %w", err)
		}
		if err := json.Unmarshal(evidence, &finding.EvidenceEventIDs); err != nil {
			return nil, fmt.Errorf("decode audit finding %d evidence: %w", finding.FindingID, err)
		}
		findings = append(findings, finding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit findings: %w", err)
	}
	return findings, nil
}

func (s *AuditStore) GetActivityDaily(
	ctx context.Context,
	filter audit.ActivityFilter,
) ([]audit.ActivityDaily, error) {
	conditions, args := make([]string, 0, 5), make([]any, 0, 6)
	conditions, args = appendAuditCondition(conditions, args, "scope_id", filter.ScopeID)
	conditions, args = appendAuditCondition(conditions, args, "user_id", filter.UserID)
	conditions, args = appendAuditCondition(conditions, args, "agent_id", filter.AgentID)
	if !filter.FromDay.IsZero() {
		args = append(args, filter.FromDay)
		conditions = append(conditions, fmt.Sprintf("day >= $%d", len(args)))
	}
	if !filter.ToDay.IsZero() {
		args = append(args, filter.ToDay)
		conditions = append(conditions, fmt.Sprintf("day <= $%d", len(args)))
	}
	args = append(args, normalizeAuditLimit(filter.Limit))
	query := fmt.Sprintf(`
SELECT scope_id, user_id, agent_id, day, event_count, tool_call_count,
       high_risk_count, session_count, tool_breakdown
FROM audit_activity_daily
%s
ORDER BY day DESC, user_id, agent_id
LIMIT $%d`, auditWhereClause(conditions), len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit activity: %w", err)
	}
	defer rows.Close()
	activity := make([]audit.ActivityDaily, 0)
	for rows.Next() {
		var day audit.ActivityDaily
		var breakdown []byte
		if err := rows.Scan(
			&day.ScopeID, &day.UserID, &day.AgentID, &day.Day, &day.EventCount,
			&day.ToolCallCount, &day.HighRiskCount, &day.SessionCount, &breakdown,
		); err != nil {
			return nil, fmt.Errorf("scan audit activity: %w", err)
		}
		if err := json.Unmarshal(breakdown, &day.ToolBreakdown); err != nil {
			return nil, fmt.Errorf("decode audit activity %q breakdown: %w", day.UserID, err)
		}
		activity = append(activity, day)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit activity: %w", err)
	}
	return activity, nil
}

func appendAuditCondition(
	conditions []string,
	args []any,
	column string,
	value string,
) ([]string, []any) {
	if strings.TrimSpace(value) == "" {
		return conditions, args
	}
	args = append(args, value)
	return append(conditions, fmt.Sprintf("%s = $%d", column, len(args))), args
}

func auditWhereClause(conditions []string) string {
	if len(conditions) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(conditions, "\n  AND ")
}

func normalizeAuditLimit(limit int) int {
	if limit <= 0 {
		return defaultAuditQueryLimit
	}
	if limit > maxAuditQueryLimit {
		return maxAuditQueryLimit
	}
	return limit
}

var (
	_ audit.Store = (*AuditStore)(nil)
	_ audit.Query = (*AuditStore)(nil)
)
