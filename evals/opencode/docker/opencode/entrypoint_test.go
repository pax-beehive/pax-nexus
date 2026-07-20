package opencode_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"
)

type entrypointSuite struct{ suite.Suite }

func TestEntrypointSuite(t *testing.T) { suite.Run(t, new(entrypointSuite)) }

func (s *entrypointSuite) TestConsumerKeepsAnswerPolicyOutOfRecallPrompt() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	directory := s.T().TempDir()
	binDirectory := filepath.Join(directory, "bin")
	s.Require().NoError(os.Mkdir(binDirectory, 0o700))
	capture := filepath.Join(directory, "docker-args")
	docker := filepath.Join(binDirectory, "docker")
	s.Require().NoError(os.WriteFile(docker, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_CAPTURE\"\n"), 0o700))

	question := "What is the current approach?"
	command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v2-opencode.sh"), "consumer", "team_note")
	command.Dir = repositoryRoot
	command.Env = []string{
		"PATH=" + binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DOCKER_CAPTURE=" + capture,
		"PAX_EVAL_RUN_ID=consumer-query-test",
		"PAX_EVAL_CASE_ID=case-1",
		"PAX_EVAL_SCOPE_ID=scope-1",
		"PAX_EVAL_USER_ID=eval-owner",
		"PAX_EVAL_CONSUMER_WORKSPACE=" + directory,
		"PAX_EVAL_QUESTION=" + question,
		"OPENCODE_MODEL=deepseek/deepseek-v4-flash",
		"TEAM_MEMORY_API_KEYS={}",
	}
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))
	input, err := os.ReadFile(capture)
	s.Require().NoError(err)
	arguments := strings.Split(strings.TrimSpace(string(input)), "\n")
	s.Equal(question, arguments[len(arguments)-1])
	s.Contains(arguments, "PAXM_EVAL_CONSUMER_POLICY=1")
	s.Contains(arguments, "PAXM_EVAL_RECALL_MODE=passive")
	s.Contains(arguments, "PAXM_AGENT_ID=groupmembench-eval-owner")
	s.Contains(arguments, "--agent")
	s.Contains(arguments, "eval-consumer")
}

func (s *entrypointSuite) TestEvalV3ConsumerKeepsPairedAnswererAndArmTopology() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	tests := []struct {
		name          string
		arm           string
		providerType  string
		recall        string
		providerAgent string
	}{
		{name: "no memory", arm: "no_memory_team", providerType: "team-memory", recall: "0", providerAgent: "groupmembench-User_3"},
		{name: "shared mem0", arm: "groupmembench_mem0", providerType: "mem0", recall: "1", providerAgent: "groupmembench-shared-agent"},
		{name: "private plus team", arm: "private_sqlite_plus_team_note", providerType: "team-memory-sqlite", recall: "1", providerAgent: "groupmembench-User_3"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			directory := s.T().TempDir()
			binDirectory := filepath.Join(directory, "bin")
			s.Require().NoError(os.Mkdir(binDirectory, 0o700))
			capture := filepath.Join(directory, "docker-args")
			docker := filepath.Join(binDirectory, "docker")
			s.Require().NoError(os.WriteFile(docker, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_CAPTURE\"\n"), 0o700))
			privateDirectory := filepath.Join(directory, "memory", "private")
			s.Require().NoError(os.MkdirAll(privateDirectory, 0o700))
			if test.arm == "private_sqlite_plus_team_note" {
				s.Require().NoError(os.WriteFile(filepath.Join(privateDirectory, "groupmembench-User_3.sqlite"), nil, 0o600))
			}
			command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v3-opencode.sh"), "consumer", test.arm)
			command.Dir = repositoryRoot
			command.Env = []string{
				"PATH=" + binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
				"DOCKER_CAPTURE=" + capture,
				"PAX_EVAL_RUN_ID=eval-v3-test", "PAX_EVAL_CASE_ID=case-1",
				"PAX_EVAL_SCOPE_ID=groupmembench-Finance",
				"PAX_EVAL_USER_ID=User_1", "PAX_EVAL_ASKING_USER_ID=User_1",
				"PAX_EVAL_ANSWERING_AGENT_ID=groupmembench-User_3",
				"PAX_EVAL_CONSUMER_WORKSPACE=" + directory, "PAX_EVAL_OUTPUT_DIR=" + directory,
				"PAX_EVAL_QUESTION=What changed?", "OPENCODE_MODEL=deepseek/deepseek-v4-flash",
				"TEAM_MEMORY_API_KEYS={}",
			}
			output, err := command.CombinedOutput()
			s.Require().NoError(err, string(output))
			input, err := os.ReadFile(capture)
			s.Require().NoError(err)
			arguments := strings.Split(strings.TrimSpace(string(input)), "\n")
			s.Contains(arguments, "PAXM_AGENT_ID=groupmembench-User_3")
			s.Contains(arguments, "PAXM_PROVIDER_TYPE="+test.providerType)
			s.Contains(arguments, "PAXM_RECALL_ENABLED="+test.recall)
			s.Contains(arguments, "PAXM_PROVIDER_AGENT_ID="+test.providerAgent)
		})
	}
}

