package groupmembench

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

// PromptVersion identifies the prompt contract Annotate sends to the judge.
// Bump it whenever the judge request shape or instructions change so a
// reviewer can tell which prompt produced a given annotation.
const PromptVersion = "groupmembench-supporting-events-v1"

const (
	// ConfidenceHigh marks an annotation the judge grounded in at least one
	// event, where every event ID the judge returned exists in the domain
	// events supplied to it.
	ConfidenceHigh = "high"
	// ConfidenceLow marks every other outcome: no supporting events, a judge
	// error, or a judge response referencing an event ID that does not
	// exist. Low confidence never means "no annotation" — it means a human
	// should look at it before trusting a strict trial built on it.
	ConfidenceLow = "low"
)

// DomainEvent is one conversation event a judge can cite as evidence for a
// case's gold answer. Author is the bare dataset user ID (e.g. "User_7"),
// not an agent ID; Annotate resolves authorship to agent IDs itself using
// the same convention internal/eval/v3.SelectAnswerer expects.
type DomainEvent struct {
	ID      string
	Author  string
	Content string
}

// Annotation records the supporting authorship Annotate derived for one
// case. An empty SupportingAgentIDs with Confidence "low" is a valid,
// honest result: it means the case cannot be a strict cross-agent trial.
type Annotation struct {
	CaseID             string   `json:"case_id"`
	SupportingAgentIDs []string `json:"supporting_agent_ids"`
	SupportingEventIDs []string `json:"supporting_event_ids"`
	Confidence         string   `json:"confidence"`
	Method             string   `json:"method"`
}

// JudgeRequest is what Annotate sends the judge for a single case: the gold
// answer to ground, and the domain events it may cite as evidence.
type JudgeRequest struct {
	CaseID   string
	Question string
	Answer   string
	Events   []DomainEvent
}

// JudgeResponse is the judge's determination of which domain events supply
// the facts the gold answer asserts. Model identifies which model produced
// the judgement, so Annotate can record it in Annotation.Method.
type JudgeResponse struct {
	SupportingEventIDs []string
	Model              string
}

// LLM judges, for one case at a time, which domain events support the gold
// answer. It is the seam Annotate depends on so tests can stub judgement
// without calling a real model.
type LLM interface {
	SupportingEvents(ctx context.Context, request JudgeRequest) (JudgeResponse, error)
}

// Annotate derives a supporting-author annotation for every case by asking
// the judge which domain events ground the case's gold answer, then
// resolving those events to agent IDs.
//
// It never invents an author: an event ID the judge returns that is not
// found among events, an author outside the case's participant_agent_ids,
// and the asking user are all excluded rather than silently accepted. A
// judge error fails only that case's annotation (confidence low, no
// supporting authors) and does not abort the batch.
func Annotate(ctx context.Context, cases []ManifestCase, events []DomainEvent, judge LLM) ([]Annotation, error) {
	if judge == nil {
		return nil, fmt.Errorf("annotate GroupMemBench cases: judge is required")
	}
	eventByID := make(map[string]DomainEvent, len(events))
	ordered := make([]DomainEvent, 0, len(events))
	for _, event := range events {
		if _, duplicate := eventByID[event.ID]; duplicate {
			continue
		}
		eventByID[event.ID] = event
		ordered = append(ordered, event)
	}
	slices.SortFunc(ordered, func(left, right DomainEvent) int {
		return strings.Compare(left.ID, right.ID)
	})
	annotations := make([]Annotation, 0, len(cases))
	for _, evalCase := range cases {
		annotations = append(annotations, annotateCase(ctx, evalCase, ordered, eventByID, judge))
	}
	return annotations, nil
}

