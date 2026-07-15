package groupmembench_test

import (
	"fmt"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
	"github.com/stretchr/testify/suite"
)

type selectorSuite struct {
	suite.Suite
}

func TestSelectorSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(selectorSuite))
}

func (s *selectorSuite) TestSelectsDeterministicBalancedCases() {
	questions := map[string][]groupmembench.Question{}
	for _, category := range groupmembench.Categories() {
		questions[category] = []groupmembench.Question{
			{ID: category + "_1", Question: "first", Answer: "one", AskingUserID: "User_1"},
			{ID: category + "_2", Question: "second", Answer: "two", AskingUserID: "User_2"},
			{ID: category + "_3", Question: "third", Answer: "three", AskingUserID: "User_3"},
		}
	}
	messages := []groupmembench.Message{{NodeID: "Msg_1", Channel: "channel", Content: "first second third"}}
	config := groupmembench.Config{PerCategory: 2, TopK: 1, Seed: "fixed"}

	first, err := groupmembench.Select(questions, messages, config)
	s.Require().NoError(err)
	second, err := groupmembench.Select(questions, messages, config)
	s.Require().NoError(err)
	s.Equal(first, second)
	s.Len(first, 12)
	counts := make(map[string]int)
	for _, selected := range first {
		counts[selected.Category]++
	}
	for _, category := range groupmembench.Categories() {
		s.Equal(2, counts[category])
	}
}

func (s *selectorSuite) TestRetrievesMessagesAndConversationContext() {
	messages := []groupmembench.Message{
		{NodeID: "Msg_1", Channel: "risk", Content: "The old deadline is July 10.", Timestamp: "2026-07-01T10:00:00"},
		{NodeID: "Msg_2", Channel: "risk", Content: "The final launch deadline is July 18.", ReplyTo: "Msg_1", Timestamp: "2026-07-02T10:00:00"},
		{NodeID: "Msg_3", Channel: "risk", Content: "Finance acknowledged the updated date.", Timestamp: "2026-07-03T10:00:00"},
		{NodeID: "Msg_4", Channel: "noise", Content: "Lunch is available downstairs.", Timestamp: "2026-07-04T10:00:00"},
	}
	questions := map[string][]groupmembench.Question{}
	for _, category := range groupmembench.Categories() {
		questions[category] = []groupmembench.Question{{
			ID: category + "_1", Question: "What is the final launch deadline?", Answer: "July 18", AskingUserID: "User_7",
		}}
	}

	selected, err := groupmembench.Select(questions, messages, groupmembench.Config{
		PerCategory: 1, TopK: 1, NeighborRadius: 1, Seed: "fixed",
	})
	s.Require().NoError(err)
	for _, evalCase := range selected {
		ids := make([]string, 0, len(evalCase.Messages))
		for _, message := range evalCase.Messages {
			ids = append(ids, message.NodeID)
		}
		s.Contains(ids, "Msg_2")
		s.Contains(ids, "Msg_1")
		s.Contains(ids, "Msg_3")
		s.NotContains(ids, "Msg_4")
	}
}

func (s *selectorSuite) TestBoundsContextWhileKeepingTopHits() {
	messages := make([]groupmembench.Message, 0, 20)
	for index := range 20 {
		messages = append(messages, groupmembench.Message{
			NodeID: fmt.Sprintf("Msg_%02d", index), Channel: "channel",
			Content: "ordinary context", Timestamp: fmt.Sprintf("2026-07-%02dT10:00:00", index+1),
		})
	}
	messages[10].Content = "unique launch deadline"
	questions := completeQuestions()
	for category := range questions {
		questions[category][0].Question = "unique launch deadline"
	}
	selected, err := groupmembench.Select(questions, messages, groupmembench.Config{
		PerCategory: 1, TopK: 2, NeighborRadius: 5, MaxContextMessages: 4,
	})
	s.Require().NoError(err)
	for _, evalCase := range selected {
		s.Len(evalCase.Messages, 4)
		ids := make([]string, 0, len(evalCase.Messages))
		for _, message := range evalCase.Messages {
			ids = append(ids, message.NodeID)
		}
		s.Contains(ids, "Msg_10")
	}
}

func (s *selectorSuite) TestRejectsIncompleteInputs() {
	tests := []struct {
		name      string
		questions map[string][]groupmembench.Question
		messages  []groupmembench.Message
		config    groupmembench.Config
	}{
		{name: "missing messages", questions: map[string][]groupmembench.Question{"multi_hop": {{ID: "one"}}}},
		{name: "missing categories", messages: []groupmembench.Message{{NodeID: "Msg_1", Content: "value"}}},
		{name: "invalid limits", questions: completeQuestions(), messages: []groupmembench.Message{{NodeID: "Msg_1", Content: "value"}}, config: groupmembench.Config{PerCategory: -1}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := groupmembench.Select(test.questions, test.messages, test.config)
			s.Require().Error(err)
		})
	}
}

func completeQuestions() map[string][]groupmembench.Question {
	result := make(map[string][]groupmembench.Question)
	for _, category := range groupmembench.Categories() {
		result[category] = []groupmembench.Question{{ID: category, Question: category, Answer: category, AskingUserID: "User"}}
	}
	return result
}
