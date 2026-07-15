package v2

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type CommandResult struct {
	Output   []byte
	Duration time.Duration
}

type Executor interface {
	Execute(context.Context, CommandSpec, map[string]string, string, string) (CommandResult, error)
}

type ProcessExecutor struct{}

func (ProcessExecutor) Execute(ctx context.Context, spec CommandSpec, variables map[string]string, stdoutPath, stderrPath string) (CommandResult, error) {
	program := expand(spec.Program, variables)
	args := make([]string, len(spec.Args))
	for index, arg := range spec.Args {
		args[index] = expand(arg, variables)
	}
	command := exec.CommandContext(ctx, program, args...)
	if spec.WorkingDir != "" {
		command.Dir = expand(spec.WorkingDir, variables)
	}
	command.Env = commandEnvironment(spec.Env, variables)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	started := time.Now()
	err := command.Run()
	duration := time.Since(started)
	if writeErr := writeCommandOutput(stdoutPath, stdout.Bytes()); writeErr != nil {
		return CommandResult{}, writeErr
	}
	if writeErr := writeCommandOutput(stderrPath, stderr.Bytes()); writeErr != nil {
		return CommandResult{}, writeErr
	}
	if err != nil {
		return CommandResult{}, fmt.Errorf("execute %q: %w", program, err)
	}
	return CommandResult{Output: slices.Clone(stdout.Bytes()), Duration: duration}, nil
}

func expand(value string, variables map[string]string) string {
	result := value
	for name, replacement := range variables {
		result = strings.ReplaceAll(result, "{{"+name+"}}", replacement)
	}
	return result
}

func commandEnvironment(configured, variables map[string]string) []string {
	values := make(map[string]string, len(os.Environ())+len(configured)+len(variables))
	for _, entry := range os.Environ() {
		name, value, found := strings.Cut(entry, "=")
		if found {
			values[name] = value
		}
	}
	for name, value := range configured {
		values[name] = expand(value, variables)
	}
	for name, value := range variables {
		environmentName := "PAX_EVAL_" + strings.ToUpper(name)
		values[environmentName] = value
	}
	keys := make([]string, 0, len(values))
	for name := range values {
		keys = append(keys, name)
	}
	slices.Sort(keys)
	environment := make([]string, 0, len(keys))
	for _, name := range keys {
		environment = append(environment, name+"="+values[name])
	}
	return environment
}

func writeCommandOutput(path string, output []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create command artifact directory: %w", err)
	}
	if err := os.WriteFile(path, output, 0o644); err != nil {
		return fmt.Errorf("write command artifact %q: %w", path, err)
	}
	return nil
}
