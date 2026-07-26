package groupmembench_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
	"github.com/stretchr/testify/suite"
)

type annotateSuite struct {
	suite.Suite
}

func TestAnnotateSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(annotateSuite))
}

// stubJudge is a scripted groupmembench.LLM used so tests never call a real
// model. It also records how many times each case ID was judged.
type stubJudge struct {
	responses map[string]groupmembench.JudgeResponse
	errs      map[string]error
	calls     map[string]int
	requests  map[string]groupmembench.JudgeRequest
}

func newStubJudge() *stubJudge {
	return &stubJudge{
		responses: make(map[string]groupmembench.JudgeResponse),
		errs:      make(map[string]error),
		calls:     make(map[string]int),
		requests:  make(map[string]groupmembench.JudgeRequest),
	}
}

func (s *stubJudge) SupportingEvents(_ context.Context, request groupmembench.JudgeRequest) (groupmembench.JudgeResponse, error) {
	s.calls[request.CaseID]++
	s.requests[request.CaseID] = request
	if err, ok := s.errs[request.CaseID]; ok {
		return groupmembench.JudgeResponse{}, err
	}
	return s.responses[request.CaseID], nil
}

func participantCase(id, askingUserID string, participants ...string) groupmembench.ManifestCase {
	return groupmembench.ManifestCase{
		ID: id, Category: "multi_hop", Question: "question " + id, Answer: "answer " + id,
		AskingUserID: askingUserID, ScopeID: "groupmembench-Finance", ParticipantAgentIDs: participants,
	}
}

func (s *annotateSuite) TestSingleEventYieldsItsAuthorAtHighConfidence() {
	cases := []groupmembench.ManifestCase{
		participantCase("case-1", "User_1", "groupmembench-User_1", "groupmembench-User_2"),
	}
	events := []groupmembench.DomainEvent{
		{ID: "Msg_1", Author: "User_2", Content: "the deadline is 2025-07-18"},
		{ID: "Msg_2", Author: "User_1", Content: "unrelated"},
	}
	judge := newStubJudge()
	judge.responses["case-1"] = groupmembench.JudgeResponse{SupportingEventIDs: []string{"Msg_1"}, Model: "stub-v1"}

	annotations, err := groupmembench.Annotate(context.Background(), cases, events, judge)
	s.Require().NoError(err)
	s.Require().Len(annotations, 1)
	annotation := annotations[0]
	s.Equal("case-1", annotation.CaseID)
	s.Equal([]string{"groupmembench-User_2"}, annotation.SupportingAgentIDs)
	s.Equal([]string{"Msg_1"}, annotation.SupportingEventIDs)
	s.Equal(groupmembench.ConfidenceHigh, annotation.Confidence)
	s.Contains(annotation.Method, "stub-v1")
	s.Contains(annotation.Method, groupmembench.PromptVersion)
}

func (s *annotateSuite) TestTwoAuthorsAreDeduplicatedAndSorted() {
	cases := []groupmembench.ManifestCase{
		participantCase("case-2", "User_1", "groupmembench-User_1", "groupmembench-User_2", "groupmembench-User_3"),
	}
	events := []groupmembench.DomainEvent{
		{ID: "Msg_1", Author: "User_3", Content: "part one"},
		{ID: "Msg_2", Author: "User_2", Content: "part two"},
		{ID: "Msg_3", Author: "User_3", Content: "part one restated"},
	}
	judge := newStubJudge()
	judge.responses["case-2"] = groupmembench.JudgeResponse{
		SupportingEventIDs: []string{"Msg_1", "Msg_2", "Msg_3", "Msg_1"},
		Model:              "stub-v1",
	}

	annotations, err := groupmembench.Annotate(context.Background(), cases, events, judge)
	s.Require().NoError(err)
	s.Require().Len(annotations, 1)
	s.Equal([]string{"groupmembench-User_2", "groupmembench-User_3"}, annotations[0].SupportingAgentIDs)
	s.Equal([]string{"Msg_1", "Msg_2", "Msg_3"}, annotations[0].SupportingEventIDs)
	s.Equal(groupmembench.ConfidenceHigh, annotations[0].Confidence)
}

