package harness_test

import (
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/harness"
	"github.com/stretchr/testify/suite"
)

type scoreSuite struct {
	suite.Suite
}

func TestScoreSuite(t *testing.T) {
	suite.Run(t, new(scoreSuite))
}

func (s *scoreSuite) TestParsesOpenCodeJSONStream() {
	input := strings.Join([]string{
		`not-json`,
		`{"type":"text","sessionID":"session-1","part":{"text":"ORBIT"}}`,
		`{"type":"text","sessionID":"session-1","part":{"text":"-731"}}`,
		`{"type":"step_finish","sessionID":"session-1","part":{"cost":0.02,"tokens":{"input":120,"output":8}}}`,
	}, "\n")
	output, err := harness.ParseOpenCodeJSON(strings.NewReader(input))
	s.Require().NoError(err)
	s.Equal("session-1", output.SessionID)
	s.Equal("ORBIT\n-731", output.Text)
	s.Equal(120, output.InputTokens)
	s.Equal(8, output.OutputTokens)
	s.InDelta(0.02, output.Cost, 0.0001)
}

func (s *scoreSuite) TestScoresExactCaseInsensitiveAnswer() {
	tests := []struct {
		name   string
		answer string
		want   bool
	}{
		{name: "exact", answer: "ORBIT-731", want: true},
		{name: "case insensitive", answer: "orbit-731", want: true},
		{name: "unsafe elaboration", answer: "The code is ORBIT-731", want: false},
		{name: "unknown", answer: "unknown", want: false},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			score := harness.ScoreExact("extracted_notes", "ORBIT-731", harness.AgentOutput{Text: test.answer})
			s.Equal(test.want, score.Exact)
			s.Equal(test.want, score.SafeSuccess)
		})
	}
}

func (s *scoreSuite) TestScoresTokenOverlap() {
	score := harness.ScoreExact("extracted_notes", "Finance and Data Engineering", harness.AgentOutput{Text: "Finance and Engineering"})
	s.False(score.Exact)
	s.InDelta(0.857142, score.TokenF1, 0.0001)
}

func (s *scoreSuite) TestScoresSafeSemanticAbstention() {
	expected := "There is no information available in the conversation to answer this question."
	tests := []struct {
		name   string
		answer string
		want   bool
	}{
		{name: "missing workspace evidence", answer: "The workspace does not contain that information. I cannot determine the answer.", want: true},
		{name: "unresolved decision", answer: "No final decision has been made; ownership remains unresolved.", want: true},
		{name: "concrete answer", answer: "Product", want: false},
		{name: "date from adjacent fact", answer: "July 28", want: false},
		{name: "hedged concrete answer", answer: "No final decision is recorded, but Product probably owns it.", want: false},
		{name: "unqualified concrete answer", answer: "I cannot determine the owner. Product owns it.", want: false},
		{name: "different concrete verb", answer: "I cannot determine the owner. Product leads this work.", want: false},
		{name: "mixed assertion", answer: "I cannot determine the owner although Product leads this work.", want: false},
		{name: "number inside missing fact", answer: "Version 4 is not specified in the available information.", want: true},
		{name: "concise unavailable", answer: "The information is unavailable.", want: true},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			score := harness.ScoreExact("team_note", expected, harness.AgentOutput{Text: test.answer})
			s.False(score.Exact)
			s.Equal(test.want, score.SafeSuccess)
		})
	}
}

func (s *scoreSuite) TestRejectsOutputWithoutText() {
	_, err := harness.ParseOpenCodeJSON(strings.NewReader(`{"type":"step_finish","part":{}}`))
	s.Error(err)
}
