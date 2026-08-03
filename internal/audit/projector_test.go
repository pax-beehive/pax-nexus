package audit_test

import (
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/audit"
	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/stretchr/testify/suite"
)

type projectorSuite struct {
	suite.Suite
	stream audit.Stream
	actor  session.Actor
}

func TestProjectorSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(projectorSuite))
}

func (s *projectorSuite) SetupTest() {
	s.actor = session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "sess-1"}
	s.stream = audit.Stream{ScopeID: "local-team", Actor: s.actor, Head: 4}
}

func (s *projectorSuite) event(id string, sequence int64, eventType string) session.SessionEvent {
	return session.SessionEvent{
		ID:         id,
		Actor:      s.actor,
		Sequence:   sequence,
		Type:       eventType,
		Visibility: "team",
		OccurredAt: time.Date(2026, 7, 30, 10, 0, int(sequence), 0, time.UTC),
		CapturedAt: time.Date(2026, 7, 30, 10, 1, int(sequence), 0, time.UTC),
	}
}

func (s *projectorSuite) toolCall(id string, sequence int64, metadata map[string]string) session.SessionEvent {
	event := s.event(id, sequence, audit.EventTypeToolCall)
	event.Metadata = metadata
	return event
}

func (s *projectorSuite) TestCorrelatesApprovalFromSeparateEvent() {
	events := []session.SessionEvent{
		s.toolCall("evt-1", 1, map[string]string{
			audit.MetadataToolCallID:       "call-1",
			audit.MetadataToolName:         "Bash",
			audit.MetadataToolInputSummary: "rm -rf /tmp/cache",
		}),
		func() session.SessionEvent {
			event := s.event("evt-2", 2, audit.EventTypeToolApproval)
			event.Metadata = map[string]string{
				audit.MetadataToolCallID:       "call-1",
				audit.MetadataApprovalDecision: audit.DecisionDenied,
				audit.MetadataApprovalActor:    "owner",
			}
			return event
		}(),
	}

	batch := audit.Project(s.stream, events)

	s.Require().Len(batch.ToolCalls, 1)
	call := batch.ToolCalls[0]
	s.Equal(audit.ApprovalDenied, call.ApprovalState)
	s.Equal(audit.LevelCritical, call.RiskLevel)
	s.Equal("call-1", call.CallID)
	s.Equal("evt-1", call.EventID)
	s.Empty(batch.ApprovalUpdates, "in-batch approval is claimed by the tool call")
	kinds := findingKinds(batch.Findings)
	s.Contains(kinds, audit.FindingDeniedToolExecuted)
	s.NotContains(kinds, audit.FindingHighRiskUnapproved)
}

func (s *projectorSuite) TestCorrelatesInlineApprovalDecisionOnToolCall() {
	events := []session.SessionEvent{
		s.toolCall("evt-1", 1, map[string]string{
			audit.MetadataToolCallID:       "call-1",
			audit.MetadataToolName:         "Bash",
			audit.MetadataToolInputSummary: "rm -rf /tmp/cache",
			audit.MetadataApprovalDecision: audit.DecisionApproved,
		}),
	}

	batch := audit.Project(s.stream, events)

	s.Require().Len(batch.ToolCalls, 1)
	s.Equal(audit.ApprovalApproved, batch.ToolCalls[0].ApprovalState)
	s.Empty(batch.Findings)
	s.Empty(batch.ApprovalUpdates)
}

func (s *projectorSuite) TestHighRiskWithoutApprovalProducesFinding() {
	events := []session.SessionEvent{
		s.toolCall("evt-1", 1, map[string]string{
			audit.MetadataToolCallID:       "call-1",
			audit.MetadataToolName:         "Bash",
			audit.MetadataToolInputSummary: "cat ~/.ssh/id_rsa",
		}),
	}

	batch := audit.Project(s.stream, events)

	s.Require().Len(batch.ToolCalls, 1)
	s.Equal(audit.ApprovalUnknown, batch.ToolCalls[0].ApprovalState)
	s.Equal(audit.LevelHigh, batch.ToolCalls[0].RiskLevel)
	s.Require().Len(batch.Findings, 1)
	finding := batch.Findings[0]
	s.Equal(audit.FindingHighRiskUnapproved, finding.Kind)
	s.Equal(string(audit.LevelHigh), finding.Severity)
	s.Equal([]string{"evt-1"}, finding.EvidenceEventIDs)
	s.Equal("owner", finding.UserID)
	s.Equal("local-team", finding.ScopeID)
}

func (s *projectorSuite) TestLowRiskWithoutApprovalProducesNoFinding() {
	events := []session.SessionEvent{
		s.toolCall("evt-1", 1, map[string]string{
			audit.MetadataToolCallID:       "call-1",
			audit.MetadataToolName:         "Read",
			audit.MetadataToolInputSummary: "read README.md",
		}),
	}

	batch := audit.Project(s.stream, events)

	s.Require().Len(batch.ToolCalls, 1)
	s.Equal(audit.LevelLow, batch.ToolCalls[0].RiskLevel)
	s.Empty(batch.Findings)
}

func (s *projectorSuite) TestApprovalWithoutToolCallEmitsMonotonicUpdate() {
	events := []session.SessionEvent{
		func() session.SessionEvent {
			event := s.event("evt-1", 1, audit.EventTypeToolApproval)
			event.Metadata = map[string]string{
				audit.MetadataToolCallID:       "call-9",
				audit.MetadataApprovalDecision: audit.DecisionAuto,
			}
			return event
		}(),
	}

	batch := audit.Project(s.stream, events)

	s.Empty(batch.ToolCalls)
	s.Require().Len(batch.ApprovalUpdates, 1)
	s.Equal(audit.ApprovalUpdate{CallID: "call-9", State: audit.ApprovalApproved}, batch.ApprovalUpdates[0])
	s.Empty(batch.Findings)
}

