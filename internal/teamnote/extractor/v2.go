package extractor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

const extractionProtocolV2RevisionClaimCardV1 = "claim-card-v1-temporal-deterministic"
const extractionProtocolV2RevisionClaimCardV2 = "claim-card-v2-temporal-deterministic"

// Extraction v2 separates one model response into explicit internal products:
// grounded Claims, proposed State Decisions, and interaction observations.
// Deterministic code validates and maps them onto the existing Candidate
// shape, so a missing or incorrect Team Note is attributable to claim
// detection, identity resolution, state transition, or admission.

// ClaimType classifies one source-faithful assertion.
type ClaimType string

const (
	ClaimTypeDecision   ClaimType = "decision"
	ClaimTypeStatus     ClaimType = "status"
	ClaimTypeBlocker    ClaimType = "blocker"
	ClaimTypeHandoff    ClaimType = "handoff"
	ClaimTypeArtifact   ClaimType = "artifact"
	ClaimTypeSchedule   ClaimType = "schedule"
	ClaimTypeCorrection ClaimType = "correction"
)

func validClaimType(value ClaimType) bool {
	switch value {
	case ClaimTypeDecision, ClaimTypeStatus, ClaimTypeBlocker, ClaimTypeHandoff,
		ClaimTypeArtifact, ClaimTypeSchedule, ClaimTypeCorrection:
		return true
	default:
		return false
	}
}

// TemporalResolution describes how a claim's valid time was derived.
type TemporalResolution string

const (
	TemporalExplicit   TemporalResolution = "explicit"
	TemporalAnchored   TemporalResolution = "anchored"
	TemporalUnresolved TemporalResolution = "unresolved"
)

// Claim is a source-faithful assertion found in one or more new Session
// Events. Claims are diagnostic products and are never admitted directly.
type Claim struct {
	ClaimID            string             `json:"claim_id"`
	ClaimType          ClaimType          `json:"claim_type"`
	Subject            string             `json:"subject"`
	Predicate          string             `json:"predicate"`
	Value              string             `json:"value"`
	Speaker            string             `json:"speaker"`
	EvidenceEventIDs   []string           `json:"evidence_event_ids"`
	TemporalExpression string             `json:"temporal_expression,omitempty"`
	ValidAt            string             `json:"valid_at,omitempty"`
	InvalidAt          string             `json:"invalid_at,omitempty"`
	TemporalResolution TemporalResolution `json:"temporal_resolution,omitempty"`
}

// DecisionAction proposes how claims affect one canonical memory identity.
type DecisionAction string

const (
	DecisionCreate           DecisionAction = "create"
	DecisionUpdate           DecisionAction = "update"
	DecisionResolve          DecisionAction = "resolve"
	DecisionNoChange         DecisionAction = "no_change"
	DecisionKeepConflictOpen DecisionAction = "keep_conflict_open"
)

func validDecisionAction(value DecisionAction) bool {
	switch value {
	case DecisionCreate, DecisionUpdate, DecisionResolve, DecisionNoChange, DecisionKeepConflictOpen:
		return true
	default:
		return false
	}
}

// Reason codes explaining one state decision. Keep in sync with the v2 prompt.
var validReasonCodes = map[string]struct{}{
	"explicit_new_fact":            {},
	"explicit_correction":          {},
	"same_obligation_new_deadline": {},
	"explicit_resolution":          {},
	"corroborating_report":         {},
	"conflicting_report":           {},
	"insufficient_temporal_anchor": {},
}

var validInteractionStances = map[string]struct{}{
	"support": {}, "oppose": {}, "question": {}, "neutral": {},
}

var validInteractionSpeechActs = map[string]struct{}{
	"propose": {}, "request": {}, "commit": {}, "approve": {}, "reject": {},
	"handoff": {}, "escalate": {}, "acknowledge": {}, "question": {},
	"express_concern": {}, "express_urgency": {},
}

// DecisionCandidate is the storage-ready note proposed by one create, update,
// or resolve decision.
type DecisionCandidate struct {
	Kind            string   `json:"kind"`
	Subject         string   `json:"subject"`
	Body            string   `json:"body"`
	RelatedSubjects []string `json:"related_subjects,omitempty"`
}

