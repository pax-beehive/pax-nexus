package datasetinstall

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type Runner interface {
	Install(context.Context, string, string) error
}

type CommandRunner struct {
	fetchScript   string
	prepareScript string
}

func NewCommandRunner(repositoryRoot string) (*CommandRunner, error) {
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}
	return &CommandRunner{
		fetchScript:   filepath.Join(root, "scripts", "fetch-llmwiki-session-datasets.sh"),
		prepareScript: filepath.Join(root, "scripts", "prepare_llmwiki_session_datasets.py"),
	}, nil
}

func (r *CommandRunner) Install(ctx context.Context, dataset string, dataRoot string) error {
	rawRoot := filepath.Join(dataRoot, "raw")
	if err := runCommand(ctx, "bash", r.fetchScript, rawRoot, dataset); err != nil {
		return fmt.Errorf("download %s: %w", dataset, err)
	}
	if err := runCommand(
		ctx,
		"python3",
		r.prepareScript,
		"--data-root",
		dataRoot,
		"--dataset",
		dataset,
	); err != nil {
		return fmt.Errorf("prepare %s: %w", dataset, err)
	}
	return nil
}

func runCommand(ctx context.Context, name string, arguments ...string) error {
	command := exec.CommandContext(ctx, name, arguments...)
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if len(message) > 4000 {
		message = message[len(message)-4000:]
	}
	if message == "" {
		return fmt.Errorf("run %s: %w", name, err)
	}
	return fmt.Errorf("run %s: %w: %s", name, err, message)
}
