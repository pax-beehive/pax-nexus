package groupmembench_test

import (
	"context"
	"errors"
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
}

func newStubJudge() *stubJudge {
	return &stubJudge{
		responses: make(map[string]groupmembench.JudgeResponse),
		errs:      make(map[string]error),
		calls:     make(map[string]int),
	}
}

func (s *stubJudge) SupportingEvents(_ context.Context, request groupmembench.JudgeRequest) (groupmembench.JudgeResponse, error) {
	s.calls[request.CaseID]++
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

func (s *annotateSuite) TestNilJudgeIsRejected() {
	cases := []groupmembench.ManifestCase{participantCase("case-7", "User_1", "groupmembench-User_1")}
	_, err := groupmembench.Annotate(context.Background(), cases, nil, nil)
	s.Require().Error(err)
}