// EvidenceClause identifies the exact Event text that authorizes one State
// Decision. It is extraction provenance and never changes the Candidate
// storage schema.
type EvidenceClause struct {
	EventID string `json:"event_id"`
	Quote   string `json:"quote"`
}

// StateDecision proposes one canonical state transition backed by claims.
type StateDecision struct {
	Decision           DecisionAction     `json:"decision"`
	IdentityRef        string             `json:"identity_ref,omitempty"`
	ClaimIDs           []string           `json:"claim_ids,omitempty"`
	EvidenceEventIDs   []string           `json:"evidence_event_ids,omitempty"`
	EvidenceClauses    []EvidenceClause   `json:"evidence_clauses,omitempty"`
	PriorStateRef      string             `json:"prior_state_ref,omitempty"`
	TemporalExpression string             `json:"temporal_expression,omitempty"`
	ValidAt            string             `json:"valid_at,omitempty"`
	InvalidAt          string             `json:"invalid_at,omitempty"`
	TemporalResolution TemporalResolution `json:"temporal_resolution,omitempty"`
	ReasonCodes        []string           `json:"reason_codes"`
	Candidate          *DecisionCandidate `json:"candidate,omitempty"`
}

// InteractionObservation records a team coordination signal. It never changes
// factual confidence, authority, or temporal truth.
type InteractionObservation struct {
	Actor           string `json:"actor"`
	Target          string `json:"target"`
	Stance          string `json:"stance"`
	SpeechAct       string `json:"speech_act"`
	EvidenceEventID string `json:"evidence_event_id"`
}

// ClaimRejection records one claim dropped by deterministic validation.
type ClaimRejection struct {
	Claim  Claim  `json:"claim"`
	Reason string `json:"reason"`
}

// DecisionRejection records one state decision dropped by deterministic
// validation.
type DecisionRejection struct {
	Decision StateDecision `json:"decision"`
	Reason   string        `json:"reason"`
}

// InteractionRejection records one coordination signal dropped by
// deterministic source and vocabulary validation.
type InteractionRejection struct {
	Observation InteractionObservation `json:"observation"`
	Reason      string                 `json:"reason"`
}

// TraceV2 is the per-slice extraction v2 diagnostic product. It never enters
// passive agent context.
type TraceV2 struct {
	Claims                  []Claim                  `json:"claims"`
	StateDecisions          []StateDecision          `json:"state_decisions"`
	InteractionObservations []InteractionObservation `json:"interaction_observations,omitempty"`
	ClaimRejections         []ClaimRejection         `json:"claim_rejections,omitempty"`
	DecisionRejections      []DecisionRejection      `json:"decision_rejections,omitempty"`
	InteractionRejections   []InteractionRejection   `json:"interaction_rejections,omitempty"`
	// NoStateEventIDs are new Events the model explicitly reviewed and
	// classified as containing no durable collaboration state. Together with
	// evidence citations, they make source-event coverage inspectable without
	// requiring a duplicate Claim for every straightforward decision.
	NoStateEventIDs        []string `json:"no_state_event_ids,omitempty"`
	UnreviewedEventIDs     []string `json:"unreviewed_event_ids,omitempty"`
	InvalidNoStateEventIDs []string `json:"invalid_no_state_event_ids,omitempty"`
	OrphanClaimIDs         []string `json:"orphan_claim_ids,omitempty"`
	// WouldVerify records targeted-verification trigger classes observed in
	// this slice, so the need for a second verification call can be sized
	// before one is implemented.
	WouldVerify []string `json:"would_verify,omitempty"`
}

type extractionOutputV2 struct {
	Claims                  []Claim                  `json:"claims"`
	StateDecisions          []StateDecision          `json:"state_decisions"`
	NoStateEventIDs         []string                 `json:"no_state_event_ids"`
	InteractionObservations []InteractionObservation `json:"interaction_observations"`
}

// decodeExtractionResponseV2 parses one v2 model response into its raw
// products. Slice-grounded validation happens in mapExtractionV2.
func decodeExtractionResponseV2(body []byte) (Result, string, error) {
	var response chatResponse
	if err := json.Unmarshal(body, &response); err != nil || len(response.Choices) == 0 {
		return Result{}, "", fmt.Errorf("decode extractor v2 response: %w", ErrInvalidModelResponse)
	}
	content := trimCodeFence(response.Choices[0].Message.Content)
	result, err := decodeExtractionContentV2(content)
	if err != nil {
		return Result{}, "", err
	}
	result.Usage = Usage{
		InputTokens: response.Usage.PromptTokens, OutputTokens: response.Usage.CompletionTokens,
		PromptCacheHitTokens:  response.Usage.PromptCacheHitTokens,
		PromptCacheMissTokens: response.Usage.PromptCacheMissTokens,
	}
	return result, content, nil
}

