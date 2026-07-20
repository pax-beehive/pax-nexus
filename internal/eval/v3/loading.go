package v3

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	"gopkg.in/yaml.v3"
)

const ManifestProtocol = "multi-agent-groupmembench-v3"

type manifestHeader struct {
	Protocol             string `json:"protocol"`
	DomainSessionBatches string `json:"domain_session_batches"`
	FullDomainMessages   int    `json:"full_domain_messages"`
	DatasetRevision      string `json:"dataset_revision"`
}

func LoadConfig(path string) (v2.Config, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return v2.Config{}, fmt.Errorf("read eval v3 config: %w", err)
	}
	var config v2.Config
	if err := yaml.Unmarshal(input, &config); err != nil {
		return v2.Config{}, fmt.Errorf("decode eval v3 config: %w", err)
	}
	if err := Validate(config); err != nil {
		return v2.Config{}, err
	}
	return config, nil
}

func LoadCases(path string, answererSeed string) ([]v2.Case, string, error) {
	if err := validateManifest(path); err != nil {
		return nil, "", err
	}
	cases, revision, err := v2.LoadCases(path)
	if err != nil {
		return nil, "", err
	}
	for _, evalCase := range cases {
		if len(evalCase.ParticipantAgentIDs) < 2 {
			return nil, "", fmt.Errorf("load eval v3 case %q: at least two participant agents are required", evalCase.ID)
		}
		if strings.TrimSpace(evalCase.ScopeID) == "" {
			return nil, "", fmt.Errorf("load eval v3 case %q: full-domain scope is required", evalCase.ID)
		}
	}
	assigned, err := AssignAnswerers(cases, revision, answererSeed)
	if err != nil {
		return nil, "", err
	}
	return assigned, revision, nil
}

func validateManifest(path string) error {
	input, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read eval v3 manifest: %w", err)
	}
	var header manifestHeader
	if err := json.Unmarshal(input, &header); err != nil {
		return fmt.Errorf("decode eval v3 manifest: %w", err)
	}
	if header.Protocol != ManifestProtocol {
		return fmt.Errorf("validate eval v3 manifest: protocol must be %q", ManifestProtocol)
	}
	if strings.TrimSpace(header.DomainSessionBatches) == "" || header.FullDomainMessages <= 0 {
		return fmt.Errorf("validate eval v3 manifest: full-domain session batches and message count are required")
	}
	batchesPath := header.DomainSessionBatches
	if !filepath.IsAbs(batchesPath) {
		batchesPath = filepath.Join(filepath.Dir(path), batchesPath)
	}
	if _, err := os.Stat(batchesPath); err != nil {
		return fmt.Errorf("validate eval v3 manifest domain session batches: %w", err)
	}
	return nil
}
