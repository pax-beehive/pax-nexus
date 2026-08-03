package postgres_test

import (
	"context"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/audit"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/session"
)

func (s *storeSuite) TestAuditMigrationReplays() {
	ctx := context.Background()
	s.Require().NoError(s.store.Migrate(ctx), "migration replay must be idempotent")

	for _, table := range []string{"audit_tool_calls", "audit_findings", "audit_activity_daily"} {
		var regclass *string
		err := s.store.Pool().QueryRow(ctx, `SELECT to_regclass($1)::text`, table).Scan(&regclass)
		s.Require().NoError(err)
		s.Require().NotNil(regclass, "table %s must exist", table)
	}
}

func (s *storeSuite) TestAuditStoreProjectsAndAdvancesAtomically() {
	ctx := context.Background()
	scopeID := uniqueScope("audit-atomic")
	actor := session.Actor{UserID: "audit-owner", AgentID: "agent-audit", SessionID: "sess-audit"}
	stream := s.seedAuditSession(ctx, scopeID, actor, []session.SessionEvent{
		auditEvent("audit-evt-1", actor, 1, "message", nil),
		auditEvent("audit-evt-2", actor, 2, audit.EventTypeToolCall, map[string]string{
			audit.MetadataToolCallID:       "call-1",
			audit.MetadataToolName:         "Bash",
			audit.MetadataToolInputSummary: "rm -rf /tmp/cache",
		}),
		auditEvent("audit-evt-3", actor, 3, audit.EventTypeToolApproval, map[string]string{
			audit.MetadataToolCallID:       "call-1",
			audit.MetadataApprovalDecision: audit.DecisionDenied,
		}),
	})

	auditStore, err := postgres.NewAuditStore(s.store.Pool())
	s.Require().NoError(err)
	_, err = auditStore.PendingStreams(ctx)
	s.Require().NoError(err, "pending stream scan must succeed")
	s.True(s.auditStreamPending(ctx, scopeID, actor), "fresh stream must be pending")

	events, err := auditStore.SessionEvents(ctx, stream)
	s.Require().NoError(err)
	s.Require().Len(events, 3)
	s.Equal("call-1", events[1].Metadata[audit.MetadataToolCallID])

	s.Require().NoError(auditStore.ApplyBatch(ctx, audit.Project(stream, events)))

	s.False(s.auditStreamPending(ctx, scopeID, actor),
		"applied stream must no longer be pending")

	calls, err := auditStore.ListToolCalls(ctx, audit.ToolCallFilter{ScopeID: scopeID})
	s.Require().NoError(err)
	s.Require().Len(calls, 1)
	s.Equal("audit-evt-2", calls[0].EventID)
	s.Equal(audit.LevelCritical, calls[0].RiskLevel)
	s.Contains(calls[0].RiskReasons, audit.ReasonDestructiveCommand)
	s.Equal(audit.ApprovalDenied, calls[0].ApprovalState)

	findings, err := auditStore.ListFindings(ctx, audit.FindingFilter{
		ScopeID: scopeID, Kind: audit.FindingDeniedToolExecuted,
	})
	s.Require().NoError(err)
	s.Require().Len(findings, 1)
	s.Equal([]string{"audit-evt-2"}, findings[0].EvidenceEventIDs)
	s.NotZero(findings[0].FindingID)

	activity, err := auditStore.GetActivityDaily(ctx, audit.ActivityFilter{ScopeID: scopeID})
	s.Require().NoError(err)
	s.Require().Len(activity, 1)
	s.Equal(int64(3), activity[0].EventCount)
	s.Equal(int64(1), activity[0].ToolCallCount)
	s.Equal(int64(1), activity[0].HighRiskCount)
	s.Equal(int64(1), activity[0].SessionCount)
	s.Equal(map[string]int64{"Bash": 1}, activity[0].ToolBreakdown)
}

func (s *storeSuite) TestAuditCursorIsMonotonic() {
	ctx := context.Background()
	scopeID := uniqueScope("audit-cursor")
	actor := session.Actor{UserID: "audit-owner", AgentID: "agent-audit-mono", SessionID: "sess-mono"}
	stream := s.seedAuditSession(ctx, scopeID, actor, []session.SessionEvent{
		auditEvent("mono-evt-1", actor, 1, "message", nil),
		auditEvent("mono-evt-2", actor, 2, "message", nil),
		auditEvent("mono-evt-3", actor, 3, "message", nil),
	})
	auditStore, err := postgres.NewAuditStore(s.store.Pool())
	s.Require().NoError(err)

	s.Require().NoError(auditStore.ApplyBatch(ctx, audit.Batch{
		ScopeID: scopeID, Actor: actor, Cursor: stream.Head,
	}))
	s.Equal(stream.Head, s.auditCommittedSequence(ctx, scopeID, actor))

	// A stale batch at an older cursor must not move committed_sequence back.
	s.Require().NoError(auditStore.ApplyBatch(ctx, audit.Batch{
		ScopeID: scopeID, Actor: actor, Cursor: 1,
	}))

	s.Equal(stream.Head, s.auditCommittedSequence(ctx, scopeID, actor))
	s.False(s.auditStreamPending(ctx, scopeID, actor))
}

