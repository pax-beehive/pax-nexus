package v2

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type loadingSuite struct{ suite.Suite }

func TestLoadingSuite(t *testing.T) { suite.Run(t, new(loadingSuite)) }

func (s *loadingSuite) TestLoadConfigAndCases() {
	directory := s.T().TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	configYAML := `
version: v2
run: {id: run, dataset: suite, manifest: manifest.json, output_dir: output, parallelism: 1}
store: {dsn_env: EVAL_DSN}
baseline_arm: control
trial_timeout: 1m
arms:
  - name: control
    consumer: {program: consumer}
  - name: memory
    producer: {program: producer}
    consumer: {program: consumer}
`
	s.Require().NoError(os.WriteFile(configPath, []byte(configYAML), 0o644))
	config, err := LoadConfig(configPath)
	s.Require().NoError(err)
	s.Equal("run", config.Run.ID)

	manifestPath := filepath.Join(directory, "manifest.json")
	manifestJSON := `{"dataset_revision":"abc123","cases":[{"id":"case-1","category":"temporal","question":"when","answer":"tomorrow","asking_user_id":"user","scope_id":"scope","participant_agent_ids":["groupmembench-user","groupmembench-peer"],"supporting_agent_ids":["groupmembench-user"]}]}`
	s.Require().NoError(os.WriteFile(manifestPath, []byte(manifestJSON), 0o644))
	cases, revision, err := LoadCases(manifestPath)
	s.Require().NoError(err)
	s.Equal("abc123", revision)
	s.Require().Len(cases, 1)
	s.Equal(filepath.Join(directory, "cases", "case-1", "producer"), cases[0].ProducerWorkspace)
	s.Equal([]string{"groupmembench-user", "groupmembench-peer"}, cases[0].ParticipantAgentIDs)
	s.Equal([]string{"groupmembench-user"}, cases[0].SupportingAgentIDs)
}

func (s *loadingSuite) TestLoadingErrors() {
	directory := s.T().TempDir()
	tests := []struct {
		name    string
		content string
		load    func(string) error
	}{
		{name: "invalid config yaml", content: "version: [", load: func(path string) error { _, err := LoadConfig(path); return err }},
		{name: "invalid config value", content: "version: v1", load: func(path string) error { _, err := LoadConfig(path); return err }},
		{name: "invalid manifest json", content: "{", load: func(path string) error { _, _, err := LoadCases(path); return err }},
		{name: "empty manifest", content: `{}`, load: func(path string) error { _, _, err := LoadCases(path); return err }},
		{name: "empty case", content: `{"dataset_revision":"rev","cases":[{}]}`, load: func(path string) error { _, _, err := LoadCases(path); return err }},
		{name: "duplicate case", content: `{"dataset_revision":"rev","cases":[{"id":"x","question":"q","answer":"a","asking_user_id":"u"},{"id":"x","question":"q","answer":"a","asking_user_id":"u"}]}`, load: func(path string) error { _, _, err := LoadCases(path); return err }},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			path := filepath.Join(directory, test.name)
			s.Require().NoError(os.WriteFile(path, []byte(test.content), 0o644))
			s.Error(test.load(path))
		})
	}
	_, err := LoadConfig(filepath.Join(directory, "missing"))
	s.Require().Error(err)
	_, _, err = LoadCases(filepath.Join(directory, "missing"))
	s.Require().Error(err)
}