func (s *entrypointSuite) TestEvalV3ValidatesFullDomainIngestReceipts() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	tests := []struct {
		name       string
		mem0Writes int
		wantError  bool
	}{
		{name: "complete receipts", mem0Writes: 2},
		{name: "zero Mem0 writes", wantError: true},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			directory := s.T().TempDir()
			memoryDirectory := filepath.Join(directory, "memory")
			s.Require().NoError(os.MkdirAll(memoryDirectory, 0o700))
			manifest := filepath.Join(directory, "manifest.json")
			s.Require().NoError(os.WriteFile(manifest, []byte(`{"full_domain_messages":2}`), 0o600))
			receipts := map[string]string{
				"team-note-ingest.json":      `{"provider":"team_note","accepted":2,"duplicate":0,"source_events":2}`,
				"mem0-ingest.json":           `{"provider":"mem0_messages","accepted":2,"created":` + fmt.Sprint(test.mem0Writes) + `,"updated":0,"deleted":0,"source_events":2}`,
				"private-sqlite-ingest.json": `{"provider":"private_sqlite","accepted":2,"created":2,"source_events":2}`,
			}
			for name, receipt := range receipts {
				s.Require().NoError(os.WriteFile(filepath.Join(memoryDirectory, name), []byte(receipt), 0o600))
			}
			command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v3-opencode.sh"), "validate-receipts")
			command.Dir = repositoryRoot
			command.Env = append(os.Environ(),
				"PAX_EVAL_RUN_ID=receipt-test",
				"PAX_EVAL_MANIFEST="+manifest,
				"PAX_EVAL_OUTPUT_DIR="+directory,
			)
			output, runErr := command.CombinedOutput()
			if test.wantError {
				s.Error(runErr, string(output))
				return
			}
			s.NoError(runErr, string(output))
		})
	}
}

func (s *entrypointSuite) TestHybridArmEnablesBoundedActiveRecall() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	directory := s.T().TempDir()
	binDirectory := filepath.Join(directory, "bin")
	s.Require().NoError(os.Mkdir(binDirectory, 0o700))
	capture := filepath.Join(directory, "docker-args")
	docker := filepath.Join(binDirectory, "docker")
	s.Require().NoError(os.WriteFile(docker, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_CAPTURE\"\n"), 0o700))

	command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v2-opencode.sh"), "consumer", "team_note_hybrid")
	command.Dir = repositoryRoot
	command.Env = []string{
		"PATH=" + binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DOCKER_CAPTURE=" + capture,
		"PAX_EVAL_RUN_ID=hybrid-query-test",
		"PAX_EVAL_CASE_ID=case-1",
		"PAX_EVAL_SCOPE_ID=scope-1",
		"PAX_EVAL_USER_ID=eval-owner",
		"PAX_EVAL_CONSUMER_WORKSPACE=" + directory,
		"PAX_EVAL_QUESTION=What is the current approach?",
		"OPENCODE_MODEL=deepseek/deepseek-v4-flash",
		"TEAM_MEMORY_API_KEYS={}",
	}
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))
	input, err := os.ReadFile(capture)
	s.Require().NoError(err)
	arguments := strings.Split(strings.TrimSpace(string(input)), "\n")
	s.Contains(arguments, "PAXM_PROVIDER_TYPE=team-memory")
	s.Contains(arguments, "PAXM_RECALL_ENABLED=1")
	s.Contains(arguments, "PAXM_EVAL_RECALL_MODE=hybrid")
	s.Contains(arguments, "PAXM_ACTIVE_RECALL_MAX_CALLS=2")
}

func (s *entrypointSuite) TestDirectContextArmSuppliesRawConversationWithoutRecall() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	directory := s.T().TempDir()
	producerWorkspace := filepath.Join(directory, "producer")
	s.Require().NoError(os.Mkdir(producerWorkspace, 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(producerWorkspace, "source.md"),
		[]byte("Ops Lead owns the rollback evidence pack."),
		0o600,
	))
	binDirectory := filepath.Join(directory, "bin")
	s.Require().NoError(os.Mkdir(binDirectory, 0o700))
	capture := filepath.Join(directory, "docker-args")
	docker := filepath.Join(binDirectory, "docker")
	s.Require().NoError(os.WriteFile(docker, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_CAPTURE\"\n"), 0o700))

	command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v2-opencode.sh"), "consumer", "direct_context")
	command.Dir = repositoryRoot
	command.Env = []string{
		"PATH=" + binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DOCKER_CAPTURE=" + capture,
		"PAX_EVAL_RUN_ID=direct-context-test",
		"PAX_EVAL_CASE_ID=case-1",
		"PAX_EVAL_SCOPE_ID=scope-1",
		"PAX_EVAL_USER_ID=User_3",
		"PAX_EVAL_PRODUCER_WORKSPACE=" + producerWorkspace,
		"PAX_EVAL_CONSUMER_WORKSPACE=" + directory,
		"PAX_EVAL_QUESTION=Who owns the rollback evidence pack?",
		"OPENCODE_MODEL=deepseek/deepseek-v4-flash",
		"TEAM_MEMORY_API_KEYS={}",
	}
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))
	input, err := os.ReadFile(capture)
	s.Require().NoError(err)
	arguments := strings.Split(strings.TrimSpace(string(input)), "\n")
	s.Contains(arguments, "PAXM_RECALL_ENABLED=0")
	s.Contains(arguments, "PAXM_EVAL_RECALL_MODE=direct")
	s.Contains(arguments, "Asking user: User_3")
	s.Contains(arguments, "Ops Lead owns the rollback evidence pack.")
}