func (s *projectorSuite) TestVisibilityUnknownFindingAggregatesPerValue() {
	first := s.event("evt-1", 1, "message")
	first.Visibility = "public"
	second := s.event("evt-2", 2, "message")
	second.Visibility = "public"
	third := s.event("evt-3", 3, "message")
	third.Visibility = "team"

	batch := audit.Project(s.stream, []session.SessionEvent{first, second, third})

	s.Require().Len(batch.Findings, 1)
	finding := batch.Findings[0]
	s.Equal(audit.FindingVisibilityUnknown, finding.Kind)
	s.Equal(string(audit.LevelMedium), finding.Severity)
	s.Equal([]string{"evt-1", "evt-2"}, finding.EvidenceEventIDs)
	s.Equal("agent-1", finding.AgentID)
}

func (s *projectorSuite) TestAllowedVisibilityValuesProduceNoFinding() {
	events := make([]session.SessionEvent, 0, 4)
	for index, visibility := range []string{"", "team", "team_note_eligible", "team_visible"} {
		event := s.event("evt-vis", int64(index+1), "message")
		event.ID = "evt-vis-" + visibility
		event.Visibility = visibility
		events = append(events, event)
	}

	batch := audit.Project(s.stream, events)

	s.Empty(batch.Findings)
}

func (s *projectorSuite) TestAttributionMissingFinding() {
	unnamed := s.event("evt-1", 1, "message")
	unnamed.Actor.UserID = ""
	named := s.event("evt-2", 2, "message")

	batch := audit.Project(s.stream, []session.SessionEvent{unnamed, named})

	s.Require().Len(batch.Findings, 1)
	finding := batch.Findings[0]
	s.Equal(audit.FindingAttributionMissing, finding.Kind)
	s.Equal(string(audit.LevelLow), finding.Severity)
	s.Equal([]string{"evt-1"}, finding.EvidenceEventIDs)
	s.Equal("agent-1", finding.AgentID, "agent attribution is preserved on the finding")
}

func (s *projectorSuite) TestDailyActivityDeltas() {
	dayOne := s.toolCall("evt-1", 1, map[string]string{
		audit.MetadataToolCallID:       "call-1",
		audit.MetadataToolName:         "Bash",
		audit.MetadataToolInputSummary: "rm -rf /tmp/cache",
	})
	dayOneSecond := s.toolCall("evt-2", 2, map[string]string{
		audit.MetadataToolCallID:       "call-2",
		audit.MetadataToolName:         "Read",
		audit.MetadataToolInputSummary: "read README.md",
	})
	dayTwo := s.event("evt-3", 3, "message")
	dayTwo.OccurredAt = time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)

	batch := audit.Project(s.stream, []session.SessionEvent{dayOne, dayOneSecond, dayTwo})

	s.Require().Len(batch.Activity, 2)
	first := batch.Activity[0]
	s.Equal(time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC), first.Day)
	s.Equal(int64(2), first.EventCount)
	s.Equal(int64(2), first.ToolCallCount)
	s.Equal(int64(1), first.HighRiskCount)
	s.Equal([]string{"sess-1"}, first.SessionIDs)
	s.Equal(map[string]int64{"Bash": 1, "Read": 1}, first.ToolBreakdown)
	second := batch.Activity[1]
	s.Equal(time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC), second.Day)
	s.Equal(int64(1), second.EventCount)
	s.Zero(second.ToolCallCount)
	s.Zero(second.HighRiskCount)
}

func (s *projectorSuite) TestEmptyBatchCarriesOnlyTheCursor() {
	batch := audit.Project(s.stream, nil)

	s.Equal(int64(4), batch.Cursor)
	s.Equal("local-team", batch.ScopeID)
	s.Empty(batch.ToolCalls)
	s.Empty(batch.Findings)
	s.Empty(batch.Activity)
	s.Empty(batch.ApprovalUpdates)
}

func (s *projectorSuite) TestLegacyEventsWithoutToolTypesAreTolerated() {
	first := s.event("evt-1", 1, "message")
	first.Metadata = map[string]string{"key": "value"}
	second := s.event("evt-2", 2, "assistant")
	checkpoint := s.event("evt-3", 3, "checkpoint")

	batch := audit.Project(s.stream, []session.SessionEvent{first, second, checkpoint})

	s.Empty(batch.ToolCalls)
	s.Empty(batch.Findings)
	s.Empty(batch.ApprovalUpdates)
	s.Equal(int64(4), batch.Cursor)
	s.Require().Len(batch.Activity, 1)
	s.Equal(int64(3), batch.Activity[0].EventCount)
	s.Zero(batch.Activity[0].ToolCallCount)
}

func (s *projectorSuite) TestToolCallWithoutCallIDIsAuditedButNotCorrelated() {
	events := []session.SessionEvent{
		s.toolCall("evt-1", 1, map[string]string{
			audit.MetadataToolName:         "Bash",
			audit.MetadataToolInputSummary: "git push --force origin main",
		}),
	}

	batch := audit.Project(s.stream, events)

	s.Require().Len(batch.ToolCalls, 1)
	s.Empty(batch.ToolCalls[0].CallID)
	s.Equal(audit.ApprovalUnknown, batch.ToolCalls[0].ApprovalState)
	s.Contains(findingKinds(batch.Findings), audit.FindingHighRiskUnapproved)
}

func findingKinds(findings []audit.Finding) []string {
	kinds := make([]string, 0, len(findings))
	for _, finding := range findings {
		kinds = append(kinds, finding.Kind)
	}
	return kinds
}