// decodeExtractionContentV2 decodes the saved v2 response content. It is also
// the replay path: a saved run replays the identical raw products.
func decodeExtractionContentV2(content string) (Result, error) {
	var output extractionOutputV2
	if err := json.Unmarshal([]byte(content), &output); err != nil {
		return Result{}, fmt.Errorf("decode extractor v2 candidates: %w", errors.Join(ErrInvalidModelResponse, err))
	}
	if output.Claims == nil && output.StateDecisions == nil {
		return Result{}, fmt.Errorf("decode extractor v2 candidates: claims and state_decisions are required: %w", ErrInvalidModelResponse)
	}
	return Result{Trace: &TraceV2{
		Claims:                  output.Claims,
		StateDecisions:          output.StateDecisions,
		NoStateEventIDs:         output.NoStateEventIDs,
		InteractionObservations: output.InteractionObservations,
	}, ExtractionVersion: ExtractionVersionV2}, nil
}

// mapExtractionV2 validates raw v2 products against the slice and maps
// admitted decisions onto the existing Candidate shape. Invalid claims and
// decisions are dropped with trace rejections instead of failing the slice.
func mapExtractionV2(result *Result, slice sessionlake.Slice) {
	mapExtractionV2With(result, slice, mapStandardDecision)
	result.TransitionAuthorities = nil
}

func mapExtractionClaimCardV1(result *Result, slice sessionlake.Slice) {
	mapExtractionV2With(result, slice, mapClaimCardDecision)
	result.TransitionAuthorities = nil
}

type stateDecisionMapper func(
	StateDecision,
	map[string]Claim,
	map[string]struct{},
	map[string]struct{},
	[]teamnote.SessionEvent,
	sessionlake.Slice,
) (*teamnote.Candidate, string)

func mapExtractionV2With(result *Result, slice sessionlake.Slice, mapDecisionWith stateDecisionMapper) {
	trace := result.Trace
	if trace == nil {
		trace = &TraceV2{}
		result.Trace = trace
	}
	allEvents := stringSet(eventIDs(slice.Events))
	newEvents := stringSet(slice.NewEventIDs)
	admittedClaims := make(map[string]Claim, len(trace.Claims))
	seenClaims := make(map[string]struct{}, len(trace.Claims))
	keptClaims := trace.Claims[:0]
	for _, claim := range trace.Claims {
		claim = normalizeClaimTemporal(claim)
		reason := claimRejectionReason(claim, allEvents, newEvents, seenClaims)
		if reason != "" {
			trace.ClaimRejections = append(trace.ClaimRejections, ClaimRejection{Claim: claim, Reason: reason})
			continue
		}
		seenClaims[claim.ClaimID] = struct{}{}
		admittedClaims[claim.ClaimID] = claim
		keptClaims = append(keptClaims, claim)
	}
	trace.Claims = keptClaims
	keptInteractions := trace.InteractionObservations[:0]
	for _, observation := range trace.InteractionObservations {
		if reason := interactionRejectionReason(observation, allEvents, newEvents); reason != "" {
			trace.InteractionRejections = append(trace.InteractionRejections, InteractionRejection{
				Observation: observation, Reason: reason,
			})
			continue
		}
		keptInteractions = append(keptInteractions, observation)
	}
	trace.InteractionObservations = keptInteractions

	candidates := make([]teamnote.Candidate, 0, len(trace.StateDecisions))
	authorities := make([]teamnote.TransitionAuthority, 0, len(trace.StateDecisions))
	keptDecisions := trace.StateDecisions[:0]
	for _, decision := range trace.StateDecisions {
		decision = normalizeDecisionTemporal(decision)
		candidate, reason := mapDecisionWith(decision, admittedClaims, allEvents, newEvents, slice.Events, slice)
		if reason != "" {
			trace.DecisionRejections = append(trace.DecisionRejections, DecisionRejection{Decision: decision, Reason: reason})
			continue
		}
		keptDecisions = append(keptDecisions, decision)
		if candidate != nil {
			candidates = append(candidates, *candidate)
			authorities = append(authorities, transitionAuthority(decision))
		}
	}
	trace.StateDecisions = keptDecisions
	traceCoverage(trace, slice, admittedClaims)
	trace.WouldVerify = wouldVerifyTriggers(trace)
	result.Candidates = candidates
	result.TransitionAuthorities = authorities
	result.ExtractionVersion = ExtractionVersionV2
}

