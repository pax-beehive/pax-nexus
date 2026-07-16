package harness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
)

type AgentOutput struct {
	SessionID    string  `json:"session_id"`
	Text         string  `json:"text"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Cost         float64 `json:"cost"`
}

type Score struct {
	Arm          string  `json:"arm"`
	Expected     string  `json:"expected"`
	Answer       string  `json:"answer"`
	Exact        bool    `json:"exact"`
	SafeSuccess  bool    `json:"safe_success"`
	TokenF1      float64 `json:"token_f1"`
	SessionID    string  `json:"session_id"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Cost         float64 `json:"cost"`
}

type GroupMemBenchJudgment struct {
	Correct      bool
	Text         string
	SessionID    string
	InputTokens  int
	OutputTokens int
	Cost         float64
}

func ParseOpenCodeJSON(input io.Reader) (AgentOutput, error) {
	var output AgentOutput
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionID"`
			Part      struct {
				Text   string  `json:"text"`
				Cost   float64 `json:"cost"`
				Tokens struct {
					Input  int `json:"input"`
					Output int `json:"output"`
				} `json:"tokens"`
			} `json:"part"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.SessionID != "" {
			output.SessionID = event.SessionID
		}
		switch event.Type {
		case "text":
			text := strings.TrimSpace(event.Part.Text)
			if text != "" {
				output.Text = strings.TrimSpace(strings.Join([]string{output.Text, text}, "\n"))
			}
		case "step_finish":
			output.InputTokens += event.Part.Tokens.Input
			output.OutputTokens += event.Part.Tokens.Output
			output.Cost += event.Part.Cost
		}
	}
	if err := scanner.Err(); err != nil {
		return AgentOutput{}, fmt.Errorf("scan OpenCode output: %w", err)
	}
	if strings.TrimSpace(output.Text) == "" {
		return AgentOutput{}, fmt.Errorf("OpenCode output contains no text")
	}
	return output, nil
}

func ScoreExact(arm, expected string, output AgentOutput) Score {
	expected = strings.TrimSpace(expected)
	answer := strings.TrimSpace(output.Text)
	exact := strings.EqualFold(answer, expected)
	safeSuccess := exact || (isAbstention(expected) && isAbstention(answer))
	return Score{
		Arm: arm, Expected: expected, Answer: answer, Exact: exact, SafeSuccess: safeSuccess, TokenF1: tokenF1(expected, answer),
		SessionID: output.SessionID, InputTokens: output.InputTokens,
		OutputTokens: output.OutputTokens, Cost: output.Cost,
	}
}

func ParseGroupMemBenchJudgment(output AgentOutput) (GroupMemBenchJudgment, error) {
	verdicts := make([]bool, 0, 1)
	for _, line := range strings.Split(output.Text, "\n") {
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "final: correct":
			verdicts = append(verdicts, true)
		case "final: incorrect":
			verdicts = append(verdicts, false)
		}
	}
	if len(verdicts) != 1 {
		return GroupMemBenchJudgment{}, fmt.Errorf("parse GroupMemBench judgment: expected exactly one final verdict")
	}
	return GroupMemBenchJudgment{
		Correct: verdicts[0], Text: output.Text, SessionID: output.SessionID,
		InputTokens: output.InputTokens, OutputTokens: output.OutputTokens, Cost: output.Cost,
	}, nil
}

func isAbstention(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	unsafeQualifiers := []string{
		" but ", " however", " although ", " while ", " even though ", " yet ", " despite ",
		" likely ", " probably ", " appears to be ", " suggests that ", " leads ", " owns ",
	}
	for _, qualifier := range unsafeQualifiers {
		if strings.Contains(value, qualifier) {
			return false
		}
	}
	markers := []string{
		"no information", "information unavailable", "information is unavailable",
		"cannot find", "can't find", "could not find", "couldn't find",
		"cannot determine", "can't determine", "could not determine", "couldn't determine",
		"cannot answer", "can't answer", "unable to answer", "not enough information",
		"insufficient information", "does not contain", "do not contain", "doesn't contain",
		"not present", "not mention", "does not mention", "do not mention", "doesn't mention",
		"not specified", "not provided", "no final decision", "has not been decided",
		"remains unresolved", "still unresolved", "no relevant", "no files", "no document",
		"don't have access", "do not have access", "doesn't have access",
	}
	foundMarker := false
	for _, statement := range splitStatements(value) {
		statementHasMarker := false
		for _, marker := range markers {
			if strings.Contains(statement, marker) {
				foundMarker = true
				statementHasMarker = true
				break
			}
		}
		if statementHasMarker || containsAnyText(statement,
			"could you", "please provide", "point me", "provide more context", "clarify which", "clarify what") {
			continue
		}
		return false
	}
	return foundMarker
}

func splitStatements(value string) []string {
	runes := []rune(value)
	statements := make([]string, 0, 2)
	start := 0
	for index, character := range runes {
		if character != '.' && character != '!' && character != '?' && character != ';' && character != '\n' {
			continue
		}
		if character != '\n' && index+1 < len(runes) && !unicode.IsSpace(runes[index+1]) {
			continue
		}
		if statement := strings.TrimSpace(string(runes[start:index])); statement != "" {
			statements = append(statements, statement)
		}
		start = index + 1
	}
	if statement := strings.TrimSpace(string(runes[start:])); statement != "" {
		statements = append(statements, statement)
	}
	return statements
}

func containsAnyText(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func tokenF1(expected, answer string) float64 {
	expectedCounts := tokenCounts(expected)
	answerCounts := tokenCounts(answer)
	common := 0
	for token, expectedCount := range expectedCounts {
		common += min(expectedCount, answerCounts[token])
	}
	expectedTotal := countTokens(expectedCounts)
	answerTotal := countTokens(answerCounts)
	if expectedTotal == 0 || answerTotal == 0 || common == 0 {
		return 0
	}
	precision := float64(common) / float64(answerTotal)
	recall := float64(common) / float64(expectedTotal)
	return 2 * precision * recall / (precision + recall)
}

func tokenCounts(value string) map[string]int {
	counts := make(map[string]int)
	for _, token := range strings.FieldsFunc(strings.ToLower(value), func(current rune) bool {
		return !unicode.IsLetter(current) && !unicode.IsNumber(current)
	}) {
		counts[token]++
	}
	return counts
}

func countTokens(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}
