package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

const storageSnapshotSchemaVersion int32 = 1

const operationsRetentionBatchSize = 1000

const (
	storageStatementTimeout = time.Second
	storageWriteReserve     = 250 * time.Millisecond
)

type OperationsStore struct {
	pool     *pgxpool.Pool
	readPool *pgxpool.Pool
}

func (s *OperationsStore) storageReadPool() *pgxpool.Pool {
	if s.readPool != nil {
		return s.readPool
	}
	return s.pool
}

func (s *OperationsStore) Record(ctx context.Context, event operations.Event) (operations.Event, error) {
	if err := event.Validate(); err != nil {
		return operations.Event{}, err
	}
	err := s.pool.QueryRow(ctx, `
INSERT INTO onprem_operation_events (
    attempt_id, operation_kind, outcome, actor_kind, actor_user_id,
    actor_membership_id, actor_agent_id, actor_credential_id, session_id,
    started_at, completed_at, duration_ms, input_items, accepted_items,
    duplicate_items, result_items, delivered_items, evidence_items, hint_items,
    reference_items, input_tokens, output_tokens, detail_kind, detail_id, error_code
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
    $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25
)
RETURNING operation_event_id`,
		event.AttemptID, event.Kind, event.Outcome, event.Actor.Kind,
		nullableText(event.Actor.UserID), nullableText(event.Actor.MembershipID),
		nullableText(event.Actor.AgentID), nullableText(event.Actor.CredentialID),
		nullableText(event.SessionID), event.StartedAt.UTC(), event.CompletedAt.UTC(), event.DurationMS,
		event.InputItems, event.AcceptedItems, event.DuplicateItems, event.ResultItems,
		event.DeliveredItems, event.EvidenceItems, event.HintItems, event.ReferenceItems,
		event.InputTokens, event.OutputTokens, nullableText(event.DetailKind), nullableText(event.DetailID),
		strings.TrimSpace(event.ErrorCode),
	).Scan(&event.OperationEventID)
	if err != nil {
		return operations.Event{}, fmt.Errorf("record postgres operation event: %w", err)
	}
	return event, nil
}

func (s *OperationsStore) Summary(
	ctx context.Context,
	filter operations.TimeFilter,
	generatedAt time.Time,
) (operations.Summary, error) {
	result := operations.Summary{From: filter.From, To: filter.To, GeneratedAt: generatedAt}
	if err := s.scanOperationSummary(ctx, filter, &result); err != nil {
		return operations.Summary{}, fmt.Errorf("summarize postgres operation events: %w", err)
	}
	if err := s.scanExtractionSummary(ctx, filter, &result.Extraction); err != nil {
		return operations.Summary{}, fmt.Errorf("summarize postgres extraction: %w", err)
	}
	return result, nil
}

func (s *OperationsStore) scanOperationSummary(
	ctx context.Context,
	filter operations.TimeFilter,
	result *operations.Summary,
) error {
	err := s.pool.QueryRow(ctx, `
SELECT
    count(*) FILTER (WHERE operation_kind = 'observation.observe'),
    count(*) FILTER (WHERE operation_kind = 'observation.observe' AND outcome = 'succeeded'),
    COALESCE(sum(input_items) FILTER (WHERE operation_kind = 'observation.observe'), 0),
    COALESCE(sum(accepted_items) FILTER (WHERE operation_kind = 'observation.observe'), 0),
    COALESCE(sum(duplicate_items) FILTER (WHERE operation_kind = 'observation.observe'), 0),
    count(*) FILTER (WHERE operation_kind IN ('memory.search', 'memory.get', 'team_note.recall')),
    count(*) FILTER (WHERE operation_kind IN ('memory.search', 'memory.get', 'team_note.recall') AND outcome = 'succeeded'),
    count(*) FILTER (WHERE operation_kind IN ('memory.search', 'team_note.recall') AND outcome = 'succeeded' AND evidence_items > 0),
    count(*) FILTER (WHERE operation_kind IN ('memory.search', 'team_note.recall') AND outcome = 'succeeded' AND result_items = 0 AND delivered_items = 0),
    COALESCE(sum(result_items) FILTER (WHERE operation_kind = 'memory.search'), 0),
    COALESCE(sum(delivered_items) FILTER (WHERE operation_kind IN ('memory.search', 'team_note.recall')), 0),
    count(*) FILTER (WHERE operation_kind = 'memory.search'),
    count(*) FILTER (WHERE operation_kind = 'memory.get'),
    count(*) FILTER (WHERE operation_kind = 'team_note.recall'),
    COALESCE(sum(evidence_items) FILTER (WHERE operation_kind = 'memory.search'), 0),
    COALESCE(sum(hint_items) FILTER (WHERE operation_kind = 'memory.search'), 0),
    COALESCE(sum(reference_items) FILTER (WHERE operation_kind = 'memory.search'), 0),
    count(*) FILTER (WHERE operation_kind IN ('memory.search', 'memory.get', 'team_note.recall')),
    CASE WHEN count(*) FILTER (WHERE operation_kind IN ('memory.search', 'memory.get', 'team_note.recall')) >= 2
      THEN percentile_disc(0.5) WITHIN GROUP (ORDER BY duration_ms)
        FILTER (WHERE operation_kind IN ('memory.search', 'memory.get', 'team_note.recall')) END,
    CASE WHEN count(*) FILTER (WHERE operation_kind IN ('memory.search', 'memory.get', 'team_note.recall')) >= 20
      THEN percentile_disc(0.95) WITHIN GROUP (ORDER BY duration_ms)
        FILTER (WHERE operation_kind IN ('memory.search', 'memory.get', 'team_note.recall')) END,
    count(*) FILTER (WHERE outcome IN ('failed', 'timed_out', 'cancelled'))
FROM onprem_operation_events
WHERE started_at >= $1 AND started_at < $2
  AND ($3 = '' OR actor_agent_id = $3)`, filter.From, filter.To, filter.AgentID).Scan(
		&result.Observations.Requests, &result.Observations.Succeeded,
		&result.Observations.InputEvents, &result.Observations.EventsWritten,
		&result.Observations.DuplicateEvents, &result.Recalls.Requests,
		&result.Recalls.Succeeded, &result.Recalls.WithEvidence, &result.Recalls.Empty,
		&result.Recalls.MemoryHits, &result.Recalls.TeamNotesDelivered,
		&result.Recalls.MemorySearchRequests, &result.Recalls.MemoryGetRequests,
		&result.Recalls.TeamNoteRecallRequests, &result.Recalls.EvidenceHits,
		&result.Recalls.HintHits, &result.Recalls.ReferenceHits,
		&result.Latency.SampleCount, &result.Latency.P50MS, &result.Latency.P95MS, &result.Errors,
	)
	if err != nil {
		return fmt.Errorf("scan operation summary: %w", err)
	}
	return nil
}