func transitionAuthority(decision StateDecision) teamnote.TransitionAuthority {
	clauses := make([]teamnote.TransitionEvidenceClause, 0, len(decision.EvidenceClauses))
	for _, clause := range decision.EvidenceClauses {
		clauses = append(clauses, teamnote.TransitionEvidenceClause{EventID: clause.EventID, Quote: clause.Quote})
	}
	return teamnote.TransitionAuthority{
		PriorStateRef:   decision.PriorStateRef,
		EvidenceClauses: clauses,
		ReasonCodes:     append([]string(nil), decision.ReasonCodes...),
	}
}

func mapStandardDecision(
	decision StateDecision,
	claims map[string]Claim,
	allEvents map[string]struct{},
	newEvents map[string]struct{},
	events []teamnote.SessionEvent,
	slice sessionlake.Slice,
) (*teamnote.Candidate, string) {
	return mapDecision(decision, claims, allEvents, newEvents, events, extractionObservationTime(slice))
}

func interactionRejectionReason(
	observation InteractionObservation,
	allEvents map[string]struct{},
	newEvents map[string]struct{},
) string {
	if strings.TrimSpace(observation.Actor) == "" {
		return "interaction observation is missing actor"
	}
	if _, ok := validInteractionStances[observation.Stance]; !ok {
		return fmt.Sprintf("interaction stance %q is not in the stance vocabulary", observation.Stance)
	}
	if _, ok := validInteractionSpeechActs[observation.SpeechAct]; !ok {
		return fmt.Sprintf("interaction speech act %q is not in the speech-act vocabulary", observation.SpeechAct)
	}
	eventID := strings.TrimSpace(observation.EvidenceEventID)
	if _, ok := allEvents[eventID]; !ok {
		return fmt.Sprintf("interaction observation cites unknown event %q", eventID)
	}
	if _, ok := newEvents[eventID]; !ok {
		return "interaction observation is not grounded in a new event"
	}
	return ""
}

// claimRejectionReason returns the deterministic reason one claim can never
// ground a state decision, or "" when the claim is admissible.
func claimRejectionReason(claim Claim, allEvents, newEvents, seen map[string]struct{}) string {
	if strings.TrimSpace(claim.ClaimID) == "" {
		return "claim is missing claim_id"
	}
	if _, duplicate := seen[claim.ClaimID]; duplicate {
		return fmt.Sprintf("duplicate claim_id %q", claim.ClaimID)
	}
	if !validClaimType(claim.ClaimType) {
		return fmt.Sprintf("claim type %q is not in the claim vocabulary", claim.ClaimType)
	}
	if strings.TrimSpace(claim.Subject) == "" || strings.TrimSpace(claim.Predicate) == "" {
		return "claim is missing subject or predicate"
	}
	if strings.TrimSpace(claim.Value) == "" || strings.TrimSpace(claim.Speaker) == "" {
		return "claim is missing value or speaker"
	}
	if len(claim.EvidenceEventIDs) == 0 {
		return "claim is missing evidence"
	}
	grounded := false
	for _, eventID := range claim.EvidenceEventIDs {
		if _, ok := allEvents[eventID]; !ok {
			return fmt.Sprintf("claim cites unknown event %q", eventID)
		}
		if _, ok := newEvents[eventID]; ok {
			grounded = true
		}
	}
	if !grounded {
		return "claim is not grounded in a new event"
	}
	if reason := temporalRejectionReason(
		claim.TemporalExpression, claim.TemporalResolution, claim.ValidAt, claim.InvalidAt,
	); reason != "" {
		return "claim " + reason
	}
	return ""
}

