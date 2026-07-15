package opencode_test

import (
	"encoding/json"
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
	s.Contains(arguments, "PAXM_AGENT_ID=groupmembench-eval-owner")
	s.Contains(arguments, "--agent")
	s.Contains(arguments, "eval-consumer")
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
				} `yaml:"providers"`
			}
			s.Require().NoError(yaml.Unmarshal(input, &providers))
			s.Equal("distance", providers.Providers["memory"].ScoreSemantics)
			s.Equal("top_level", providers.Providers["memory"].SearchScopePayload)
		})
	}
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