func (s *OperationsStore) scanExtractionSummary(
	ctx context.Context,
	filter operations.TimeFilter,
	result *operations.ExtractionSummary,
) error {
	err := s.pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM extraction_runs
      WHERE created_at >= $1 AND created_at < $2 AND ($3 = '' OR agent_id = $3)),
    (SELECT count(*) FROM extraction_runs
      WHERE created_at >= $1 AND created_at < $2 AND status = 'completed' AND ($3 = '' OR agent_id = $3)),
    (SELECT count(*) FROM extraction_runs
      WHERE created_at >= $1 AND created_at < $2 AND status = 'quarantined' AND ($3 = '' OR agent_id = $3)),
    (SELECT count(*) FROM extraction_runs
      WHERE created_at >= $1 AND created_at < $2 AND status = 'failed' AND ($3 = '' OR agent_id = $3)),
    (SELECT count(*) FROM note_revisions revisions
      JOIN team_notes notes ON notes.scope_id = revisions.scope_id AND notes.note_id = revisions.note_id
      WHERE revisions.created_at >= $1 AND revisions.created_at < $2
        AND ($3 = '' OR notes.origin_agent_id = $3)),
    (SELECT count(*) FROM session_events
      WHERE extracted_at IS NULL AND ($3 = '' OR agent_id = $3)),
    (SELECT min(captured_at) FROM session_events
      WHERE extracted_at IS NULL AND ($3 = '' OR agent_id = $3))`,
		filter.From, filter.To, filter.AgentID,
	).Scan(
		&result.Runs, &result.Completed, &result.Quarantined, &result.Failed,
		&result.AdmittedRevisions, &result.UnextractedEvents, &result.OldestUnextractedAt,
	)
	if err != nil {
		return fmt.Errorf("scan extraction summary: %w", err)
	}
	return nil
}

func (s *OperationsStore) ListEvents(
	ctx context.Context,
	filter operations.EventFilter,
) ([]operations.Event, error) {
	cursorTime, cursorID, err := operationsCursor(filter.Cursor)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT operation_event_id, attempt_id, operation_kind, outcome, actor_kind,
       COALESCE(actor_user_id, ''), COALESCE(actor_membership_id, ''),
       COALESCE(actor_agent_id, ''), COALESCE(actor_credential_id, ''),
       COALESCE(session_id, ''), started_at, completed_at, duration_ms,
       input_items, accepted_items, duplicate_items, result_items,
       delivered_items, evidence_items, hint_items, reference_items,
       input_tokens, output_tokens, COALESCE(detail_kind, ''),
       COALESCE(detail_id, ''), error_code
FROM onprem_operation_events
WHERE started_at >= $1 AND started_at < $2
  AND ($3 = '' OR operation_kind = $3)
  AND ($4 = '' OR outcome = $4)
  AND ($5 = '' OR actor_agent_id = $5)
  AND ($6::timestamptz IS NULL OR (started_at, operation_event_id) < ($6, $7))
ORDER BY started_at DESC, operation_event_id DESC
LIMIT $8`, filter.From, filter.To, filter.Kind, filter.Outcome, filter.AgentID,
		nullableTime(cursorTime), cursorID, filter.Limit+1)
	if err != nil {
		return nil, fmt.Errorf("list postgres operation events: %w", err)
	}
	defer rows.Close()
	result := make([]operations.Event, 0, filter.Limit+1)
	for rows.Next() {
		event, scanErr := scanOperationEvent(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan postgres operation event: %w", scanErr)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres operation events: %w", err)
	}
	return result, nil
}

