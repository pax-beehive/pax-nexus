package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
	"github.com/stretchr/testify/suite"
)

type selectMainSuite struct {
	suite.Suite
}

func TestSelectMainSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(selectMainSuite))
}

// writeFullDomainFixture writes a minimal but valid conversation.json and
// question directory that -mode full-domain can select from: one question
// per GroupMemBench category, two authors in a single channel.
func writeFullDomainFixture(t *testing.T, directory string) (conversationPath, questionsDirectory string) {
	t.Helper()
	messages := map[string][]groupmembench.Message{
		"finance": {
			{NodeID: "Msg_1", Author: "User_1", Role: "Lead", Content: "first", Timestamp: "2026-07-01T10:00:00Z"},
			{NodeID: "Msg_2", Author: "User_2", Role: "Analyst", Content: "second", Timestamp: "2026-07-01T10:01:00Z"},
		},
	}
	conversationPath = filepath.Join(directory, "conversation.json")
	writeJSONFixture(t, conversationPath, messages)

	questionsDirectory = filepath.Join(directory, "questions")
	if err := os.MkdirAll(questionsDirectory, 0o755); err != nil {
		t.Fatalf("create questions directory: %v", err)
	}
	for _, category := range groupmembench.Categories() {
		question := groupmembench.Question{
			ID: category + "_1", Question: "What happened in " + category + "?",
			Answer: "the " + category + " answer", AskingUserID: "User_1",
		}
		encoded, err := json.Marshal(question)
		if err != nil {
			t.Fatalf("encode question fixture: %v", err)
		}
		path := filepath.Join(questionsDirectory, category+".jsonl")
		if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
			t.Fatalf("write question fixture %q: %v", path, err)
		}
	}
	return conversationPath, questionsDirectory
}

func writeJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("encode fixture %q: %v", path, err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatalf("write fixture %q: %v", path, err)
	}
}

func baseFullDomainArgs(conversationPath, questionsDirectory, output string) []string {
	return []string{
		"-mode", "full-domain",
		"-conversation", conversationPath,
		"-questions", questionsDirectory,
		"-output", output,
		"-domain", "Finance",
		"-revision", "test-revision",
		"-seed", "test-seed",
		"-per-category", "1",
	}
}

func (s *selectMainSuite) TestRunWithoutAnnotationsFlagOmitsSupportingAgentIDsKey() {
	directory := s.T().TempDir()
	conversationPath, questionsDirectory := writeFullDomainFixture(s.T(), directory)
	output := filepath.Join(directory, "out")

	s.Require().NoError(run(baseFullDomainArgs(conversationPath, questionsDirectory, output), observability.DiscardLogger()))

	raw, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	s.Require().NoError(err)
	s.NotContains(string(raw), "supporting_agent_ids", "absent -annotations, the manifest must carry no supporting_agent_ids key at all")
}

func (s *selectMainSuite) TestRunIsDeterministicWithoutAnnotations() {
	directory := s.T().TempDir()
	conversationPath, questionsDirectory := writeFullDomainFixture(s.T(), directory)
	outputA := filepath.Join(directory, "out-a")
	outputB := filepath.Join(directory, "out-b")

	s.Require().NoError(run(baseFullDomainArgs(conversationPath, questionsDirectory, outputA), observability.DiscardLogger()))
	s.Require().NoError(run(baseFullDomainArgs(conversationPath, questionsDirectory, outputB), observability.DiscardLogger()))

	rawA, err := os.ReadFile(filepath.Join(outputA, "manifest.json"))
	s.Require().NoError(err)
	rawB, err := os.ReadFile(filepath.Join(outputB, "manifest.json"))
	s.Require().NoError(err)
	s.Equal(string(rawA), string(rawB), "two runs with identical inputs and no -annotations flag must produce byte-identical manifests")
}

func (s *selectMainSuite) TestRunWithAnnotationsMergesHighConfidenceSupportingAgentIDs() {
	directory := s.T().TempDir()
	conversationPath, questionsDirectory := writeFullDomainFixture(s.T(), directory)
	output := filepath.Join(directory, "out")
	s.Require().NoError(run(baseFullDomainArgs(conversationPath, questionsDirectory, output), observability.DiscardLogger()))

	manifest := readManifestFixture(s.T(), filepath.Join(output, "manifest.json"))
	s.Require().NotEmpty(manifest.Cases)
	targetCaseID := manifest.Cases[0].ID

	annotationsPath := filepath.Join(directory, "annotations.json")
	writeJSONFixture(s.T(), annotationsPath, []groupmembench.Annotation{
		{CaseID: targetCaseID, SupportingAgentIDs: []string{"groupmembench-User_2"}, SupportingEventIDs: []string{"Msg_2"}, Confidence: groupmembench.ConfidenceHigh, Method: "test"},
	})

	args := append(baseFullDomainArgs(conversationPath, questionsDirectory, output), "-annotations", annotationsPath)
	s.Require().NoError(run(args, observability.DiscardLogger()))

	merged := readManifestFixture(s.T(), filepath.Join(output, "manifest.json"))
	found := false
	for _, evalCase := range merged.Cases {
		if evalCase.ID != targetCaseID {
			continue
		}
		found = true
		s.Equal([]string{"groupmembench-User_2"}, evalCase.SupportingAgentIDs)
	}
	s.True(found, "annotated case must still be present in the merged manifest")
}