func (s *entrypointSuite) TestZepNativeArmSuppliesProcessedNativeContextWithoutPaxmRecall() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	directory := s.T().TempDir()
	batches := filepath.Join(directory, "session-batches.json")
	s.Require().NoError(os.WriteFile(batches, []byte("[]"), 0o600))
	artifactDirectory := filepath.Join(directory, "artifacts")
	binDirectory := filepath.Join(directory, "bin")
	s.Require().NoError(os.Mkdir(binDirectory, 0o700))
	dockerCapture := filepath.Join(directory, "docker-args")
	goCapture := filepath.Join(directory, "go-args")
	docker := filepath.Join(binDirectory, "docker")
	goCommand := filepath.Join(binDirectory, "go")
	s.Require().NoError(os.WriteFile(docker, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_CAPTURE\"\n"), 0o700))
	goScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GO_CAPTURE\"\nprintf '%s\\n' '{\"context\":\"Zep processed evidence.\",\"episodes\":1,\"processed\":1}'\n"
	s.Require().NoError(os.WriteFile(goCommand, []byte(goScript), 0o700))

	command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v2-opencode.sh"), "consumer", "zep_native")
	command.Dir = repositoryRoot
	command.Env = []string{
		"PATH=" + binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DOCKER_CAPTURE=" + dockerCapture,
		"GO_CAPTURE=" + goCapture,
		"ZEP_API_KEY=test-zep-key",
		"PAX_EVAL_RUN_ID=zep-native-test",
		"PAX_EVAL_CASE_ID=case-1",
		"PAX_EVAL_SCOPE_ID=scope-1",
		"PAX_EVAL_USER_ID=User_3",
		"PAX_EVAL_CONSUMER_WORKSPACE=" + directory,
		"PAX_EVAL_SESSION_BATCHES_FILE=" + batches,
		"PAX_EVAL_ARTIFACT_DIR=" + artifactDirectory,
		"PAX_EVAL_QUESTION=Who owns the rollback evidence?",
		"OPENCODE_MODEL=deepseek/deepseek-v4-flash",
		"TEAM_MEMORY_API_KEYS={}",
	}
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))
	goArguments, err := os.ReadFile(goCapture)
	s.Require().NoError(err)
	s.Contains(string(goArguments), "./cmd/eval-v2-zep")
	s.Contains(string(goArguments), "-action")
	s.Contains(string(goArguments), "search")
	s.Contains(string(goArguments), "zep-eval-zep-native-test-case-1")
	s.NotContains(strings.Split(strings.TrimSpace(string(goArguments)), "\n"), "opencode")
	dockerArguments, err := os.ReadFile(dockerCapture)
	s.Require().NoError(err)
	s.Contains(string(dockerArguments), "PAXM_RECALL_ENABLED=0")
	s.Contains(string(dockerArguments), "PAXM_EVAL_RECALL_MODE=direct")
	s.Contains(string(dockerArguments), "Zep processed evidence.")
	artifact, err := os.ReadFile(filepath.Join(artifactDirectory, "zep-native.json"))
	s.Require().NoError(err)
	s.JSONEq(`{"context":"Zep processed evidence.","episodes":1,"processed":1}`, string(artifact))
	summary, err := os.ReadFile(filepath.Join(artifactDirectory, "zep-native-summary.json"))
	s.Require().NoError(err)
	s.JSONEq(`{"provider":null,"episodes":1,"processed":1,"context_characters":23}`, string(summary))
}

func (s *entrypointSuite) TestZepNativeSearchFailureWritesDiagnosticArtifact() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	directory := s.T().TempDir()
	artifactDirectory := filepath.Join(directory, "artifacts")
	binDirectory := filepath.Join(directory, "bin")
	s.Require().NoError(os.Mkdir(binDirectory, 0o700))
	goCommand := filepath.Join(binDirectory, "go")
	goScript := "#!/bin/sh\necho 'simulated Zep TLS failure' >&2\nexit 7\n"
	s.Require().NoError(os.WriteFile(goCommand, []byte(goScript), 0o700))

	command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v2-opencode.sh"), "consumer", "zep_native")
	command.Dir = repositoryRoot
	command.Env = []string{
		"PATH=" + binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
		"ZEP_API_KEY=test-zep-key",
		"PAX_EVAL_RUN_ID=zep-native-failure-test",
		"PAX_EVAL_CASE_ID=case-1",
		"PAX_EVAL_SCOPE_ID=scope-1",
		"PAX_EVAL_USER_ID=User_3",
		"PAX_EVAL_ARTIFACT_DIR=" + artifactDirectory,
		"PAX_EVAL_QUESTION=Who owns the rollback evidence?",
		"TEAM_MEMORY_API_KEYS={}",
	}
	output, err := command.CombinedOutput()
	s.Require().Error(err, string(output))
	artifact, err := os.ReadFile(filepath.Join(artifactDirectory, "zep-native.json"))
	s.Require().NoError(err)
	s.JSONEq(`{"status":"failed","action":"search","exit_code":7,"stdout":"","stderr":"simulated Zep TLS failure\n"}`, string(artifact))
}

