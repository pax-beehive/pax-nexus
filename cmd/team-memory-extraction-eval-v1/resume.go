package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/pax-beehive/pax-nexus/internal/eval/extractioneval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
)

const resumeConfigSchemaVersion = "pax-extraction-eval-resume-v1"

type resumeConfig struct {
	SchemaVersion    string                    `json:"schema_version"`
	RunID            string                    `json:"run_id"`
	SourceRunID      string                    `json:"source_run_id"`
	ExtractorVersion string                    `json:"extractor_version"`
	V2Variant        string                    `json:"v2_variant"`
	BaseURL          string                    `json:"base_url"`
	Model            string                    `json:"model"`
	PromptVersion    string                    `json:"prompt_version"`
	ProfileName      string                    `json:"profile_name"`
	SliceEventLimit  int                       `json:"slice_event_limit"`
	ExecutionPolicy  extractor.ExecutionPolicy `json:"execution_policy"`
	Domains          []resumeDomain            `json:"domains"`
}

type resumeDomain struct {
	ScopeID  string   `json:"scope_id"`
	EventIDs []string `json:"event_ids"`
}

func ensureResumeConfig(
	config evalConfig,
	profile extractioneval.Profile,
	prepared []preparedDomain,
) error {
	desired := resumeConfig{
		SchemaVersion: resumeConfigSchemaVersion, RunID: config.runID, SourceRunID: config.sourceRunID,
		ExtractorVersion: config.extractorVersion, V2Variant: config.v2Variant,
		BaseURL: config.baseURL, Model: config.model, PromptVersion: config.promptVersion,
		ProfileName: profile.Name, SliceEventLimit: config.sliceEventLimit,
		ExecutionPolicy: config.executionPolicy,
	}
	for _, domain := range prepared {
		desired.Domains = append(desired.Domains, resumeDomain{
			ScopeID: domain.plan.ScopeID, EventIDs: append([]string(nil), domain.selection.EventIDs...),
		})
	}
	path := filepath.Join(config.outputDir, "resume-config.json")
	if !config.resume {
		if err := os.MkdirAll(config.outputDir, 0o755); err != nil {
			return fmt.Errorf("create extraction eval resume directory: %w", err)
		}
		return writeFile(path, func(writer io.Writer) error {
			return writeJSON(writer, desired)
		})
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read extraction eval resume config: %w", err)
	}
	var saved resumeConfig
	if err := json.Unmarshal(data, &saved); err != nil {
		return fmt.Errorf("decode extraction eval resume config: %w", err)
	}
	if !reflect.DeepEqual(saved, desired) {
		return fmt.Errorf("validate extraction eval resume config: run inputs changed")
	}
	return nil
}
