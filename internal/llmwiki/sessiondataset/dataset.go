// Package sessiondataset adapts answer-blind public benchmark Session histories
// into immutable local-first Wiki Sources.
package sessiondataset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
)

const schemaVersion = "pax-session-dataset/v1"

type Config struct {
	IngestPath string
	Root       string
	CaseID     string
	Start      int
	End        int
}

type Result struct {
	CaseID     string `json:"case_id"`
	SourceKind string `json:"source_kind"`
	Sessions   int    `json:"sessions"`
	Turns      int    `json:"turns"`
	Sources    int    `json:"sources"`
}

type EvidenceLocation struct {
	SourcePath string
	Anchor     string
}

type record struct {
	SchemaVersion   string    `json:"schema_version"`
	CaseID          string    `json:"case_id"`
	SourceKind      string    `json:"source_kind"`
	Participants    []string  `json:"participants"`
	Sessions        []session `json:"sessions"`
	TrajectoryIDs   []string  `json:"trajectory_ids"`
	TrajectoryStore string    `json:"trajectory_store"`
}

type session struct {
	SessionID  string `json:"session_id"`
	OccurredAt string `json:"occurred_at"`
	Turns      []turn `json:"turns"`
}

type turn struct {
	DiaID   string `json:"dia_id"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

func Build(ctx context.Context, config Config) (Result, error) {
	if strings.TrimSpace(config.Root) == "" {
		return Result{}, errors.New("workspace root is required")
	}
	selected, err := loadCase(config.IngestPath, config.CaseID)
	if err != nil {
		return Result{}, err
	}
	if config.Start < 0 ||
		config.End <= config.Start ||
		config.End > len(selected.Sessions) {
		return Result{}, fmt.Errorf(
			"session range [%d,%d) is invalid for %d sessions",
			config.Start,
			config.End,
			len(selected.Sessions),
		)
	}
	if selected.SourceKind == "agent-trajectory-haystack" {
		return Result{}, errors.New(
			"agent trajectory datasets require the trajectory adapter",
		)
	}
	result := Result{
		CaseID: selected.CaseID, SourceKind: selected.SourceKind,
		Sessions: config.End - config.Start,
	}
	seenSessions := make(map[string]struct{}, result.Sessions)
	for _, sourceSession := range selected.Sessions[config.Start:config.End] {
		sessionID := strings.TrimSpace(sourceSession.SessionID)
		if sessionID == "" {
			return Result{}, errors.New("dataset Session is missing session_id")
		}
		if _, exists := seenSessions[sessionID]; exists {
			return Result{}, fmt.Errorf("duplicate dataset Session %q", sessionID)
		}
		seenSessions[sessionID] = struct{}{}
		exported, convertErr := toSessionExport(selected, sourceSession)
		if convertErr != nil {
			return Result{}, convertErr
		}
		encoded, marshalErr := json.Marshal(exported)
		if marshalErr != nil {
			return Result{}, fmt.Errorf("encode dataset Session %s: %w", sessionID, marshalErr)
		}
		payload := encoded
		built, buildErr := workspace.Build(
			ctx,
			workspace.BuildConfig{
				Root: config.Root,
				ReadSession: func(context.Context, string) ([]byte, error) {
					return payload, nil
				},
			},
			workspace.BuildRequest{
				SessionID: sessionID,
				TurnStart: 0,
				TurnEnd:   len(exported.Turns),
			},
		)
		if buildErr != nil {
			return Result{}, fmt.Errorf("build dataset Session %s: %w", sessionID, buildErr)
		}
		result.Turns += built.MessageCount
		result.Sources++
	}
	return result, nil
}

func SourceMap(root string) (map[string]EvidenceLocation, error) {
	encoded, err := os.ReadFile(filepath.Join(root, ".pax", "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read workspace manifest: %w", err)
	}
	var manifest workspace.Manifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return nil, fmt.Errorf("decode workspace manifest: %w", err)
	}
	result := make(map[string]EvidenceLocation)
	for _, source := range manifest.Sources {
		for _, anchor := range source.Anchors {
			if _, duplicate := result[anchor.TurnID]; duplicate {
				return nil, fmt.Errorf("duplicate evidence ID %s", anchor.TurnID)
			}
			result[anchor.TurnID] = EvidenceLocation{
				SourcePath: source.Path,
				Anchor:     anchor.ID,
			}
		}
	}
	return result, nil
}

func loadCase(path, caseID string) (result record, resultErr error) {
	if strings.TrimSpace(path) == "" {
		return record{}, errors.New("maintainer ingest path is required")
	}
	requestedID := strings.TrimSpace(caseID)
	if requestedID == "" {
		return record{}, errors.New("dataset case ID is required")
	}
	input, err := os.Open(path)
	if err != nil {
		return record{}, fmt.Errorf("open maintainer ingest: %w", err)
	}
	defer func() {
		if err := input.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close maintainer ingest: %w", err))
		}
	}()

	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	var selected *record
	for {
		var candidate record
		if err := decoder.Decode(&candidate); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return record{}, fmt.Errorf("decode maintainer ingest: %w", err)
		}
		if candidate.SchemaVersion != schemaVersion {
			return record{}, fmt.Errorf(
				"unsupported Session dataset schema %q",
				candidate.SchemaVersion,
			)
		}
		if candidate.CaseID != requestedID {
			continue
		}
		if selected != nil {
			return record{}, fmt.Errorf("duplicate dataset case %q", requestedID)
		}
		copy := candidate
		selected = &copy
	}
	if selected == nil {
		return record{}, fmt.Errorf("dataset case %q not found", requestedID)
	}
	if len(selected.Sessions) == 0 {
		return record{}, fmt.Errorf("dataset case %q has no Sessions", requestedID)
	}
	return *selected, nil
}

func toSessionExport(dataset record, source session) (workspace.SessionExport, error) {
	occurredAt, err := parseOccurredAt(source.OccurredAt)
	if err != nil {
		return workspace.SessionExport{}, fmt.Errorf(
			"parse Session %s timestamp: %w",
			source.SessionID,
			err,
		)
	}
	exported := workspace.SessionExport{
		SchemaVersion: workspace.PaxmSessionSchema,
		Agent:         "public-session-dataset",
		SessionID:     source.SessionID,
		Workspace:     "dataset:" + dataset.CaseID,
		Turns:         make([]workspace.SessionTurn, 0, len(source.Turns)),
	}
	for index, rawTurn := range source.Turns {
		converted, convertErr := convertTurn(
			dataset,
			source.SessionID,
			index,
			rawTurn,
			occurredAt,
		)
		if convertErr != nil {
			return workspace.SessionExport{}, convertErr
		}
		exported.Turns = append(exported.Turns, converted)
	}
	if len(exported.Turns) == 0 {
		return workspace.SessionExport{}, fmt.Errorf(
			"dataset Session %s has no turns",
			source.SessionID,
		)
	}
	return exported, nil
}

func convertTurn(
	dataset record,
	sessionID string,
	index int,
	raw turn,
	occurredAt time.Time,
) (workspace.SessionTurn, error) {
	converted := workspace.SessionTurn{CreatedAt: occurredAt}
	switch dataset.SourceKind {
	case "long-running-conversation":
		if len(dataset.Participants) != 2 {
			return converted, errors.New(
				"long-running conversation requires exactly two participants",
			)
		}
		converted.ID = strings.TrimSpace(raw.DiaID)
		content := strings.TrimSpace(raw.Speaker) + ": " + strings.TrimSpace(raw.Text)
		switch raw.Speaker {
		case dataset.Participants[0]:
			converted.User = content
		case dataset.Participants[1]:
			converted.Assistant = content
		default:
			return converted, fmt.Errorf(
				"dialogue %s has unknown speaker %q",
				raw.DiaID,
				raw.Speaker,
			)
		}
	case "chat-session-history":
		converted.ID = fmt.Sprintf("%s:turn:%d", sessionID, index)
		switch raw.Role {
		case "user":
			converted.User = raw.Content
		case "assistant":
			converted.Assistant = raw.Content
		default:
			return converted, fmt.Errorf(
				"session %s turn %d has unsupported role %q",
				sessionID,
				index,
				raw.Role,
			)
		}
	default:
		return converted, fmt.Errorf(
			"unsupported Session source kind %q",
			dataset.SourceKind,
		)
	}
	if strings.TrimSpace(converted.ID) == "" {
		return converted, fmt.Errorf("session %s turn %d is missing an ID", sessionID, index)
	}
	return converted, nil
}

func parseOccurredAt(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{
		"3:04 pm on 2 January, 2006",
		"2006/01/02 (Mon) 15:04",
		time.RFC3339,
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp %q", value)
}
