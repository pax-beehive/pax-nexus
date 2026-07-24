package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/llmwiki/effecteval"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
)

const defaultInstruction = `Maintain the whole human Wiki from every immutable
Source in this workspace. Integrate new evidence into existing knowledge instead
of generating a Session transcript or a duplicate Wiki. Create a coherent topic
tree, durable pages, cross-links, and exact message-anchor citations.`

func main() {
	if err := execute(context.Background(), os.Args[1:], os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func execute(ctx context.Context, arguments []string, output io.Writer) error {
	if len(arguments) == 0 {
		return errors.New("command is required")
	}
	switch arguments[0] {
	case "build":
		return buildCommand(ctx, arguments[1:], output)
	case "init-store":
		return initStoreCommand(ctx, arguments[1:], output)
	case "checkout":
		return checkoutCommand(ctx, arguments[1:])
	case "run":
		return runAgentCommand(ctx, arguments[1:], output)
	case "validate":
		return validateCommand(arguments[1:], output)
	case "commit":
		return commitCommand(ctx, arguments[1:], output)
	case "publish":
		return publishCommand(ctx, arguments[1:])
	case "diff":
		return diffCommand(ctx, arguments[1:], output)
	case "rollback":
		return rollbackCommand(ctx, arguments[1:])
	case "eval-prepare":
		return evalPrepareCommand(ctx, arguments[1:], output)
	case "eval-score":
		return evalScoreCommand(arguments[1:], output)
	case "serve":
		return serveCommand(arguments[1:], output)
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func buildCommand(ctx context.Context, arguments []string, output io.Writer) error {
	flags := newFlags("build")
	root := flags.String("workspace", "", "private workspace path")
	sessionID := flags.String("session", "", "native paxm Session ID")
	start := flags.Int("start", 0, "zero-based inclusive turn index")
	end := flags.Int("end", 0, "zero-based exclusive turn index")
	exportPath := flags.String("export", "", "optional pre-exported paxm JSON")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{
		"workspace": *root, "session": *sessionID,
	}); err != nil {
		return err
	}
	config := workspace.BuildConfig{Root: *root}
	if strings.TrimSpace(*exportPath) != "" {
		config.ReadSession = func(context.Context, string) ([]byte, error) {
			encoded, err := os.ReadFile(*exportPath)
			if err != nil {
				return nil, fmt.Errorf("read exported Session: %w", err)
			}
			return encoded, nil
		}
	}
	result, err := workspace.Build(ctx, config, workspace.BuildRequest{
		SessionID: *sessionID, TurnStart: *start, TurnEnd: *end,
	})
	if err != nil {
		return err
	}
	return writeJSON(output, result)
}

func initStoreCommand(ctx context.Context, arguments []string, output io.Writer) error {
	flags := newFlags("init-store")
	root := flags.String("workspace", "", "seed workspace path")
	store := flags.String("store", "", "bare Git snapshot store")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{"workspace": *root, "store": *store}); err != nil {
		return err
	}
	revision, err := workspace.InitStore(ctx, *store, *root)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, revision)
	return err
}

func checkoutCommand(ctx context.Context, arguments []string) error {
	flags := newFlags("checkout")
	store := flags.String("store", "", "bare Git snapshot store")
	root := flags.String("workspace", "", "private checkout destination")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{"store": *store, "workspace": *root}); err != nil {
		return err
	}
	return workspace.Checkout(ctx, *store, *root)
}

func runAgentCommand(ctx context.Context, arguments []string, output io.Writer) error {
	flags := newFlags("run")
	root := flags.String("workspace", "", "private workspace path")
	runID := flags.String("run-id", "", "stable run audit ID")
	model := flags.String("model", "deepseek-v4-pro", "DeepSeek model")
	baseURL := flags.String("base-url", "https://api.deepseek.com", "DeepSeek API base URL")
	instruction := flags.String("instruction", defaultInstruction, "maintenance instruction")
	maxRounds := flags.Int("max-rounds", 30, "maximum model turns")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{"workspace": *root, "run-id": *runID}); err != nil {
		return err
	}
	client := workspace.NewDeepSeekClient(workspace.DeepSeekConfig{
		BaseURL: *baseURL, APIKey: os.Getenv("DEEPSEEK_API_KEY"),
	})
	result, err := workspace.RunAgent(ctx, workspace.AgentConfig{
		Root: *root, Model: *model, MaxRounds: *maxRounds, Client: client,
	}, workspace.AgentRequest{RunID: *runID, Instruction: *instruction})
	if err != nil {
		return err
	}
	return writeJSON(output, result)
}