func (s *entrypointSuite) TestZepNativeReadinessRequiresAllIngestedEpisodes() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	directory := s.T().TempDir()
	artifactDirectory := filepath.Join(directory, "artifacts")
	s.Require().NoError(os.MkdirAll(artifactDirectory, 0o700))
	s.Require().NoError(os.WriteFile(filepath.Join(artifactDirectory, "ingest.log"), []byte(`{"accepted":2}`), 0o600))
	binDirectory := filepath.Join(directory, "bin")
	s.Require().NoError(os.Mkdir(binDirectory, 0o700))
	goCapture := filepath.Join(directory, "go-args")
	goCommand := filepath.Join(binDirectory, "go")
	goScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GO_CAPTURE\"\nprintf '%s\\n' '{\"episodes\":2,\"processed\":2}'\n"
	s.Require().NoError(os.WriteFile(goCommand, []byte(goScript), 0o700))

	command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v2-opencode.sh"), "ready", "zep_native")
	command.Dir = repositoryRoot
	command.Env = []string{
		"PATH=" + binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GO_CAPTURE=" + goCapture,
		"ZEP_API_KEY=test-zep-key",
		"PAX_EVAL_RUN_ID=zep-ready-test",
		"PAX_EVAL_CASE_ID=case-1",
		"PAX_EVAL_SCOPE_ID=scope-1",
		"PAX_EVAL_USER_ID=User_3",
		"PAX_EVAL_ARTIFACT_DIR=" + artifactDirectory,
		"PAX_EVAL_ZEP_READINESS_ATTEMPTS=1",
		"TEAM_MEMORY_API_KEYS={}",
	}
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))
	goArguments, err := os.ReadFile(goCapture)
	s.Require().NoError(err)
	s.Contains(string(goArguments), "./cmd/eval-v2-zep")
	s.Contains(string(goArguments), "-action")
	s.Contains(string(goArguments), "ready")
	s.Contains(string(goArguments), "zep-eval-zep-ready-test-case-1")
	artifact, err := os.ReadFile(filepath.Join(artifactDirectory, "zep-readiness.json"))
	s.Require().NoError(err)
	s.JSONEq(`{"episodes":2,"processed":2,"accepted":2,"attempts":1}`, string(artifact))
}

func (s *entrypointSuite) TestZepNativeReadinessAcceptsCompleteEpisodesWithoutProcessingField() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	directory := s.T().TempDir()
	artifactDirectory := filepath.Join(directory, "artifacts")
	s.Require().NoError(os.MkdirAll(artifactDirectory, 0o700))
	s.Require().NoError(os.WriteFile(filepath.Join(artifactDirectory, "ingest.log"), []byte(`{"accepted":2}`), 0o600))
	binDirectory := filepath.Join(directory, "bin")
	s.Require().NoError(os.Mkdir(binDirectory, 0o700))
	goCommand := filepath.Join(binDirectory, "go")
	goScript := "#!/bin/sh\nprintf '%s\\n' '{\"episodes\":2,\"episode_ids\":[\"episode-1\",\"episode-2\"]}'\n"
	s.Require().NoError(os.WriteFile(goCommand, []byte(goScript), 0o700))

	command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v2-opencode.sh"), "ready", "zep_native")
	command.Dir = repositoryRoot
	command.Env = []string{
		"PATH=" + binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
		"ZEP_API_KEY=test-zep-key",
		"PAX_EVAL_RUN_ID=zep-ready-unreported-test",
		"PAX_EVAL_CASE_ID=case-1",
		"PAX_EVAL_SCOPE_ID=scope-1",
		"PAX_EVAL_USER_ID=User_3",
		"PAX_EVAL_ARTIFACT_DIR=" + artifactDirectory,
		"PAX_EVAL_ZEP_READINESS_ATTEMPTS=1",
		"TEAM_MEMORY_API_KEYS={}",
	}
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))
	artifact, err := os.ReadFile(filepath.Join(artifactDirectory, "zep-readiness.json"))
	s.Require().NoError(err)
	s.JSONEq(`{"episodes":2,"episode_ids":["episode-1","episode-2"],"accepted":2,"attempts":1,"processing_status":"unreported"}`, string(artifact))
}

func (s *entrypointSuite) TestMem0UsesSharedIdentityAcrossAskingUsers() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	tests := []struct {
		name         string
		askingUserID string
		caseID       string
	}{
		{name: "first asking user", askingUserID: "User_3", caseID: "case-1"},
		{name: "second asking user", askingUserID: "User_12", caseID: "case-2"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			directory := s.T().TempDir()
			binDirectory := filepath.Join(directory, "bin")
			s.Require().NoError(os.Mkdir(binDirectory, 0o700))
			capture := filepath.Join(directory, "docker-args")
			docker := filepath.Join(binDirectory, "docker")
			s.Require().NoError(os.WriteFile(docker, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_CAPTURE\"\n"), 0o700))

			command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v2-opencode.sh"), "consumer", "mem0")
			command.Dir = repositoryRoot
			command.Env = []string{
				"PATH=" + binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
				"DOCKER_CAPTURE=" + capture,
				"PAX_EVAL_RUN_ID=shared-mem0-identity-test",
				"PAX_EVAL_CASE_ID=" + test.caseID,
				"PAX_EVAL_SCOPE_ID=scope-1",
				"PAX_EVAL_USER_ID=" + test.askingUserID,
				"PAX_EVAL_CONSUMER_WORKSPACE=" + directory,
				"PAX_EVAL_QUESTION=Who am I relying on?",
				"MEM0_EVAL_USER_ID=groupmembench-shared-user",
				"MEM0_EVAL_AGENT_ID=groupmembench-shared-agent",
				"OPENCODE_MODEL=deepseek/deepseek-v4-flash",
				"TEAM_MEMORY_API_KEYS={}",
			}
			output, runErr := command.CombinedOutput()
			s.Require().NoError(runErr, string(output))
			input, readErr := os.ReadFile(capture)
			s.Require().NoError(readErr)
			arguments := strings.Split(strings.TrimSpace(string(input)), "\n")
			s.Contains(arguments, "PAXM_USER_ID=groupmembench-shared-user")
			s.Contains(arguments, "PAXM_AGENT_ID=groupmembench-shared-agent")
			s.Contains(arguments, "MEM0_RUN_ID=shared-mem0-identity-test-"+test.caseID)
			s.NotContains(arguments, "PAXM_USER_ID="+test.askingUserID)
		})
	}
}

