package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/extractioneval"
	"github.com/pax-beehive/pax-nexus/internal/eval/extractionshadow"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
	"github.com/stretchr/testify/suite"
)

type commandSuite struct{ suite.Suite }

func TestCommandSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(commandSuite))
}

func (s *commandSuite) TestParseFlagsUsesIndependentDefaultOutput() {
	config, err := parseFlags([]string{
		"-dsn", "postgres://example", "-manifest", "manifest.json", "-fixtures", "fixtures.json",
		"-source-run-id", "source-run", "-run-id", "eval-run", "-extractor-base-url", "https://example.test",
		"-extractor-model", "model",
	})
	s.Require().NoError(err)
	s.Equal(filepath.Join("runs", "extraction-eval-v1", "eval-run"), config.outputDir)
	s.Equal("v2", config.promptVersion)
	s.Equal(extractor.DefaultCandidateStrategy(), config.v2Variant)
}

func (s *commandSuite) TestParseFlagsAcceptsCandidateStrategy() {
	config, err := parseFlags([]string{
		"-dsn", "postgres://example", "-manifest", "manifest.json", "-fixtures", "fixtures.json",
		"-source-run-id", "source-run", "-run-id", "eval-run", "-extractor-base-url", "https://example.test",
		"-extractor-model", "model", "-candidate-strategy", extractor.CandidateStrategyTyped2,
	})

	s.Require().NoError(err)
	s.Equal(extractor.CandidateStrategyTyped2, config.v2Variant)
}

func (s *commandSuite) TestParseFlagsRequiresSourceIdentity() {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing source identity", args: []string{"-dsn", "postgres://example"}},
		{name: "missing provider configuration", args: []string{
			"-dsn", "postgres://example", "-manifest", "manifest.json", "-fixtures", "fixtures.json",
			"-source-run-id", "source-run", "-run-id", "eval-run",
		}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := parseFlags(test.args)
			s.Require().Error(err)
		})
	}
}

func (s *commandSuite) TestPreflightOnlyDoesNotRequireProviderConfiguration() {
	config, err := parseFlags([]string{
		"-dsn", "postgres://example", "-manifest", "manifest.json", "-fixtures", "fixtures.json",
		"-source-run-id", "source-run", "-run-id", "preflight", "-preflight-only",
	})
	s.Require().NoError(err)
	s.True(config.preflightOnly)
}

func (s *commandSuite) TestWriteArtifactsFinalizesExistingJournalDirectory() {
	dir := s.T().TempDir()
	config := evalConfig{outputDir: filepath.Join(dir, "eval-run"), runID: "eval-run"}
	report := extractioneval.Report{SchemaVersion: extractioneval.SchemaVersion, RunID: "eval-run"}
	steps := []struct {
		name string
		runs []extractionshadow.CaseRun
	}{
		{name: "initial write", runs: []extractionshadow.CaseRun{{ScopeID: "scope"}}},
		{name: "finalize existing directory"},
	}
	for _, step := range steps {
		s.Run(step.name, func() {
			s.Require().NoError(writeArtifacts(config, report, step.runs))
			_, err := os.Stat(filepath.Join(config.outputDir, "report.json"))
			s.Require().NoError(err)
		})
	}
}

func (s *commandSuite) TestValidateOutputAvailable() {
	dir := s.T().TempDir()
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "new output", path: filepath.Join(dir, "new-run")},
		{name: "existing output", path: dir, wantErr: true},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			err := validateOutputAvailable(test.path)
			if test.wantErr {
				s.Require().Error(err)
				return
			}
			s.Require().NoError(err)
		})
	}
}

func (s *commandSuite) TestRunJournalResumesSlicesAndProviderCalls() {
	dir := filepath.Join(s.T().TempDir(), "run")
	journal, err := openRunJournal(dir, false)
	s.Require().NoError(err)
	slices := []struct {
		name   string
		record extractionshadow.SliceRecord
	}{
		{name: "completed slice", record: extractionshadow.SliceRecord{InputChecksum: "checksum", DurationMS: 123}},
		{name: "failed slice", record: extractionshadow.SliceRecord{InputChecksum: "failed-checksum", Error: "provider timeout"}},
	}
	for _, slice := range slices {
		s.Run(slice.name, func() {
			s.Require().NoError(journal.appendSlice("scope", slice.record))
		})
	}
	journal.observeProviderCall(extractor.ProviderCall{
		Type: extractor.ProviderCallPrimary, ScopeID: "scope", Usage: extractor.Usage{InputTokens: 10},
	})
	s.Require().NoError(journal.close())

	resumed, err := openRunJournal(dir, true)
	s.Require().NoError(err)
	s.Require().Len(resumed.initialSlices("scope"), 1)
	s.Equal("checksum", resumed.initialSlices("scope")[0].InputChecksum)
	s.Require().Len(resumed.callsForScope("scope"), 1)
	s.Equal(10, resumed.callsForScope("scope")[0].Usage.InputTokens)
	s.Require().NoError(resumed.close())
}

func (s *commandSuite) TestResumeRejectsChangedRunInputs() {
	dir := filepath.Join(s.T().TempDir(), "run")
	config := evalConfig{
		outputDir: dir, runID: "run", sourceRunID: "source", extractorVersion: "v2",
		v2Variant: extractor.V2VariantCurrent, baseURL: "https://example.test", model: "model",
		promptVersion: "v2", sliceEventLimit: 25,
	}
	profile := extractioneval.Profile{Name: "quick"}
	prepared := []preparedDomain{{
		plan:      extractioneval.DomainPlan{ScopeID: "scope"},
		selection: extractioneval.Selection{EventIDs: []string{"event-1"}},
	}}
	s.Require().NoError(ensureResumeConfig(config, profile, prepared))

	tests := []struct {
		name    string
		mutate  func(*evalConfig)
		wantErr bool
	}{
		{name: "unchanged inputs", mutate: func(candidate *evalConfig) { candidate.resume = true }},
		{name: "changed variant", wantErr: true, mutate: func(candidate *evalConfig) {
			candidate.resume = true
			candidate.v2Variant = extractor.V2VariantInteractionSlim
		}},
		{name: "changed model", wantErr: true, mutate: func(candidate *evalConfig) {
			candidate.resume = true
			candidate.model = "other-model"
		}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			candidate := config
			test.mutate(&candidate)
			err := ensureResumeConfig(candidate, profile, prepared)
			if test.wantErr {
				s.Require().Error(err)
				return
			}
			s.Require().NoError(err)
		})
	}
}