// mapDecision validates one state decision and, for create, update, and
// resolve, produces the existing Candidate shape. no_change and
// keep_conflict_open are trace-only decisions.
func mapDecision(
	decision StateDecision,
	claims map[string]Claim,
	allEvents map[string]struct{},
	newEvents map[string]struct{},
	events []teamnote.SessionEvent,
	observationTime time.Time,
) (*teamnote.Candidate, string) {
	if !validDecisionAction(decision.Decision) {
		return nil, fmt.Sprintf("decision %q is not in the decision vocabulary", decision.Decision)
	}
	if reason := invalidReasonCode(decision.ReasonCodes); reason != "" {
		return nil, reason
	}
	evidence := make([]string, 0, len(decision.EvidenceEventIDs)+len(decision.ClaimIDs))
	seenEvidence := make(map[string]struct{})
	grounded := false
	for _, eventID := range decision.EvidenceEventIDs {
		if _, ok := allEvents[eventID]; !ok {
			return nil, fmt.Sprintf("decision cites unknown event %q", eventID)
		}
		if _, ok := newEvents[eventID]; ok {
			grounded = true
		}
		appendUnique(&evidence, seenEvidence, eventID)
	}
	referencedClaims := make([]Claim, 0, len(decision.ClaimIDs))
	for _, claimID := range decision.ClaimIDs {
		claim, ok := claims[claimID]
		if !ok {
			// A dangling claim reference does not void direct evidence the
			// decision already cites; the claim is skipped instead.
			continue
		}
		referencedClaims = append(referencedClaims, claim)
		for _, eventID := range claim.EvidenceEventIDs {
			if _, ok := newEvents[eventID]; ok {
				grounded = true
			}
			appendUnique(&evidence, seenEvidence, eventID)
		}
	}
	if len(evidence) == 0 {
		return nil, "decision has no grounded evidence or admitted claim"
	}
	if !grounded {
		return nil, "decision is not grounded in a new event"
	}
	if reason := stateDecisionAdmissionReason(
		decision.Decision, evidence, decision.EvidenceClauses, events, decision.Candidate,
	); reason != "" {
		return nil, reason
	}
	temporal := decisionTemporal(decision, referencedClaims)
	if reason := temporalRejectionReason(
		temporal.expression, temporal.resolution, temporal.validAt, temporal.invalidAt,
	); reason != "" {
		return nil, "decision " + reason
	}
	if decision.Decision == DecisionNoChange || decision.Decision == DecisionKeepConflictOpen {
		return nil, ""
	}
	if decision.Candidate == nil {
		return nil, "create, update, and resolve decisions require a candidate"
	}
	action := candidateAction(decision.Decision)
	identityRef := strings.TrimSpace(decision.IdentityRef)
	if identityRef == "" {
		identityRef = strings.TrimSpace(decision.PriorStateRef)
	}
	validAt, invalidAt, reason := candidateTemporalWindow(decision.Decision, temporal, observationTime)
	if reason != "" {
		return nil, reason
	}
	return &teamnote.Candidate{
		Action: action, Kind: teamnote.NoteKind(decision.Candidate.Kind),
		Subject: decision.Candidate.Subject, Body: decision.Candidate.Body,
		IdentityRef: identityRef, EvidenceEventIDs: evidence,
		RelatedSubjects: append([]string(nil), decision.Candidate.RelatedSubjects...),
		ValidAt:         validAt, InvalidAt: invalidAt,
	}, ""
}

func mapClaimCardDecision(
	decision StateDecision,
	claims map[string]Claim,
	allEvents map[string]struct{},
	newEvents map[string]struct{},
	events []teamnote.SessionEvent,
	slice sessionlake.Slice,
) (*teamnote.Candidate, string) {
	candidate, reason := mapDecision(decision, claims, allEvents, newEvents, events, extractionObservationTime(slice))
	if reason != "" || candidate == nil {
		return candidate, reason
	}
	card, reason := buildClaimCard(decision, claims, slice)
	if reason != "" {
		return nil, reason
	}
	candidate.Kind = card.kind
	candidate.Subject = card.subject
	candidate.Body = card.body
	candidate.IdentityRef = card.identityRef
	candidate.RelatedSubjects = card.relatedSubjects
	return candidate, ""
}

type claimCard struct {
	kind            teamnote.NoteKind
	subject         string
	body            string
	identityRef     string
	relatedSubjects []string
}