func (s *entrypointSuite) TestIngestUsesNativeSessionBatchArtifact() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	directory := s.T().TempDir()
	binDirectory := filepath.Join(directory, "bin")
	s.Require().NoError(os.Mkdir(binDirectory, 0o700))
	capture := filepath.Join(directory, "docker-args")
	docker := filepath.Join(binDirectory, "docker")
	s.Require().NoError(os.WriteFile(docker, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_CAPTURE\"\n"), 0o700))
	batches := filepath.Join(directory, "session-batches.json")
	s.Require().NoError(os.WriteFile(batches, []byte("[]"), 0o600))

	command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v2-opencode.sh"), "ingest", "team_note")
	command.Dir = repositoryRoot
	command.Env = []string{
		"PATH=" + binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DOCKER_CAPTURE=" + capture,
		"PAX_EVAL_RUN_ID=native-ingest-test",
		"PAX_EVAL_CASE_ID=case-1",
		"PAX_EVAL_SCOPE_ID=scope-1",
		"PAX_EVAL_USER_ID=User_3",
		"PAX_EVAL_SESSION_BATCHES_FILE=" + batches,
		"TEAM_MEMORY_API_KEYS={}",
	}
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))
	input, err := os.ReadFile(capture)
	s.Require().NoError(err)
	arguments := strings.Split(strings.TrimSpace(string(input)), "\n")
	s.Contains(arguments, "PAXM_AGENT_ID=groupmembench-User_3")
	s.Contains(arguments, "-session-batches-file")
	s.Contains(arguments, "/artifact/session-batches.json")
	s.NotContains(arguments, "-text-file")
}

func (s *entrypointSuite) TestHybridIngestUsesIsolatedTeamNoteScope() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	directory := s.T().TempDir()
	binDirectory := filepath.Join(directory, "bin")
	s.Require().NoError(os.Mkdir(binDirectory, 0o700))
	capture := filepath.Join(directory, "docker-args")
	docker := filepath.Join(binDirectory, "docker")
	s.Require().NoError(os.WriteFile(docker, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_CAPTURE\"\n"), 0o700))
	batches := filepath.Join(directory, "session-batches.json")
	s.Require().NoError(os.WriteFile(batches, []byte("[]"), 0o600))

	command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v2-opencode.sh"), "ingest", "team_note_hybrid")
	command.Dir = repositoryRoot
	command.Env = []string{
		"PATH=" + binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DOCKER_CAPTURE=" + capture,
		"PAX_EVAL_RUN_ID=hybrid-ingest-test",
		"PAX_EVAL_CASE_ID=case-1",
		"PAX_EVAL_SCOPE_ID=scope-1",
		"PAX_EVAL_USER_ID=User_3",
		"PAX_EVAL_SESSION_BATCHES_FILE=" + batches,
		"TEAM_MEMORY_API_KEYS={}",
	}
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))
	input, err := os.ReadFile(capture)
	s.Require().NoError(err)
	arguments := strings.Split(strings.TrimSpace(string(input)), "\n")
	s.Contains(arguments, "TEAM_MEMORY_API_KEY=eval-hybrid-ingest-test-case-1-team-note-hybrid")
	s.Contains(arguments, "team_note")
	s.NotContains(arguments, "team_note_hybrid")
}

func (s *entrypointSuite) TestEvalStackProvisionsIsolatedHybridScope() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	directory := s.T().TempDir()
	binDirectory := filepath.Join(directory, "bin")
	s.Require().NoError(os.Mkdir(binDirectory, 0o700))
	capture := filepath.Join(directory, "key-maps")
	docker := filepath.Join(binDirectory, "docker")
	s.Require().NoError(os.WriteFile(docker, []byte("#!/bin/sh\nprintf '%s\\n' \"$TEAM_MEMORY_API_KEYS\" >> \"$DOCKER_CAPTURE\"\n"), 0o700))
	manifest := filepath.Join(directory, "manifest.json")
	s.Require().NoError(os.WriteFile(manifest, []byte(`{"cases":[{"id":"case-1","scope_id":"scope-1"}]}`), 0o600))

	command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v2-stack.sh"), "up", manifest, "scope-test")
	command.Dir = repositoryRoot
	command.Env = []string{
		"PATH=" + binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DOCKER_CAPTURE=" + capture,
		"EVAL_V2_ENV_FILE=" + filepath.Join(directory, "missing.env"),
		"MEM0_OPENAI_API_KEY=test-mem0-key",
	}
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))
	input, err := os.ReadFile(capture)
	s.Require().NoError(err)
	first := strings.Split(strings.TrimSpace(string(input)), "\n")[0]
	var keyMap map[string]string
	s.Require().NoError(json.Unmarshal([]byte(first), &keyMap))
	s.Equal("scope-test-scope-1", keyMap["eval-scope-test-case-1"])
	s.Equal("scope-test-scope-1-team-note-hybrid", keyMap["eval-scope-test-case-1-team-note-hybrid"])
}

