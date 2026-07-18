package teamnote

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	GeneralRecallV3PlanVersion    = "general-recall-v3"
	GeneralRecallV3ScoringVersion = "evidence-scorecard-v1-uncalibrated"
)

// RecallMode is the temporal interpretation compiled from a recall query.
type RecallMode string

const (
	RecallModeCurrent      RecallMode = "current"
	RecallModeAsOf         RecallMode = "as_of"
	RecallModeChangesSince RecallMode = "changes_since"
	RecallModeHistory      RecallMode = "history"
	RecallModeDiscover     RecallMode = "discover"
)

// RecallIntent is the inspectable query representation used by General Recall v3.
type RecallIntent struct {
	Mode           RecallMode `json:"mode"`
	RequestedFacts []string   `json:"requested_facts,omitempty"`
	Entities       []string   `json:"entities,omitempty"`
	TaskRef        string     `json:"task_ref,omitempty"`
	ThreadRef      string     `json:"thread_ref,omitempty"`
	ValidAt        *time.Time `json:"valid_at,omitempty"`
	ChangedSince   *time.Time `json:"changed_since,omitempty"`
	RelationBudget int        `json:"relation_budget"`
	TokenBudget    int        `json:"token_budget"`
}

// RecallLane identifies one inspectable candidate retrieval path.
type RecallLane string

const (
	RecallLaneExactScope       RecallLane = "exact_scope"
	RecallLaneLexical          RecallLane = "lexical"
	RecallLaneTemporal         RecallLane = "temporal"
	RecallLaneRelation         RecallLane = "relation"
	RecallLaneCoordination     RecallLane = "coordination"
	RecallLaneAgentRouting     RecallLane = "agent_routing"
	RecallLaneSemanticFallback RecallLane = "semantic_fallback"
)

// RecallDisposition is the planner's final treatment of a candidate.
type RecallDisposition string

const (
	RecallDispositionEvidence RecallDisposition = "evidence"
	RecallDispositionSuppress RecallDisposition = "suppress"
)

// RecallHardGateResult records a hard constraint checked by the planner or an adapter.
type RecallHardGateResult struct {
	Gate   string `json:"gate"`
	Passed bool   `json:"passed"`
	Reason string `json:"reason,omitempty"`
}

// RecallTemporalResolution records the time interpretation applied to a candidate.
type RecallTemporalResolution struct {
	Mode                 RecallMode `json:"mode"`
	QueryTime            *time.Time `json:"query_time,omitempty"`
	GatePassed           bool       `json:"gate_passed"`
	ValidAtQueryTime     *bool      `json:"valid_at_query_time,omitempty"`
	ChangedAfterBoundary *bool      `json:"changed_after_boundary,omitempty"`
}

// RecallScoreContribution is one monotonic, inspectable evidence score feature.
type RecallScoreContribution struct {
	Feature string  `json:"feature"`
	Points  float64 `json:"points"`
}

// RecallCandidateTrace explains retrieval, gating, scoring, and disposition.
type RecallCandidateTrace struct {
	NoteID             string                    `json:"note_id"`
	RetrievalLanes     []RecallLane              `json:"retrieval_lanes,omitempty"`
	RetrievalReasons   []string                  `json:"retrieval_reasons,omitempty"`
	MatchedTerms       []string                  `json:"matched_terms,omitempty"`
	RelationPath       []string                  `json:"relation_path,omitempty"`
	HardGateResults    []RecallHardGateResult    `json:"hard_gate_results"`
	TemporalResolution RecallTemporalResolution  `json:"temporal_resolution"`
	ScoreContributions []RecallScoreContribution `json:"score_contributions,omitempty"`
	EvidenceConfidence float64                   `json:"evidence_confidence"`
	RoutingAffinity    int                       `json:"routing_affinity"`
	Disposition        RecallDisposition         `json:"disposition"`
	RejectionReason    RecallRejectReason        `json:"rejection_reason,omitempty"`
}

var recallDatePattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}(?:T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z)?`)

func compileRecallIntent(request RecallRequest) RecallIntent {
	query := strings.ToLower(request.Query)
	intent := RecallIntent{
		Mode: RecallModeCurrent, TaskRef: request.TaskRef, ThreadRef: request.ThreadRef,
		RelationBudget: 1, TokenBudget: request.TokenBudget,
	}
	intent.RequestedFacts = requestedRecallFacts(query)
	intent.Entities = nonEmptyRecallEntities(request.TaskRef, request.ThreadRef)
	boundary := recallTimeBoundary(query)
	switch {
	case strings.Contains(query, "since") && containsAny(query, "change", "changed", "update", "updated"):
		intent.Mode = RecallModeChangesSince
		intent.ChangedSince = boundary
	case containsAny(query, "as of", "as_of"):
		intent.Mode = RecallModeAsOf
		intent.ValidAt = boundary
	case containsAny(query, "history", "historical", "revision chain"):
		intent.Mode = RecallModeHistory
	case containsAny(query, "discover", "find an agent", "who can help"):
		intent.Mode = RecallModeDiscover
	}
	return intent
}

func requestedRecallFacts(query string) []string {
	type factRule struct {
		fact  string
		terms []string
	}
	rules := []factRule{
		{fact: "decision", terms: []string{"decision", "decided", "approved", "approval"}},
		{fact: "owner", terms: []string{"owner", "owns", "responsible", "who"}},
		{fact: "blocker", terms: []string{"blocker", "blocked", "waiting on"}},
		{fact: "handoff", terms: []string{"handoff", "handed off", "delegate", "assigned"}},
		{fact: "artifact", terms: []string{"artifact", "document", "report", "pull request", " pr "}},
		{fact: "schedule", terms: []string{"deadline", "date", "when", "time"}},
		{fact: "status", terms: []string{"status", "state", "current", "latest", "progress"}},
	}
	result := make([]string, 0, len(rules))
	for _, rule := range rules {
		if containsAny(query, rule.terms...) {
			result = append(result, rule.fact)
		}
	}
	if len(result) == 0 {
		result = append(result, "status")
	}
	return result
}

func nonEmptyRecallEntities(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}

func recallTimeBoundary(query string) *time.Time {
	value := recallDatePattern.FindString(query)
	if value == "" {
		return nil
	}
	formats := []string{time.RFC3339Nano, "2006-01-02"}
	for _, format := range formats {
		parsed, err := time.Parse(format, value)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}

func temporalGate(candidate RecallCandidate, intent RecallIntent) (bool, *time.Time) {
	switch intent.Mode {
	case RecallModeChangesSince:
		if intent.ChangedSince == nil {
			return true, nil
		}
		changedAt := candidate.UpdatedAt
		if candidate.SourceOccurredAt.After(changedAt) {
			changedAt = candidate.SourceOccurredAt
		}
		return !changedAt.Before(*intent.ChangedSince), intent.ChangedSince
	case RecallModeAsOf:
		if intent.ValidAt == nil {
			return true, nil
		}
		if candidate.SourceOccurredAt.After(*intent.ValidAt) {
			return false, intent.ValidAt
		}
		if candidate.InvalidAt != nil && !candidate.InvalidAt.After(*intent.ValidAt) {
			return false, intent.ValidAt
		}
		return true, intent.ValidAt
	case RecallModeCurrent:
		if candidate.InvalidAt != nil {
			return false, nil
		}
		return true, nil
	default:
		return true, nil
	}
}

func evaluateRecallCandidate(candidate RecallCandidate, request RecallRequest, intent RecallIntent) RecallCandidateTrace {
	temporalPassed, queryTime := temporalGate(candidate, intent)
	matchedTerms := matchedRecallTerms(candidate.Note, request.Query)
	contributions := recallScorecard(candidate.Note, request, intent, matchedTerms, temporalPassed)
	total := 0.0
	for _, contribution := range contributions {
		total += contribution.Points
	}
	trace := RecallCandidateTrace{
		NoteID: candidate.ID, MatchedTerms: matchedTerms,
		HardGateResults:    precheckedHardGates(temporalPassed),
		TemporalResolution: temporalRecallResolution(intent.Mode, queryTime, temporalPassed),
		ScoreContributions: contributions, EvidenceConfidence: math.Min(total/100, 1),
		RoutingAffinity: recallRoutingAffinity(candidate.Note, request, intent),
		Disposition:     RecallDispositionSuppress,
	}
	trace.RetrievalLanes, trace.RetrievalReasons = recallCandidateLanes(candidate, request, intent, matchedTerms)
	return trace
}

func temporalRecallResolution(mode RecallMode, queryTime *time.Time, passed bool) RecallTemporalResolution {
	resolution := RecallTemporalResolution{Mode: mode, QueryTime: queryTime, GatePassed: passed}
	switch mode {
	case RecallModeChangesSince:
		resolution.ChangedAfterBoundary = recallBoolPointer(passed)
	case RecallModeCurrent, RecallModeAsOf:
		resolution.ValidAtQueryTime = recallBoolPointer(passed)
	}
	return resolution
}

func recallBoolPointer(value bool) *bool {
	return &value
}

func precheckedHardGates(temporalPassed bool) []RecallHardGateResult {
	return []RecallHardGateResult{
		{Gate: "scope", Passed: true, Reason: "adapter_prechecked"},
		{Gate: "authorization_audience", Passed: true, Reason: "adapter_prechecked"},
		{Gate: "task_thread", Passed: true, Reason: "adapter_prechecked"},
		{Gate: "active_state", Passed: true, Reason: "adapter_prechecked"},
		{Gate: "temporal", Passed: temporalPassed},
		{Gate: "source_provenance", Passed: true, Reason: "adapter_prechecked"},
		{Gate: "delivery_eligibility", Passed: true, Reason: "adapter_prechecked"},
	}
}

func recallCandidateLanes(candidate RecallCandidate, request RecallRequest, intent RecallIntent, matchedTerms []string) ([]RecallLane, []string) {
	lanes := []RecallLane{RecallLaneTemporal}
	reasons := []string{"temporal_mode:" + string(intent.Mode)}
	if exactScopeMatch(candidate.Note, request) {
		lanes = append(lanes, RecallLaneExactScope)
		reasons = append(reasons, "exact_scope_match")
	}
	if candidate.LexicalScore > 0 || len(matchedTerms) > 0 {
		lanes = append(lanes, RecallLaneLexical)
		reasons = append(reasons, "lexical_terms:"+strings.Join(matchedTerms, ","))
	}
	if coordinationMatch(candidate.Note, intent) > 0 {
		lanes = append(lanes, RecallLaneCoordination)
		reasons = append(reasons, "coordination_fact_match")
	}
	if recallRoutingAffinity(candidate.Note, request, intent) > 0 {
		lanes = append(lanes, RecallLaneAgentRouting)
		reasons = append(reasons, "current_responsibility_or_source")
	}
	return uniqueRecallLanes(lanes), reasons
}

func matchedRecallTerms(note Note, query string) []string {
	queryTerms := searchableTerms(query)
	noteTerms := searchableTerms(note.Subject + " " + note.Body)
	result := make([]string, 0, len(queryTerms))
	for term := range queryTerms {
		if _, ok := noteTerms[term]; ok {
			result = append(result, term)
		}
	}
	sort.Strings(result)
	return result
}

func recallScorecard(note Note, request RecallRequest, intent RecallIntent, matchedTerms []string, temporalPassed bool) []RecallScoreContribution {
	contributions := make([]RecallScoreContribution, 0, 6)
	if exactScopeMatch(note, request) {
		contributions = append(contributions, RecallScoreContribution{Feature: "exact_scope_or_entity_match", Points: 15})
	}
	if coverage := requiredFactCoverage(note, intent); coverage > 0 {
		contributions = append(contributions, RecallScoreContribution{Feature: "required_fact_match", Points: math.Min(float64(coverage)*20, 40)})
	}
	queryTerms := searchableTerms(request.Query)
	if len(queryTerms) > 0 && len(matchedTerms) > 0 {
		points := 25 * float64(len(matchedTerms)) / float64(len(queryTerms))
		contributions = append(contributions, RecallScoreContribution{Feature: "lexical_coverage", Points: points})
	}
	if temporalPassed {
		contributions = append(contributions, RecallScoreContribution{Feature: "temporal_validity", Points: 15})
	}
	if len(note.EvidenceEventIDs) > 0 {
		contributions = append(contributions, RecallScoreContribution{Feature: "source_evidence", Points: 10})
	}
	if coordinationMatch(note, intent) > 0 {
		contributions = append(contributions, RecallScoreContribution{Feature: "coordination_relevance", Points: 10})
	}
	return contributions
}

func requiredFactCoverage(note Note, intent RecallIntent) int {
	coverage := 0
	text := strings.ToLower(note.Subject + " " + note.Body)
	for _, fact := range intent.RequestedFacts {
		switch fact {
		case "status":
			if note.Kind == KindStatus {
				coverage++
			}
		case "blocker":
			if note.Kind == KindBlocker {
				coverage++
			}
		case "handoff":
			if note.Kind == KindHandoff {
				coverage++
			}
		case "artifact":
			if note.Kind == KindArtifactReference {
				coverage++
			}
		case "owner":
			if containsAny(text, " owns ", " owner", "responsible", "assigned", "designated") {
				coverage++
			}
		case "schedule":
			if containsDigit(text) || containsAny(text, "deadline", "today", "tomorrow", "before", "after", " by ") {
				coverage++
			}
		case "decision":
			if containsAny(text, "decided", "decision", "approved", "rejected") {
				coverage++
			}
		}
	}
	return coverage
}

func coordinationMatch(note Note, intent RecallIntent) int {
	switch note.Kind {
	case KindBlocker, KindHandoff:
		return 1
	}
	if requiredFactCoverage(note, intent) > 0 && containsAny(strings.ToLower(note.Body), "owns", "assigned", "committed", "approved", "rejected") {
		return 1
	}
	return 0
}

func recallRoutingAffinity(note Note, request RecallRequest, intent RecallIntent) int {
	if QueryRequestsOwnContext(request.Query) && note.Origin.UserID == request.Actor.UserID {
		return 2
	}
	for _, fact := range intent.RequestedFacts {
		if fact == "owner" && containsAny(strings.ToLower(note.Body), "owns", "responsible", "assigned") {
			return 1
		}
	}
	return 0
}

func exactScopeMatch(note Note, request RecallRequest) bool {
	if request.TaskRef != "" && note.TaskRef == request.TaskRef {
		return true
	}
	if request.ThreadRef != "" && note.ThreadRef == request.ThreadRef {
		return true
	}
	subject := strings.TrimSpace(strings.ToLower(note.Subject))
	return subject != "" && strings.Contains(strings.ToLower(request.Query), subject)
}

func uniqueRecallLanes(lanes []RecallLane) []RecallLane {
	seen := make(map[RecallLane]struct{}, len(lanes))
	result := make([]RecallLane, 0, len(lanes))
	for _, lane := range lanes {
		if _, exists := seen[lane]; exists {
			continue
		}
		seen[lane] = struct{}{}
		result = append(result, lane)
	}
	return result
}

func initializeRecallTrace(
	candidates []RecallCandidate,
	ranked []RecallCandidate,
	request RecallRequest,
	intent RecallIntent,
	rejections []RecallRejection,
) RecallTrace {
	trace := RecallTrace{
		PlanVersion: GeneralRecallV3PlanVersion, ScoringVersion: GeneralRecallV3ScoringVersion,
		Intent: intent, Candidates: len(candidates), FusionKept: len(ranked),
		CandidateTraces: make([]RecallCandidateTrace, 0, len(candidates)),
	}
	for _, candidate := range candidates {
		trace.CandidateTraces = append(trace.CandidateTraces, evaluateRecallCandidate(candidate, request, intent))
	}
	for _, candidate := range ranked {
		if candidate.explicitLane == 0 && candidate.SemanticScore != nil {
			addRecallLane(&trace, candidate.ID, RecallLaneSemanticFallback, "semantic_compatibility_fallback")
		}
	}
	for _, rejection := range rejections {
		recordRecallRejection(&trace, rejection)
	}
	trace.LanesExecuted = collectRecallLanes(trace.CandidateTraces)
	return trace
}

func collectRecallLanes(candidates []RecallCandidateTrace) []RecallLane {
	var result []RecallLane
	for _, candidate := range candidates {
		result = append(result, candidate.RetrievalLanes...)
	}
	return uniqueRecallLanes(result)
}

func addRecallLane(trace *RecallTrace, noteID string, lane RecallLane, reason string) {
	for index := range trace.CandidateTraces {
		candidate := &trace.CandidateTraces[index]
		if candidate.NoteID != noteID {
			continue
		}
		candidate.RetrievalLanes = uniqueRecallLanes(append(candidate.RetrievalLanes, lane))
		if reason != "" {
			candidate.RetrievalReasons = append(candidate.RetrievalReasons, reason)
		}
		trace.LanesExecuted = uniqueRecallLanes(append(trace.LanesExecuted, lane))
		return
	}
}

func recordRecallRejection(trace *RecallTrace, rejection RecallRejection) {
	trace.Rejections = append(trace.Rejections, rejection)
	for index := range trace.CandidateTraces {
		candidate := &trace.CandidateTraces[index]
		if candidate.NoteID == rejection.NoteID {
			candidate.Disposition = RecallDispositionSuppress
			candidate.RejectionReason = rejection.Reason
			return
		}
	}
}

// RecordRecallDeliveryClaimLoss reconciles a planned item with an adapter's
// failed delivery claim before the trace is persisted.
func RecordRecallDeliveryClaimLoss(trace *RecallTrace, noteID string, tokens int) {
	recordRecallRejection(trace, RecallRejection{NoteID: noteID, Reason: RejectDeliveryClaim, Tokens: tokens})
	trace.DeliveredItems = removeRecallID(trace.DeliveredItems, noteID)
}

func markRecallEvidence(trace *RecallTrace, primary Note, related []Note) {
	markRecallCandidateEvidence(trace, primary.ID)
	trace.DeliveredItems = append(trace.DeliveredItems, primary.ID)
	trace.SelectedSet = appendRecallID(trace.SelectedSet, primary.ID)
	for _, note := range related {
		markRecallCandidateEvidence(trace, note.ID)
		trace.SelectedSet = appendRecallID(trace.SelectedSet, note.ID)
		addRecallLane(trace, note.ID, RecallLaneRelation, "one_hop_related_subject")
		for index := range trace.CandidateTraces {
			candidate := &trace.CandidateTraces[index]
			if candidate.NoteID == note.ID {
				candidate.RelationPath = []string{primary.ID, "related_subject", note.ID}
				break
			}
		}
	}
}

func markRecallCandidateEvidence(trace *RecallTrace, noteID string) {
	for index := range trace.CandidateTraces {
		candidate := &trace.CandidateTraces[index]
		if candidate.NoteID == noteID {
			candidate.Disposition = RecallDispositionEvidence
			candidate.RejectionReason = ""
			return
		}
	}
}

func appendRecallID(values []string, noteID string) []string {
	if recallIDSelected(values, noteID) {
		return values
	}
	return append(values, noteID)
}

func removeRecallID(values []string, noteID string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != noteID {
			result = append(result, value)
		}
	}
	return result
}

func recallIDSelected(values []string, noteID string) bool {
	for _, value := range values {
		if value == noteID {
			return true
		}
	}
	return false
}

func finalizeRecallTrace(trace *RecallTrace) {
	kept := make([]RecallRejection, 0, len(trace.Rejections))
	for _, rejection := range trace.Rejections {
		if recallIDSelected(trace.SelectedSet, rejection.NoteID) {
			continue
		}
		kept = append(kept, rejection)
		if isRecallBudgetDrop(rejection.Reason) {
			trace.BudgetDrops = append(trace.BudgetDrops, rejection)
		}
	}
	trace.Rejections = kept
	trace.LanesExecuted = collectRecallLanes(trace.CandidateTraces)
}

func isRecallBudgetDrop(reason RecallRejectReason) bool {
	switch reason {
	case RejectMaxItems, RejectTokenBudget, RejectDuplicate:
		return true
	default:
		return false
	}
}