type operationEventScanner interface {
	Scan(...any) error
}

func scanOperationEvent(scanner operationEventScanner) (operations.Event, error) {
	var result operations.Event
	err := scanner.Scan(
		&result.OperationEventID, &result.AttemptID, &result.Kind, &result.Outcome,
		&result.Actor.Kind, &result.Actor.UserID, &result.Actor.MembershipID,
		&result.Actor.AgentID, &result.Actor.CredentialID, &result.SessionID,
		&result.StartedAt, &result.CompletedAt, &result.DurationMS,
		&result.InputItems, &result.AcceptedItems, &result.DuplicateItems,
		&result.ResultItems, &result.DeliveredItems, &result.EvidenceItems,
		&result.HintItems, &result.ReferenceItems, &result.InputTokens,
		&result.OutputTokens, &result.DetailKind, &result.DetailID, &result.ErrorCode,
	)
	return result, err
}

func operationsCursor(value string) (time.Time, int64, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, 0, nil
	}
	return operations.DecodeCursor(value)
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func (s *OperationsStore) GetRecallDiagnostic(
	ctx context.Context,
	observationID int64,
) (operations.RecallDiagnostic, error) {
	var result operations.RecallDiagnostic
	var encodedEnvelope []byte
	var encodedTrace []byte
	var expired bool
	err := s.pool.QueryRow(ctx, `
SELECT observation_id, created_at, recipient_agent_id, recipient_session_id,
       duration_ms, token_budget, max_items, envelope, trace, expires_at <= NOW()
FROM team_note_recall_observations
WHERE observation_id = $1`, observationID).Scan(
		&result.ObservationID, &result.OccurredAt, &result.AgentID, &result.SessionID,
		&result.DurationMS, &result.TokenBudget, &result.MaxItems, &encodedEnvelope, &encodedTrace, &expired,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		linked, linkErr := s.hasRecallDiagnosticEvent(ctx, observationID)
		if linkErr != nil {
			return operations.RecallDiagnostic{}, linkErr
		}
		if linked {
			return operations.RecallDiagnostic{}, operations.ErrRecallExpired
		}
		return operations.RecallDiagnostic{}, operations.ErrRecallNotFound
	}
	if err != nil {
		return operations.RecallDiagnostic{}, fmt.Errorf("get postgres recall diagnostic: %w", err)
	}
	if expired {
		return operations.RecallDiagnostic{}, operations.ErrRecallExpired
	}
	var envelope teamnote.NoteEnvelope
	if err := json.Unmarshal(encodedEnvelope, &envelope); err != nil {
		return operations.RecallDiagnostic{}, fmt.Errorf("decode postgres recall envelope: %w", err)
	}
	var trace teamnote.RecallTrace
	if err := json.Unmarshal(encodedTrace, &trace); err != nil {
		return operations.RecallDiagnostic{}, fmt.Errorf("decode postgres recall trace: %w", err)
	}
	populateRecallDiagnostic(&result, envelope, trace)
	return result, nil
}