func (s *entrypointSuite) TestMem0IngestUsesSharedIdentity() {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	s.Require().NoError(err)
	directory := s.T().TempDir()
	binDirectory := filepath.Join(directory, "bin")
	s.Require().NoError(os.Mkdir(binDirectory, 0o700))
	capture := filepath.Join(directory, "docker-args")
	docker := filepath.Join(binDirectory, "docker")
	s.Require().NoError(os.WriteFile(docker, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_CAPTURE\"\n"), 0o700))
	batches := filepath.Join(directory, "session-batches.json")
	s.Require().NoError(os.WriteFile(batches, []byte("[]"), 0o600))

	command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "eval-v2-opencode.sh"), "ingest", "mem0")
	command.Dir = repositoryRoot
	command.Env = []string{
		"PATH=" + binDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DOCKER_CAPTURE=" + capture,
		"PAX_EVAL_RUN_ID=shared-mem0-ingest-test",
		"PAX_EVAL_CASE_ID=case-1",
		"PAX_EVAL_SCOPE_ID=scope-1",
		"PAX_EVAL_USER_ID=User_3",
		"PAX_EVAL_SESSION_BATCHES_FILE=" + batches,
		"MEM0_EVAL_USER_ID=groupmembench-shared-user",
		"MEM0_EVAL_AGENT_ID=groupmembench-shared-agent",
		"TEAM_MEMORY_API_KEYS={}",
	}
	output, runErr := command.CombinedOutput()
	s.Require().NoError(runErr, string(output))
	input, readErr := os.ReadFile(capture)
	s.Require().NoError(readErr)
	arguments := strings.Split(strings.TrimSpace(string(input)), "\n")
	s.Contains(arguments, "PAXM_USER_ID=groupmembench-shared-user")
	s.Contains(arguments, "PAXM_AGENT_ID=groupmembench-shared-agent")
	s.Contains(arguments, "MEM0_RUN_ID=shared-mem0-ingest-test-case-1")
}

func (s *entrypointSuite) TestConsumerPolicyUsesDedicatedOpenCodeAgent() {
	directory := s.T().TempDir()
	plugin := filepath.Join(directory, "paxm.js")
	s.Require().NoError(os.WriteFile(plugin, []byte("// test plugin"), 0o600))
	command := exec.Command("sh", "entrypoint.sh")
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"PAXM_AGENT_ID=consumer-policy-test",
		"PAXM_PROVIDER_TYPE=mem0",
		"PAXM_USER_ID=eval-owner",
		"MEM0_RUN_ID=consumer-policy-test",
		"PAXM_CONFIG_ROOT=" + directory,
		"PAXM_PLUGIN_SOURCE=" + plugin,
		"PAXM_CONFIG_ONLY=1",
		"PAXM_EVAL_CONSUMER_POLICY=1",
	}
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))

	input, err := os.ReadFile(filepath.Join(directory, "opencode", "opencode.json"))
	s.Require().NoError(err)
	var config struct {
		Instructions []string          `json:"instructions"`
		Permission   map[string]string `json:"permission"`
		Tools        map[string]bool   `json:"tools"`
		Agent        map[string]struct {
			Mode       string            `json:"mode"`
			Prompt     string            `json:"prompt"`
			Permission map[string]string `json:"permission"`
			Tools      map[string]bool   `json:"tools"`
		} `json:"agent"`
	}
	s.Require().NoError(json.Unmarshal(input, &config))
	s.Empty(config.Instructions)
	s.False(config.Tools["read"])
	s.False(config.Tools["glob"])
	s.False(config.Tools["grep"])
	s.NotContains(config.Permission, "read")
	s.NotContains(config.Permission, "glob")
	s.NotContains(config.Permission, "grep")
	consumerAgent := config.Agent["eval-consumer"]
	s.Equal("primary", consumerAgent.Mode)
	s.Equal("{file:./eval-consumer-prompt.md}", consumerAgent.Prompt)
	s.Equal("deny", consumerAgent.Permission["*"])
	s.False(consumerAgent.Tools["*"])
	policy, err := os.ReadFile(filepath.Join(directory, "opencode", "eval-consumer-prompt.md"))
	s.Require().NoError(err)
	s.Contains(string(policy), "same subject")
	s.Contains(string(policy), "recalled memory context")
	s.Contains(string(policy), "Do not search, inspect, or mention the workspace")
	s.Contains(string(policy), "Do not describe or propose searches, tool calls, or attempts")
}

func (s *entrypointSuite) TestHybridConsumerAllowsOnlyTwoActiveRecalls() {
	directory := s.T().TempDir()
	plugin := filepath.Join(directory, "paxm.js")
	toolSource := filepath.Join(directory, "active_recall.ts")
	s.Require().NoError(os.WriteFile(plugin, []byte("// test plugin"), 0o600))
	s.Require().NoError(os.WriteFile(toolSource, []byte("// test active recall tool"), 0o600))
	command := exec.Command("sh", "entrypoint.sh")
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"PAXM_AGENT_ID=hybrid-policy-test",
		"PAXM_PROVIDER_TYPE=mem0",
		"PAXM_USER_ID=eval-owner",
		"MEM0_RUN_ID=hybrid-policy-test",
		"PAXM_CONFIG_ROOT=" + directory,
		"PAXM_PLUGIN_SOURCE=" + plugin,
		"PAXM_ACTIVE_RECALL_TOOL_SOURCE=" + toolSource,
		"PAXM_CONFIG_ONLY=1",
		"PAXM_EVAL_CONSUMER_POLICY=1",
		"PAXM_EVAL_RECALL_MODE=hybrid",
		"PAXM_ACTIVE_RECALL_MAX_CALLS=2",
	}
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))

	input, err := os.ReadFile(filepath.Join(directory, "opencode", "opencode.json"))
	s.Require().NoError(err)
	var config struct {
		Permission map[string]string `json:"permission"`
		Tools      map[string]bool   `json:"tools"`
		Agent      map[string]struct {
			Permission map[string]string `json:"permission"`
			Tools      map[string]bool   `json:"tools"`
		} `json:"agent"`
	}
	s.Require().NoError(json.Unmarshal(input, &config))
	s.Equal("allow", config.Permission["active_recall"])
	s.True(config.Tools["active_recall"])
	s.Equal("allow", config.Agent["eval-consumer"].Permission["active_recall"])
	s.True(config.Agent["eval-consumer"].Tools["active_recall"])
	s.False(config.Agent["eval-consumer"].Tools["read"])

	policy, err := os.ReadFile(filepath.Join(directory, "opencode", "eval-consumer-prompt.md"))
	s.Require().NoError(err)
	s.Contains(string(policy), "at most 2 times")
	s.Contains(string(policy), "active_recall")
	tool, err := os.ReadFile(filepath.Join(directory, "opencode", "tools", "active_recall.ts"))
	s.Require().NoError(err)
	s.Equal("// test active recall tool", string(tool))
}