func buildClaimCard(decision StateDecision, claims map[string]Claim, slice sessionlake.Slice) (claimCard, string) {
	if len(decision.ClaimIDs) != 1 {
		return claimCard{}, "claim-card decision must reference exactly one primary claim"
	}
	claim, ok := claims[decision.ClaimIDs[0]]
	if !ok {
		return claimCard{}, fmt.Sprintf("claim-card primary claim %q is not admitted", decision.ClaimIDs[0])
	}
	subject := compactClaimCardText(claim.Subject)
	predicate := compactClaimCardText(claim.Predicate)
	value := compactClaimCardText(claim.Value)
	sourceActor := claimCardSourceActor(slice.Actor)
	if subject == "" || predicate == "" || value == "" || sourceActor == "" {
		return claimCard{}, "claim-card primary claim has incomplete content"
	}
	return claimCard{
		kind:        claimCardKind(claim.ClaimType),
		subject:     subject,
		body:        renderClaimCard(claim, subject, predicate, value, sourceActor),
		identityRef: claimCardIdentity(slice, subject, predicate),
	}, ""
}

func claimCardSourceActor(actor teamnote.Actor) string {
	return strings.Join([]string{actor.UserID, actor.AgentID, actor.SessionID}, "/")
}

func claimCardKind(claimType ClaimType) teamnote.NoteKind {
	switch claimType {
	case ClaimTypeBlocker:
		return teamnote.KindBlocker
	case ClaimTypeHandoff:
		return teamnote.KindHandoff
	case ClaimTypeArtifact:
		return teamnote.KindArtifactReference
	default:
		return teamnote.KindStatus
	}
}

func renderClaimCard(claim Claim, subject, predicate, value, speaker string) string {
	lines := []string{
		"[claim card; deterministic rendering of a source-backed state decision]",
		fmt.Sprintf("[claim_type=%s asserted_by=%s]", claim.ClaimType, speaker),
		"Subject: " + subject,
		"Predicate: " + predicate,
		"Value: " + value,
	}
	if expression := compactClaimCardText(claim.TemporalExpression); expression != "" {
		lines = append(lines, "Temporal expression: "+expression)
		lines = append(lines, "Temporal resolution: "+string(claim.TemporalResolution))
		if validAt := compactClaimCardText(claim.ValidAt); validAt != "" {
			lines = append(lines, "Valid at: "+validAt)
		}
		if invalidAt := compactClaimCardText(claim.InvalidAt); invalidAt != "" {
			lines = append(lines, "Invalid at: "+invalidAt)
		}
	}
	return strings.Join(lines, "\n")
}

func claimCardIdentity(slice sessionlake.Slice, subject, predicate string) string {
	input := strings.Join([]string{
		compactClaimCardText(slice.Events[0].TaskRef),
		compactClaimCardText(slice.Events[0].ThreadRef),
		strings.ToLower(subject),
		strings.ToLower(predicate),
	}, "\x00")
	digest := sha256.Sum256([]byte(input))
	return "claim-card/" + hex.EncodeToString(digest[:])
}

func compactClaimCardText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func decisionChangesState(action DecisionAction) bool {
	return action == DecisionCreate || action == DecisionUpdate || action == DecisionResolve
}

func candidateAction(decision DecisionAction) teamnote.CandidateAction {
	switch decision {
	case DecisionUpdate:
		return teamnote.ActionUpdate
	case DecisionResolve:
		return teamnote.ActionResolve
	default:
		return teamnote.ActionCreate
	}
}

func stateDecisionAdmissionReason(
	action DecisionAction,
	evidence []string,
	evidenceClauses []EvidenceClause,
	events []teamnote.SessionEvent,
	candidate *DecisionCandidate,
) string {
	if !decisionChangesState(action) {
		return ""
	}
	if sourceClausesContainCommittedCandidate(evidenceClauses, candidate) {
		return ""
	}
	if evidenceIsOnlyNonCommittalSourceLanguage(evidence, events, candidate) {
		return "decision evidence contains only a non-committal source proposal or request"
	}
	return ""
}

func sourceClausesContainCommittedCandidate(clauses []EvidenceClause, candidate *DecisionCandidate) bool {
	for _, clause := range clauses {
		if committedSourceClauseSupportsCandidate(clause.Quote, candidate) {
			return true
		}
	}
	return false
}