func (s *OperationsStore) hasRecallDiagnosticEvent(ctx context.Context, observationID int64) (bool, error) {
	var linked bool
	err := s.pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM onprem_operation_events
    WHERE detail_kind = 'recall_observation' AND detail_id = $1
)`, fmt.Sprintf("%d", observationID)).Scan(&linked)
	if err != nil {
		return false, fmt.Errorf("check postgres recall diagnostic event: %w", err)
	}
	return linked, nil
}

func populateRecallDiagnostic(
	result *operations.RecallDiagnostic,
	envelope teamnote.NoteEnvelope,
	trace teamnote.RecallTrace,
) {
	result.EvidenceSufficient = envelope.Decision.EvidenceSufficient
	result.ReasonCodes = make([]string, len(envelope.Decision.ReasonCodes))
	for index, reason := range envelope.Decision.ReasonCodes {
		result.ReasonCodes[index] = string(reason)
	}
	result.LanesExecuted = make([]string, len(trace.LanesExecuted))
	for index, lane := range trace.LanesExecuted {
		result.LanesExecuted[index] = string(lane)
	}
	result.Candidates = int64(trace.Candidates)
	result.FusionKept = int64(trace.FusionKept)
	result.PlannedNotes = int64(trace.PlannedNotes)
	result.PlannedTokens = int64(trace.PlannedTokens)
	result.DeliveredItems = int64(len(trace.DeliveredItems))
	result.DispositionCounts = make(map[string]int64)
	result.RejectionCounts = make(map[string]int64)
	result.BudgetDropCounts = make(map[string]int64)
	result.HardGateFailureCounts = make(map[string]int64)
	for _, candidate := range trace.CandidateTraces {
		result.DispositionCounts[string(candidate.Disposition)]++
		for _, gate := range candidate.HardGateResults {
			if !gate.Passed {
				result.HardGateFailureCounts[gate.Gate]++
			}
		}
	}
	for _, rejection := range trace.Rejections {
		result.RejectionCounts[string(rejection.Reason)]++
	}
	for _, rejection := range trace.RelationRejections {
		result.RejectionCounts[string(rejection.Reason)]++
	}
	for _, rejection := range trace.BudgetDrops {
		result.BudgetDropCounts[string(rejection.Reason)]++
	}
}

type storageDefinition struct {
	name    string
	tables  []string
	measure func(context.Context, time.Time) (operations.StorageComponent, error)
}

func (s *OperationsStore) CaptureStorage(
	ctx context.Context,
	capturedAt time.Time,
) (operations.StorageSnapshot, error) {
	capturedAt = capturedAt.UTC()
	warnings := make([]string, 0)
	definitions := s.storageDefinitions()
	remainingReads := len(definitions) + 2
	databaseContext, cancelDatabase := storageReadContext(ctx, remainingReads)
	databaseBytes, err := s.databasePhysicalBytes(databaseContext)
	cancelDatabase()
	remainingReads--
	if err != nil {
		warnings = append(warnings, "database_physical_unavailable")
	}
	riverContext, cancelRiver := storageReadContext(ctx, remainingReads)
	riverTables, err := s.riverTables(riverContext)
	cancelRiver()
	remainingReads--
	if err != nil {
		warnings = append(warnings, "extraction_river_relations_unavailable")
	} else {
		definitions[1].tables = append(definitions[1].tables, riverTables...)
	}
	components := make([]operations.StorageComponent, 0, len(definitions))
	var categorizedBytes int64
	for _, definition := range definitions {
		componentContext, cancelComponent := storageReadContext(ctx, remainingReads)
		component, componentWarnings := s.captureStorageComponent(componentContext, capturedAt, definition)
		cancelComponent()
		remainingReads--
		warnings = append(warnings, componentWarnings...)
		categorizedBytes += component.PhysicalBytes
		components = append(components, component)
	}
	otherBytes := databaseBytes - categorizedBytes
	if otherBytes < 0 {
		otherBytes = 0
		warnings = append(warnings, "categorized_physical_exceeds_database")
	}
	status := "complete"
	if len(warnings) > 0 {
		status = "partial"
	}
	sort.Strings(warnings)
	snapshot := operations.StorageSnapshot{
		SchemaVersion: storageSnapshotSchemaVersion, CapturedAt: capturedAt, Status: status,
		WarningCodes: warnings, DatabasePhysicalBytes: databaseBytes,
		OtherPhysicalBytes: otherBytes, Components: components,
	}
	encoded, err := json.Marshal(snapshot.Components)
	if err != nil {
		return operations.StorageSnapshot{}, fmt.Errorf("encode postgres storage components: %w", err)
	}
	err = s.pool.QueryRow(ctx, `