func (s *entrypointSuite) TestHybridConsumerRejectsRecallLimitAboveTwo() {
	directory := s.T().TempDir()
	plugin := filepath.Join(directory, "paxm.js")
	toolSource := filepath.Join(directory, "active_recall.ts")
	s.Require().NoError(os.WriteFile(plugin, []byte("// test plugin"), 0o600))
	s.Require().NoError(os.WriteFile(toolSource, []byte("// test active recall tool"), 0o600))
	command := exec.Command("sh", "entrypoint.sh")
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"PAXM_AGENT_ID=hybrid-limit-test",
		"PAXM_PROVIDER_TYPE=mem0",
		"PAXM_USER_ID=eval-owner",
		"MEM0_RUN_ID=hybrid-limit-test",
		"PAXM_CONFIG_ROOT=" + directory,
		"PAXM_PLUGIN_SOURCE=" + plugin,
		"PAXM_ACTIVE_RECALL_TOOL_SOURCE=" + toolSource,
		"PAXM_CONFIG_ONLY=1",
		"PAXM_EVAL_CONSUMER_POLICY=1",
		"PAXM_EVAL_RECALL_MODE=hybrid",
		"PAXM_ACTIVE_RECALL_MAX_CALLS=3",
	}
	output, err := command.CombinedOutput()
	s.Require().Error(err)
	s.Contains(string(output), "must be between 1 and 2")
}

func (s *entrypointSuite) TestPassiveRecallThresholdGuard() {
	tests := []struct {
		name       string
		thresholds []string
		wantError  string
	}{
		{name: "raw top k defaults"},
		{name: "reject zero value profile", thresholds: []string{"PAXM_PASSIVE_MIN_RELEVANCE=0", "PAXM_PASSIVE_MIN_SCORE=0"}, wantError: "cannot both be 0"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			directory := s.T().TempDir()
			plugin := filepath.Join(directory, "paxm.js")
			s.Require().NoError(os.WriteFile(plugin, []byte("// test plugin"), 0o600))
			command := exec.Command("sh", "entrypoint.sh")
			command.Env = append([]string{
				"PATH=" + os.Getenv("PATH"),
				"PAXM_AGENT_ID=threshold-test",
				"PAXM_PROVIDER_TYPE=mem0",
				"PAXM_USER_ID=eval-owner",
				"MEM0_RUN_ID=threshold-test",
				"PAXM_CONFIG_ROOT=" + directory,
				"PAXM_PLUGIN_SOURCE=" + plugin,
				"PAXM_CONFIG_ONLY=1",
			}, test.thresholds...)
			output, err := command.CombinedOutput()
			if test.wantError != "" {
				s.Require().Error(err)
				s.Contains(string(output), test.wantError)
				return
			}
			s.Require().NoError(err, string(output))
			input, err := os.ReadFile(filepath.Join(directory, "paxm.yaml"))
			s.Require().NoError(err)
			var config struct {
				RecallProfiles map[string]struct {
					Thresholds struct {
						MinRelevance float64 `yaml:"min_relevance"`
						MinScore     float64 `yaml:"min_score"`
					} `yaml:"thresholds"`
				} `yaml:"recall_profiles"`
			}
			s.Require().NoError(yaml.Unmarshal(input, &config))
			s.InDelta(-1.0, config.RecallProfiles["passive"].Thresholds.MinRelevance, 0.000001)
			s.InDelta(-1.0, config.RecallProfiles["passive"].Thresholds.MinScore, 0.000001)
			var providers struct {
				Providers map[string]struct {
					ScoreSemantics     string `yaml:"score_semantics"`
					SearchScopePayload string `yaml:"search_scope_payload"`
					AgentID            string `yaml:"agent_id"`
				} `yaml:"providers"`
			}
			s.Require().NoError(yaml.Unmarshal(input, &providers))
			s.Equal("distance", providers.Providers["memory"].ScoreSemantics)
			s.Equal("top_level", providers.Providers["memory"].SearchScopePayload)
			s.Equal("threshold-test", providers.Providers["memory"].AgentID)
		})
	}
}

