package v2

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type manifest struct {
	DatasetRevision string         `json:"dataset_revision"`
	Cases           []manifestCase `json:"cases"`
}

type manifestCase struct {
	ID                  string   `json:"id"`
	Category            string   `json:"category"`
	Question            string   `json:"question"`
	Answer              string   `json:"answer"`
	AskingUserID        string   `json:"asking_user_id"`
	ScopeID             string   `json:"scope_id"`
	ParticipantAgentIDs []string `json:"participant_agent_ids,omitempty"`
	SupportingAgentIDs  []string `json:"supporting_agent_ids,omitempty"`
}

func LoadCases(path string) ([]Case, string, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read eval manifest: %w", err)
	}
	var source manifest
	if err := json.Unmarshal(input, &source); err != nil {
		return nil, "", fmt.Errorf("decode eval manifest: %w", err)
	}
	if strings.TrimSpace(source.DatasetRevision) == "" || len(source.Cases) == 0 {
		return nil, "", fmt.Errorf("decode eval manifest: dataset revision and cases are required")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, "", fmt.Errorf("resolve eval manifest directory: %w", err)
	}
	cases := make([]Case, 0, len(source.Cases))
	seen := make(map[string]struct{}, len(source.Cases))
	for _, item := range source.Cases {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Question) == "" || strings.TrimSpace(item.Answer) == "" || strings.TrimSpace(item.AskingUserID) == "" {
			return nil, "", fmt.Errorf("decode eval manifest: case required field is empty")
		}
		if _, exists := seen[item.ID]; exists {
			return nil, "", fmt.Errorf("decode eval manifest: duplicate case %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		cases = append(cases, Case{
			ID: item.ID, Category: item.Category, Question: item.Question, Expected: item.Answer,
			AskingUserID: item.AskingUserID, ScopeID: item.ScopeID,
			ParticipantAgentIDs: append([]string(nil), item.ParticipantAgentIDs...),
			SupportingAgentIDs:  append([]string(nil), item.SupportingAgentIDs...),
			ProducerWorkspace:   filepath.Join(base, "cases", item.ID, "producer"),
			ConsumerWorkspace:   filepath.Join(base, "cases", item.ID, "consumer"),
		})
	}
	return cases, source.DatasetRevision, nil
}