func readManifestFixture(t *testing.T, path string) groupmembench.Manifest {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest %q: %v", path, err)
	}
	var manifest groupmembench.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest %q: %v", path, err)
	}
	return manifest
}

func (s *selectMainSuite) TestMergeAnnotationsAddsHighConfidenceSupportingAgentIDs() {
	directory := s.T().TempDir()
	writeManifestFixture(s.T(), directory, []groupmembench.ManifestCase{
		{ID: "multi_hop_1", Category: "multi_hop"},
		{ID: "abstention_1", Category: "abstention"},
	})
	annotationsPath := filepath.Join(directory, "annotations.json")
	writeJSONFixture(s.T(), annotationsPath, []groupmembench.Annotation{
		{CaseID: "multi_hop_1", SupportingAgentIDs: []string{"groupmembench-User_3", "groupmembench-User_9"}, Confidence: groupmembench.ConfidenceHigh},
	})

	annotated, withheld, unmatched, err := mergeAnnotations(directory, annotationsPath)
	s.Require().NoError(err)
	s.Equal(1, annotated)
	s.Equal(0, withheld)
	s.Equal(0, unmatched)

	manifest := readManifestFixture(s.T(), filepath.Join(directory, "manifest.json"))
	s.Equal([]string{"groupmembench-User_3", "groupmembench-User_9"}, manifest.Cases[0].SupportingAgentIDs)
	s.Empty(manifest.Cases[1].SupportingAgentIDs)
}

func (s *selectMainSuite) TestMergeAnnotationsWithholdsLowConfidence() {
	directory := s.T().TempDir()
	writeManifestFixture(s.T(), directory, []groupmembench.ManifestCase{
		{ID: "abstention_1", Category: "abstention"},
	})
	annotationsPath := filepath.Join(directory, "annotations.json")
	writeJSONFixture(s.T(), annotationsPath, []groupmembench.Annotation{
		{CaseID: "abstention_1", SupportingAgentIDs: []string{"groupmembench-User_3"}, Confidence: groupmembench.ConfidenceLow},
	})

	annotated, withheld, unmatched, err := mergeAnnotations(directory, annotationsPath)
	s.Require().NoError(err)
	s.Equal(0, annotated, "a low-confidence annotation must never be emitted into the manifest")
	s.Equal(1, withheld)
	s.Equal(0, unmatched)

	manifest := readManifestFixture(s.T(), filepath.Join(directory, "manifest.json"))
	s.Empty(manifest.Cases[0].SupportingAgentIDs)
	s.NotContains(mustReadFile(s.T(), filepath.Join(directory, "manifest.json")), "supporting_agent_ids")
}

func (s *selectMainSuite) TestMergeAnnotationsReportsUnmatchedCaseID() {
	directory := s.T().TempDir()
	writeManifestFixture(s.T(), directory, []groupmembench.ManifestCase{
		{ID: "multi_hop_1", Category: "multi_hop"},
	})
	annotationsPath := filepath.Join(directory, "annotations.json")
	writeJSONFixture(s.T(), annotationsPath, []groupmembench.Annotation{
		{CaseID: "not_a_real_case", SupportingAgentIDs: []string{"groupmembench-User_3"}, Confidence: groupmembench.ConfidenceHigh},
	})

	annotated, withheld, unmatched, err := mergeAnnotations(directory, annotationsPath)
	s.Require().NoError(err)
	s.Equal(0, annotated)
	s.Equal(0, withheld)
	s.Equal(1, unmatched, "an annotation for a case ID outside the selection must be reported as unmatched")

	manifest := readManifestFixture(s.T(), filepath.Join(directory, "manifest.json"))
	s.Empty(manifest.Cases[0].SupportingAgentIDs)
}

func writeManifestFixture(t *testing.T, directory string, cases []groupmembench.ManifestCase) {
	t.Helper()
	manifest := groupmembench.Manifest{
		Protocol: "multi-agent-groupmembench-v3", Dataset: "GroupMemBench",
		DatasetRevision: "revision", Domain: "Finance", Seed: "seed", Cases: cases,
	}
	writeJSONFixture(t, filepath.Join(directory, "manifest.json"), manifest)
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(raw)
}
