package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type MainSuite struct {
	suite.Suite
	directory string
}

func TestMainSuite(t *testing.T) {
	suite.Run(t, new(MainSuite))
}

func (s *MainSuite) SetupTest() {
	s.directory = s.T().TempDir()
}

func (s *MainSuite) TestRunWritesStageArtifacts() {
	fixtures := filepath.Join(s.directory, "fixtures.json")
	observations := filepath.Join(s.directory, "observations.jsonl")
	output := filepath.Join(s.directory, "output")
	s.Require().NoError(os.WriteFile(fixtures, []byte(`{"schema_version":"pax-stage-eval-v1","cases":[{"case_id":"case","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recall_context":{"consumer_user_id":"user","query":"question","token_budget":512},"required_atoms":[{"id":"fact","patterns":["ready"]}]}]}`), 0o600))
	s.Require().NoError(os.WriteFile(observations, []byte("{\"case_id\":\"case\",\"stage\":\"extraction\",\"source_revision\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"items\":[{\"id\":\"note\",\"text\":\"ready\"}]}\n{\"case_id\":\"case\",\"stage\":\"recall\",\"source_revision\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"recall_context\":{\"consumer_user_id\":\"user\",\"query\":\"question\",\"token_budget\":512},\"items\":[{\"id\":\"note\",\"text\":\"ready\"}]}\n"), 0o600))
	var stdout bytes.Buffer

	err := run([]string{"-fixtures", fixtures, "-observations", observations, "-output-dir", output}, &stdout)

	s.Require().NoError(err)
	s.Contains(stdout.String(), "conditional recall 1.000")
	for _, name := range []string{"stage-results.jsonl", "stage-summary.json"} {
		_, statErr := os.Stat(filepath.Join(output, name))
		s.NoError(statErr)
	}
}

func (s *MainSuite) TestRunRequiresAllPaths() {
	s.Error(run(nil, &bytes.Buffer{}))
}

func (s *MainSuite) TestWriteFileWrapsEncoderFailure() {
	expected := errors.New("encode failed")
	err := writeFile(filepath.Join(s.directory, "artifact.json"), func(io.Writer) error {
		return expected
	})

	s.Require().ErrorIs(err, expected)
	s.Contains(fmt.Sprint(err), "write stage eval artifact")
}