func validateCommand(arguments []string, output io.Writer) error {
	flags := newFlags("validate")
	root := flags.String("workspace", "", "workspace path")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{"workspace": *root}); err != nil {
		return err
	}
	report := workspace.Validate(*root)
	if err := writeJSON(output, report); err != nil {
		return err
	}
	if !report.Valid {
		return errors.New("workspace validation failed")
	}
	return nil
}

func commitCommand(ctx context.Context, arguments []string, output io.Writer) error {
	flags := newFlags("commit")
	root := flags.String("workspace", "", "private workspace path")
	message := flags.String("message", "", "snapshot commit message")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{"workspace": *root, "message": *message}); err != nil {
		return err
	}
	revision, err := workspace.Commit(ctx, *root, *message)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, revision)
	return err
}

func publishCommand(ctx context.Context, arguments []string) error {
	flags := newFlags("publish")
	store := flags.String("store", "", "bare Git snapshot store")
	root := flags.String("workspace", "", "private workspace path")
	base := flags.String("base", "", "expected canonical base revision")
	revision := flags.String("revision", "", "validated snapshot revision")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{
		"store": *store, "workspace": *root, "base": *base, "revision": *revision,
	}); err != nil {
		return err
	}
	return workspace.Publish(ctx, *store, *root, *base, *revision)
}

func diffCommand(ctx context.Context, arguments []string, output io.Writer) error {
	flags := newFlags("diff")
	repository := flags.String("repo", "", "Git workspace containing both revisions")
	base := flags.String("base", "", "base revision")
	revision := flags.String("revision", "", "new revision")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{
		"repo": *repository, "base": *base, "revision": *revision,
	}); err != nil {
		return err
	}
	diff, err := workspace.Diff(ctx, *repository, *base, *revision)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, diff)
	return err
}

func rollbackCommand(ctx context.Context, arguments []string) error {
	flags := newFlags("rollback")
	store := flags.String("store", "", "bare Git snapshot store")
	head := flags.String("head", "", "expected current HEAD")
	target := flags.String("target", "", "ancestor revision to restore")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{
		"store": *store, "head": *head, "target": *target,
	}); err != nil {
		return err
	}
	return workspace.Rollback(ctx, *store, *head, *target)
}

func evalPrepareCommand(
	ctx context.Context,
	arguments []string,
	output io.Writer,
) error {
	flags := newFlags("eval-prepare")
	fixture := flags.String("fixture", "", "private effect-eval fixture")
	root := flags.String("workspace", "", "isolated maintainer workspace")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{
		"fixture": *fixture, "workspace": *root,
	}); err != nil {
		return err
	}
	prepared, err := effecteval.Prepare(ctx, *fixture, *root)
	if err != nil {
		return err
	}
	return writeJSON(output, prepared)
}

func evalScoreCommand(arguments []string, output io.Writer) error {
	flags := newFlags("eval-score")
	fixture := flags.String("fixture", "", "private effect-eval fixture")
	root := flags.String("workspace", "", "maintained eval workspace")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{
		"fixture": *fixture, "workspace": *root,
	}); err != nil {
		return err
	}
	report, err := effecteval.Score(*fixture, *root)
	if err != nil {
		return err
	}
	return writeJSON(output, report)
}

func serveCommand(arguments []string, output io.Writer) error {
	flags := newFlags("serve")
	root := flags.String("workspace", "", "published Wiki checkout")
	address := flags.String("addr", "127.0.0.1:8090", "local listen address")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{"workspace": *root}); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "PAX Wiki viewer: http://%s/\n", *address); err != nil {
		return fmt.Errorf("write viewer address: %w", err)
	}
	server := &http.Server{
		Addr:              *address,
		Handler:           workspace.NewViewer(*root),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server.ListenAndServe()
}

func newFlags(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func requireFlags(values map[string]string) error {
	for name, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("--%s is required", name)
		}
	}
	return nil
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