func (s *entrypointSuite) TestPassiveRecallProfilesUseConfiguredProviderTimeout() {
	directory := s.T().TempDir()
	plugin := filepath.Join(directory, "paxm.js")
	s.Require().NoError(os.WriteFile(plugin, []byte("// test plugin"), 0o600))
	command := exec.Command("sh", "entrypoint.sh")
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"PAXM_AGENT_ID=passive-timeout-test",
		"PAXM_PROVIDER_TYPE=team-memory",
		"PAXM_USER_ID=eval-owner",
		"TEAM_MEMORY_API_KEY=eval-key",
		"PAXM_CONFIG_ROOT=" + directory,
		"PAXM_PLUGIN_SOURCE=" + plugin,
		"PAXM_CONFIG_ONLY=1",
		"PAXM_PASSIVE_PROVIDER_TIMEOUT=2s",
	}
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))

	input, err := os.ReadFile(filepath.Join(directory, "paxm.yaml"))
	s.Require().NoError(err)
	var config struct {
		RecallProfiles map[string]struct {
			Providers []struct {
				Name    string `yaml:"name"`
				Timeout string `yaml:"timeout"`
			} `yaml:"providers"`
		} `yaml:"recall_profiles"`
	}
	s.Require().NoError(yaml.Unmarshal(input, &config))
	for _, test := range []struct {
		name    string
		profile string
	}{
		{name: "passive recall", profile: "passive"},
		{name: "initial passive recall", profile: "passive_initial"},
	} {
		s.Run(test.name, func() {
			profile, ok := config.RecallProfiles[test.profile]
			s.Require().True(ok)
			s.Require().Len(profile.Providers, 1)
			s.Equal("memory", profile.Providers[0].Name)
			s.Equal("2s", profile.Providers[0].Timeout)
		})
	}
}

func (s *entrypointSuite) TestTeamMemorySQLiteProfileReadsOnlyOwnPrivateDatabaseAndSharedTeamMemory() {
	directory := s.T().TempDir()
	plugin := filepath.Join(directory, "paxm.js")
	s.Require().NoError(os.WriteFile(plugin, []byte("// test plugin"), 0o600))
	privatePath := filepath.Join(directory, "groupmembench-User_3.sqlite")
	command := exec.Command("sh", "entrypoint.sh")
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"PAXM_AGENT_ID=groupmembench-User_3",
		"PAXM_PROVIDER_TYPE=team-memory-sqlite",
		"PAXM_PRIVATE_SQLITE_PATH=" + privatePath,
		"PAXM_USER_ID=User_3",
		"TEAM_MEMORY_API_KEY=eval-key",
		"PAXM_CONFIG_ROOT=" + directory,
		"PAXM_PLUGIN_SOURCE=" + plugin,
		"PAXM_CONFIG_ONLY=1",
	}
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))

	input, err := os.ReadFile(filepath.Join(directory, "paxm.yaml"))
	s.Require().NoError(err)
	var config struct {
		Providers map[string]struct {
			Type string `yaml:"type"`
			Path string `yaml:"path"`
		} `yaml:"providers"`
		RecallProfiles map[string]struct {
			Providers []struct {
				Name string `yaml:"name"`
			} `yaml:"providers"`
		} `yaml:"recall_profiles"`
	}
	s.Require().NoError(yaml.Unmarshal(input, &config))
	s.Equal("sqlite", config.Providers["private"].Type)
	s.Equal(privatePath, config.Providers["private"].Path)
	s.Equal("jsonrpc", config.Providers["team"].Type)
	s.Equal([]string{"private", "team"}, []string{
		config.RecallProfiles["passive"].Providers[0].Name,
		config.RecallProfiles["passive"].Providers[1].Name,
	})
}

func (s *entrypointSuite) TestPrivateSQLiteIngestCreatesOneDatabasePerSourceAgent() {
	directory := s.T().TempDir()
	batches := filepath.Join(directory, "batches.json")
	privateRoot := filepath.Join(directory, "private")
	capture := filepath.Join(directory, "paxm-calls")
	paxm := filepath.Join(directory, "paxm")
	s.Require().NoError(os.WriteFile(batches, []byte(`[
  {"complete":true,"events":[{"id":"Msg_1","actor":{"user_id":"User_1","agent_id":"groupmembench-User_1","session_id":"s1"},"sequence":1,"content":"first","occurred_at":"2026-07-01T10:00:00Z"}]},
  {"complete":true,"events":[{"id":"Msg_2","actor":{"user_id":"User_2","agent_id":"groupmembench-User_2","session_id":"s2"},"sequence":1,"content":"second","occurred_at":"2026-07-01T10:01:00Z"}]}
]`), 0o600))
	s.Require().NoError(os.WriteFile(paxm, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PAXM_CAPTURE\"\ncat \"$2\" >> \"$PAXM_CAPTURE\"\n"), 0o700))
	command := exec.Command("node", "ingest-private-sqlite.mjs", batches, privateRoot)
	command.Env = append(os.Environ(), "PAXM_BINARY="+paxm, "PAXM_CAPTURE="+capture)
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))
	input, err := os.ReadFile(capture)
	s.Require().NoError(err)
	s.Contains(string(input), "groupmembench-User_1.sqlite")
	s.Contains(string(input), "groupmembench-User_2.sqlite")
	s.Contains(string(input), "groupmembench:Msg_1")
	s.Contains(string(input), "groupmembench:Msg_2")
}

func (s *entrypointSuite) TestRejectsUnexpectedPaxmVersion() {
	directory := s.T().TempDir()
	plugin := filepath.Join(directory, "paxm.js")
	binary := filepath.Join(directory, "paxm")
	s.Require().NoError(os.WriteFile(plugin, []byte("// test plugin"), 0o600))
	s.Require().NoError(os.WriteFile(binary, []byte("#!/bin/sh\necho v0.1.27\n"), 0o700))
	command := exec.Command("sh", "entrypoint.sh")
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"PAXM_AGENT_ID=version-test",
		"PAXM_PROVIDER_TYPE=mem0",
		"PAXM_USER_ID=eval-owner",
		"MEM0_RUN_ID=version-test",
		"PAXM_CONFIG_ROOT=" + directory,
		"PAXM_PLUGIN_SOURCE=" + plugin,
		"PAXM_BINARY=" + binary,
	}
	output, err := command.CombinedOutput()
	s.Require().Error(err)
	s.Contains(string(output), "paxm version v0.1.27 does not match required v0.1.29")
}