INSERT INTO onprem_storage_snapshots (
    schema_version, captured_at, status, warning_codes,
    database_physical_bytes, other_physical_bytes, components
) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
RETURNING snapshot_id`, snapshot.SchemaVersion, snapshot.CapturedAt, snapshot.Status,
		snapshot.WarningCodes, snapshot.DatabasePhysicalBytes, snapshot.OtherPhysicalBytes, string(encoded),
	).Scan(&snapshot.SnapshotID)
	if err != nil {
		return operations.StorageSnapshot{}, fmt.Errorf("save postgres storage snapshot: %w", err)
	}
	return snapshot, nil
}

func storageReadContext(parent context.Context, remainingReads int) (context.Context, context.CancelFunc) {
	budget := storageStatementTimeout
	if deadline, ok := parent.Deadline(); ok && remainingReads > 0 {
		available := time.Until(deadline) - storageWriteReserve
		fairShare := available / time.Duration(remainingReads)
		if fairShare < budget {
			budget = fairShare
		}
	}
	if budget < time.Millisecond {
		budget = time.Millisecond
	}
	return context.WithTimeout(parent, budget)
}

func (s *OperationsStore) storageDefinitions() []storageDefinition {
	return []storageDefinition{
		{name: "session_lake", tables: []string{"session_events", "session_streams"}, measure: s.measureSessionLake},
		{name: "extraction", tables: []string{"extraction_runs", "note_candidates", "extraction_episodes"}, measure: s.measureExtraction},
		{name: "team_memory", tables: []string{"team_notes", "note_revisions", "note_evidence", "note_deliveries"}, measure: s.measureTeamMemory},
		{name: "recall_diagnostics", tables: []string{"team_note_recall_observations", "recall_hint_deliveries"}, measure: s.measureRecallDiagnostics},
		{name: "capsule_channel", tables: []string{"onprem_channel_envelopes"}, measure: s.measureCapsuleChannel},
		{name: "identity_audit", tables: []string{
			"onprem_installation_state", "onprem_users", "onprem_memberships", "onprem_human_sessions",
			"onprem_membership_invitations", "onprem_agents", "onprem_agent_identities",
			"agent_enrollments", "agent_credentials", "onprem_audit_events",
		}, measure: s.measureIdentityAudit},
		{name: "operations", tables: []string{"onprem_operation_events", "onprem_storage_snapshots"}, measure: s.measureOperations},
	}
}

func (s *OperationsStore) captureStorageComponent(
	ctx context.Context,
	capturedAt time.Time,
	definition storageDefinition,
) (operations.StorageComponent, []string) {
	component, err := definition.measure(ctx, capturedAt)
	warnings := make([]string, 0, 2)
	if err != nil {
		component = operations.StorageComponent{Component: definition.name, Counts: make(map[string]int64)}
		warnings = append(warnings, definition.name+"_logical_unavailable")
	}
	component.Component = definition.name
	physicalBytes, err := s.relationPhysicalBytes(ctx, definition.tables)
	if err != nil {
		warnings = append(warnings, definition.name+"_relation_size_unavailable")
	} else {
		component.PhysicalBytes = physicalBytes
	}
	if component.Counts == nil {
		component.Counts = make(map[string]int64)
	}
	return component, warnings
}

func (s *OperationsStore) databasePhysicalBytes(ctx context.Context) (int64, error) {
	var result int64
	if err := s.storageReadPool().QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&result); err != nil {
		return 0, fmt.Errorf("measure postgres database size: %w", err)
	}
	return result, nil
}

func (s *OperationsStore) riverTables(ctx context.Context) ([]string, error) {
	rows, err := s.storageReadPool().Query(ctx, `
SELECT relname
FROM pg_class
WHERE relnamespace = current_schema()::regnamespace
  AND relkind IN ('r', 'p') AND relname LIKE 'river_%'
ORDER BY relname`)
	if err != nil {
		return nil, fmt.Errorf("list River relations: %w", err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan River relation: %w", err)
		}
		result = append(result, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate River relations: %w", err)
	}
	return result, nil
}

func (s *OperationsStore) relationPhysicalBytes(ctx context.Context, tables []string) (int64, error) {
	var physicalBytes int64
	pool := s.storageReadPool()
	for _, table := range tables {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			return 0, fmt.Errorf("resolve postgres relation %q: %w", table, err)
		}
		if !exists {
			return 0, fmt.Errorf("resolve postgres relation %q: relation is missing", table)
		}
		var currentPhysical int64
		if err := pool.QueryRow(ctx, `SELECT pg_total_relation_size($1::regclass)`, table).Scan(&currentPhysical); err != nil {
			return 0, fmt.Errorf("measure postgres relation %q physical bytes: %w", table, err)
		}
		physicalBytes += currentPhysical
	}
	return physicalBytes, nil
}

func (s *OperationsStore) measureSessionLake(
	ctx context.Context,
	_ time.Time,
) (operations.StorageComponent, error) {
	component := operations.StorageComponent{Counts: make(map[string]int64)}
	err := s.storageReadPool().QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM session_events),
    (SELECT count(*) FROM session_streams),
    (SELECT count(*) FROM session_events WHERE extracted_at IS NULL),
    (SELECT COALESCE(sum(octet_length(content)), 0) FROM session_events),
    (SELECT COALESCE(sum(pg_column_size(metadata)), 0) FROM session_events),
    (SELECT min(occurred_at) FROM session_events),
    (SELECT max(occurred_at) FROM session_events)`).Scan(
		countTarget(component.Counts, "events"), countTarget(component.Counts, "streams"),
		countTarget(component.Counts, "unextracted_events"), countTarget(component.Counts, "content_bytes"),
		countTarget(component.Counts, "metadata_bytes"), &component.OldestAt, &component.NewestAt,
	)
	if err != nil {
		return operations.StorageComponent{}, fmt.Errorf("measure session lake logical inventory: %w", err)
	}
	component.LogicalBytes = component.Counts["content_bytes"] + component.Counts["metadata_bytes"]
	return component, nil
}

