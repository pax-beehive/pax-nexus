package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	llmwikidriver "github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/artifact/llmwiki"
	pagewikidriver "github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/artifact/pagewiki"
	teamnotedriver "github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/artifact/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/benchmark/qa"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/benchmark/quality"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/benchmark/tester"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	pagewikidomain "github.com/pax-beehive/pax-nexus/internal/pagewiki"
	teamnotedomain "github.com/pax-beehive/pax-nexus/internal/teamnote"
)

type ArtifactSummary struct {
	Product      string                       `json:"product"`
	Artifact     knowledgeeval.ArtifactRecord `json:"artifact"`
	Capabilities knowledgeeval.CapabilitySet  `json:"capabilities"`
	Views        map[string]string            `json:"views"`
}

type ChecklistItem struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type AcceptanceBundle struct {
	SchemaVersion string                      `json:"schema_version"`
	GeneratedAt   time.Time                   `json:"generated_at"`
	Summary       string                      `json:"summary"`
	Artifacts     []ArtifactSummary           `json:"artifacts"`
	Query         knowledgeeval.QuerySnapshot `json:"query"`
	Comparison    []knowledgeeval.MetricDelta `json:"comparison"`
	Checklist     []ChecklistItem             `json:"checklist"`
}

type fixedClock struct {
	current time.Time
}

func (c *fixedClock) Now() time.Time {
	value := c.current
	c.current = c.current.Add(time.Second)
	return value
}

func Generate(ctx context.Context, outputDirectory string) (AcceptanceBundle, error) {
	if outputDirectory == "" {
		return AcceptanceBundle{}, fmt.Errorf("%w: output directory is required", knowledgeeval.ErrInvalidRecord)
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return AcceptanceBundle{}, fmt.Errorf("create demo output directory: %w", err)
	}
	artifactStore, err := knowledgeeval.NewArtifactStore(filepath.Join(outputDirectory, "artifacts"))
	if err != nil {
		return AcceptanceBundle{}, err
	}
	if err := os.MkdirAll(filepath.Join(outputDirectory, "views"), 0o755); err != nil {
		return AcceptanceBundle{}, fmt.Errorf("create demo view directory: %w", err)
	}
	clock := &fixedClock{current: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)}
	now := clock.Now
	runStore := knowledgeeval.NewMemoryRunStore(now)
	runner, err := knowledgeeval.NewRunner(runStore, now)
	if err != nil {
		return AcceptanceBundle{}, err
	}

	llmArtifacts, llmSubjects, llmViews, err := generateLLMWiki(
		ctx,
		outputDirectory,
		artifactStore,
		now,
	)
	if err != nil {
		return AcceptanceBundle{}, err
	}
	llmBenchmarks, err := wikiBenchmarks(artifactStore)
	if err != nil {
		return AcceptanceBundle{}, err
	}
	baselineRun, err := evaluate(
		ctx, runner, now, "run-llmwiki-baseline", llmArtifacts[0], llmSubjects[0], llmBenchmarks,
	)
	if err != nil {
		return AcceptanceBundle{}, err
	}
	currentRun, err := evaluate(
		ctx, runner, now, "run-llmwiki-current", llmArtifacts[1], llmSubjects[1], llmBenchmarks,
	)
	if err != nil {
		return AcceptanceBundle{}, err
	}

	pageArtifact, pageSubject, pageViews, err := generatePageWiki(
		ctx, outputDirectory, artifactStore, now,
	)
	if err != nil {
		return AcceptanceBundle{}, err
	}
	if _, err := evaluate(
		ctx, runner, now, "run-pagewiki", pageArtifact, pageSubject, llmBenchmarks[:2],
	); err != nil {
		return AcceptanceBundle{}, err
	}

	noteArtifact, noteSubject, noteViews, err := generateTeamNote(
		ctx, outputDirectory, artifactStore, now,
	)
	if err != nil {
		return AcceptanceBundle{}, err
	}
	noteBenchmark, err := tester.NewAdapter(artifactStore, tester.Config{Tasks: []tester.Task{{
		ID: "recall-blocker",
		Steps: []tester.Step{{
			Tool: "recall", Input: "evaluation fixtures", Expected: "blocked on fixtures",
		}},
	}}})
	if err != nil {
		return AcceptanceBundle{}, err
	}
	if _, err := evaluate(
		ctx,
		runner,
		now,
		"run-teamnote",
		noteArtifact,
		noteSubject,
		[]knowledgeeval.BenchmarkAdapter{noteBenchmark},
	); err != nil {
		return AcceptanceBundle{}, err
	}

	query, err := exportQuery(ctx, runStore, now)
	if err != nil {
		return AcceptanceBundle{}, err
	}
	bundle := AcceptanceBundle{
		SchemaVersion: "pax.knowledge-eval.acceptance.v1",
		GeneratedAt:   now(),
		Summary:       "Four deterministic runs exercise LLM Wiki, PageWiki, and Team Note through shared benchmark capabilities.",
		Artifacts: []ArtifactSummary{
			{
				Product: "LLM Wiki baseline", Artifact: llmArtifacts[0],
				Capabilities: llmSubjects[0].Capabilities(), Views: llmViews[0],
			},
			{
				Product: "LLM Wiki current", Artifact: llmArtifacts[1],
				Capabilities: llmSubjects[1].Capabilities(), Views: llmViews[1],
			},
			{
				Product: "PageWiki", Artifact: pageArtifact,
				Capabilities: pageSubject.Capabilities(), Views: pageViews,
			},
			{
				Product: "Team Note", Artifact: noteArtifact,
				Capabilities: noteSubject.Capabilities(), Views: noteViews,
			},
		},
		Query:      query,
		Comparison: knowledgeeval.CompareRuns(baselineRun, currentRun),
		Checklist:  completedChecklist(),
	}
	sanitizeBundleRefs(&bundle)
	if err := writeJSON(filepath.Join(outputDirectory, "eval-lab-demo.json"), bundle); err != nil {
		return AcceptanceBundle{}, err
	}
	return bundle, nil
}

