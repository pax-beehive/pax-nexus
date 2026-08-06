package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/audit"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/explorer"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/mocks"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/router"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

type overviewHandlerSuite struct {
	suite.Suite
	controller   *gomock.Controller
	identity     *humanIdentityService
	operations   *operationsLifecycle
	explorer     *explorerLifecycle
	registry     *agentRegistryService
	sessionAudit *overviewSessionAudit
	logs         *bytes.Buffer
	handler      *handler.Handler
}

func TestGetOverviewHandlerSuite(t *testing.T) {
	suite.Run(t, new(overviewHandlerSuite))
}

func (s *overviewHandlerSuite) SetupTest() {
	s.controller = gomock.NewController(s.T())
	s.identity = &humanIdentityService{principal: onprem.HumanPrincipal{
		UserID: "admin-user", MembershipID: "admin-membership", Role: onprem.RoleAdmin,
		MembershipStatus: onprem.MembershipStatusActive,
	}}
	s.operations = &operationsLifecycle{now: time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)}
	s.explorer = &explorerLifecycle{now: s.operations.now}
	s.registry = &agentRegistryService{}
	s.sessionAudit = &overviewSessionAudit{}
	// logs captures every record at Debug and above (instead of discarding
	// them) so tests can assert on log level/content — in particular that a
	// role-forbidden degraded source stays at Debug while a genuine failure
	// on the same source still reaches Warn.
	s.logs = &bytes.Buffer{}
	s.handler = s.newHandler()
}