func (s *OperationsStore) measureTeamMemory(
	ctx context.Context,
	capturedAt time.Time,
) (operations.StorageComponent, error) {
	component := operations.StorageComponent{Counts: make(map[string]int64)}
	err := s.storageReadPool().QueryRow(ctx, `
WITH effective_notes AS (
    SELECT *, CASE
        WHEN state = 'expired' OR hard_expires_at <= $1 THEN 'expired'
        WHEN state = 'resolved' OR (invalid_at IS NOT NULL AND invalid_at <= $1) THEN 'resolved'
        ELSE 'active'
    END AS effective_state
    FROM team_notes
)
SELECT
    (SELECT count(*) FROM effective_notes),
    (SELECT count(*) FROM effective_notes WHERE effective_state = 'active'),
    (SELECT count(*) FROM effective_notes WHERE effective_state = 'resolved'),
    (SELECT count(*) FROM effective_notes WHERE effective_state = 'expired'),
    (SELECT count(*) FROM note_revisions),
    (SELECT count(*) FROM note_evidence),
    (SELECT count(*) FROM note_deliveries),
    (SELECT COALESCE(sum(cardinality(related_subjects)), 0) FROM team_notes),
    (SELECT COALESCE(sum(octet_length(subject) + octet_length(body)), 0) FROM team_notes),
    (SELECT COALESCE(sum(octet_length(body)), 0) FROM note_revisions),
    (SELECT COALESCE(sum(pg_column_size(embedding)) FILTER (WHERE embedding IS NOT NULL), 0) FROM team_notes),
    (SELECT min(created_at) FROM team_notes),
    (SELECT max(updated_at) FROM team_notes)`, capturedAt).Scan(
		countTarget(component.Counts, "notes"), countTarget(component.Counts, "active_notes"),
		countTarget(component.Counts, "resolved_notes"), countTarget(component.Counts, "expired_notes"),
		countTarget(component.Counts, "revisions"), countTarget(component.Counts, "evidence_links"),
		countTarget(component.Counts, "deliveries"), countTarget(component.Counts, "relation_references"),
		countTarget(component.Counts, "current_note_bytes"), countTarget(component.Counts, "revision_history_bytes"),
		countTarget(component.Counts, "embedding_bytes"), &component.OldestAt, &component.NewestAt,
	)
	if err != nil {
		return operations.StorageComponent{}, fmt.Errorf("measure team memory logical inventory: %w", err)
	}
	component.LogicalBytes = component.Counts["current_note_bytes"] +
		component.Counts["revision_history_bytes"] + component.Counts["embedding_bytes"]
	return component, nil
}

func (s *OperationsStore) measureExtraction(
	ctx context.Context,
	_ time.Time,
) (operations.StorageComponent, error) {
	component := operations.StorageComponent{Counts: make(map[string]int64)}
	err := s.storageReadPool().QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM extraction_runs),
    (SELECT count(*) FROM extraction_runs WHERE status = 'started'),
    (SELECT count(*) FROM extraction_runs WHERE status = 'completed'),
    (SELECT count(*) FROM extraction_runs WHERE status = 'quarantined'),
    (SELECT count(*) FROM extraction_runs WHERE status = 'failed'),
    (SELECT count(*) FROM note_candidates),
    (SELECT count(*) FROM extraction_episodes),
    (SELECT COALESCE(sum(octet_length(subject) + octet_length(body)), 0) FROM note_candidates),
    (SELECT COALESCE(sum(pg_column_size(checkpoint) + pg_column_size(messages) + pg_column_size(runs)), 0)
       FROM extraction_episodes),
    (SELECT COALESCE(sum(octet_length(input_checksum) + octet_length(model) +
                                octet_length(prompt_version) + octet_length(error)), 0)
       FROM extraction_runs),
    (SELECT min(created_at) FROM extraction_runs),
    (SELECT max(COALESCE(completed_at, created_at)) FROM extraction_runs)`).Scan(
		countTarget(component.Counts, "runs"), countTarget(component.Counts, "started_runs"),
		countTarget(component.Counts, "completed_runs"), countTarget(component.Counts, "quarantined_runs"),
		countTarget(component.Counts, "failed_runs"), countTarget(component.Counts, "candidates"),
		countTarget(component.Counts, "episodes"), countTarget(component.Counts, "candidate_bytes"),
		countTarget(component.Counts, "episode_bytes"), countTarget(component.Counts, "run_metadata_bytes"),
		&component.OldestAt, &component.NewestAt,
	)
	if err != nil {
		return operations.StorageComponent{}, fmt.Errorf("measure extraction logical inventory: %w", err)
	}
	component.LogicalBytes = component.Counts["candidate_bytes"] +
		component.Counts["episode_bytes"] + component.Counts["run_metadata_bytes"]
	return component, nil
}

func (s *OperationsStore) measureRecallDiagnostics(
	ctx context.Context,
	_ time.Time,
) (operations.StorageComponent, error) {
	component := operations.StorageComponent{Counts: make(map[string]int64)}
	err := s.storageReadPool().QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM team_note_recall_observations),
    (SELECT count(*) FROM recall_hint_deliveries),
    (SELECT COALESCE(sum(pg_column_size(extraction_snapshot) + pg_column_size(extraction_provenance) +
                                pg_column_size(envelope) + pg_column_size(trace)), 0)
       FROM team_note_recall_observations),
    (SELECT min(expires_at) FROM team_note_recall_observations),
    (SELECT max(created_at) FROM team_note_recall_observations)`).Scan(
		countTarget(component.Counts, "recall_observations"), countTarget(component.Counts, "hint_deliveries"),
		countTarget(component.Counts, "diagnostic_bytes"), &component.OldestAt, &component.NewestAt,
	)
	if err != nil {
		return operations.StorageComponent{}, fmt.Errorf("measure recall diagnostics logical inventory: %w", err)
	}
	component.LogicalBytes = component.Counts["diagnostic_bytes"]
	return component, nil
}