func (s *annotateSuite) TestAskingUserIsNeverEmittedEvenWhenTheyAuthoredAMatchingEvent() {
	cases := []groupmembench.ManifestCase{
		participantCase("case-3", "User_1", "groupmembench-User_1", "groupmembench-User_2"),
	}
	events := []groupmembench.DomainEvent{
		{ID: "Msg_1", Author: "User_1", Content: "the asking user's own statement"},
	}
	judge := newStubJudge()
	judge.responses["case-3"] = groupmembench.JudgeResponse{SupportingEventIDs: []string{"Msg_1"}, Model: "stub-v1"}

	annotations, err := groupmembench.Annotate(context.Background(), cases, events, judge)
	s.Require().NoError(err)
	s.Empty(annotations[0].SupportingAgentIDs)
	s.NotContains(annotations[0].SupportingAgentIDs, "groupmembench-User_1")
}

func (s *annotateSuite) TestNoSupportingEventsYieldsEmptyAndLowConfidenceNeverFabricated() {
	cases := []groupmembench.ManifestCase{
		participantCase("case-4", "User_1", "groupmembench-User_1", "groupmembench-User_2"),
	}
	events := []groupmembench.DomainEvent{
		{ID: "Msg_1", Author: "User_2", Content: "unrelated content"},
	}
	judge := newStubJudge()
	judge.responses["case-4"] = groupmembench.JudgeResponse{Model: "stub-v1"}

	annotations, err := groupmembench.Annotate(context.Background(), cases, events, judge)
	s.Require().NoError(err)
	s.Empty(annotations[0].SupportingAgentIDs)
	s.Empty(annotations[0].SupportingEventIDs)
	s.Equal(groupmembench.ConfidenceLow, annotations[0].Confidence)
}

func (s *annotateSuite) TestAuthorOutsideParticipantsIsDroppedAndRecorded() {
	// Only groupmembench-User_1 (the asking user) and groupmembench-User_2
	// are declared participants; User_9 authored a matching event but is
	// not a participant of this case and must not be emitted.
	cases := []groupmembench.ManifestCase{
		participantCase("case-5", "User_1", "groupmembench-User_1", "groupmembench-User_2"),
	}
	events := []groupmembench.DomainEvent{
		{ID: "Msg_1", Author: "User_9", Content: "an outside author's statement"},
	}
	judge := newStubJudge()
	judge.responses["case-5"] = groupmembench.JudgeResponse{SupportingEventIDs: []string{"Msg_1"}, Model: "stub-v1"}

	annotations, err := groupmembench.Annotate(context.Background(), cases, events, judge)
	s.Require().NoError(err)
	s.Empty(annotations[0].SupportingAgentIDs)
	s.Contains(annotations[0].Method, "groupmembench-User_9")
	s.Contains(annotations[0].Method, "dropped")
}

func (s *annotateSuite) TestJudgeCalledOncePerCaseAndErrorFailsOnlyThatCase() {
	cases := []groupmembench.ManifestCase{
		participantCase("ok-case", "User_1", "groupmembench-User_1", "groupmembench-User_2"),
		participantCase("error-case", "User_1", "groupmembench-User_1", "groupmembench-User_2"),
	}
	events := []groupmembench.DomainEvent{
		{ID: "Msg_1", Author: "User_2", Content: "supporting fact"},
	}
	judge := newStubJudge()
	judge.responses["ok-case"] = groupmembench.JudgeResponse{SupportingEventIDs: []string{"Msg_1"}, Model: "stub-v1"}
	judge.errs["error-case"] = errors.New("judge unavailable")

	annotations, err := groupmembench.Annotate(context.Background(), cases, events, judge)
	s.Require().NoError(err)
	s.Require().Len(annotations, 2)

	s.Equal(1, judge.calls["ok-case"])
	s.Equal(1, judge.calls["error-case"])

	var okAnnotation, errAnnotation groupmembench.Annotation
	for _, annotation := range annotations {
		switch annotation.CaseID {
		case "ok-case":
			okAnnotation = annotation
		case "error-case":
			errAnnotation = annotation
		}
	}
	s.Equal([]string{"groupmembench-User_2"}, okAnnotation.SupportingAgentIDs)
	s.Equal(groupmembench.ConfidenceHigh, okAnnotation.Confidence)

	s.Empty(errAnnotation.SupportingAgentIDs)
	s.Equal(groupmembench.ConfidenceLow, errAnnotation.Confidence)
	s.Contains(errAnnotation.Method, "judge unavailable")
}

