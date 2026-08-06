package handler

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/pax-beehive/pax-nexus/internal/audit"
	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/explorer"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

// overviewWindowSpec pairs a requested window with the bucket size used to
// render its throughput series. Bucket sizes are always server-fixed — the
// client only ever names a window, never a bucket duration.
type overviewWindowSpec struct {
	span   time.Duration
	bucket time.Duration
}

var overviewWindowSpecs = map[string]overviewWindowSpec{
	"1h":  {span: time.Hour, bucket: 10 * time.Minute},
	"24h": {span: 24 * time.Hour, bucket: 3 * time.Hour},
	"7d":  {span: 7 * 24 * time.Hour, bucket: 24 * time.Hour},
}

const (
	overviewDefaultWindow   = "24h"
	overviewExpiringWithin  = 24 * time.Hour
	overviewAttentionLimit  = 20
	overviewFindingsLimit   = 100
	overviewInvitationLimit = 100
	overviewEnrollmentLimit = 100

	overviewEnrollmentStatusExpired = "expired"
)

// GetOverview assembles the Overview landing page's aggregate: operations
// metrics and throughput series, the live team-note mix, and an attention
// queue that surfaces work across four owning contexts (session audit,
// extraction, invitations, agent enrollment). It is read-only and touches no
// write path.
//
// Authorization is the operations capability (view.operations) — identical
// to GetOperationsSummary — and is checked in full before any downstream
// call, so a caller without it never reaches the six concurrent reads below.
//
// Five of those six reads degrade independently: a failing source leaves its
// section empty and logs a warning, so one broken subsystem never blanks the
// whole page. h.operations.Summary is the exception — it supplies the
// metrics body the page is built around, so its failure fails the request.
func (h *Handler) GetOverview(ctx context.Context, c *app.RequestContext) {
	principal, ok := h.authorizeOperations(ctx, c)
	if !ok {
		return
	}
	if !principal.HasCapability(onprem.CapabilityViewOperations) {
		h.writeOperationsError(c, "get overview", onprem.ErrForbidden)
		return
	}

	window := strings.TrimSpace(c.Query("window"))
	if window == "" {
		window = overviewDefaultWindow
	}
	spec, ok := overviewWindowSpecs[window]
	if !ok {
		h.writeOperationsError(c, "get overview", operations.ErrInvalidInput)
		return
	}

	now := time.Now().UTC()
	filter := operations.TimeFilter{From: now.Add(-spec.span), To: now}
	expiringBefore := now.Add(overviewExpiringWithin)

	var (
		wg sync.WaitGroup

		summary    operations.Summary
		summaryErr error

		series                         []operations.SeriesBucket
		noteMix                        []explorer.NoteKindCount
		highFindings, criticalFindings []audit.Finding
		invitations                    []onprem.Invitation
		enrollments                    []onprem.AgentEnrollmentMetadata
	)

	wg.Add(6)
	go func() {
		defer wg.Done()
		summary, summaryErr = h.operations.Summary(ctx, principal, filter)
	}()
	go func() {
		defer wg.Done()
		result, err := h.operations.Series(ctx, principal, filter, spec.bucket)
		if err != nil {
			h.logger.Warn("overview series degraded", "error", err)
			return
		}
		series = result
	}()
	go func() {
		defer wg.Done()
		if h.explorer == nil {
			return
		}
		result, err := h.explorer.NoteMix(ctx, principal, now)
		if err != nil {
			h.logger.Warn("overview note mix degraded", "error", err)
			return
		}
		noteMix = result
	}()
	go func() {
		defer wg.Done()
		if h.sessionAudit == nil {
			return
		}
		high, err := h.sessionAudit.ListFindings(ctx, audit.FindingFilter{
			ScopeID: principal.ScopeID, Severity: string(audit.LevelHigh), Limit: overviewFindingsLimit,
		})
		if err != nil {
			h.logger.Warn("overview findings degraded", "severity", "high", "error", err)
			return
		}
		critical, err := h.sessionAudit.ListFindings(ctx, audit.FindingFilter{
			ScopeID: principal.ScopeID, Severity: string(audit.LevelCritical), Limit: overviewFindingsLimit,
		})
		if err != nil {
			h.logger.Warn("overview findings degraded", "severity", "critical", "error", err)
			return
		}
		highFindings, criticalFindings = high, critical
	}()
	go func() {
		defer wg.Done()
		if h.identity == nil {
			return
		}
		result, err := h.identity.ListInvitations(ctx, principal, onprem.InvitationFilter{
			Status: onprem.InvitationStatusPending, Limit: overviewInvitationLimit,
		})
		if err != nil {
			h.logger.Warn("overview invitations degraded", "error", err)
			return
		}
		invitations = result
	}()
	go func() {
		defer wg.Done()
		if h.registry == nil {
			return
		}
		result, err := h.registry.ListExpiringEnrollments(ctx, principal, expiringBefore, overviewEnrollmentLimit)
		if err != nil {
			h.logger.Warn("overview enrollments degraded", "error", err)
			return
		}
		enrollments = result
	}()
	wg.Wait()

	if summaryErr != nil {
		h.writeOperationsError(c, "get overview", summaryErr)
		return
	}

	attention, attentionTotal := overviewAttention(
		now, expiringBefore, summary, highFindings, criticalFindings, invitations, enrollments,
	)
	if len(attention) > overviewAttentionLimit {
		attention = attention[:overviewAttentionLimit]
	}

	c.JSON(consts.StatusOK, overviewResponseToAPI(summary, series, noteMix, attention, attentionTotal))
}

