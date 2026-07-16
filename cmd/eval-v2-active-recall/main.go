package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const hardMaximumCalls = 2

type runConfig struct {
	PaxmBinary string
	PaxmConfig string
	StateDir   string
	MaxCalls   int
}

type mcpResponse struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Result struct {
		IsError bool `json:"isError,omitempty"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
}

func main() {
	query := flag.String("query", "", "focused recall query")
	sessionID := flag.String("session-id", "", "consumer session ID")
	maxCalls := flag.Int("max-calls", hardMaximumCalls, "maximum active recall calls")
	flag.Parse()
	config := runConfig{
		PaxmBinary: envOrDefault("PAXM_BINARY", "/usr/local/bin/paxm"),
		PaxmConfig: os.Getenv("PAXM_CONFIG"),
		StateDir:   os.Getenv("PAXM_ACTIVE_RECALL_STATE_DIR"),
		MaxCalls:   *maxCalls,
	}
	if err := run(context.Background(), config, *sessionID, *query, os.Stdout); err != nil {
		if _, writeErr := fmt.Fprintln(os.Stderr, err); writeErr != nil {
			os.Exit(1)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, config runConfig, sessionID, query string, output io.Writer) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("session ID is required")
	}
	if strings.TrimSpace(query) == "" {
		return errors.New("query is required")
	}
	if config.MaxCalls < 1 || config.MaxCalls > hardMaximumCalls {
		return fmt.Errorf("maximum active recall calls must be between 1 and %d", hardMaximumCalls)
	}
	if strings.TrimSpace(config.PaxmBinary) == "" || strings.TrimSpace(config.PaxmConfig) == "" || strings.TrimSpace(config.StateDir) == "" {
		return errors.New("paxm binary, config, and active recall state directory are required")
	}
	call, allowed, err := claimCall(config.StateDir, sessionID, config.MaxCalls)
	if err != nil {
		return fmt.Errorf("claim active recall call: %w", err)
	}
	if !allowed {
		_, err = fmt.Fprintf(output, "Active recall limit reached: %d calls were already used.\n", config.MaxCalls)
		return err
	}
	text, err := callPaxm(ctx, config, sessionID, query, call)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, text)
	return err
}

func claimCall(stateDir, sessionID string, maximum int) (call int, allowed bool, err error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return 0, false, fmt.Errorf("create state directory: %w", err)
	}
	digest := sha256.Sum256([]byte(sessionID))
	path := filepath.Join(stateDir, hex.EncodeToString(digest[:])+".count")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 0, false, fmt.Errorf("open call counter: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close call counter: %w", closeErr)
		}
	}()
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return 0, false, fmt.Errorf("lock call counter: %w", err)
	}
	defer func() {
		if unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err == nil && unlockErr != nil {
			err = fmt.Errorf("unlock call counter: %w", unlockErr)
		}
	}()
	data, err := io.ReadAll(file)
	if err != nil {
		return 0, false, fmt.Errorf("read call counter: %w", err)
	}
	used := 0
	if value := strings.TrimSpace(string(data)); value != "" {
		used, err = strconv.Atoi(value)
		if err != nil || used < 0 {
			return 0, false, errors.New("call counter is invalid")
		}
	}
	if used >= maximum {
		return used, false, nil
	}
	call = used + 1
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return 0, false, fmt.Errorf("seek call counter: %w", err)
	}
	if err = file.Truncate(0); err != nil {
		return 0, false, fmt.Errorf("truncate call counter: %w", err)
	}
	if _, err = fmt.Fprint(file, call); err != nil {
		return 0, false, fmt.Errorf("write call counter: %w", err)
	}
	if err = file.Sync(); err != nil {
		return 0, false, fmt.Errorf("sync call counter: %w", err)
	}
	return call, true, nil
}

func callPaxm(ctx context.Context, config runConfig, sessionID, query string, call int) (string, error) {
	request := map[string]any{
		"jsonrpc": "2.0", "id": call, "method": "tools/call",
		"params": map[string]any{
			"name": "paxm_recall",
			"arguments": map[string]any{
				"query": query, "limit": 5, "meta": map[string]string{"session_id": sessionID},
			},
		},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode active recall request: %w", err)
	}
	command := exec.CommandContext(ctx, config.PaxmBinary, "--config", config.PaxmConfig, "mcp", "serve", "--agent", "opencode")
	command.Stdin = bytes.NewReader(append(payload, '\n'))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("run paxm active recall: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[len(lines)-1]) == "" {
		return "", errors.New("paxm active recall returned no response")
	}
	var response mcpResponse
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &response); err != nil {
		return "", fmt.Errorf("decode paxm active recall response: %w", err)
	}
	if response.Error != nil {
		return "", fmt.Errorf("paxm active recall: %s", response.Error.Message)
	}
	texts := make([]string, 0, len(response.Result.Content))
	for _, content := range response.Result.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			texts = append(texts, content.Text)
		}
	}
	if response.Result.IsError {
		message := strings.Join(texts, "\n")
		if message == "" {
			message = "unknown MCP tool error"
		}
		return "", fmt.Errorf("paxm active recall tool failed: %s", message)
	}
	if len(texts) == 0 {
		return "", errors.New("paxm active recall returned no text content")
	}
	return strings.Join(texts, "\n"), nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