func (s *OperationsStore) measureCapsuleChannel(
	ctx context.Context,
	_ time.Time,
) (operations.StorageComponent, error) {
	component := operations.StorageComponent{Counts: make(map[string]int64)}
	err := s.storageReadPool().QueryRow(ctx, `
SELECT count(*),
       count(*) FILTER (WHERE status = 'pending'),
       count(*) FILTER (WHERE status = 'accepted'),
       count(*) FILTER (WHERE status = 'archived'),
       COALESCE(sum(pg_column_size(payload_json) + octet_length(message)), 0),
       min(created_at), max(created_at)
FROM onprem_channel_envelopes`).Scan(
		countTarget(component.Counts, "envelopes"), countTarget(component.Counts, "pending_envelopes"),
		countTarget(component.Counts, "accepted_envelopes"), countTarget(component.Counts, "archived_envelopes"),
		countTarget(component.Counts, "payload_bytes"), &component.OldestAt, &component.NewestAt,
	)
	if err != nil {
		return operations.StorageComponent{}, fmt.Errorf("measure capsule channel logical inventory: %w", err)
	}
	component.LogicalBytes = component.Counts["payload_bytes"]
	return component, nil
}

func (s *OperationsStore) measureIdentityAudit(
	ctx context.Context,
	capturedAt time.Time,
) (operations.StorageComponent, error) {
	component := operations.StorageComponent{Counts: make(map[string]int64)}
	err := s.storageReadPool().QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM onprem_users),
    (SELECT count(*) FROM onprem_memberships),
    (SELECT count(*) FROM onprem_agents),
    (SELECT count(*) FROM agent_credentials WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > $1)),
    (SELECT count(*) FROM onprem_human_sessions WHERE revoked_at IS NULL AND expires_at > $1),
    (SELECT count(*) FROM onprem_membership_invitations),
    (SELECT count(*) FROM onprem_audit_events),
    (SELECT COALESCE(sum(pg_column_size(metadata)), 0) FROM onprem_audit_events),
    (SELECT COALESCE(sum(octet_length(COALESCE(email, '')) + octet_length(display_name)), 0) FROM onprem_users),
    (SELECT COALESCE(sum(octet_length(display_name) + octet_length(description) + octet_length(agent_type)), 0)
       FROM onprem_agents),
    (SELECT min(occurred_at) FROM onprem_audit_events),
    (SELECT max(occurred_at) FROM onprem_audit_events)`, capturedAt).Scan(
		countTarget(component.Counts, "users"), countTarget(component.Counts, "memberships"),
		countTarget(component.Counts, "agents"), countTarget(component.Counts, "active_credentials"),
		countTarget(component.Counts, "active_human_sessions"), countTarget(component.Counts, "invitations"),
		countTarget(component.Counts, "audit_events"), countTarget(component.Counts, "audit_metadata_bytes"),
		countTarget(component.Counts, "user_profile_bytes"), countTarget(component.Counts, "agent_profile_bytes"),
		&component.OldestAt, &component.NewestAt,
	)
	if err != nil {
		return operations.StorageComponent{}, fmt.Errorf("measure identity and audit logical inventory: %w", err)
	}
	component.LogicalBytes = component.Counts["audit_metadata_bytes"] +
		component.Counts["user_profile_bytes"] + component.Counts["agent_profile_bytes"]
	return component, nil
}

func (s *OperationsStore) measureOperations(
	ctx context.Context,
	_ time.Time,
) (operations.StorageComponent, error) {
	component := operations.StorageComponent{Counts: make(map[string]int64)}
	err := s.storageReadPool().QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM onprem_operation_events),
    (SELECT count(*) FROM onprem_storage_snapshots),
    (SELECT COALESCE(sum(octet_length(attempt_id) + octet_length(operation_kind) + octet_length(outcome) +
                                octet_length(error_code)), 0) FROM onprem_operation_events),
    (SELECT COALESCE(sum(pg_column_size(components) + pg_column_size(warning_codes)), 0)
       FROM onprem_storage_snapshots),
    (SELECT min(started_at) FROM onprem_operation_events),
    (SELECT max(started_at) FROM onprem_operation_events)`).Scan(
		countTarget(component.Counts, "operation_events"), countTarget(component.Counts, "storage_snapshots"),
		countTarget(component.Counts, "event_metadata_bytes"), countTarget(component.Counts, "snapshot_bytes"),
		&component.OldestAt, &component.NewestAt,
	)
	if err != nil {
		return operations.StorageComponent{}, fmt.Errorf("measure operations logical inventory: %w", err)
	}
	component.LogicalBytes = component.Counts["event_metadata_bytes"] + component.Counts["snapshot_bytes"]
	return component, nil
}