func generateLLMWiki(
	ctx context.Context,
	outputDirectory string,
	store *knowledgeeval.ArtifactStore,
	now func() time.Time,
) ([]knowledgeeval.ArtifactRecord, []knowledgeeval.Subject, []map[string]string, error) {
	driver, err := llmwikidriver.NewDriver(store, now)
	if err != nil {
		return nil, nil, nil, err
	}
	builder, err := llmwikidriver.NewDirectoryBuilder(store, now, "demo-revision")
	if err != nil {
		return nil, nil, nil, err
	}
	statements := []string{
		"A local-first Wiki uses immutable Sources.",
		"A local-first Wiki uses immutable Sources and expected-base CAS.",
	}
	artifacts := make([]knowledgeeval.ArtifactRecord, 0, 2)
	subjects := make([]knowledgeeval.Subject, 0, 2)
	views := make([]map[string]string, 0, 2)
	var base *knowledgeeval.ArtifactRecord
	for index, statement := range statements {
		root, cleanup, err := buildWorkspace(ctx, statement, now)
		if err != nil {
			return nil, nil, nil, err
		}
		input, err := store.PutDirectory(ctx, "benchmark-build-input", "v1", root)
		cleanupErr := cleanup()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("store LLM Wiki build input: %w", err)
		}
		if cleanupErr != nil {
			return nil, nil, nil, cleanupErr
		}
		hidden, err := store.PutBytes(ctx, "benchmark-eval-input", "v1", []byte("{}"))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("store LLM Wiki evaluation input: %w", err)
		}
		checkpoint := fmt.Sprintf("checkpoint-%d", index+1)
		artifact, err := builder.Build(ctx, knowledgeeval.BuildRequest{
			Group: knowledgeeval.BenchmarkGroup{
				GroupID: "llmwiki-demo", WorldID: "eval-world",
				CheckpointID: checkpoint, BuildInput: input, EvaluationInput: hidden,
			},
			BaseArtifact: base,
		})
		if err != nil {
			return nil, nil, nil, err
		}
		subject, err := driver.Open(ctx, artifact)
		if err != nil {
			return nil, nil, nil, err
		}
		productViews := make(map[string]string)
		kinds := []string{"native", "canonical", "raw"}
		if base != nil {
			kinds = append(kinds, "diff")
		}
		for _, kind := range kinds {
			view, err := driver.RenderView(ctx, knowledgeeval.ArtifactViewRequest{
				Artifact: artifact, BaseArtifact: base, Kind: kind,
			})
			if err != nil {
				return nil, nil, nil, err
			}
			name := fmt.Sprintf("llmwiki-%d-%s.html", index+1, kind)
			if err := copyView(ctx, store, view, filepath.Join(outputDirectory, "views", name)); err != nil {
				return nil, nil, nil, err
			}
			productViews[kind] = "views/" + name
		}
		artifacts = append(artifacts, artifact)
		subjects = append(subjects, subject)
		views = append(views, productViews)
		base = &artifacts[len(artifacts)-1]
	}
	return artifacts, subjects, views, nil
}

