package v2

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type executorSuite struct{ suite.Suite }

func TestExecutorSuite(t *testing.T) { suite.Run(t, new(executorSuite)) }

func (s *executorSuite) TestProcessExecutionAndExpansion() {
	tests := []struct {
		name       string
		command    CommandSpec
		variables  map[string]string
		wantOutput string
		wantStderr string
		wantError  bool
	}{
		{
			name: "expands arguments and environment",
			command: CommandSpec{
				Program: "sh", Args: []string{"-c", `printf '%s|%s|%s' "$1" "$PAX_EVAL_CASE_ID" "$CUSTOM"; printf problem >&2`, "shell", "{{case_id}}"},
				Env: map[string]string{"CUSTOM": "{{case_id}}"},
			},
			variables: map[string]string{"case_id": "case-1"}, wantOutput: "case-1|case-1|case-1", wantStderr: "problem",
		},
		{name: "returns command failure", command: CommandSpec{Program: "sh", Args: []string{"-c", "exit 7"}}, wantError: true},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			directory := s.T().TempDir()
			stdoutPath := filepath.Join(directory, "nested", "stdout")
			stderrPath := filepath.Join(directory, "nested", "stderr")
			result, err := (ProcessExecutor{}).Execute(context.Background(), test.command, test.variables, stdoutPath, stderrPath)
			if test.wantError {
				s.Require().Error(err)
				return
			}
			s.Require().NoError(err)
			s.Equal(test.wantOutput, string(result.Output))
			stderr, err := os.ReadFile(stderrPath)
			s.Require().NoError(err)
			s.Equal(test.wantStderr, string(stderr))
		})
	}
}
