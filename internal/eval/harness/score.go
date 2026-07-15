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
	return Score{
		Arm: arm, Expected: expected, Answer: answer, Exact: exact, SafeSuccess: exact, TokenF1: tokenF1(expected, answer),
		SessionID: output.SessionID, InputTokens: output.InputTokens,
		OutputTokens: output.OutputTokens, Cost: output.Cost,
	}
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