func invalidReasonCode(reasonCodes []string) string {
	for _, code := range reasonCodes {
		if _, ok := validReasonCodes[code]; !ok {
			return fmt.Sprintf("reason code %q is not in the reason vocabulary", code)
		}
	}
	return ""
}

func appendUnique(values *[]string, seen map[string]struct{}, value string) {
	if _, ok := seen[value]; ok {
		return
	}
	seen[value] = struct{}{}
	*values = append(*values, value)
}

type temporalFields struct {
	expression string
	validAt    string
	invalidAt  string
	resolution TemporalResolution
}

func normalizeClaimTemporal(claim Claim) Claim {
	claim.TemporalResolution, claim.ValidAt, claim.InvalidAt = stripTemporalWithoutSourceExpression(
		claim.TemporalExpression, claim.TemporalResolution, claim.ValidAt, claim.InvalidAt,
	)
	return claim
}

func normalizeDecisionTemporal(decision StateDecision) StateDecision {
	decision.TemporalResolution, decision.ValidAt, decision.InvalidAt = stripTemporalWithoutSourceExpression(
		decision.TemporalExpression, decision.TemporalResolution, decision.ValidAt, decision.InvalidAt,
	)
	if decision.TemporalResolution == TemporalUnresolved {
		decision.ValidAt, decision.InvalidAt = "", ""
	}
	return decision
}

// stripTemporalWithoutSourceExpression discards temporal metadata that cites
// no source expression. The model contract requires preserving the source
// phrase in temporal_expression; metadata without it is dropped instead of
// rejecting the whole decision, so the fact survives with an unscoped window.
func stripTemporalWithoutSourceExpression(
	expression string,
	resolution TemporalResolution,
	validAt string,
	invalidAt string,
) (TemporalResolution, string, string) {
	if strings.TrimSpace(expression) != "" {
		return resolution, validAt, invalidAt
	}
	return "", "", ""
}

func decisionTemporal(decision StateDecision, claims []Claim) temporalFields {
	fields := temporalFields{
		expression: strings.TrimSpace(decision.TemporalExpression),
		validAt:    strings.TrimSpace(decision.ValidAt), invalidAt: strings.TrimSpace(decision.InvalidAt),
		resolution: decision.TemporalResolution,
	}
	if fields.expression != "" || fields.validAt != "" || fields.invalidAt != "" || fields.resolution != "" {
		return fields
	}
	if len(claims) != 1 {
		return fields
	}
	claim := claims[0]
	return temporalFields{
		expression: strings.TrimSpace(claim.TemporalExpression),
		validAt:    strings.TrimSpace(claim.ValidAt), invalidAt: strings.TrimSpace(claim.InvalidAt),
		resolution: claim.TemporalResolution,
	}
}

func temporalRejectionReason(
	expression string,
	resolution TemporalResolution,
	validAt string,
	invalidAt string,
) string {
	expression = strings.TrimSpace(expression)
	validAt = strings.TrimSpace(validAt)
	invalidAt = strings.TrimSpace(invalidAt)
	if expression == "" && resolution == "" && validAt == "" && invalidAt == "" {
		return ""
	}
	if expression == "" {
		return "has temporal metadata without a source expression"
	}
	if resolution != TemporalExplicit && resolution != TemporalAnchored && resolution != TemporalUnresolved {
		return fmt.Sprintf("has invalid temporal resolution %q", resolution)
	}
	if resolution == TemporalUnresolved && (validAt != "" || invalidAt != "") {
		return "marks unresolved time with a resolved validity timestamp"
	}
	_, _, err := parseTemporalWindow(validAt, invalidAt)
	if err != nil {
		return "contains an invalid temporal window"
	}
	return ""
}

// candidateTemporalWindow parses the decision's temporal window. A create
// asserts current state, so an already-past invalid_at contradicts the
// assertion and cannot be admitted as a timeless current fact.
func candidateTemporalWindow(
	action DecisionAction,
	temporal temporalFields,
	observationTime time.Time,
) (*time.Time, *time.Time, string) {
	validAt, invalidAt, err := parseTemporalWindow(temporal.validAt, temporal.invalidAt)
	if err != nil {
		return nil, nil, "decision temporal window is invalid"
	}
	if action == DecisionCreate && invalidAt != nil && observationTime.IsZero() {
		return nil, nil, "decision temporal admission requires an extraction observation time"
	}
	if action == DecisionCreate && invalidAt != nil && !invalidAt.After(observationTime) {
		return nil, nil, "create decision is not current at the extraction observation time"
	}
	return validAt, invalidAt, ""
}

