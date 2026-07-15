package opencode_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"
)

type entrypointSuite struct{ suite.Suite }

func TestEntrypointSuite(t *testing.T) { suite.Run(t, new(entrypointSuite)) }

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
		})
	}
}
