// Package effecteval compares immutable raw Sources with a model-maintained Wiki
// while keeping evaluator-only questions and patterns outside the workspace.
package effecteval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
)

const SchemaVersion = "pax.llmwiki.effect-eval.v1"

type Fixture struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Cases         []Case `json:"cases"`
}

type Case struct {
	ID        string    `json:"id"`
	Category  string    `json:"category"`
	Sessions  []Session `json:"sessions"`
	Evaluator Evaluator `json:"evaluator"`
}

type Session struct {
	ID     string  `json:"id"`
	Events []Event `json:"events"`
}

type Event struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Evaluator struct {
	Question           string   `json:"question"`
	AnswerPatterns     []string `json:"answer_patterns"`
	SupportingEventIDs []string `json:"supporting_event_ids"`
}

type Prepared struct {
	DatasetID string `json:"dataset_id"`
	Cases     int    `json:"cases"`
	Sources   int    `json:"sources"`
}

type EvidenceLocation struct {
	SourcePath string
	Anchor     string
}

type CaseResult struct {
	ID           string `json:"id"`
	Category     string `json:"category"`
	RawSourceHit bool   `json:"raw_source_hit"`
	WikiHit      bool   `json:"wiki_hit"`
	Grounded     bool   `json:"grounded"`
}

type Report struct {
	DatasetID        string       `json:"dataset_id"`
	Cases            int          `json:"cases"`
	RawSourceHits    int          `json:"raw_source_hits"`
	WikiHits         int          `json:"wiki_hits"`
	GroundedWikiHits int          `json:"grounded_wiki_hits"`
	RawSourceBytes   int          `json:"raw_source_bytes"`
	WikiBytes        int          `json:"wiki_bytes"`
	Results          []CaseResult `json:"results"`
}

func Prepare(ctx context.Context, fixturePath, root string) (Prepared, error) {
	fixture, err := loadFixture(fixturePath)
	if err != nil {
		return Prepared{}, err
	}
	seenSessions := make(map[string]struct{})
	prepared := Prepared{DatasetID: fixture.ID, Cases: len(fixture.Cases)}
	for _, evalCase := range fixture.Cases {
		for _, session := range evalCase.Sessions {
			if _, exists := seenSessions[session.ID]; exists {
				continue
			}
			seenSessions[session.ID] = struct{}{}
			exported, exportErr := sessionExport(session)
			if exportErr != nil {
				return Prepared{}, fmt.Errorf(
					"prepare case %s: %w",
					evalCase.ID,
					exportErr,
				)
			}
			encoded, marshalErr := json.Marshal(exported)
			if marshalErr != nil {
				return Prepared{}, fmt.Errorf("encode eval Source: %w", marshalErr)
			}
			_, buildErr := workspace.Build(ctx, workspace.BuildConfig{
				Root: root,
				ReadSession: func(context.Context, string) ([]byte, error) {
					return encoded, nil
				},
			}, workspace.BuildRequest{
				SessionID: session.ID, TurnStart: 0, TurnEnd: len(exported.Turns),
			})
			if buildErr != nil {
				return Prepared{}, fmt.Errorf(
					"build eval Source %s: %w",
					session.ID,
					buildErr,
				)
			}
			prepared.Sources++
		}
	}
	return prepared, nil
}