func buildWorkspace(
	ctx context.Context,
	statement string,
	now func() time.Time,
) (string, func() error, error) {
	root, err := os.MkdirTemp("", "knowledge-eval-wiki-")
	if err != nil {
		return "", nil, fmt.Errorf("create LLM Wiki demo workspace: %w", err)
	}
	cleanup := func() error {
		writableErr := makeDirectoriesWritable(root)
		removeErr := os.RemoveAll(root)
		if writableErr != nil || removeErr != nil {
			return fmt.Errorf(
				"clean LLM Wiki demo workspace: %w",
				errors.Join(writableErr, removeErr),
			)
		}
		return nil
	}
	exported := workspace.SessionExport{
		SchemaVersion: workspace.PaxmSessionSchema,
		Agent:         "codex", SessionID: "demo-session", Workspace: "/demo",
		Turns: []workspace.SessionTurn{{
			ID: "turn-1", User: "How should the Wiki work?",
			Assistant: statement, CreatedAt: now(),
		}},
	}
	encoded, err := json.Marshal(exported)
	if err != nil {
		return "", nil, errors.Join(
			fmt.Errorf("encode LLM Wiki demo session: %w", err),
			cleanup(),
		)
	}
	result, err := workspace.Build(ctx, workspace.BuildConfig{
		Root: root,
		ReadSession: func(context.Context, string) ([]byte, error) {
			return encoded, nil
		},
	}, workspace.BuildRequest{SessionID: "demo-session", TurnStart: 0, TurnEnd: 1})
	if err != nil {
		return "", nil, errors.Join(
			fmt.Errorf("build LLM Wiki demo workspace: %w", err),
			cleanup(),
		)
	}
	index := "---\ntype: portal\n---\n\n# Knowledge Eval\n\n" +
		"This page explains the architecture of the knowledge evaluation system.\n\n" +
		"## Architecture\n\n" + statement + " [Source](../" +
		result.Source.Path + "#" + result.Source.Anchors[1].ID + ")\n"
	if err := os.WriteFile(filepath.Join(root, "wiki", "index.md"), []byte(index), 0o644); err != nil {
		return "", nil, errors.Join(
			fmt.Errorf("write LLM Wiki demo article: %w", err),
			cleanup(),
		)
	}
	if report := workspace.Validate(root); !report.Valid {
		return "", nil, errors.Join(
			fmt.Errorf("%w: demo workspace invalid: %s", knowledgeeval.ErrInvalidRecord, report.String()),
			cleanup(),
		)
	}
	return root, cleanup, nil
}

