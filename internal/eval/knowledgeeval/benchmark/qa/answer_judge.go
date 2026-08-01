package qa

import (
	"context"
	"math"
	"strconv"
	"strings"
	"unicode"
)

type AnswerKind string

const (
	AnswerFact         AnswerKind = "fact"
	AnswerUnanswerable AnswerKind = "unanswerable"
	AnswerList         AnswerKind = "list"
	AnswerNumeric      AnswerKind = "numeric"
	AnswerTemporal     AnswerKind = "temporal"
)

type AnswerJudgmentRequest struct {
	Question        string
	Expected        string
	Actual          string
	Kind            AnswerKind
	DatasetCategory string
}

type AnswerJudgment struct {
	Correct    bool
	Score      float64
	Verdict    string
	Confidence float64
	Disputed   bool
	ReasonCode string
	Reason     string
}

type AnswerJudge interface {
	ID() string
	Judge(context.Context, AnswerJudgmentRequest) (AnswerJudgment, error)
}

type DeterministicAnswerJudge struct{}

func (DeterministicAnswerJudge) ID() string {
	return "deterministic-answer-judge:v1"
}

func (DeterministicAnswerJudge) Judge(
	_ context.Context,
	request AnswerJudgmentRequest,
) (AnswerJudgment, error) {
	switch answerKindOrDefault(request.Kind) {
	case AnswerUnanswerable:
		if isAbstention(request.Actual) {
			return correctJudgment("abstention_match"), nil
		}
		return incorrectJudgment("expected_abstention"), nil
	case AnswerList:
		return judgeList(request.Expected, request.Actual), nil
	case AnswerNumeric:
		return judgeNumeric(request.Expected, request.Actual), nil
	default:
		if answersMatch(request.Expected, request.Actual) {
			return correctJudgment("normalized_match"), nil
		}
		return incorrectJudgment("normalized_mismatch"), nil
	}
}

func answerKindOrDefault(kind AnswerKind) AnswerKind {
	if kind == "" {
		return AnswerFact
	}
	return kind
}

func isAbstention(answer string) bool {
	normalized := normalizeAnswer(answer)
	if normalized == "unknown" || normalized == "n a" || normalized == "i don t know" {
		return true
	}
	markers := []string{
		"not enough information",
		"insufficient information",
		"cannot determine",
		"can t determine",
		"not mentioned",
		"not provided",
		"no information",
	}
	for _, marker := range markers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func judgeList(expected, actual string) AnswerJudgment {
	expectedItems := answerSet(expected)
	actualItems := answerSet(actual)
	if len(expectedItems) == 0 {
		return incorrectJudgment("empty_expected_list")
	}
	matched := 0
	for item := range expectedItems {
		if _, exists := actualItems[item]; exists {
			matched++
		}
	}
	score := float64(matched) / float64(len(expectedItems))
	if matched == len(expectedItems) && len(actualItems) == len(expectedItems) {
		return correctJudgment("list_match")
	}
	if matched > 0 {
		return AnswerJudgment{
			Score: score, Verdict: "partial", Confidence: 1,
			ReasonCode: "partial_list_match",
		}
	}
	return incorrectJudgment("list_mismatch")
}

func answerSet(answer string) map[string]struct{} {
	normalized := strings.NewReplacer(" and ", ",", " 和 ", ",").Replace(strings.ToLower(answer))
	items := strings.FieldsFunc(normalized, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '、', '，', '；':
			return true
		default:
			return false
		}
	})
	result := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item = normalizeAnswer(item); item != "" {
			result[item] = struct{}{}
		}
	}
	return result
}

func judgeNumeric(expected, actual string) AnswerJudgment {
	expectedNumber, expectedOK := parseNumber(expected)
	actualNumber, actualOK := parseNumber(actual)
	if expectedOK && actualOK && math.Abs(expectedNumber-actualNumber) < 1e-9 {
		return correctJudgment("numeric_match")
	}
	return incorrectJudgment("numeric_mismatch")
}

func parseNumber(value string) (float64, bool) {
	normalized := strings.TrimSpace(strings.ReplaceAll(value, ",", ""))
	normalized = strings.TrimSuffix(normalized, "%")
	parsed, err := strconv.ParseFloat(normalized, 64)
	return parsed, err == nil
}

func answersMatch(expected, actual string) bool {
	normalizedExpected := normalizeAnswer(expected)
	normalizedActual := normalizeAnswer(actual)
	return normalizedExpected != "" && (normalizedActual == normalizedExpected ||
		strings.Contains(normalizedActual, normalizedExpected))
}

func normalizeAnswer(value string) string {
	var normalized strings.Builder
	spacePending := false
	for _, current := range strings.ToLower(value) {
		if unicode.IsLetter(current) || unicode.IsNumber(current) {
			if spacePending && normalized.Len() > 0 {
				normalized.WriteByte(' ')
			}
			normalized.WriteRune(current)
			spacePending = false
			continue
		}
		spacePending = true
	}
	return normalized.String()
}

func correctJudgment(reasonCode string) AnswerJudgment {
	return AnswerJudgment{
		Correct: true, Score: 1, Verdict: "correct", Confidence: 1,
		ReasonCode: reasonCode,
	}
}

func incorrectJudgment(reasonCode string) AnswerJudgment {
	return AnswerJudgment{
		Verdict: "incorrect", Confidence: 1, ReasonCode: reasonCode,
	}
}
