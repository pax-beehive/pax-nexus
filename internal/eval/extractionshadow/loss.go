package extractionshadow

import (
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
)

func attributeExtractionLosses(
	fixture stageeval.Fixture,
	run CaseRun,
	extraction stageeval.Observation,
) ([]ExtractionAtomLoss, error) {
	matched, err := matchedAtomIDs(fixture, extraction)
	if err != nil {
		return nil, err
	}
	losses := make([]ExtractionAtomLoss, 0, len(fixture.RequiredAtoms))
	for _, atom := range fixture.RequiredAtoms {
		loss := ExtractionAtomLoss{
			CaseID: fixture.CaseID, AtomID: atom.ID,
			SupportingEventIDs: append([]string(nil), atom.SupportingEventIDs...),
			Matched:            matched[atom.ID],
		}
		loss.SourceCovered = anySliceEvent(run.Slices, atom.SupportingEventIDs)
		loss.Reviewed = anyReviewedEvent(run.Slices, atom.SupportingEventIDs)
		if !loss.Matched {
			loss.LostAt, loss.Reason = firstExtractionLoss(run.Slices, atom.SupportingEventIDs, loss.SourceCovered)
		}
		losses = append(losses, loss)
	}
	return losses, nil
}

func matchedAtomIDs(fixture stageeval.Fixture, extraction stageeval.Observation) (map[string]bool, error) {
	matched := make(map[string]bool, len(fixture.RequiredAtoms))
	result, err := stageeval.Evaluate(fixture, extraction, stageeval.Observation{
		CaseID: fixture.CaseID, Stage: stageeval.StageRecall,
		SourceRevision: fixture.SourceRevision, RecallContext: &fixture.RecallContext,
	})
	if err != nil {
		return nil, err
	}
	missing := make(map[string]struct{}, len(result.Extraction.MissingAtomIDs))
	for _, atomID := range result.Extraction.MissingAtomIDs {
		missing[atomID] = struct{}{}
	}
	for _, atom := range fixture.RequiredAtoms {
		_, isMissing := missing[atom.ID]
		matched[atom.ID] = !isMissing
	}
	return matched, nil
}

func anySliceEvent(slices []SliceRecord, eventIDs []string) bool {
	return anyEventID(eventIDs, func(eventID string) bool {
		for _, slice := range slices {
			if containsString(slice.NewEventIDs, eventID) {
				return true
			}
		}
		return false
	})
}

func anyReviewedEvent(slices []SliceRecord, eventIDs []string) bool {
	return anyEventID(eventIDs, func(eventID string) bool {
		for _, slice := range slices {
			if traceReviewsEvent(slice.Trace, eventID) {
				return true
			}
		}
		return false
	})
}

func firstExtractionLoss(slices []SliceRecord, eventIDs []string, sourceCovered bool) (ExtractionLossStage, string) {
	if len(eventIDs) == 0 {
		return ExtractionLossSourceCoverage, "fixture_missing_supporting_events"
	}
	if !sourceCovered {
		return ExtractionLossSourceCoverage, "supporting_events_not_in_extraction_input"
	}
	for _, slice := range slices {
		trace := slice.Trace
		if trace == nil {
			continue
		}
		for _, rejection := range trace.ClaimRejections {
			if intersects(rejection.Claim.EvidenceEventIDs, eventIDs) {
				return ExtractionLossClaimValidation, rejection.Reason
			}
		}
		for _, rejection := range trace.DecisionRejections {
			if decisionSupports(rejection.Decision, trace.Claims, eventIDs) {
				return ExtractionLossDecisionAdmission, rejection.Reason
			}
		}
		if intersects(trace.InvalidNoStateEventIDs, eventIDs) {
			return ExtractionLossEventReview, "invalid_no_state_classification"
		}
		if intersects(trace.UnreviewedEventIDs, eventIDs) {
			return ExtractionLossEventReview, "supporting_event_unreviewed"
		}
		if intersects(trace.NoStateEventIDs, eventIDs) {
			return ExtractionLossEventReview, "supporting_event_classified_no_state"
		}
		for _, decision := range trace.StateDecisions {
			if decisionSupports(decision, trace.Claims, eventIDs) {
				return ExtractionLossNoteMaterialization, "admitted_decision_missing_atom"
			}
		}
	}
	return ExtractionLossEventReview, "supporting_event_has_no_extraction_product"
}

func traceReviewsEvent(trace *extractor.TraceV2, eventID string) bool {
	if trace == nil {
		return false
	}
	if containsString(trace.NoStateEventIDs, eventID) {
		return true
	}
	for _, claim := range trace.Claims {
		if containsString(claim.EvidenceEventIDs, eventID) {
			return true
		}
	}
	for _, rejection := range trace.ClaimRejections {
		if containsString(rejection.Claim.EvidenceEventIDs, eventID) {
			return true
		}
	}
	for _, decision := range trace.StateDecisions {
		if containsString(decision.EvidenceEventIDs, eventID) {
			return true
		}
	}
	for _, rejection := range trace.DecisionRejections {
		if decisionSupports(rejection.Decision, trace.Claims, []string{eventID}) {
			return true
		}
	}
	return false
}

func decisionSupports(decision extractor.StateDecision, claims []extractor.Claim, eventIDs []string) bool {
	if intersects(decision.EvidenceEventIDs, eventIDs) {
		return true
	}
	claimIDs := make(map[string]struct{}, len(decision.ClaimIDs))
	for _, claimID := range decision.ClaimIDs {
		claimIDs[claimID] = struct{}{}
	}
	for _, claim := range claims {
		if _, ok := claimIDs[claim.ClaimID]; ok && intersects(claim.EvidenceEventIDs, eventIDs) {
			return true
		}
	}
	return false
}

func anyEventID(eventIDs []string, predicate func(string) bool) bool {
	for _, eventID := range eventIDs {
		if predicate(strings.TrimSpace(eventID)) {
			return true
		}
	}
	return false
}

func intersects(left, right []string) bool {
	for _, value := range left {
		if containsString(right, value) {
			return true
		}
	}
	return false
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