// overviewAttentionEntry is the handler's internal shape for one attention
// item; At is the sort key (never rendered) — see overviewAttention for what
// each source uses as its "when".
type overviewAttentionEntry struct {
	Kind, Severity, Title, Body, Ref, Target string
	At                                       time.Time
}

// overviewSeverityRank orders attention items most-urgent first. It reuses
// the session-audit risk vocabulary (low/medium/high/critical) as the single
// severity scale across all four attention sources, so the sort is coherent
// even though the sources otherwise share nothing.
var overviewSeverityRank = map[string]int{
	string(audit.LevelCritical): 3,
	string(audit.LevelHigh):     2,
	string(audit.LevelMedium):   1,
	string(audit.LevelLow):      0,
}

// overviewAttention builds the merged attention queue from the four owning
// sources and sorts it by severity (descending), then by At (ascending —
// the most imminent/most recent item within a severity band leads). It
// returns the full sorted list and its length; the caller truncates to
// overviewAttentionLimit and uses the pre-truncation length for
// metrics.attention_count.
func overviewAttention(
	now, expiringBefore time.Time,
	summary operations.Summary,
	highFindings, criticalFindings []audit.Finding,
	invitations []onprem.Invitation,
	enrollments []onprem.AgentEnrollmentMetadata,
) ([]overviewAttentionEntry, int) {
	var entries []overviewAttentionEntry

	for _, finding := range highFindings {
		entries = append(entries, findingAttentionEntry(finding))
	}
	for _, finding := range criticalFindings {
		entries = append(entries, findingAttentionEntry(finding))
	}
	if summary.Extraction.Quarantined > 0 {
		entries = append(entries, overviewAttentionEntry{
			Kind: "quarantine", Severity: string(audit.LevelHigh),
			Title: "Quarantined extractions need review",
			Body: fmt.Sprintf(
				"%d extraction candidate(s) are quarantined and waiting for review", summary.Extraction.Quarantined,
			),
			Ref: "quarantine", Target: "/governance/pipeline", At: now,
		})
	}
	for _, invitation := range invitations {
		if invitation.Status != onprem.InvitationStatusPending || invitation.ExpiresAt.After(expiringBefore) {
			continue
		}
		entries = append(entries, overviewAttentionEntry{
			Kind: "invitation", Severity: string(audit.LevelMedium),
			Title:  "Invitation expiring soon",
			Body:   fmt.Sprintf("Invitation to %s expires %s", invitation.TargetEmail, invitation.ExpiresAt.Format(time.RFC3339)),
			Ref:    "invitation:" + invitation.InvitationID,
			Target: "/management/invitations", At: invitation.ExpiresAt,
		})
	}
	for _, enrollment := range enrollments {
		entries = append(entries, enrollmentAttentionEntry(enrollment))
	}

	sort.SliceStable(entries, func(i, j int) bool {
		rankI, rankJ := overviewSeverityRank[entries[i].Severity], overviewSeverityRank[entries[j].Severity]
		if rankI != rankJ {
			return rankI > rankJ
		}
		return entries[i].At.Before(entries[j].At)
	})

	return entries, len(entries)
}

func findingAttentionEntry(finding audit.Finding) overviewAttentionEntry {
	return overviewAttentionEntry{
		Kind: "finding", Severity: finding.Severity, Title: findingAttentionTitle(finding.Kind),
		Body: finding.Summary, Ref: fmt.Sprintf("finding:%d", finding.FindingID),
		Target: "/governance/sessions", At: finding.CreatedAt,
	}
}