func generatePageWiki(
	ctx context.Context,
	outputDirectory string,
	store *knowledgeeval.ArtifactStore,
	now func() time.Time,
) (knowledgeeval.ArtifactRecord, knowledgeeval.Subject, map[string]string, error) {
	driver, err := pagewikidriver.NewDriver(store, now)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, nil, nil, err
	}
	snapshot := pagewikidriver.Snapshot{
		Pages: []pagewikidomain.Page{{
			ID: "page-architecture", Slug: "architecture",
			Title: "Architecture", CurrentRevisionID: "revision-1",
		}},
		Revisions: []pagewikidomain.PageRevision{{
			ID: "revision-1", PageID: "page-architecture",
			Title: "Architecture", Summary: "Evaluation system design",
			Markdown: "The architecture is local-first and uses expected-base CAS.",
			Citations: []pagewikidomain.PageCitation{{SourceAnchors: []pagewikidomain.SourceAnchor{{
				ID: "anchor-1", SourceRevisionID: "source-1",
			}}}},
		}},
		TopicTree: pagewikidomain.TopicTree{
			Topics: []pagewikidomain.Topic{{ID: "topic-1", Slug: "eval", Title: "Evaluation"}},
			Placements: []pagewikidomain.PagePlacement{{
				PageID: "page-architecture", TopicID: "topic-1", Rank: 1,
			}},
		},
	}
	artifact, err := driver.Publish(
		ctx,
		snapshot,
		knowledgeeval.BenchmarkGroup{
			GroupID: "pagewiki-demo", WorldID: "eval-world", CheckpointID: "checkpoint-1",
		},
		knowledgeeval.Provenance{
			BuilderID: "pagewiki-fixture", BuilderVersion: "v1", CodeRevision: "demo-revision",
		},
	)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, nil, nil, err
	}
	subject, err := driver.Open(ctx, artifact)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, nil, nil, err
	}
	views := make(map[string]string)
	for _, kind := range []string{"native", "raw"} {
		view, err := driver.RenderView(ctx, knowledgeeval.ArtifactViewRequest{
			Artifact: artifact, Kind: kind,
		})
		if err != nil {
			return knowledgeeval.ArtifactRecord{}, nil, nil, err
		}
		name := "pagewiki-" + kind + ".html"
		if err := copyView(ctx, store, view, filepath.Join(outputDirectory, "views", name)); err != nil {
			return knowledgeeval.ArtifactRecord{}, nil, nil, err
		}
		views[kind] = "views/" + name
	}
	return artifact, subject, views, nil
}

func generateTeamNote(
	ctx context.Context,
	outputDirectory string,
	store *knowledgeeval.ArtifactStore,
	now func() time.Time,
) (knowledgeeval.ArtifactRecord, knowledgeeval.Subject, map[string]string, error) {
	driver, err := teamnotedriver.NewDriver(store, now)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, nil, nil, err
	}
	artifact, err := driver.Publish(
		ctx,
		teamnotedriver.Snapshot{Notes: []teamnotedomain.Note{{
			ID: "note-1", Kind: teamnotedomain.KindBlocker, Subject: "Evaluation",
			Body: "Evaluation is blocked on fixtures.", State: teamnotedomain.StateActive,
		}}},
		knowledgeeval.BenchmarkGroup{
			GroupID: "teamnote-demo", WorldID: "eval-world", CheckpointID: "checkpoint-1",
		},
		knowledgeeval.Provenance{
			BuilderID: "teamnote-fixture", BuilderVersion: "v1", CodeRevision: "demo-revision",
		},
	)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, nil, nil, err
	}
	subject, err := driver.Open(ctx, artifact)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, nil, nil, err
	}
	views := make(map[string]string)
	for _, kind := range []string{"native", "raw"} {
		view, err := driver.RenderView(ctx, knowledgeeval.ArtifactViewRequest{
			Artifact: artifact, Kind: kind,
		})
		if err != nil {
			return knowledgeeval.ArtifactRecord{}, nil, nil, err
		}
		name := "teamnote-" + kind + ".html"
		if err := copyView(ctx, store, view, filepath.Join(outputDirectory, "views", name)); err != nil {
			return knowledgeeval.ArtifactRecord{}, nil, nil, err
		}
		views[kind] = "views/" + name
	}
	return artifact, subject, views, nil
}