func (s *storeSuite) TestAuditQueryFilters() {
	ctx := context.Background()
	scopeID := uniqueScope("audit-filters")
	owner := session.Actor{UserID: "owner-a", AgentID: "agent-a", SessionID: "sess-a"}
	other := session.Actor{UserID: "owner-b", AgentID: "agent-b", SessionID: "sess-b"}
	ownerStream := s.seedAuditSession(ctx, scopeID, owner, []session.SessionEvent{
		auditEvent("filter-evt-1", owner, 1, audit.EventTypeToolCall, map[string]string{
			audit.MetadataToolCallID:       "call-a",
			audit.MetadataToolName:         "Bash",
			audit.MetadataToolInputSummary: "rm -rf /tmp/cache",
		}),
	})
	otherStream := s.seedAuditSession(ctx, scopeID, other, []session.SessionEvent{
		auditEvent("filter-evt-2", other, 1, audit.EventTypeToolCall, map[string]string{
			audit.MetadataToolCallID:       "call-b",
			audit.MetadataToolName:         "Read",
			audit.MetadataToolInputSummary: "read README.md",
		}),
	})
	auditStore, err := postgres.NewAuditStore(s.store.Pool())
	s.Require().NoError(err)
	for _, stream := range []audit.Stream{ownerStream, otherStream} {
		events, err := auditStore.SessionEvents(ctx, stream)
		s.Require().NoError(err)
		s.Require().NoError(auditStore.ApplyBatch(ctx, audit.Project(stream, events)))
	}

	byUser, err := auditStore.ListToolCalls(ctx, audit.ToolCallFilter{ScopeID: scopeID, UserID: "owner-a"})
	s.Require().NoError(err)
	s.Require().Len(byUser, 1)
	s.Equal("call-a", byUser[0].CallID)

	byRisk, err := auditStore.ListToolCalls(ctx, audit.ToolCallFilter{
		ScopeID: scopeID, RiskLevel: string(audit.LevelCritical),
	})
	s.Require().NoError(err)
	s.Require().Len(byRisk, 1)
	s.Equal("call-a", byRisk[0].CallID)

	byApproval, err := auditStore.ListToolCalls(ctx, audit.ToolCallFilter{
		ScopeID: scopeID, ApprovalState: audit.ApprovalUnknown,
	})
	s.Require().NoError(err)
	s.Len(byApproval, 2)

	highRiskFindings, err := auditStore.ListFindings(ctx, audit.FindingFilter{
		ScopeID: scopeID, Kind: audit.FindingHighRiskUnapproved,
	})
	s.Require().NoError(err)
	s.Require().Len(highRiskFindings, 1)
	s.Equal("owner-a", highRiskFindings[0].UserID)

	activityAll, err := auditStore.GetActivityDaily(ctx, audit.ActivityFilter{ScopeID: scopeID})
	s.Require().NoError(err)
	s.Len(activityAll, 2)

	activityOne, err := auditStore.GetActivityDaily(ctx, audit.ActivityFilter{
		ScopeID: scopeID,
		UserID:  "owner-a",
		FromDay: time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
		ToDay:   time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
	})
	s.Require().NoError(err)
	s.Require().Len(activityOne, 1)
	s.Equal("owner-a", activityOne[0].UserID)
}

// seedAuditSession appends one legacy agent-session batch and returns the
// audit stream handle pointing at its head.
func (s *storeSuite) seedAuditSession(
	ctx context.Context,
	scopeID string,
	actor session.Actor,
	events []session.SessionEvent,
) audit.Stream {
	receipt, err := s.sessions.AppendSession(ctx, scopeID, session.SessionBatch{Events: events})
	s.Require().NoError(err)
	return audit.Stream{ScopeID: scopeID, Actor: actor, Head: receipt.Cursor}
}

// auditStreamPending applies the PendingStreams predicate to one stream. The
// shared integration database carries many streams that are permanently
// pending for session_audit, so the LIMIT 100 window of PendingStreams
// itself cannot reliably surface a freshly seeded stream.
func (s *storeSuite) auditStreamPending(ctx context.Context, scopeID string, actor session.Actor) bool {
	var pending bool
	err := s.store.Pool().QueryRow(ctx, `
SELECT stream.last_sequence > COALESCE(cursor.committed_sequence, 0)
FROM session_streams AS stream
LEFT JOIN session_processor_cursors AS cursor
  ON cursor.processor_name = $1
 AND cursor.processor_version = $2
 AND cursor.scope_id = stream.scope_id
 AND cursor.agent_id = stream.agent_id
 AND cursor.session_id = stream.session_id
WHERE stream.scope_id = $3 AND stream.agent_id = $4 AND stream.session_id = $5
  AND stream.source = 'agent-session'`,
		audit.ProcessorName, audit.ProcessorVersion, scopeID, actor.AgentID, actor.SessionID,
	).Scan(&pending)
	s.Require().NoError(err)
	return pending
}

func (s *storeSuite) auditCommittedSequence(
	ctx context.Context,
	scopeID string,
	actor session.Actor,
) int64 {
	var committed int64
	err := s.store.Pool().QueryRow(ctx, `
SELECT committed_sequence
FROM session_processor_cursors
WHERE processor_name = $1 AND processor_version = $2
  AND scope_id = $3 AND agent_id = $4 AND session_id = $5`,
		audit.ProcessorName, audit.ProcessorVersion, scopeID, actor.AgentID, actor.SessionID,
	).Scan(&committed)
	s.Require().NoError(err)
	return committed
}

func auditEvent(
	id string,
	actor session.Actor,
	sequence int64,
	eventType string,
	metadata map[string]string,
) session.SessionEvent {
	return session.SessionEvent{
		ID:         id,
		Actor:      actor,
		Sequence:   sequence,
		Type:       eventType,
		Content:    "content",
		Visibility: "team",
		OccurredAt: time.Date(2026, 7, 30, 10, 0, int(sequence), 0, time.UTC),
		Metadata:   metadata,
	}
}
