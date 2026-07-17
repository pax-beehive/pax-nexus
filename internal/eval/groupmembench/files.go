package groupmembench

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

type Manifest struct {
	Protocol             string         `json:"protocol,omitempty"`
	Dataset              string         `json:"dataset"`
	DatasetRevision      string         `json:"dataset_revision"`
	Domain               string         `json:"domain"`
	Seed                 string         `json:"seed"`
	DomainSessionBatches string         `json:"domain_session_batches,omitempty"`
	FullDomainMessages   int            `json:"full_domain_messages,omitempty"`
	Cases                []ManifestCase `json:"cases"`
}

type ManifestCase struct {
	ID                  string   `json:"id"`
	Category            string   `json:"category"`
	Question            string   `json:"question"`
	Answer              string   `json:"answer"`
	AskingUserID        string   `json:"asking_user_id"`
	ScopeID             string   `json:"scope_id"`
	ContextMessages     int      `json:"context_messages"`
	IngestMode          string   `json:"ingest_mode"`
	SessionBatches      int      `json:"session_batches"`
	Actors              int      `json:"actors"`
	ParticipantAgentIDs []string `json:"participant_agent_ids,omitempty"`
	SupportingAgentIDs  []string `json:"supporting_agent_ids,omitempty"`
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
		batches, actorCount, err := nativeSessionBatches(caseID, evalCase.Messages)
		if err != nil {
			return fmt.Errorf("prepare GroupMemBench native sessions %q: %w", caseID, err)
		}
		if err := writeJSON(filepath.Join(caseDirectory, "producer", "session-batches.json"), batches); err != nil {
			return fmt.Errorf("write GroupMemBench native sessions %q: %w", caseID, err)
		}
		consumer := []byte("# Consumer workspace\n\nThis workspace intentionally contains no GroupMemBench source messages.\n")
		if err := os.WriteFile(filepath.Join(caseDirectory, "consumer", "README.md"), consumer, 0o644); err != nil {
			return fmt.Errorf("write consumer workspace %q: %w", caseID, err)
		}
		manifest.Cases = append(manifest.Cases, ManifestCase{
			ID: caseID, Category: evalCase.Category, Question: evalCase.Question.Question,
			Answer: evalCase.Question.Answer, AskingUserID: evalCase.Question.AskingUserID,
			ScopeID: "groupmembench-" + caseID, ContextMessages: len(evalCase.Messages),
			IngestMode: "native_sessions", SessionBatches: len(batches), Actors: actorCount,
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

// WriteV3Cases writes one immutable full-domain history plus question-only
// cases. Every arm reuses the same domain batches; questions never select
// source messages before ingestion.
func WriteV3Cases(directory, revision, domain, seed string, cases []Case, messages []Message) error {
	if strings.TrimSpace(directory) == "" || strings.TrimSpace(revision) == "" ||
		strings.TrimSpace(domain) == "" || len(cases) == 0 || len(messages) == 0 {
		return fmt.Errorf("write GroupMemBench v3 cases: directory, revision, domain, cases, and messages are required")
	}
	chronological, err := chronologicalMessages(messages)
	if err != nil {
		return fmt.Errorf("write GroupMemBench v3 cases: %w", err)
	}
	messages = chronological
	domainProducer := filepath.Join(directory, "domain", "producer")
	if err := os.MkdirAll(domainProducer, 0o755); err != nil {
		return fmt.Errorf("create GroupMemBench v3 domain workspace: %w", err)
	}
	if err := writeSource(filepath.Join(domainProducer, "source.md"), messages); err != nil {
		return err
	}
	batches, actorCount, err := nativeSessionBatches("domain-"+safeCaseID(domain), messages)
	if err != nil {
		return fmt.Errorf("prepare GroupMemBench v3 domain sessions: %w", err)
	}
	if err := writeJSON(filepath.Join(domainProducer, "session-batches.json"), batches); err != nil {
		return fmt.Errorf("write GroupMemBench v3 domain sessions: %w", err)
	}
	participants := participantAgentIDs(messages)
	manifest := Manifest{
		Protocol: "multi-agent-groupmembench-v3", Dataset: "GroupMemBench", DatasetRevision: revision,
		Domain: domain, Seed: seed, DomainSessionBatches: "domain/producer/session-batches.json",
		FullDomainMessages: len(messages),
	}
	for _, evalCase := range cases {
		caseID := safeCaseID(evalCase.Question.ID)
		consumerDirectory := filepath.Join(directory, "cases", caseID, "consumer")
		if err := os.MkdirAll(consumerDirectory, 0o755); err != nil {
			return fmt.Errorf("create GroupMemBench v3 consumer workspace %q: %w", caseID, err)
		}
		consumer := []byte("# Consumer workspace\n\nThis workspace intentionally contains no GroupMemBench source messages.\n")
		if err := os.WriteFile(filepath.Join(consumerDirectory, "README.md"), consumer, 0o644); err != nil {
			return fmt.Errorf("write GroupMemBench v3 consumer workspace %q: %w", caseID, err)
		}
		manifest.Cases = append(manifest.Cases, ManifestCase{
			ID: caseID, Category: evalCase.Category, Question: evalCase.Question.Question,
			Answer: evalCase.Question.Answer, AskingUserID: evalCase.Question.AskingUserID,
			ScopeID: "groupmembench-" + safeCaseID(domain), IngestMode: "full_domain_native_sessions",
			ContextMessages: len(messages), SessionBatches: len(batches), Actors: actorCount,
			ParticipantAgentIDs: append([]string(nil), participants...),
		})
	}
	smoke, err := smokeManifest(manifest)
	if err != nil && len(cases) >= len(categories) {
		return err
	}
	if err := writeJSON(filepath.Join(directory, "manifest.json"), manifest); err != nil {
		return err
	}
	if err == nil {
		return writeJSON(filepath.Join(directory, "manifest.smoke.json"), smoke)
	}
	return nil
}

func chronologicalMessages(messages []Message) ([]Message, error) {
	type timedMessage struct {
		message Message
		time    time.Time
	}
	timed := make([]timedMessage, 0, len(messages))
	for _, message := range messages {
		occurredAt, err := parseTimestamp(message.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("order message %q: %w", message.NodeID, err)
		}
		timed = append(timed, timedMessage{message: message, time: occurredAt})
	}
	slices.SortStableFunc(timed, func(left, right timedMessage) int {
		if comparison := left.time.Compare(right.time); comparison != 0 {
			return comparison
		}
		return strings.Compare(left.message.NodeID, right.message.NodeID)
	})
	result := make([]Message, 0, len(timed))
	for _, current := range timed {
		result = append(result, current.message)
	}
	return result, nil
}

func participantAgentIDs(messages []Message) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, message := range messages {
		agentID := "groupmembench-" + safeCaseID(message.Author)
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		result = append(result, agentID)
	}
	slices.Sort(result)
	return result
}

func nativeSessionBatches(caseID string, messages []Message) ([]session.SessionBatch, int, error) {
	type batchState struct {
		index int
		actor session.Actor
	}
	batches := make([]session.SessionBatch, 0)
	states := make(map[string]batchState)
	actors := make(map[string]struct{})
	for _, message := range messages {
		occurredAt, err := parseTimestamp(message.Timestamp)
		if err != nil {
			return nil, 0, fmt.Errorf("write GroupMemBench session batch %q message %q: %w", caseID, message.NodeID, err)
		}
		key := strings.Join([]string{message.Author, message.Channel, message.PhaseName}, "\x00")
		state, found := states[key]
		if !found {
			actor := session.Actor{
				UserID:    message.Author,
				AgentID:   "groupmembench-" + safeCaseID(message.Author),
				SessionID: nativeSessionID(caseID, key),
			}
			state = batchState{index: len(batches), actor: actor}
			states[key] = state
			batches = append(batches, session.SessionBatch{Complete: true})
			actors[actor.AgentID] = struct{}{}
		}
		metadata := map[string]string{
			"role": message.Role, "channel": message.Channel, "phase": message.PhaseName,
			"topic": message.Topic, "decision_point": fmt.Sprintf("%t", message.IsDecisionPoint),
			"noise": fmt.Sprintf("%t", message.IsNoise),
		}
		if message.ReplyTo != "" {
			metadata["reply_to"] = message.ReplyTo
		}
		if len(message.DecisionChangeMetadata) > 0 {
			encoded, err := json.Marshal(message.DecisionChangeMetadata)
			if err != nil {
				return nil, 0, fmt.Errorf("encode message %q decision change metadata: %w", message.NodeID, err)
			}
			metadata["decision_change_metadata"] = string(encoded)
		}
		sequence := int64(len(batches[state.index].Events) + 1)
		batches[state.index].Events = append(batches[state.index].Events, session.SessionEvent{
			ID: message.NodeID, Actor: state.actor, Sequence: sequence, Type: "message", Content: message.Content,
			Visibility: "team_note_eligible",
			OccurredAt: occurredAt, CapturedAt: occurredAt, Metadata: metadata,
		})
	}
	return batches, len(actors), nil
}

func parseTimestamp(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05"} {
		var (
			parsed time.Time
			err    error
		)
		if layout == time.RFC3339Nano {
			parsed, err = time.Parse(layout, value)
		} else {
			parsed, err = time.ParseInLocation(layout, value, time.UTC)
		}
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("timestamp %q must use RFC3339 format", value)
}

func nativeSessionID(caseID, key string) string {
	digest := sha256.Sum256([]byte(caseID + "\x00" + key))
	return fmt.Sprintf("groupmembench-%s-%x", safeCaseID(caseID), digest[:8])
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
