package qa

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"
)

type AnswerJudgeSuite struct {
	suite.Suite
	judge DeterministicAnswerJudge
}

func TestAnswerJudgeSuite(t *testing.T) {
	suite.Run(t, new(AnswerJudgeSuite))
}

func (s *AnswerJudgeSuite) TestJudgesTypedAnswers() {
	tests := []struct {
		name       string
		request    AnswerJudgmentRequest
		correct    bool
		verdict    string
		score      float64
		reasonCode string
	}{
		{
			name: "unanswerable alias",
			request: AnswerJudgmentRequest{
				Expected: "The information provided is not enough. You mentioned living in Harajuku but not Shinjuku.",
				Actual:   "unknown", Kind: AnswerUnanswerable,
			},
			correct: true, verdict: "correct", score: 1, reasonCode: "abstention_match",
		},
		{
			name: "unanswerable rejects asserted fact",
			request: AnswerJudgmentRequest{
				Expected: "There is not enough information.", Actual: "Shinjuku",
				Kind: AnswerUnanswerable,
			},
			correct: false, verdict: "incorrect", score: 0, reasonCode: "expected_abstention",
		},
		{
			name: "fact allows answer in a sentence",
			request: AnswerJudgmentRequest{
				Expected: "local-first", Actual: "The architecture is local-first.",
				Kind: AnswerFact,
			},
			correct: true, verdict: "correct", score: 1, reasonCode: "normalized_match",
		},
		{
			name: "numeric ignores formatting",
			request: AnswerJudgmentRequest{
				Expected: "1,234", Actual: "1234", Kind: AnswerNumeric,
			},
			correct: true, verdict: "correct", score: 1, reasonCode: "numeric_match",
		},
		{
			name: "list is order independent",
			request: AnswerJudgmentRequest{
				Expected: "red, blue", Actual: "blue and red", Kind: AnswerList,
			},
			correct: true, verdict: "correct", score: 1, reasonCode: "list_match",
		},
		{
			name: "list reports partial credit",
			request: AnswerJudgmentRequest{
				Expected: "red, blue", Actual: "red", Kind: AnswerList,
			},
			correct: false, verdict: "partial", score: 0.5, reasonCode: "partial_list_match",
		},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			judgment, err := s.judge.Judge(context.Background(), test.request)
			s.Require().NoError(err)
			s.Equal(test.correct, judgment.Correct)
			s.Equal(test.verdict, judgment.Verdict)
			s.InDelta(test.score, judgment.Score, 0.0001)
			s.Equal(test.reasonCode, judgment.ReasonCode)
		})
	}
}

func (s *AnswerJudgeSuite) TestHasStableIdentity() {
	s.Equal("deterministic-answer-judge:v1", s.judge.ID())
}