type countMapScanner struct {
	counts map[string]int64
	key    string
}

func (s *countMapScanner) Scan(source any) error {
	value, ok := source.(int64)
	if !ok {
		return fmt.Errorf("scan operation count %q: expected int64, got %T", s.key, source)
	}
	s.counts[s.key] = value
	return nil
}

func countTarget(counts map[string]int64, key string) *countMapScanner {
	return &countMapScanner{counts: counts, key: key}
}

func (s *OperationsStore) LatestStorage(ctx context.Context) (operations.StorageSnapshot, error) {
	snapshot, err := scanStorageSnapshot(s.pool.QueryRow(ctx, `
SELECT snapshot_id, schema_version, captured_at, status, warning_codes,
       database_physical_bytes, other_physical_bytes, components
FROM onprem_storage_snapshots
ORDER BY captured_at DESC, snapshot_id DESC
LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return operations.StorageSnapshot{}, operations.ErrStorageNotAvailable
	}
	if err != nil {
		return operations.StorageSnapshot{}, fmt.Errorf("get latest postgres storage snapshot: %w", err)
	}
	return snapshot, nil
}

func (s *OperationsStore) ListStorage(
	ctx context.Context,
	filter operations.StorageFilter,
) ([]operations.StorageSnapshot, error) {
	cursorTime, cursorID, err := operationsCursor(filter.Cursor)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT snapshot_id, schema_version, captured_at, status, warning_codes,
       database_physical_bytes, other_physical_bytes, components
FROM onprem_storage_snapshots
WHERE captured_at >= $1 AND captured_at < $2
  AND ($3::timestamptz IS NULL OR (captured_at, snapshot_id) < ($3, $4))
ORDER BY captured_at DESC, snapshot_id DESC
LIMIT $5`, filter.From, filter.To, nullableTime(cursorTime), cursorID, filter.Limit+1)
	if err != nil {
		return nil, fmt.Errorf("list postgres storage snapshots: %w", err)
	}
	defer rows.Close()
	result := make([]operations.StorageSnapshot, 0, filter.Limit+1)
	for rows.Next() {
		snapshot, scanErr := scanStorageSnapshot(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan postgres storage snapshot: %w", scanErr)
		}
		result = append(result, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres storage snapshots: %w", err)
	}
	return result, nil
}

type storageSnapshotScanner interface {
	Scan(...any) error
}

func scanStorageSnapshot(scanner storageSnapshotScanner) (operations.StorageSnapshot, error) {
	var snapshot operations.StorageSnapshot
	var encodedComponents []byte
	if err := scanner.Scan(
		&snapshot.SnapshotID, &snapshot.SchemaVersion, &snapshot.CapturedAt,
		&snapshot.Status, &snapshot.WarningCodes, &snapshot.DatabasePhysicalBytes,
		&snapshot.OtherPhysicalBytes, &encodedComponents,
	); err != nil {
		return operations.StorageSnapshot{}, err
	}
	if err := json.Unmarshal(encodedComponents, &snapshot.Components); err != nil {
		return operations.StorageSnapshot{}, fmt.Errorf("decode storage components: %w", err)
	}
	return snapshot, nil
}

func (s *OperationsStore) DeleteBefore(
	ctx context.Context,
	operationCutoff time.Time,
	storageCutoff time.Time,
) (deletedEvents int64, deletedSnapshots int64, returnedErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("begin operations retention: %w", err)
	}
	defer rollbackTx(&returnedErr, tx, "operations retention")
	eventResult, err := tx.Exec(ctx, `
WITH expired AS (
    SELECT operation_event_id
    FROM onprem_operation_events
    WHERE started_at < $1
    ORDER BY operation_event_id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
DELETE FROM onprem_operation_events events
USING expired
WHERE events.operation_event_id = expired.operation_event_id`, operationCutoff.UTC(), operationsRetentionBatchSize)
	if err != nil {
		return 0, 0, fmt.Errorf("delete expired operation events: %w", err)
	}
	snapshotResult, err := tx.Exec(ctx, `
WITH expired AS (
    SELECT snapshot_id
    FROM onprem_storage_snapshots
    WHERE captured_at < $1
    ORDER BY snapshot_id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
DELETE FROM onprem_storage_snapshots snapshots
USING expired
WHERE snapshots.snapshot_id = expired.snapshot_id`, storageCutoff.UTC(), operationsRetentionBatchSize)
	if err != nil {
		return 0, 0, fmt.Errorf("delete expired storage snapshots: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit operations retention: %w", err)
	}
	return eventResult.RowsAffected(), snapshotResult.RowsAffected(), nil
}
