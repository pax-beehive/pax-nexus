package datasetinstall

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type runnerSuite struct {
	suite.Suite
}

func TestRunner(t *testing.T) {
	suite.Run(t, new(runnerSuite))
}

func (s *runnerSuite) TestRunsDownloadThenPreparation() {
	root := s.T().TempDir()
	scripts := filepath.Join(root, "scripts")
	s.Require().NoError(os.MkdirAll(scripts, 0o755))
	fetch := "#!/usr/bin/env bash\nset -eu\nmkdir -p \"$1/$2\"\nprintf fetched > \"$1/$2/downloaded\"\n"
	prepare := "#!/usr/bin/env python3\nimport pathlib, sys\nroot = pathlib.Path(sys.argv[sys.argv.index('--data-root') + 1])\ndataset = sys.argv[sys.argv.index('--dataset') + 1]\n(root / 'prepared').mkdir(parents=True, exist_ok=True)\n(root / 'prepared' / dataset).write_text('ready')\n"
	s.Require().NoError(os.WriteFile(
		filepath.Join(scripts, "fetch-llmwiki-session-datasets.sh"),
		[]byte(fetch),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(scripts, "prepare_llmwiki_session_datasets.py"),
		[]byte(prepare),
		0o644,
	))
	runner, err := NewCommandRunner(root)
	s.Require().NoError(err)
	dataRoot := filepath.Join(root, "data")
	s.Require().NoError(runner.Install(context.Background(), "locomo", dataRoot))
	s.FileExists(filepath.Join(dataRoot, "raw", "locomo", "downloaded"))
	s.FileExists(filepath.Join(dataRoot, "prepared", "locomo"))
}

func (s *runnerSuite) TestReturnsCommandOutputOnFailure() {
	root := s.T().TempDir()
	scripts := filepath.Join(root, "scripts")
	s.Require().NoError(os.MkdirAll(scripts, 0o755))
	s.Require().NoError(os.WriteFile(
		filepath.Join(scripts, "fetch-llmwiki-session-datasets.sh"),
		[]byte("#!/usr/bin/env bash\necho failed-download >&2\nexit 7\n"),
		0o644,
	))
	runner, err := NewCommandRunner(root)
	s.Require().NoError(err)
	err = runner.Install(context.Background(), "locomo", filepath.Join(root, "data"))
	s.ErrorContains(err, "failed-download")
}