func Score(fixturePath, root string) (Report, error) {
	fixture, err := loadFixture(fixturePath)
	if err != nil {
		return Report{}, err
	}
	sourceMap, err := LoadSourceMap(root)
	if err != nil {
		return Report{}, err
	}
	raw, rawBytes, err := readMarkdownTree(root, "sources")
	if err != nil {
		return Report{}, err
	}
	wiki, wikiBytes, err := readMarkdownTree(root, "wiki")
	if err != nil {
		return Report{}, err
	}
	report := Report{
		DatasetID: fixture.ID, Cases: len(fixture.Cases),
		RawSourceBytes: rawBytes, WikiBytes: wikiBytes,
		Results: make([]CaseResult, 0, len(fixture.Cases)),
	}
	for _, evalCase := range fixture.Cases {
		result := CaseResult{ID: evalCase.ID, Category: evalCase.Category}
		for _, patternText := range evalCase.Evaluator.AnswerPatterns {
			pattern, compileErr := regexp.Compile(patternText)
			if compileErr != nil {
				return Report{}, fmt.Errorf(
					"compile evaluator pattern for %s: %w",
					evalCase.ID,
					compileErr,
				)
			}
			result.RawSourceHit = result.RawSourceHit || pattern.MatchString(raw)
			result.WikiHit = result.WikiHit || pattern.MatchString(wiki)
		}
		result.Grounded = result.WikiHit
		for _, eventID := range evalCase.Evaluator.SupportingEventIDs {
			location, exists := sourceMap[eventID]
			if !exists {
				return Report{}, fmt.Errorf(
					"case %s supporting event %s is missing from Sources",
					evalCase.ID,
					eventID,
				)
			}
			if !strings.Contains(wiki, "#"+location.Anchor) {
				result.Grounded = false
			}
		}
		if result.RawSourceHit {
			report.RawSourceHits++
		}
		if result.WikiHit {
			report.WikiHits++
		}
		if result.Grounded {
			report.GroundedWikiHits++
		}
		report.Results = append(report.Results, result)
	}
	return report, nil
}

func LoadSourceMap(root string) (map[string]EvidenceLocation, error) {
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
				return nil, fmt.Errorf("duplicate evaluator event ID %s", anchor.TurnID)
			}
			result[anchor.TurnID] = EvidenceLocation{
				SourcePath: source.Path, Anchor: anchor.ID,
			}
		}
	}
	return result, nil
}

func loadFixture(path string) (Fixture, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, fmt.Errorf("read effect eval fixture: %w", err)
	}
	var fixture Fixture
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		return Fixture{}, fmt.Errorf("decode effect eval fixture: %w", err)
	}
	if fixture.SchemaVersion != SchemaVersion {
		return Fixture{}, fmt.Errorf(
			"unsupported effect eval schema %q",
			fixture.SchemaVersion,
		)
	}
	if strings.TrimSpace(fixture.ID) == "" || len(fixture.Cases) == 0 {
		return Fixture{}, errors.New("effect eval fixture requires an ID and cases")
	}
	return fixture, nil
}

func sessionExport(session Session) (workspace.SessionExport, error) {
	if strings.TrimSpace(session.ID) == "" || len(session.Events) == 0 {
		return workspace.SessionExport{}, errors.New("eval session requires an ID and events")
	}
	exported := workspace.SessionExport{
		SchemaVersion: workspace.PaxmSessionSchema,
		Agent:         "eval-fixture",
		SessionID:     session.ID,
		Workspace:     "isolated-eval",
		Turns:         make([]workspace.SessionTurn, 0, len(session.Events)),
	}
	for _, event := range session.Events {
		turn := workspace.SessionTurn{ID: event.ID}
		switch event.Role {
		case "user":
			turn.User = event.Content
		case "assistant":
			turn.Assistant = event.Content
		default:
			return workspace.SessionExport{}, fmt.Errorf(
				"event %s has unsupported role %q",
				event.ID,
				event.Role,
			)
		}
		exported.Turns = append(exported.Turns, turn)
	}
	return exported, nil
}

func readMarkdownTree(root, relative string) (string, int, error) {
	var rendered strings.Builder
	totalBytes := 0
	directory := filepath.Join(root, relative)
	err := filepath.WalkDir(directory, func(
		target string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		content, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		rendered.Write(content)
		rendered.WriteByte('\n')
		totalBytes += len(content)
		return nil
	})
	if err != nil {
		return "", 0, fmt.Errorf("read %s Markdown: %w", relative, err)
	}
	return rendered.String(), totalBytes, nil
}