func wikiBenchmarks(
	store *knowledgeeval.ArtifactStore,
) ([]knowledgeeval.BenchmarkAdapter, error) {
	qualityBenchmark, err := quality.NewAdapter(store, quality.Config{MinimumScore: 0.75})
	if err != nil {
		return nil, err
	}
	qaBenchmark, err := qa.NewAdapter(store, qa.Config{Cases: []qa.Case{{
		ID: "cas-answer", Question: "architecture expected-base CAS",
		Expected: "expected-base CAS",
	}}})
	if err != nil {
		return nil, err
	}
	testerBenchmark, err := tester.NewAdapter(store, tester.Config{Tasks: []tester.Task{{
		ID: "inspect-wiki",
		Steps: []tester.Step{
			{Tool: "search", Input: "expected-base CAS", Expected: "expected-base CAS"},
			{Tool: "get", Input: "wiki/index.md", Expected: "expected-base CAS"},
			{Tool: "navigate", Input: "wiki/index.md", Expected: "Knowledge Eval"},
		},
	}}})
	if err != nil {
		return nil, err
	}
	return []knowledgeeval.BenchmarkAdapter{
		qualityBenchmark,
		qaBenchmark,
		testerBenchmark,
	}, nil
}

func evaluate(
	ctx context.Context,
	runner *knowledgeeval.Runner,
	now func() time.Time,
	runID string,
	artifact knowledgeeval.ArtifactRecord,
	subject knowledgeeval.Subject,
	benchmarks []knowledgeeval.BenchmarkAdapter,
) (knowledgeeval.RunDetail, error) {
	detail, err := runner.Evaluate(ctx, knowledgeeval.Run{
		ID: runID, WorldID: artifact.WorldID, GroupID: artifact.GroupID,
		CheckpointID: artifact.CheckpointID, ArtifactID: artifact.ArtifactID,
		Metadata: runMetadata(artifact), CreatedAt: now(),
	}, subject, benchmarks)
	if err != nil {
		return knowledgeeval.RunDetail{}, fmt.Errorf("evaluate demo run %s: %w", runID, err)
	}
	return detail, nil
}

func runMetadata(artifact knowledgeeval.ArtifactRecord) map[string]string {
	model := artifact.Provenance.Metadata["model"]
	if model == "" {
		return nil
	}
	return map[string]string{"model": model}
}

func exportQuery(
	ctx context.Context,
	store knowledgeeval.RunStore,
	now func() time.Time,
) (knowledgeeval.QuerySnapshot, error) {
	service, err := knowledgeeval.NewQueryService(store, now)
	if err != nil {
		return knowledgeeval.QuerySnapshot{}, err
	}
	var encoded bytes.Buffer
	if err := service.Export(ctx, &encoded); err != nil {
		return knowledgeeval.QuerySnapshot{}, err
	}
	var snapshot knowledgeeval.QuerySnapshot
	if err := json.Unmarshal(encoded.Bytes(), &snapshot); err != nil {
		return knowledgeeval.QuerySnapshot{}, fmt.Errorf("decode demo query snapshot: %w", err)
	}
	return snapshot, nil
}

func copyView(
	ctx context.Context,
	store *knowledgeeval.ArtifactStore,
	view knowledgeeval.ArtifactViewRecord,
	target string,
) error {
	content, err := store.OpenBytes(ctx, view.Payload)
	if err != nil {
		return fmt.Errorf("open rendered artifact view: %w", err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		return fmt.Errorf("write rendered artifact view: %w", err)
	}
	return nil
}

func sanitizeBundleRefs(bundle *AcceptanceBundle) {
	for index := range bundle.Artifacts {
		sanitizeRef(&bundle.Artifacts[index].Artifact.Payload)
	}
	for runIndex := range bundle.Query.Runs {
		for trialIndex := range bundle.Query.Runs[runIndex].Trials {
			result := bundle.Query.Runs[runIndex].Trials[trialIndex].Result
			if result == nil {
				continue
			}
			sanitizeRef(&result.Observations)
			sanitizeRef(&result.RawReport)
		}
	}
}

func sanitizeRef(ref *knowledgeeval.OpaqueRef) {
	ref.URI = "artifact://sha256/" + ref.SHA256
}

func completedChecklist() []ChecklistItem {
	items := make([]ChecklistItem, 0, 16)
	for index := 0; index <= 15; index++ {
		items = append(items, ChecklistItem{
			ID: fmt.Sprintf("S%02d", index), Status: "complete",
		})
	}
	return items
}

func writeJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode demo acceptance bundle: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("write demo acceptance bundle: %w", err)
	}
	return nil
}

func makeDirectoriesWritable(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || !entry.IsDir() {
			return walkErr
		}
		return os.Chmod(path, 0o755)
	})
}
