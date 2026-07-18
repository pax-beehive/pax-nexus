package extractor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

type typedFactModality string

const (
	typedFactAsserted  typedFactModality = "asserted"
	typedFactCommitted typedFactModality = "committed"
	typedFactProposed  typedFactModality = "proposed"
	typedFactUncertain typedFactModality = "uncertain"
)

type typedFact struct {
	Kind            string            `json:"kind"`
	Subject         string            `json:"subject"`
	Statement       string            `json:"statement"`
	Actor           string            `json:"actor,omitempty"`
	Predicate       string            `json:"predicate,omitempty"`
	Value           string            `json:"value,omitempty"`
	Modality        typedFactModality `json:"modality"`
	RelatedSubjects []string          `json:"related_subjects,omitempty"`
}

type typedStateDecision struct {
	Decision           DecisionAction     `json:"decision"`
	IdentityRef        string             `json:"identity_ref,omitempty"`
	ClaimIDs           []string           `json:"claim_ids,omitempty"`
	EvidenceEventIDs   []string           `json:"evidence_event_ids,omitempty"`
	PriorStateRef      string             `json:"prior_state_ref,omitempty"`
	TemporalExpression string             `json:"temporal_expression,omitempty"`
	ValidAt            string             `json:"valid_at,omitempty"`
	InvalidAt          string             `json:"invalid_at,omitempty"`
	TemporalResolution TemporalResolution `json:"temporal_resolution,omitempty"`
	ReasonCodes        []string           `json:"reason_codes"`
	Fact               *typedFact         `json:"fact,omitempty"`
}

type extractionOutputV2Typed struct {
	Claims                  []Claim                  `json:"claims"`
	StateDecisions          []typedStateDecision     `json:"state_decisions"`
	NoStateEventIDs         []string                 `json:"no_state_event_ids"`
	InteractionObservations []InteractionObservation `json:"interaction_observations"`
}

func decodeExtractionResponseV2Typed(body []byte) (Result, string, error) {
	var response chatResponse
	if err := json.Unmarshal(body, &response); err != nil || len(response.Choices) == 0 {
		return Result{}, "", fmt.Errorf("decode extractor typed v2 response: %w", ErrInvalidModelResponse)
	}
	content := trimCodeFence(response.Choices[0].Message.Content)
	result, err := decodeExtractionContentV2Typed(content)
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

func decodeExtractionContentV2Typed(content string) (Result, error) {
	var output extractionOutputV2Typed
	if err := json.Unmarshal([]byte(content), &output); err != nil {
		return Result{}, fmt.Errorf("decode extractor typed v2 candidates: %w", errors.Join(ErrInvalidModelResponse, err))
	}
	if output.Claims == nil && output.StateDecisions == nil {
		return Result{}, fmt.Errorf("decode extractor typed v2 candidates: claims and state_decisions are required: %w", ErrInvalidModelResponse)
	}
	trace := &TraceV2{
		Claims:                  output.Claims,
		NoStateEventIDs:         output.NoStateEventIDs,
		InteractionObservations: output.InteractionObservations,
	}
	for _, typedDecision := range output.StateDecisions {
		decision, reason := mapTypedDecision(typedDecision)
		if reason != "" {
			trace.DecisionRejections = append(trace.DecisionRejections, DecisionRejection{
				Decision: decision,
				Reason:   reason,
			})
			continue
		}
		trace.StateDecisions = append(trace.StateDecisions, decision)
	}
	return Result{Trace: trace, ExtractionVersion: ExtractionVersionV2}, nil
}

func mapTypedDecision(typed typedStateDecision) (StateDecision, string) {
	decision := StateDecision{
		Decision: typed.Decision, IdentityRef: typed.IdentityRef,
		ClaimIDs:         append([]string(nil), typed.ClaimIDs...),
		EvidenceEventIDs: append([]string(nil), typed.EvidenceEventIDs...),
		PriorStateRef:    typed.PriorStateRef, TemporalExpression: typed.TemporalExpression,
		ValidAt: typed.ValidAt, InvalidAt: typed.InvalidAt,
		TemporalResolution: typed.TemporalResolution,
		ReasonCodes:        append([]string(nil), typed.ReasonCodes...),
	}
	if !decisionChangesState(typed.Decision) {
		return decision, ""
	}
	if typed.Fact == nil {
		return decision, "state-changing typed decision requires fact"
	}
	if strings.TrimSpace(typed.Fact.Subject) == "" {
		return decision, "typed fact is missing subject"
	}
	if !validTypedFactKind(typed.Fact.Kind) {
		return decision, fmt.Sprintf("typed fact kind %q is not in the kind vocabulary", typed.Fact.Kind)
	}
	decision.Candidate = &DecisionCandidate{
		Kind: typed.Fact.Kind, Subject: strings.TrimSpace(typed.Fact.Subject),
		RelatedSubjects: append([]string(nil), typed.Fact.RelatedSubjects...),
	}
	body, reason := renderTypedFact(*typed.Fact)
	decision.Candidate.Body = body
	if reason != "" {
		return decision, reason
	}
	switch typed.Fact.Modality {
	case typedFactAsserted, typedFactCommitted:
		return decision, ""
	case typedFactProposed, typedFactUncertain:
		return decision, fmt.Sprintf("typed fact modality %q cannot change canonical state", typed.Fact.Modality)
	default:
		return decision, fmt.Sprintf("typed fact modality %q is not in the modality vocabulary", typed.Fact.Modality)
	}
}

func validTypedFactKind(kind string) bool {
	switch teamnote.NoteKind(kind) {
	case teamnote.KindStatus, teamnote.KindBlocker, teamnote.KindHandoff, teamnote.KindArtifactReference:
		return true
	default:
		return false
	}
}

func renderTypedFact(fact typedFact) (string, string) {
	statement := strings.Join(strings.Fields(fact.Statement), " ")
	if statement == "" {
		return "", "typed fact is missing statement"
	}
	last, _ := utf8.DecodeLastRuneInString(statement)
	if !strings.ContainsRune(".!?。！？", last) {
		statement += "."
	}
	return statement, ""
}