func parseTemporalWindow(validAt string, invalidAt string) (*time.Time, *time.Time, error) {
	parse := func(value string) (*time.Time, error) {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, nil
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return nil, err
		}
		parsed = parsed.UTC()
		return &parsed, nil
	}
	valid, err := parse(validAt)
	if err != nil {
		return nil, nil, err
	}
	invalid, err := parse(invalidAt)
	if err != nil {
		return nil, nil, err
	}
	if valid != nil && invalid != nil && invalid.Before(*valid) {
		return nil, nil, errors.New("invalid time is before valid time")
	}
	return valid, invalid, nil
}

func traceCoverage(trace *TraceV2, slice sessionlake.Slice, claims map[string]Claim) {
	newEvents := stringSet(slice.NewEventIDs)
	reviewed := make(map[string]struct{}, len(newEvents))
	referencedClaims := make(map[string]struct{}, len(claims))
	for _, decision := range trace.StateDecisions {
		for _, eventID := range decision.EvidenceEventIDs {
			if _, ok := newEvents[eventID]; ok {
				reviewed[eventID] = struct{}{}
			}
		}
		for _, claimID := range decision.ClaimIDs {
			referencedClaims[claimID] = struct{}{}
		}
	}
	for claimID, claim := range claims {
		for _, eventID := range claim.EvidenceEventIDs {
			if _, ok := newEvents[eventID]; ok {
				reviewed[eventID] = struct{}{}
			}
		}
		if _, referenced := referencedClaims[claimID]; !referenced {
			trace.OrphanClaimIDs = append(trace.OrphanClaimIDs, claimID)
		}
	}

	validNoState := make([]string, 0, len(trace.NoStateEventIDs))
	seenNoState := make(map[string]struct{}, len(trace.NoStateEventIDs))
	for _, eventID := range trace.NoStateEventIDs {
		if _, duplicate := seenNoState[eventID]; duplicate {
			trace.InvalidNoStateEventIDs = append(trace.InvalidNoStateEventIDs, eventID)
			continue
		}
		seenNoState[eventID] = struct{}{}
		if _, ok := newEvents[eventID]; !ok {
			trace.InvalidNoStateEventIDs = append(trace.InvalidNoStateEventIDs, eventID)
			continue
		}
		if _, hasEvidence := reviewed[eventID]; hasEvidence {
			trace.InvalidNoStateEventIDs = append(trace.InvalidNoStateEventIDs, eventID)
			continue
		}
		validNoState = append(validNoState, eventID)
		reviewed[eventID] = struct{}{}
	}
	trace.NoStateEventIDs = validNoState

	for _, eventID := range slice.NewEventIDs {
		if _, ok := reviewed[eventID]; !ok {
			trace.UnreviewedEventIDs = append(trace.UnreviewedEventIDs, eventID)
		}
	}
	sort.Strings(trace.OrphanClaimIDs)
	sort.Strings(trace.InvalidNoStateEventIDs)
}

// wouldVerifyTriggers records targeted-verification trigger classes observed
// in one slice so the second-call rate can be sized before it exists.
func wouldVerifyTriggers(trace *TraceV2) []string {
	triggers := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	add := func(class string) {
		if _, ok := seen[class]; !ok {
			seen[class] = struct{}{}
			triggers = append(triggers, class)
		}
	}
	for _, decision := range trace.StateDecisions {
		if decision.Decision == DecisionKeepConflictOpen {
			add("conflicting_reports")
		}
		if (decision.Decision == DecisionUpdate || decision.Decision == DecisionResolve) &&
			strings.TrimSpace(decision.IdentityRef) == "" && strings.TrimSpace(decision.PriorStateRef) == "" {
			add("uncertain_prior_identity")
		}
	}
	for _, claim := range trace.Claims {
		if strings.TrimSpace(claim.TemporalExpression) != "" && claim.TemporalResolution == TemporalUnresolved {
			add("unresolved_temporal_anchor")
		}
	}
	return triggers
}