func (s *annotateSuite) TestUnknownEventIDDowngradesConfidenceButKeepsKnownAuthors() {
	cases := []groupmembench.ManifestCase{
		participantCase("case-6", "User_1", "groupmembench-User_1", "groupmembench-User_2"),
	}
	events := []groupmembench.DomainEvent{
		{ID: "Msg_1", Author: "User_2", Content: "known fact"},
	}
	judge := newStubJudge()
	judge.responses["case-6"] = groupmembench.JudgeResponse{
		SupportingEventIDs: []string{"Msg_1", "Msg_999"}, Model: "stub-v1",
	}

	annotations, err := groupmembench.Annotate(context.Background(), cases, events, judge)
	s.Require().NoError(err)
	s.Equal([]string{"groupmembench-User_2"}, annotations[0].SupportingAgentIDs)
	s.Equal([]string{"Msg_1"}, annotations[0].SupportingEventIDs)
	s.Equal(groupmembench.ConfidenceLow, annotations[0].Confidence)
	s.Contains(annotations[0].Method, "dropped_unknown_events=1")
}

func (s *annotateSuite) TestBlankEventIDsAreNotGroundingAndYieldLowConfidence() {
	cases := []groupmembench.ManifestCase{
		participantCase("case-8", "User_1", "groupmembench-User_1", "groupmembench-User_2"),
	}
	events := []groupmembench.DomainEvent{
		{ID: "Msg_1", Author: "User_2", Content: "known fact"},
	}
	judge := newStubJudge()
	// A judge response containing only blank/whitespace entries must never
	// be treated as grounded evidence, even though it's technically
	// "non-empty" as a raw response.
	judge.responses["case-8"] = groupmembench.JudgeResponse{
		SupportingEventIDs: []string{"", "   "}, Model: "stub-v1",
	}

	annotations, err := groupmembench.Annotate(context.Background(), cases, events, judge)
	s.Require().NoError(err)
	s.Empty(annotations[0].SupportingAgentIDs)
	s.Empty(annotations[0].SupportingEventIDs)
	s.Equal(groupmembench.ConfidenceLow, annotations[0].Confidence)
}