func (s *overviewHandlerSuite) newHandler() *handler.Handler {
	configured, err := handler.NewOnPrem(
		mocks.NewMockRuntime(s.controller), &credentialService{}, &memoryService{}, &channelService{},
		slog.New(slog.NewTextHandler(s.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		handler.WithAgentRegistry(s.registry),
		handler.WithHumanIdentity(s.identity, &oidcService{}, "/portal", false),
		handler.WithOperations(s.operations, &operationsRecorder{}),
		handler.WithExplorer(s.explorer),
		handler.WithSessionAudit(s.sessionAudit),
	)
	s.Require().NoError(err)
	return configured
}

func (s *overviewHandlerSuite) perform(path string) *ut.ResponseRecorder {
	hertz := server.New()
	hertz.Use(handler.InstanceMiddleware(s.handler))
	router.GeneratedRegister(hertz)
	return ut.PerformRequest(hertz.Engine, http.MethodGet, path, nil,
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Cookie", Value: "tm_human_session=session"})
}

func (s *overviewHandlerSuite) decode(response *ut.ResponseRecorder) api.OverviewResponse {
	s.T().Helper()
	var body api.OverviewResponse
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
	return body
}

// TestWindowDeterminesBucketCount pins the server-fixed bucket sizing: 1h has
// 6 ten-minute buckets, 24h has 8 three-hour buckets, 7d has 7 one-day
// buckets. An unrecognized window must 400, not silently fall back to the
// 24h default.
func (s *overviewHandlerSuite) TestWindowDeterminesBucketCount() {
	for _, test := range []struct {
		path    string
		buckets int
	}{
		{path: "/v1/admin/overview?window=1h", buckets: 6},
		{path: "/v1/admin/overview?window=24h", buckets: 8},
		{path: "/v1/admin/overview?window=7d", buckets: 7},
		{path: "/v1/admin/overview", buckets: 8}, // default window is 24h
	} {
		s.Run(test.path, func() {
			response := s.perform(test.path)
			s.Require().Equal(consts.StatusOK, response.Code)
			body := s.decode(response)
			s.Len(body.Series, test.buckets)
		})
	}

	invalid := s.perform("/v1/admin/overview?window=30m")
	s.Equal(consts.StatusBadRequest, invalid.Code)
	s.JSONEq(`{"code":"invalid_request","message":"the request is invalid"}`, invalid.Body.String())
}

// TestSingleSourceFailureDegradesOnlyThatSection is the endpoint's core
// contract: Overview is a landing page, so one broken source must not blank
// the whole thing. Here session-audit findings fail; the response is still
// 200, the attention queue carries no "finding" entries, and every other
// section (series, note mix) is unaffected.
func (s *overviewHandlerSuite) TestSingleSourceFailureDegradesOnlyThatSection() {
	s.explorer.noteMixResult = []explorer.NoteKindCount{{Kind: "decision", Count: 3}}
	s.explorer.countExpiringResult = 7
	s.sessionAudit.err = errors.New("audit projection unavailable")

	response := s.perform("/v1/admin/overview?window=1h")

	s.Equal(consts.StatusOK, response.Code)
	body := s.decode(response)
	s.Len(body.Series, 6, "the series section must stay intact when findings fail")
	s.Require().Len(body.NoteMix, 1, "the note mix section must stay intact when findings fail")
	s.Equal(int64(3), body.NoteMix[0].Count)
	s.Equal(int64(7), body.Metrics.NotesExpiringToday, "the expiring-notes count must reflect the source's fixture value")
	s.Equal(24*time.Hour, s.explorer.countExpiringWithin, "the expiring window must be 24h, matching the attention threshold")
	for _, item := range body.Attention {
		s.NotEqual("finding", item.Kind, "a failed findings source must not surface any finding attention item")
	}
	s.Positive(s.sessionAudit.calls, "the findings source must actually have been attempted")
}

// TestAttentionOrderingAndTruncation mixes all four attention sources (25
// items total), asserts the response caps at 20, that severity ranks are
// non-increasing across the truncated list, and that attention_count in
// metrics reports the pre-truncation total (25), not 20.
func (s *overviewHandlerSuite) TestAttentionOrderingAndTruncation() {
	base := s.operations.now

	// findingKinds cycles through every known finding kind plus one unknown
	// value, exercising every branch of the title-rendering switch (default
	// included) rather than just one repeated kind.
	findingKinds := []string{
		audit.FindingHighRiskUnapproved, audit.FindingDeniedToolExecuted,
		audit.FindingVisibilityUnknown, audit.FindingAttributionMissing, "unrecognized_kind",
	}

	criticalFindings := make([]audit.Finding, 0, 5)
	highFindings := make([]audit.Finding, 0, 5)
	for i := 0; i < 5; i++ {
		criticalFindings = append(criticalFindings, audit.Finding{
			FindingID: int64(i + 1), Kind: findingKinds[i], Severity: string(audit.LevelCritical),
			Summary: "critical finding", CreatedAt: base.Add(-time.Duration(i) * time.Minute),
		})
		highFindings = append(highFindings, audit.Finding{
			FindingID: int64(i + 100), Kind: findingKinds[i], Severity: string(audit.LevelHigh),
			Summary: "high finding", CreatedAt: base.Add(-time.Duration(i) * time.Minute),
		})
	}
	s.sessionAudit.byLevel = map[string][]audit.Finding{
		string(audit.LevelCritical): criticalFindings,
		string(audit.LevelHigh):     highFindings,
	}

	s.operations.summaryQuarantined = 3 // one aggregate "quarantine" attention item

	invitations := make([]onprem.Invitation, 0, 5)
	for i := 0; i < 5; i++ {
		invitations = append(invitations, onprem.Invitation{
			InvitationID: "invitation-" + string(rune('a'+i)), TargetEmail: "invitee@example.com",
			Status: onprem.InvitationStatusPending, CreatedAt: base, ExpiresAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	s.identity.listInvitationsResult = invitations

	enrollments := make([]onprem.AgentEnrollmentMetadata, 0, 9)
	for i := 0; i < 9; i++ {
		status := "pending"
		if i%2 == 0 {
			status = "expired"
		}
		enrollments = append(enrollments, onprem.AgentEnrollmentMetadata{
			EnrollmentID: "enrollment-" + string(rune('a'+i)), AgentID: "agent-" + string(rune('a'+i)),
			Status: status, ExpiresAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	s.registry.expiringEnrollmentsResult = enrollments

	response := s.perform("/v1/admin/overview?window=24h")

	s.Equal(consts.StatusOK, response.Code)
	body := s.decode(response)
	s.Require().Len(body.Attention, 20, "the attention queue must truncate to 20")
	s.Equal(int64(25), body.Metrics.AttentionCount, "attention_count must be the pre-truncation total")

	ranks := map[string]int{"critical": 3, "high": 2, "medium": 1, "low": 0}
	for i := 1; i < len(body.Attention); i++ {
		previous, current := ranks[body.Attention[i-1].Severity], ranks[body.Attention[i].Severity]
		s.GreaterOrEqual(previous, current, "attention items must be sorted by non-increasing severity")
	}
}

// TestForbiddenWithoutOperationsCapabilityMakesNoDownstreamCalls covers the
// endpoint's authorization gate: a member (no view.operations capability)
// gets 403, and — because the capability check happens before any of the six
// concurrent reads fire — none of the five other sources are ever called.
func (s *overviewHandlerSuite) TestForbiddenWithoutOperationsCapabilityMakesNoDownstreamCalls() {
	s.identity.principal.Role = onprem.RoleMember

	response := s.perform("/v1/admin/overview")

	s.Equal(consts.StatusForbidden, response.Code)
	s.Contains(response.Body.String(), `"code":"forbidden"`)
	s.Zero(s.operations.summaryCalls)
	s.Zero(s.operations.seriesCalls)
	s.Zero(s.explorer.noteMixCalls)
	s.Zero(s.explorer.countExpiringCalls)
	s.Zero(s.sessionAudit.calls)
	s.Zero(s.identity.listInvitationsCalls)
	s.Zero(s.registry.expiringEnrollmentsCalls)
}

// TestSummaryFailureFailsTheWholeRequest locks in the one exception to
// per-source degradation: operations.Summary supplies the metrics body the
// page is built around, so unlike every other source, its failure fails the
// whole request instead of leaving a section empty.
func (s *overviewHandlerSuite) TestSummaryFailureFailsTheWholeRequest() {
	s.operations.summaryErr = errors.New("operations store unavailable")

	response := s.perform("/v1/admin/overview")

	s.Equal(consts.StatusInternalServerError, response.Code)
}

// TestNoteMixForbiddenForRoleDegradesQuietly pins the fix for the
// per-Admin-request Warn noise: NoteMix requires CapabilityViewTeamMemory
// (Owner only), stricter than GetOverview's own view.operations gate
// (Owner+Admin), so an Admin principal predictably degrades that section on
// every request. That is an expected authorization outcome, not a source
// failure — the response must still be 200 with an empty note_mix, but the
// degradation must not log at Warn (that would drown real outages in
// routine per-Admin-request noise). It may still log at Debug.
func (s *overviewHandlerSuite) TestNoteMixForbiddenForRoleDegradesQuietly() {
	s.explorer.noteMixErr = onprem.ErrForbidden

	response := s.perform("/v1/admin/overview")

	s.Equal(consts.StatusOK, response.Code)
	body := s.decode(response)
	s.Empty(body.NoteMix, "a role-forbidden note mix must degrade to an empty section, not fail the request")
	s.NotContains(s.logs.String(), "level=WARN", "a role-forbidden source must never log at Warn")
	s.Contains(
		s.logs.String(), "overview note mix degraded",
		"the degradation should still be observable, just at Debug rather than Warn",
	)
}

// TestNoteMixGenuineFailureStillWarns is the other half of the assertion
// above: a real source failure (not onprem.ErrForbidden) on the very same
// NoteMix call must still log at Warn, so operators are not blinded to
// actual outages by the ErrForbidden quieting.
func (s *overviewHandlerSuite) TestNoteMixGenuineFailureStillWarns() {
	s.explorer.noteMixErr = errors.New("note mix store unavailable")

	response := s.perform("/v1/admin/overview")

	s.Equal(consts.StatusOK, response.Code)
	body := s.decode(response)
	s.Empty(body.NoteMix, "a failed note mix source must still degrade to an empty section")
	s.Contains(s.logs.String(), "level=WARN", "a genuine note mix failure must log at Warn")
	s.Contains(s.logs.String(), "overview note mix degraded")
}

// TestNoteMixFailureSkipsExpiringCountEntirely pins the "one degradation
// unit" invariant from the other direction of TestNoteMixGenuineFailureStillWarns:
// note mix and the expiring-soon count share one "at" instant and degrade
// together, so when NoteMix itself fails, CountExpiringNotes must never even
// be attempted (not just discarded) and the tile must read zero, not some
// stale or partial value.
func (s *overviewHandlerSuite) TestNoteMixFailureSkipsExpiringCountEntirely() {
	s.explorer.noteMixErr = errors.New("note mix store unavailable")

	response := s.perform("/v1/admin/overview")

	s.Equal(consts.StatusOK, response.Code)
	body := s.decode(response)
	s.Empty(body.NoteMix, "a failed note mix source must still degrade to an empty section")
	s.Zero(s.explorer.countExpiringCalls, "the expiring count must never be attempted when note mix itself fails")
	s.Equal(int64(0), body.Metrics.NotesExpiringToday, "the expiring tile must read zero when its sibling read never ran")
}

// TestExpiringCountFailureDegradesQuietlyWhileNoteMixStays is the other half
// of the degradation unit: when NoteMix succeeds but CountExpiringNotes
// alone fails, the note mix section must stay populated (it already
// succeeded) while only the expiring-soon tile degrades to zero, and the
// failure is observable via the dedicated degraded-log message.
func (s *overviewHandlerSuite) TestExpiringCountFailureDegradesQuietlyWhileNoteMixStays() {
	s.explorer.noteMixResult = []explorer.NoteKindCount{{Kind: "decision", Count: 2}}
	s.explorer.countExpiringErr = errors.New("expiring count store unavailable")

	response := s.perform("/v1/admin/overview")

	s.Equal(consts.StatusOK, response.Code)
	body := s.decode(response)
	s.Require().Len(body.NoteMix, 1, "the note mix section must stay intact when only the expiring count fails")
	s.Equal(int64(2), body.NoteMix[0].Count)
	s.Equal(int64(0), body.Metrics.NotesExpiringToday, "the expiring tile must degrade to zero on its own failure")
	s.Contains(
		s.logs.String(), "overview expiring-notes count degraded",
		"the expiring-count failure must be observable via its own degraded-log message",
	)
}

// overviewSessionAudit is a minimal SessionAuditQuery fake for the Overview
// suite: only ListFindings matters to the endpoint, byLevel lets tests supply
// distinct results per severity filter, and calls counts invocations so the
// 403 test can assert the source was never reached.
type overviewSessionAudit struct {
	calls   int
	err     error
	byLevel map[string][]audit.Finding
}

func (s *overviewSessionAudit) ListToolCalls(context.Context, audit.ToolCallFilter) ([]audit.ToolCall, error) {
	return nil, nil
}

func (s *overviewSessionAudit) ListFindings(_ context.Context, filter audit.FindingFilter) ([]audit.Finding, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.byLevel[filter.Severity], nil
}

func (s *overviewSessionAudit) GetActivityDaily(context.Context, audit.ActivityFilter) ([]audit.ActivityDaily, error) {
	return nil, nil
}
