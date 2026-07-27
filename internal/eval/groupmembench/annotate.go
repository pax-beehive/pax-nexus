package groupmembench

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

// PromptVersion identifies the prompt contract Annotate sends to the judge.
// Bump it whenever the judge request shape or instructions change so a
// reviewer can tell which prompt produced a given annotation.
const PromptVersion = "groupmembench-supporting-events-v1"

// MaxCandidateEvents bounds how many domain events Annotate hands the judge
// for a single case. GroupMemBench's full-domain event set can run into the
// tens of thousands (real Finance selections are ~30k events, tens of
// megabytes as JSON) — far past what any model's context holds, and mostly
// irrelevant to any one gold answer. narrowCandidateEvents selects this many
// candidates by lexical overlap with the case's question and answer, not by
// position in the domain, so a supporting event is not lost just because it
// sorts late.
const MaxCandidateEvents = 200

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
// Before calling the judge, it narrows the full domain event set down to at
// most MaxCandidateEvents per case (see narrowCandidateEvents): the judge
// never sees the whole domain, only the events most likely to matter for
// that one case's answer.
//
// It never invents an author: an event ID the judge returns that is not
// among the candidates it was shown, an author outside the case's
// participant_agent_ids, and the asking user are all excluded rather than
// silently accepted. Narrowing itself must never invent an author either —
// if the candidates handed to the judge don't include the events that
// actually support the answer, the honest outcome is confidence low, not a
// plausible-looking wrong one; an event ID the judge cites that fell outside
// its candidates is treated exactly like any other unknown event ID. A
// judge error fails only that case's annotation (confidence low, no
// supporting authors) and does not abort the batch.
func Annotate(ctx context.Context, cases []ManifestCase, events []DomainEvent, judge LLM) ([]Annotation, error) {
	if judge == nil {
		return nil, fmt.Errorf("annotate GroupMemBench cases: judge is required")
	}
	seen := make(map[string]struct{}, len(events))
	ordered := make([]DomainEvent, 0, len(events))
	for _, event := range events {
		if _, duplicate := seen[event.ID]; duplicate {
			continue
		}
		seen[event.ID] = struct{}{}
		ordered = append(ordered, event)
	}
	slices.SortFunc(ordered, func(left, right DomainEvent) int {
		return strings.Compare(left.ID, right.ID)
	})
	annotations := make([]Annotation, 0, len(cases))
	for _, evalCase := range cases {
		annotations = append(annotations, annotateCase(ctx, evalCase, ordered, judge))
	}
	return annotations, nil
}

func annotateCase(ctx context.Context, evalCase ManifestCase, events []DomainEvent, judge LLM) Annotation {
	candidates, totalEvents := narrowCandidateEvents(evalCase, events, MaxCandidateEvents)
	candidateByID := make(map[string]DomainEvent, len(candidates))
	for _, event := range candidates {
		candidateByID[event.ID] = event
	}
	candidateNote := fmt.Sprintf("candidates=%d/%d", len(candidates), totalEvents)

	response, err := judge.SupportingEvents(ctx, JudgeRequest{
		CaseID: evalCase.ID, Question: evalCase.Question, Answer: evalCase.Answer, Events: candidates,
	})
	if err != nil {
		return Annotation{
			CaseID:     evalCase.ID,
			Confidence: ConfidenceLow,
			Method:     fmt.Sprintf("prompt=%s %s error=%q", PromptVersion, candidateNote, err.Error()),
		}
	}

	validEventIDs, unknownCount := resolveEventIDs(response.SupportingEventIDs, candidateByID)

	askingAgentID := supportingAuthorAgentID(evalCase.AskingUserID)
	participants := make(map[string]struct{}, len(evalCase.ParticipantAgentIDs))
	for _, agentID := range evalCase.ParticipantAgentIDs {
		participants[strings.TrimSpace(agentID)] = struct{}{}
	}

	agentSet := make(map[string]struct{}, len(validEventIDs))
	var droppedNonParticipant []string
	for _, eventID := range validEventIDs {
		agentID := supportingAuthorAgentID(candidateByID[eventID].Author)
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
	method := fmt.Sprintf("model=%s prompt=%s %s", model, PromptVersion, candidateNote)
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

// narrowCandidateEvents selects at most limit domain events most likely to
// ground evalCase's gold answer, ranked by BM25-style lexical overlap
// between the case's question+answer and each event's content. Selection is
// by relevance score, not list position, so a supporting event is not
// dropped merely because it sorts late in an arbitrarily-ordered domain.
// It returns the chosen candidates (sorted by ID for determinism) and the
// size of the full domain event set they were drawn from, so callers can
// record how much narrowing occurred.
//
// Candidates that don't make the cut are not a partial view held back from
// resolveEventIDs — they are simply never sent to the judge, so a judge
// cannot cite them, and if it tries anyway that citation is treated exactly
// like any other unknown event ID (see resolveEventIDs). This is deliberate:
// an event excluded by narrowing must never resolve to an author.
func narrowCandidateEvents(evalCase ManifestCase, events []DomainEvent, limit int) ([]DomainEvent, int) {
	total := len(events)
	if limit <= 0 || total <= limit {
		ordered := slices.Clone(events)
		slices.SortFunc(ordered, func(left, right DomainEvent) int {
			return strings.Compare(left.ID, right.ID)
		})
		return ordered, total
	}

	query := tokenize(evalCase.Question + " " + evalCase.Answer)
	tokenSets := make([][]string, total)
	documentFreq := make(map[string]int)
	totalLength := 0
	for index, event := range events {
		tokenSets[index] = tokenize(event.Content)
		totalLength += len(tokenSets[index])
		seen := make(map[string]struct{}, len(tokenSets[index]))
		for _, token := range tokenSets[index] {
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			documentFreq[token]++
		}
	}
	averageLength := float64(totalLength) / float64(total)

	type scoredEvent struct {
		index int
		score float64
	}
	scored := make([]scoredEvent, total)
	for index := range events {
		scored[index] = scoredEvent{index: index, score: bm25Overlap(query, tokenSets[index], documentFreq, total, averageLength)}
	}
	slices.SortFunc(scored, func(left, right scoredEvent) int {
		if left.score != right.score {
			if left.score > right.score {
				return -1
			}
			return 1
		}
		return strings.Compare(events[left.index].ID, events[right.index].ID)
	})

	top := scored[:limit]
	result := make([]DomainEvent, 0, limit)
	for _, candidate := range top {
		result = append(result, events[candidate.index])
	}
	slices.SortFunc(result, func(left, right DomainEvent) int {
		return strings.Compare(left.ID, right.ID)
	})
	return result, total
}

// bm25Overlap scores one document's lexical overlap with query using Okapi
// BM25. It mirrors the scoring already used to build case context in
// selector.go, applied here to full DomainEvents rather than Messages.
func bm25Overlap(query, document []string, documentFreq map[string]int, totalDocuments int, averageLength float64) float64 {
	const k1 = 1.5
	const b = 0.75
	frequencies := make(map[string]int, len(document))
	for _, token := range document {
		frequencies[token]++
	}
	documentLength := float64(len(document))
	var score float64
	for _, token := range query {
		frequency := float64(frequencies[token])
		if frequency == 0 {
			continue
		}
		docFreq := float64(documentFreq[token])
		inverseDocumentFrequency := math.Log(1 + (float64(totalDocuments)-docFreq+0.5)/(docFreq+0.5))
		denominator := frequency + k1*(1-b+b*documentLength/averageLength)
		score += inverseDocumentFrequency * frequency * (k1 + 1) / denominator
	}
	return score
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
