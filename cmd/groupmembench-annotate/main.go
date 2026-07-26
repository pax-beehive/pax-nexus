// Command groupmembench-annotate derives supporting-author annotations for
// a GroupMemBench v3 manifest: for each case, it asks a judge which domain
// events ground the gold answer and resolves those events to the agent IDs
// that authored them. The result lets internal/eval/v3.SelectAnswerer
// exclude gold-supporting authors and run a strict cross-agent trial.
//
// The dataset itself carries no evidence authorship (GroupMemBench questions
// are only {answer, asking_user_id, id, question}), so this command never
// invents one: a case the judge cannot ground yields an empty, low-confidence
// annotation rather than a fabricated author.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
	"github.com/pax-beehive/pax-nexus/internal/session"
)

func main() {
	logger := observability.NewLogger(os.Stderr)
	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("GroupMemBench annotation failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("groupmembench-annotate", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "", "GroupMemBench v3 manifest.json produced by groupmembench-select -mode full-domain")
	sessionBatchesPath := flags.String("session-batches", "", "domain session-batches.json; defaults to manifest.domain_session_batches resolved against the manifest directory")
	outputDirectory := flags.String("output", "", "directory to write annotations.json into")
	judgeProgram := flags.String("judge-command", "", "program invoked once per case; receives the judge request as JSON on stdin and must print a JSON judge response on stdout")
	judgeArgs := flags.String("judge-args", "", "comma-separated arguments passed to -judge-command")
	judgeModel := flags.String("judge-model", "", "model label recorded in each annotation's method, e.g. the model backing -judge-command")
	timeout := flags.Duration("judge-timeout", 2*time.Minute, "per-case timeout for -judge-command")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse GroupMemBench annotate flags: %w", err)
	}
	if strings.TrimSpace(*manifestPath) == "" {
		return fmt.Errorf("groupmembench-annotate: -manifest is required")
	}
	if strings.TrimSpace(*outputDirectory) == "" {
		return fmt.Errorf("groupmembench-annotate: -output is required")
	}
	if strings.TrimSpace(*judgeProgram) == "" {
		return fmt.Errorf("groupmembench-annotate: -judge-command is required")
	}

	manifest, err := loadManifest(*manifestPath)
	if err != nil {
		return err
	}
	resolvedSessionBatches := strings.TrimSpace(*sessionBatchesPath)
	if resolvedSessionBatches == "" {
		if strings.TrimSpace(manifest.DomainSessionBatches) == "" {
			return fmt.Errorf("groupmembench-annotate: manifest has no domain_session_batches; pass -session-batches explicitly")
		}
		resolvedSessionBatches = filepath.Join(filepath.Dir(*manifestPath), manifest.DomainSessionBatches)
	}
	batches, err := loadSessionBatches(resolvedSessionBatches)
	if err != nil {
		return err
	}
	events := groupmembench.DomainEventsFromSessionBatches(batches)

	var args2 []string
	if strings.TrimSpace(*judgeArgs) != "" {
		args2 = strings.Split(*judgeArgs, ",")
	}
	judge := commandJudge{program: *judgeProgram, args: args2, model: *judgeModel, timeout: *timeout}

	annotations, err := groupmembench.Annotate(context.Background(), manifest.Cases, events, judge)
	if err != nil {
		return err
	}
	if err := groupmembench.WriteAnnotations(*outputDirectory, annotations); err != nil {
		return err
	}

	high, low := 0, 0
	for _, annotation := range annotations {
		if annotation.Confidence == groupmembench.ConfidenceHigh {
			high++
		} else {
			low++
		}
	}
	logger.Info("GroupMemBench annotations written", "cases", len(annotations), "confidence_high", high, "confidence_low", low, "output", *outputDirectory)
	return nil
}

func loadManifest(path string) (groupmembench.Manifest, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return groupmembench.Manifest{}, fmt.Errorf("open GroupMemBench manifest: %w", err)
	}
	var manifest groupmembench.Manifest
	if err := json.Unmarshal(input, &manifest); err != nil {
		return groupmembench.Manifest{}, fmt.Errorf("decode GroupMemBench manifest: %w", err)
	}
	if len(manifest.Cases) == 0 {
		return groupmembench.Manifest{}, fmt.Errorf("load GroupMemBench manifest: no cases")
	}
	return manifest, nil
}

func loadSessionBatches(path string) ([]session.SessionBatch, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open GroupMemBench session batches: %w", err)
	}
	var batches []session.SessionBatch
	if err := json.Unmarshal(input, &batches); err != nil {
		return nil, fmt.Errorf("decode GroupMemBench session batches: %w", err)
	}
	if len(batches) == 0 {
		return nil, fmt.Errorf("load GroupMemBench session batches: no batches")
	}
	return batches, nil
}

// commandJudge implements groupmembench.LLM by running an external program
// once per case. The request is written to stdin as JSON; the program must
// print a JSON judge response ({"supporting_event_ids": [...]}) on stdout.
// This keeps the annotate library free of any specific model integration,
// so unit tests never call a real model.
type commandJudge struct {
	program string
	args    []string
	model   string
	timeout time.Duration
}

type commandJudgeRequest struct {
	CaseID   string                    `json:"case_id"`
	Question string                    `json:"question"`
	Answer   string                    `json:"answer"`
	Events   []commandJudgeDomainEvent `json:"events"`
}

type commandJudgeDomainEvent struct {
	ID      string `json:"id"`
	Author  string `json:"author"`
	Content string `json:"content"`
}

type commandJudgeResponse struct {
	SupportingEventIDs []string `json:"supporting_event_ids"`
}

func (judge commandJudge) SupportingEvents(ctx context.Context, request groupmembench.JudgeRequest) (groupmembench.JudgeResponse, error) {
	if judge.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, judge.timeout)
		defer cancel()
	}
	events := make([]commandJudgeDomainEvent, 0, len(request.Events))
	for _, event := range request.Events {
		events = append(events, commandJudgeDomainEvent{ID: event.ID, Author: event.Author, Content: event.Content})
	}
	payload, err := json.Marshal(commandJudgeRequest{
		CaseID: request.CaseID, Question: request.Question, Answer: request.Answer, Events: events,
	})
	if err != nil {
		return groupmembench.JudgeResponse{}, fmt.Errorf("encode judge request %q: %w", request.CaseID, err)
	}

	command := exec.CommandContext(ctx, judge.program, judge.args...)
	command.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return groupmembench.JudgeResponse{}, fmt.Errorf("run judge command for case %q: %w: %s", request.CaseID, err, strings.TrimSpace(stderr.String()))
	}

	var response commandJudgeResponse
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &response); err != nil {
		return groupmembench.JudgeResponse{}, fmt.Errorf("decode judge response for case %q: %w", request.CaseID, err)
	}
	return groupmembench.JudgeResponse{SupportingEventIDs: response.SupportingEventIDs, Model: judge.model}, nil
}
