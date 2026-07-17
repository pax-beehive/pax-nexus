package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/eval/extractionshadow"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		if _, writeErr := fmt.Fprintf(os.Stderr, "extraction shadow failed: %v\n", err); writeErr != nil {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

type shadowConfig struct {
	dsn, manifestPath, fixturesPath, runID, arm string
	extractorVersion, baseURL, model            string
	promptVersion, outputDir                    string
	parallelism                                 int
}

func parseFlags(args []string) (shadowConfig, error) {
	flags := flag.NewFlagSet("team-memory-extraction-shadow", flag.ContinueOnError)
	var config shadowConfig
	flags.StringVar(&config.dsn, "dsn", os.Getenv("EVAL_V2_POSTGRES_DSN"), "PostgreSQL DSN holding the persisted run events")
	flags.StringVar(&config.manifestPath, "manifest", "", "Selection manifest JSON mapping cases to scopes")
	flags.StringVar(&config.fixturesPath, "fixtures", "", "Stage fixture set JSON with gold atoms")
	flags.StringVar(&config.runID, "run-id", "", "Eval run identifier the events were persisted under")
	flags.StringVar(&config.arm, "arm", "team_note", "Eval arm scope suffix (team_note or team_note_hybrid)")
	flags.StringVar(&config.extractorVersion, "extractor", extractor.ExtractionVersionV2, "Extraction protocol version (v1 or v2)")
	flags.StringVar(&config.baseURL, "extractor-base-url", os.Getenv("TEAM_MEMORY_EXTRACTOR_BASE_URL"), "OpenAI-compatible extractor endpoint")
	flags.StringVar(&config.model, "extractor-model", os.Getenv("TEAM_MEMORY_EXTRACTOR_MODEL"), "Extractor model")
	flags.StringVar(&config.promptVersion, "prompt-version", "", "Prompt version tag (defaults to the extractor version)")
	flags.IntVar(&config.parallelism, "parallelism", 4, "Number of cases replayed concurrently")
	flags.StringVar(&config.outputDir, "output-dir", "", "Shadow artifact output directory")
	if err := flags.Parse(args); err != nil {
		return shadowConfig{}, fmt.Errorf("parse extraction shadow flags: %w", err)
	}
	if config.dsn == "" || config.manifestPath == "" || config.fixturesPath == "" || config.runID == "" || config.outputDir == "" {
		return shadowConfig{}, fmt.Errorf("parse extraction shadow flags: dsn, manifest, fixtures, run-id, and output-dir are required")
	}
	if config.baseURL == "" || config.model == "" {
		return shadowConfig{}, fmt.Errorf("parse extraction shadow flags: extractor-base-url and extractor-model are required")
	}
	if config.promptVersion == "" {
		config.promptVersion = config.extractorVersion
	}
	return config, nil
}

func run(args []string, stdout io.Writer) error {
	config, err := parseFlags(args)
	if err != nil {
		return err
	}
	scopeSuffix, err := armScopeSuffix(config.arm)
	if err != nil {
		return err
	}
	cases, err := extractionshadow.LoadManifestCases(config.manifestPath)
	if err != nil {
		return err
	}
	fixtures, err := stageeval.LoadFixtureSet(config.fixturesPath)
	if err != nil {
		return err
	}
	fixtureByID := make(map[string]stageeval.Fixture, len(fixtures.Cases))
	for _, fixture := range fixtures.Cases {
		fixtureByID[fixture.CaseID] = fixture
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, config.dsn)
	if err != nil {
		return fmt.Errorf("open extraction shadow store: %w", err)
	}
	defer pool.Close()

	protocol, err := buildExtractor(config.extractorVersion, config.baseURL, config.model, config.promptVersion)
	if err != nil {
		return err
	}
	results, err := replayCases(ctx, pool, protocol, cases, fixtureByID, config, scopeSuffix, stdout)
	if err != nil {
		return err
	}
	report, err := extractionshadow.BuildReport(config.runID, config.arm, config.extractorVersion, fixtures, results)
	if err != nil {
		return err
	}
	if err := writeArtifacts(config.outputDir, report, results); err != nil {
		return err
	}
	stage := report.Stage
	telemetry := report.Telemetry
	if _, err := fmt.Fprintf(stdout,
		"extraction shadow (%s): %d cases, fact recall %.3f, leakage %d, calls %d (errors %d), cache %.3f, mean %.0f ms, p95 %d ms, output tokens %d, unreviewed events %d, orphan claims %d\n",
		report.Extractor, stage.Cases, stage.ExtractionFactRecall, stage.ExtractionLeakageItems,
		telemetry.Calls, telemetry.CallErrors, telemetry.CacheHitRate, telemetry.MeanDurationMS,
		telemetry.P95DurationMS, telemetry.OutputTokens, telemetry.UnreviewedEvents,
		telemetry.OrphanClaims); err != nil {
		return fmt.Errorf("write extraction shadow summary: %w", err)
	}
	return nil
}