func (s *annotateSuite) TestNarrowingBoundsPayloadAndStillFindsSupportingEventPastAnyHeadOfListTruncation() {
	cases := []groupmembench.ManifestCase{
		participantCase("case-narrow", "User_1", "groupmembench-User_1", "groupmembench-User_2"),
	}
	cases[0].Question = "what is the launch codeword"
	cases[0].Answer = "zephyrion-9427"

	// 500 decoys share no vocabulary with the question/answer at all; the
	// one real supporting event carries the rare codeword and sorts dead
	// last by ID, so a naive "take the first N events" truncation would
	// never see it. Only lexical relevance should surface it.
	events := make([]groupmembench.DomainEvent, 0, 501)
	for i := 0; i < 500; i++ {
		events = append(events, groupmembench.DomainEvent{
			ID: fmt.Sprintf("Msg_%04d", i), Author: "User_3",
			Content: "quarterly report filler content about routine budget meetings and calendar scheduling",
		})
	}
	events = append(events, groupmembench.DomainEvent{
		ID: "Zzz_supporting", Author: "User_2",
		Content: "The launch codeword is zephyrion-9427; keep it confidential until go-live.",
	})

	judge := newStubJudge()
	judge.responses["case-narrow"] = groupmembench.JudgeResponse{SupportingEventIDs: []string{"Zzz_supporting"}, Model: "stub-v1"}

	annotations, err := groupmembench.Annotate(context.Background(), cases, events, judge)
	s.Require().NoError(err)
	s.Require().Len(annotations, 1)

	sent := judge.requests["case-narrow"].Events
	s.LessOrEqual(len(sent), groupmembench.MaxCandidateEvents, "judge payload must be bounded, not the full 501-event domain")
	found := false
	for _, event := range sent {
		if event.ID == "Zzz_supporting" {
			found = true
		}
	}
	s.True(found, "the lexically-relevant supporting event must survive narrowing even though it sorts last by ID")

	s.Equal([]string{"groupmembench-User_2"}, annotations[0].SupportingAgentIDs)
	s.Equal(groupmembench.ConfidenceHigh, annotations[0].Confidence)
	s.Contains(annotations[0].Method, fmt.Sprintf("candidates=%d/501", groupmembench.MaxCandidateEvents))
}

func (s *annotateSuite) TestNarrowingExcludedSupportingEventYieldsLowConfidenceNotFabrication() {
	cases := []groupmembench.ManifestCase{
		participantCase("case-excluded", "User_1", "groupmembench-User_1", "groupmembench-User_2"),
	}
	cases[0].Question = "what happened with the budget freeze"
	cases[0].Answer = "the budget freeze was approved in march"

	// 250 decoys (more than MaxCandidateEvents) all share heavy vocabulary
	// overlap with the question/answer, so narrowing fills its whole
	// candidate budget with them. The one event that actually would have
	// supported the answer shares no vocabulary with it at all, so it never
	// makes the cut — it is excluded from the judge's view entirely.
	events := make([]groupmembench.DomainEvent, 0, 251)
	for i := 0; i < 250; i++ {
		events = append(events, groupmembench.DomainEvent{
			ID: fmt.Sprintf("Decoy_%04d", i), Author: "User_3",
			Content: "the budget freeze was approved in march after finance reviewed the freeze budget march approval",
		})
	}
	excludedEvent := groupmembench.DomainEvent{
		ID: "Excluded_supporting", Author: "User_2",
		Content: "elephants migrate through the savanna every summer without any budget context at all",
	}
	events = append(events, excludedEvent)

	judge := newStubJudge()
	// The judge response cites the excluded event anyway (whether it
	// hallucinated it or somehow knew of it out of band); it was never in
	// the candidates handed to it, so it must be treated like any other
	// unknown event ID rather than trusted.
	judge.responses["case-excluded"] = groupmembench.JudgeResponse{
		SupportingEventIDs: []string{excludedEvent.ID}, Model: "stub-v1",
	}

	annotations, err := groupmembench.Annotate(context.Background(), cases, events, judge)
	s.Require().NoError(err)
	s.Require().Len(annotations, 1)

	sent := judge.requests["case-excluded"].Events
	for _, event := range sent {
		s.NotEqual(excludedEvent.ID, event.ID, "narrowing must have excluded this event from the judge's candidates")
	}

	s.Empty(annotations[0].SupportingAgentIDs)
	s.Empty(annotations[0].SupportingEventIDs)
	s.Equal(groupmembench.ConfidenceLow, annotations[0].Confidence)
	s.Contains(annotations[0].Method, "dropped_unknown_events=1")
}

func (s *annotateSuite) TestNilJudgeIsRejected() {
	cases := []groupmembench.ManifestCase{participantCase("case-7", "User_1", "groupmembench-User_1")}
	_, err := groupmembench.Annotate(context.Background(), cases, nil, nil)
	s.Require().Error(err)
}