func findingAttentionTitle(kind string) string {
	switch kind {
	case audit.FindingHighRiskUnapproved:
		return "High-risk tool call without approval"
	case audit.FindingDeniedToolExecuted:
		return "Denied tool call executed anyway"
	case audit.FindingVisibilityUnknown:
		return "Session visibility unknown"
	case audit.FindingAttributionMissing:
		return "Tool call missing attribution"
	default:
		return "Session audit finding"
	}
}

// enrollmentAttentionEntry renders both statuses ListExpiringEnrollments can
// return: "pending" (still within its window, will expire soon) gets medium
// severity, while "expired" (the one-time token's deadline has already
// passed, unconsumed) gets high — it already failed, not merely approaching,
// so it outranks a merely-soon-to-expire pending one in the sorted queue.
func enrollmentAttentionEntry(enrollment onprem.AgentEnrollmentMetadata) overviewAttentionEntry {
	severity, title := string(audit.LevelMedium), "Agent enrollment expiring soon"
	if enrollment.Status == overviewEnrollmentStatusExpired {
		severity, title = string(audit.LevelHigh), "Agent enrollment token expired unclaimed"
	}
	return overviewAttentionEntry{
		Kind: "enrollment", Severity: severity, Title: title,
		Body: fmt.Sprintf("Enrollment for agent %s expires %s", enrollment.AgentID, enrollment.ExpiresAt.Format(time.RFC3339)),
		Ref:  "enrollment:" + enrollment.EnrollmentID, Target: "/management/agents/" + enrollment.AgentID,
		At: enrollment.ExpiresAt,
	}
}

func overviewResponseToAPI(
	summary operations.Summary,
	series []operations.SeriesBucket,
	noteMix []explorer.NoteKindCount,
	attention []overviewAttentionEntry,
	attentionTotal int,
) *api.OverviewResponse {
	var liveNotes int64
	for _, entry := range noteMix {
		liveNotes += entry.Count
	}

	var acceptRate float64
	if summary.Recalls.Requests > 0 {
		acceptRate = float64(summary.Recalls.WithEvidence) / float64(summary.Recalls.Requests)
	}

	return &api.OverviewResponse{
		FromTime: summary.From.Format(time.RFC3339Nano), ToTime: summary.To.Format(time.RFC3339Nano),
		GeneratedAt: summary.GeneratedAt.Format(time.RFC3339Nano),
		Metrics: &api.OverviewMetrics{
			EvidenceCaptured: summary.Observations.EventsWritten, LiveNotes: liveNotes,
			// notes_expiring_today has no owning source among the six
			// downstream reads this endpoint assembles (NoteMix reports live
			// counts by kind at an instant, not a same-day expiry cohort).
			// Left at zero until a dedicated source exists.
			NotesExpiringToday: 0,
			RecallsServed:      summary.Recalls.Succeeded, RecallAcceptRate: acceptRate,
			P50Ms: summary.Latency.P50MS, P95Ms: summary.Latency.P95MS,
			AttentionCount: int64(attentionTotal),
		},
		Series:    overviewSeriesToAPI(series),
		NoteMix:   overviewNoteMixToAPI(noteMix, liveNotes),
		Attention: overviewAttentionToAPI(attention),
	}
}

func overviewSeriesToAPI(series []operations.SeriesBucket) []*api.OverviewSeriesPoint {
	result := make([]*api.OverviewSeriesPoint, 0, len(series))
	for _, bucket := range series {
		result = append(result, &api.OverviewSeriesPoint{
			BucketAt: bucket.BucketAt.UTC().Format(time.RFC3339Nano),
			Evidence: bucket.Evidence, Facts: bucket.Facts, Recalls: bucket.Recalls,
		})
	}
	return result
}

func overviewNoteMixToAPI(mix []explorer.NoteKindCount, total int64) []*api.OverviewNoteMixEntry {
	result := make([]*api.OverviewNoteMixEntry, 0, len(mix))
	for _, entry := range mix {
		var pct float64
		if total > 0 {
			pct = float64(entry.Count) / float64(total) * 100
		}
		result = append(result, &api.OverviewNoteMixEntry{Kind: entry.Kind, Count: entry.Count, Pct: pct})
	}
	return result
}

func overviewAttentionToAPI(entries []overviewAttentionEntry) []*api.OverviewAttentionItem {
	result := make([]*api.OverviewAttentionItem, 0, len(entries))
	for _, entry := range entries {
		result = append(result, &api.OverviewAttentionItem{
			Kind: entry.Kind, Severity: entry.Severity, Title: entry.Title,
			Body: entry.Body, Ref: entry.Ref, Target: entry.Target,
		})
	}
	return result
}