// replayCases exports and replays every manifest case with bounded
// parallelism; every case must succeed for the shadow to be comparable.
func replayCases(
	ctx context.Context, pool *pgxpool.Pool, protocol extractor.Extractor,
	cases []extractionshadow.ManifestCase, fixtureByID map[string]stageeval.Fixture,
	config shadowConfig, scopeSuffix string, stdout io.Writer,
) ([]extractionshadow.CaseRun, error) {
	results := make([]extractionshadow.CaseRun, 0, len(cases))
	semaphore := make(chan struct{}, config.parallelism)
	var wait sync.WaitGroup
	var mu sync.Mutex
	failures := 0
	for _, manifestCase := range cases {
		if _, ok := fixtureByID[manifestCase.ID]; !ok {
			return nil, fmt.Errorf("manifest case %q has no stage fixture", manifestCase.ID)
		}
		wait.Add(1)
		go func(manifestCase extractionshadow.ManifestCase) {
			defer wait.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			err := replayOneCase(ctx, pool, protocol, config.runID, scopeSuffix, manifestCase, stdout, &mu, &results)
			if err != nil {
				mu.Lock()
				failures++
				mu.Unlock()
				if _, writeErr := fmt.Fprintf(stdout, "shadow case %s FAILED: %v\n", manifestCase.ID, err); writeErr == nil {
					return
				}
			}
		}(manifestCase)
	}
	wait.Wait()
	if failures > 0 {
		return nil, fmt.Errorf("extraction shadow: %d of %d cases failed", failures, len(cases))
	}
	return results, nil
}

func replayOneCase(
	ctx context.Context, pool *pgxpool.Pool, protocol extractor.Extractor,
	runID, scopeSuffix string, manifestCase extractionshadow.ManifestCase,
	stdout io.Writer, mu *sync.Mutex, results *[]extractionshadow.CaseRun,
) error {
	scopeID := runID + "-" + manifestCase.ScopeID + scopeSuffix
	streams, err := extractionshadow.ExportStreams(ctx, pool, scopeID)
	if err != nil {
		return err
	}
	run, err := extractionshadow.RunCase(ctx, manifestCase.ID, scopeID, streams, protocol)
	if err != nil {
		return err
	}
	mu.Lock()
	*results = append(*results, run)
	mu.Unlock()
	if _, err := fmt.Fprintf(stdout, "shadow case %s: %d streams, %d events, %d slices, %d notes\n",
		manifestCase.ID, run.Streams, run.Events, len(run.Slices), len(run.Notes)); err != nil {
		return fmt.Errorf("write shadow case progress: %w", err)
	}
	return nil
}

func writeArtifacts(outputDir string, report extractionshadow.Report, results []extractionshadow.CaseRun) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create extraction shadow output directory: %w", err)
	}
	if err := writeFile(filepath.Join(outputDir, "shadow-report.json"), func(writer io.Writer) error {
		return extractionshadow.WriteReport(writer, report)
	}); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(outputDir, "notes.jsonl"), func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		for _, run := range results {
			for _, note := range run.Notes {
				if err := encoder.Encode(struct {
					CaseID string `json:"case_id"`
					teamnote.Note
				}{run.CaseID, note}); err != nil {
					return fmt.Errorf("encode shadow note: %w", err)
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return writeFile(filepath.Join(outputDir, "slices.jsonl"), func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		for _, run := range results {
			for _, slice := range run.Slices {
				if err := encoder.Encode(struct {
					CaseID string `json:"case_id"`
					extractionshadow.SliceRecord
				}{run.CaseID, slice}); err != nil {
					return fmt.Errorf("encode shadow slice record: %w", err)
				}
			}
		}
		return nil
	})
}

func buildExtractor(version, baseURL, model, promptVersion string) (extractor.Extractor, error) {
	return extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: baseURL, APIKey: os.Getenv("TEAM_MEMORY_EXTRACTOR_API_KEY"), Model: model,
		PromptVersion: promptVersion, Client: &http.Client{},
		ContextMode: extractor.ContextModeRolling, EpisodeStore: extractor.NewMemoryEpisodeStore(),
		ExtractionVersion: version,
		SummaryEnabled:    true,
	})
}

func armScopeSuffix(arm string) (string, error) {
	switch arm {
	case "team_note":
		return "", nil
	case "team_note_hybrid":
		return "-team-note-hybrid", nil
	default:
		return "", fmt.Errorf("parse extraction shadow flags: unsupported arm %q", arm)
	}
}

func writeFile(path string, write func(io.Writer) error) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create extraction shadow artifact %q: %w", path, err)
	}
	writeErr := write(file)
	closeErr := file.Close()
	if writeErr != nil {
		writeErr = fmt.Errorf("write extraction shadow artifact %q: %w", path, writeErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close extraction shadow artifact %q: %w", path, closeErr)
	}
	return errors.Join(writeErr, closeErr)
}