func annotateCase(ctx context.Context, evalCase ManifestCase, events []DomainEvent, eventByID map[string]DomainEvent, judge LLM) Annotation {
	response, err := judge.SupportingEvents(ctx, JudgeRequest{
		CaseID: evalCase.ID, Question: evalCase.Question, Answer: evalCase.Answer, Events: events,
	})
	if err != nil {
		return Annotation{
			CaseID:     evalCase.ID,
			Confidence: ConfidenceLow,
			Method:     fmt.Sprintf("prompt=%s error=%q", PromptVersion, err.Error()),
		}
	}

	validEventIDs, unknownCount := resolveEventIDs(response.SupportingEventIDs, eventByID)

	askingAgentID := supportingAuthorAgentID(evalCase.AskingUserID)
	participants := make(map[string]struct{}, len(evalCase.ParticipantAgentIDs))
	for _, agentID := range evalCase.ParticipantAgentIDs {
		participants[strings.TrimSpace(agentID)] = struct{}{}
	}

	agentSet := make(map[string]struct{}, len(validEventIDs))
	var droppedNonParticipant []string
	for _, eventID := range validEventIDs {
		agentID := supportingAuthorAgentID(eventByID[eventID].Author)
		if agentID == askingAgentID {
			continue
		}
		if _, isParticipant := participants[agentID]; !isParticipant {
			droppedNonParticipant = append(droppedNonParticipant, agentID)
			continue
		}
		agentSet[agentID] = struct{}{}
	}
	agents := make([]string, 0, len(agentSet))
	for agentID := range agentSet {
		agents = append(agents, agentID)
	}
	slices.Sort(agents)

	confidence := ConfidenceLow
	if len(validEventIDs) > 0 && unknownCount == 0 {
		confidence = ConfidenceHigh
	}

	model := response.Model
	if strings.TrimSpace(model) == "" {
		model = "unknown"
	}
	method := fmt.Sprintf("model=%s prompt=%s", model, PromptVersion)
	if len(droppedNonParticipant) > 0 {
		slices.Sort(droppedNonParticipant)
		droppedNonParticipant = slices.Compact(droppedNonParticipant)
		method = fmt.Sprintf("%s dropped_non_participant=%s", method, strings.Join(droppedNonParticipant, ","))
	}
	if unknownCount > 0 {
		method = fmt.Sprintf("%s dropped_unknown_events=%d", method, unknownCount)
	}

	return Annotation{
		CaseID:             evalCase.ID,
		SupportingAgentIDs: agents,
		SupportingEventIDs: validEventIDs,
		Confidence:         confidence,
		Method:             method,
	}
}

// resolveEventIDs keeps only the event IDs the judge returned that exist
// among the supplied events, deduplicated and sorted, and reports how many
// were dropped because they did not resolve to a known event.
func resolveEventIDs(returned []string, eventByID map[string]DomainEvent) ([]string, int) {
	seen := make(map[string]struct{}, len(returned))
	resolved := make([]string, 0, len(returned))
	unknownCount := 0
	for _, eventID := range returned {
		eventID = strings.TrimSpace(eventID)
		if eventID == "" {
			continue
		}
		if _, ok := eventByID[eventID]; !ok {
			unknownCount++
			continue
		}
		if _, duplicate := seen[eventID]; duplicate {
			continue
		}
		seen[eventID] = struct{}{}
		resolved = append(resolved, eventID)
	}
	slices.Sort(resolved)
	return resolved, unknownCount
}

// WriteAnnotations writes annotations as JSON to <directory>/annotations.json.
func WriteAnnotations(directory string, annotations []Annotation) error {
	if strings.TrimSpace(directory) == "" {
		return fmt.Errorf("write GroupMemBench annotations: directory is required")
	}
	if err := writeJSON(filepath.Join(directory, "annotations.json"), annotations); err != nil {
		return fmt.Errorf("write GroupMemBench annotations: %w", err)
	}
	return nil
}

// DomainEventsFromSessionBatches flattens native session batches (as written
// alongside a v3 manifest's domain conversation, e.g.
// domain/producer/session-batches.json) into DomainEvents, using each
// event's actor user ID as the bare author.
func DomainEventsFromSessionBatches(batches []session.SessionBatch) []DomainEvent {
	events := make([]DomainEvent, 0)
	for _, batch := range batches {
		for _, event := range batch.Events {
			events = append(events, DomainEvent{ID: event.ID, Author: event.Actor.UserID, Content: event.Content})
		}
	}
	return events
}

// supportingAuthorAgentID converts a bare GroupMemBench user ID to the
// agent ID convention internal/eval/v3.groupMemBenchAgentID and
// participantAgentIDs (files.go) both use, so exclusion checks against
// participant_agent_ids and the asking user match by construction.
func supportingAuthorAgentID(userID string) string {
	return "groupmembench-" + safeCaseID(strings.TrimSpace(userID))
}
