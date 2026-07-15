package groupmembench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type Manifest struct {
	Dataset         string         `json:"dataset"`
	DatasetRevision string         `json:"dataset_revision"`
	Domain          string         `json:"domain"`
	Seed            string         `json:"seed"`
	Cases           []ManifestCase `json:"cases"`
}

type ManifestCase struct {
	ID              string `json:"id"`
	Category        string `json:"category"`
	Question        string `json:"question"`
	Answer          string `json:"answer"`
	AskingUserID    string `json:"asking_user_id"`
	ScopeID         string `json:"scope_id"`
	ContextMessages int    `json:"context_messages"`
}

func LoadConversation(path string) ([]Message, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open GroupMemBench conversation: %w", err)
	}
	var channels map[string][]Message
	if err := json.Unmarshal(input, &channels); err != nil {
		return nil, fmt.Errorf("decode GroupMemBench conversation: %w", err)
	}
	messages := make([]Message, 0)
	for channel, channelMessages := range channels {
		for index := range channelMessages {
			channelMessages[index].Channel = channel
			messages = append(messages, channelMessages[index])
		}
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("load GroupMemBench conversation: no messages")
	}
	return messages, nil
}

func LoadQuestions(directory string) (map[string][]Question, error) {
	result := make(map[string][]Question, len(categories))
	for _, category := range categories {
		questions, err := loadQuestionFile(filepath.Join(directory, category+".jsonl"))
		if err != nil {
			return nil, err
		}
		result[category] = questions
	}
	return result, nil
}

func WriteCases(directory, revision, domain, seed string, cases []Case) error {
	if strings.TrimSpace(directory) == "" || strings.TrimSpace(revision) == "" || strings.TrimSpace(domain) == "" || len(cases) == 0 {
		return fmt.Errorf("write GroupMemBench cases: directory, revision, domain, and cases are required")
	}
	manifest := Manifest{Dataset: "GroupMemBench", DatasetRevision: revision, Domain: domain, Seed: seed}
	for _, evalCase := range cases {
		caseID := safeCaseID(evalCase.Question.ID)
		caseDirectory := filepath.Join(directory, "cases", caseID)
		if err := os.MkdirAll(filepath.Join(caseDirectory, "producer"), 0o755); err != nil {
			return fmt.Errorf("create producer workspace %q: %w", caseID, err)
		}
		if err := os.MkdirAll(filepath.Join(caseDirectory, "consumer"), 0o755); err != nil {
			return fmt.Errorf("create consumer workspace %q: %w", caseID, err)
		}
		if err := writeSource(filepath.Join(caseDirectory, "producer", "source.md"), evalCase.Messages); err != nil {
			return err
		}
		consumer := []byte("# Consumer workspace\n\nThis workspace intentionally contains no GroupMemBench source messages.\n")
		if err := os.WriteFile(filepath.Join(caseDirectory, "consumer", "README.md"), consumer, 0o644); err != nil {
			return fmt.Errorf("write consumer workspace %q: %w", caseID, err)
		}
		manifest.Cases = append(manifest.Cases, ManifestCase{
			ID: caseID, Category: evalCase.Category, Question: evalCase.Question.Question,
			Answer: evalCase.Question.Answer, AskingUserID: evalCase.Question.AskingUserID,
			ScopeID: "groupmembench-" + caseID, ContextMessages: len(evalCase.Messages),
		})
	}
	smoke, err := smokeManifest(manifest)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(directory, "manifest.json"), manifest); err != nil {
		return err
	}
	return writeJSON(filepath.Join(directory, "manifest.smoke.json"), smoke)
}

func smokeManifest(source Manifest) (Manifest, error) {
	targets := []string{"multi_hop", "knowledge_update", "abstention"}
	smoke := source
	smoke.Cases = make([]ManifestCase, 0, len(targets))
	for _, target := range targets {
		found := false
		for _, evalCase := range source.Cases {
			if evalCase.Category == target {
				smoke.Cases = append(smoke.Cases, evalCase)
				found = true
				break
			}
		}
		if !found {
			return Manifest{}, fmt.Errorf("write GroupMemBench smoke profile: category %q is missing", target)
		}
	}
	return smoke, nil
}

func loadQuestionFile(path string) ([]Question, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open GroupMemBench questions %q: %w", filepath.Base(path), err)
	}
	return decodeQuestions(input, filepath.Base(path))
}

func decodeQuestions(input []byte, name string) ([]Question, error) {
	scanner := bufio.NewScanner(bytes.NewReader(input))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	questions := make([]Question, 0)
	for scanner.Scan() {
		var question Question
		if err := json.Unmarshal(scanner.Bytes(), &question); err != nil {
			return nil, fmt.Errorf("decode GroupMemBench question %q: %w", name, err)
		}
		if strings.TrimSpace(question.ID) == "" || strings.TrimSpace(question.Question) == "" || strings.TrimSpace(question.Answer) == "" || strings.TrimSpace(question.AskingUserID) == "" {
			return nil, fmt.Errorf("decode GroupMemBench question %q: required field is empty", name)
		}
		questions = append(questions, question)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan GroupMemBench questions %q: %w", name, err)
	}
	return questions, nil
}

func writeSource(path string, messages []Message) error {
	var output strings.Builder
	output.WriteString("# GroupMemBench retrieved conversation context\n\n")
	for _, message := range messages {
		fmt.Fprintf(&output, "## %s\n\n", message.NodeID)
		fmt.Fprintf(&output, "- Channel: %s\n- Author: %s\n- Role: %s\n- Timestamp: %s\n", message.Channel, message.Author, message.Role, message.Timestamp)
		if message.ReplyTo != "" {
			fmt.Fprintf(&output, "- Reply to: %s\n", message.ReplyTo)
		}
		fmt.Fprintf(&output, "- Phase: %s\n- Topic: %s\n- Decision point: %t\n\n%s\n\n", message.PhaseName, message.Topic, message.IsDecisionPoint, message.Content)
	}
	if err := os.WriteFile(path, []byte(output.String()), 0o644); err != nil {
		return fmt.Errorf("write GroupMemBench source: %w", err)
	}
	return nil
}

func writeJSON(path string, value any) error {
	output, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create GroupMemBench manifest: %w", err)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(value)
	closeErr := output.Close()
	if encodeErr != nil {
		return fmt.Errorf("encode GroupMemBench manifest: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close GroupMemBench manifest: %w", closeErr)
	}
	return nil
}

func safeCaseID(value string) string {
	return strings.Map(func(current rune) rune {
		if unicode.IsLetter(current) || unicode.IsNumber(current) || current == '-' || current == '_' {
			return current
		}
		return '-'
	}, value)
}
