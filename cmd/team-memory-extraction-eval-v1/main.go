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
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/eval/extractioneval"
	"github.com/pax-beehive/pax-nexus/internal/eval/extractionshadow"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractionbudget"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		if _, writeErr := fmt.Fprintf(os.Stderr, "extraction-eval-v1 failed: %v\n", err); writeErr != nil {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

type evalConfig struct {
	dsn              string
	manifestPath     string
	fixturesPath     string
	sourceRunID      string
	runID            string
	scopeSuffix      string
	extractorVersion string
	baseURL          string
	model            string
	promptVersion    string
	outputDir        string
	profilePath      string
	v2Variant        string
	resume           bool
	preflightOnly    bool
	sliceEventLimit  int
	executionPolicy  extractor.ExecutionPolicy
}

type resolvedConfig struct {
	SchemaVersion            string `json:"schema_version"`
	RunID                    string `json:"run_id"`
	SourceRunID              string `json:"source_run_id"`
	ManifestPath             string `json:"manifest_path"`
	FixturesPath             string `json:"fixtures_path"`
	ScopeSuffix              string `json:"scope_suffix,omitempty"`
	ExtractorVersion         string `json:"extractor_version"`
	ExtractorBaseURL         string `json:"extractor_base_url"`
	ExtractorModel           string `json:"extractor_model"`
	PromptVersion            string `json:"prompt_version"`
	ProfilePath              string `json:"profile_path,omitempty"`
	ProfileName              string `json:"profile_name"`
	V2Variant                string `json:"v2_variant"`
	SliceEventLimit          int    `json:"slice_event_limit"`
	ProviderTimeoutMS        int64  `json:"provider_timeout_ms"`
	ProviderMaxAttempts      int    `json:"provider_max_attempts"`
	ProviderRetryBackoffMS   int64  `json:"provider_retry_backoff_ms"`
	ProviderMaxResponseBytes int64  `json:"provider_max_response_bytes"`
	PrimaryMaxOutputTokens   int    `json:"primary_max_output_tokens"`
}

func parseFlags(args []string) (evalConfig, error) {
	flags := flag.NewFlagSet("team-memory-extraction-eval-v1", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var config evalConfig
	flags.StringVar(&config.dsn, "dsn", os.Getenv("EVAL_V2_POSTGRES_DSN"), "PostgreSQL DSN holding the persisted source events")
	flags.StringVar(&config.manifestPath, "manifest", "", "Selection manifest JSON mapping cases to source scopes")
	flags.StringVar(&config.fixturesPath, "fixtures", "", "Extraction fixture set JSON with gold atoms")
	flags.StringVar(&config.sourceRunID, "source-run-id", "", "Run identifier under which source events were persisted")
	flags.StringVar(&config.runID, "run-id", "", "Unique extraction-eval-v1 run identifier")
	flags.StringVar(&config.scopeSuffix, "scope-suffix", "", "Optional suffix on persisted source scopes")
	flags.StringVar(&config.extractorVersion, "extractor", extractor.ExtractionVersionV2, "Extraction protocol version (v1, v1.1, or v2)")
	flags.StringVar(&config.baseURL, "extractor-base-url", os.Getenv("TEAM_MEMORY_EXTRACTOR_BASE_URL"), "OpenAI-compatible extractor endpoint")
	flags.StringVar(&config.model, "extractor-model", os.Getenv("TEAM_MEMORY_EXTRACTOR_MODEL"), "Extractor model")
	flags.StringVar(&config.promptVersion, "prompt-version", "", "Prompt version tag (defaults to extractor version)")
	flags.StringVar(&config.outputDir, "output-dir", "", "Artifact directory (defaults to runs/extraction-eval-v1/<run-id>)")
	flags.StringVar(&config.profilePath, "profile", "", "Optional bounded source-event profile JSON")
	strategyDefault := extractor.DefaultCandidateStrategy()
	strategyNames := strings.Join(extractor.CandidateStrategyNames(), ", ")
	flags.StringVar(&config.v2Variant, "candidate-strategy", strategyDefault, "Extraction candidate strategy ("+strategyNames+")")
	flags.StringVar(&config.v2Variant, "v2-variant", strategyDefault, "Deprecated alias for -candidate-strategy")
	flags.BoolVar(&config.resume, "resume", false, "Resume an interrupted run from per-slice artifacts")
	flags.BoolVar(&config.preflightOnly, "preflight-only", false, "Validate and size source Events without calling a model")
	flags.IntVar(&config.sliceEventLimit, "slice-event-limit", 25, "Maximum new session events per primary extraction slice")
	providerDefaults := extractionbudget.DefaultProviderPolicy()
	flags.DurationVar(&config.executionPolicy.AttemptTimeout, "provider-timeout", providerDefaults.AttemptTimeout, "Deadline for one physical provider attempt")
	flags.IntVar(&config.executionPolicy.MaxAttempts, "provider-max-attempts", providerDefaults.MaxAttempts, "Maximum physical provider attempts per logical call")
	flags.DurationVar(&config.executionPolicy.RetryBackoff, "provider-retry-backoff", providerDefaults.RetryBackoff, "Delay between retryable provider attempts")
	flags.Int64Var(&config.executionPolicy.MaxResponseBytes, "provider-max-response-bytes", providerDefaults.MaxResponseBytes, "Maximum provider response body size")
	flags.IntVar(&config.executionPolicy.PrimaryMaxOutputTokens, "primary-max-output-tokens", providerDefaults.PrimaryMaxOutputTokens, "Primary extraction response token budget")
	config.executionPolicy.SummaryMaxOutputTokens = providerDefaults.SummaryMaxOutputTokens
	config.executionPolicy.CompactionMaxOutputTokens = providerDefaults.CompactionMaxOutputTokens
	if err := flags.Parse(args); err != nil {
		return evalConfig{}, fmt.Errorf("parse extraction-eval-v1 flags: %w", err)
	}
	if config.dsn == "" || config.manifestPath == "" || config.fixturesPath == "" || config.sourceRunID == "" || config.runID == "" {
		return evalConfig{}, fmt.Errorf("parse extraction-eval-v1 flags: dsn, manifest, fixtures, source-run-id, and run-id are required")
	}
	if !config.preflightOnly && (config.baseURL == "" || config.model == "") {
		return evalConfig{}, fmt.Errorf("parse extraction-eval-v1 flags: extractor-base-url and extractor-model are required")
	}
	if config.sliceEventLimit < 1 {
		return evalConfig{}, fmt.Errorf("parse extraction-eval-v1 flags: slice-event-limit must be positive")
	}
	if err := config.executionPolicy.Validate(); err != nil {
		return evalConfig{}, fmt.Errorf("parse extraction-eval-v1 flags: %w", err)
	}
	if config.promptVersion == "" {
		config.promptVersion = config.extractorVersion
	}
	if config.outputDir == "" {
		config.outputDir = filepath.Join("runs", "extraction-eval-v1", config.runID)
	}
	return config, nil
}

type evalInputs struct {
	profile  extractioneval.Profile
	fixtures stageeval.FixtureSet
	plans    []extractioneval.DomainPlan
}

func run(args []string, stdout io.Writer) error {
	config, err := parseFlags(args)
	if err != nil {
		return err
	}
	inputs, err := loadEvalInputs(config)
	if err != nil {
		return err
	}
	prepared, err := preflightSource(context.Background(), config, inputs, stdout)
	if err != nil {
		return err
	}
	if config.preflightOnly {
		return writePreflightSuccess(stdout, inputs.profile.Name)
	}
	return runPaidEval(context.Background(), config, inputs, prepared, stdout)
}

func loadEvalInputs(config evalConfig) (evalInputs, error) {
	manifestCases, err := extractionshadow.LoadManifestCases(config.manifestPath)
	if err != nil {
		return evalInputs{}, err
	}
	fixtures, err := stageeval.LoadFixtureSet(config.fixturesPath)
	if err != nil {
		return evalInputs{}, err
	}
	profile := extractioneval.FullSourceProfile(manifestCases)
	if config.profilePath != "" {
		profile, err = extractioneval.LoadProfile(config.profilePath)
		if err != nil {
			return evalInputs{}, err
		}
	}
	manifestCases, fixtures, err = extractioneval.SelectProfileCases(profile, manifestCases, fixtures)
	if err != nil {
		return evalInputs{}, err
	}
	plans, err := extractioneval.PlanDomains(config.sourceRunID, config.scopeSuffix, manifestCases)
	if err != nil {
		return evalInputs{}, err
	}
	return evalInputs{profile: profile, fixtures: fixtures, plans: plans}, nil
}

func preflightSource(
	ctx context.Context,
	config evalConfig,
	inputs evalInputs,
	stdout io.Writer,
) ([]preparedDomain, error) {
	pool, err := pgxpool.New(ctx, config.dsn)
	if err != nil {
		return nil, fmt.Errorf("open extraction-eval-v1 source store: %w", err)
	}
	defer pool.Close()
	return prepareDomains(ctx, pool, inputs.plans, inputs.profile, inputs.fixtures, config.sliceEventLimit, stdout)
}

func writePreflightSuccess(stdout io.Writer, profileName string) error {
	if _, err := fmt.Fprintf(stdout, "preflight %s passed with no provider calls\n", profileName); err != nil {
		return fmt.Errorf("write extraction-eval-v1 preflight result: %w", err)
	}
	return nil
}

func runPaidEval(
	ctx context.Context,
	config evalConfig,
	inputs evalInputs,
	prepared []preparedDomain,
	stdout io.Writer,
) (returnedErr error) {
	if err := validateRunDirectory(config.outputDir, config.resume); err != nil {
		return err
	}
	journal, err := openRunJournal(config.outputDir, config.resume)
	if err != nil {
		return err
	}
	defer func() {
		if journal != nil {
			returnedErr = errors.Join(returnedErr, journal.close())
		}
	}()
	if err := ensureResumeConfig(config, inputs.profile, prepared); err != nil {
		return err
	}
	episodeStore, err := extractionshadow.OpenFileEpisodeStore(
		filepath.Join(config.outputDir, "episodes.json"), config.resume,
	)
	if err != nil {
		return err
	}
	protocol, err := buildExtractor(config, episodeStore, journal.observeProviderCall)
	if err != nil {
		return err
	}
	backgroundDrained := false
	defer func() {
		if !backgroundDrained {
			returnedErr = errors.Join(returnedErr, waitForBackgroundCalls(
				context.WithoutCancel(ctx), protocol, config.executionPolicy,
			))
		}
	}()
	domainRuns, err := replayDomains(ctx, protocol, prepared, journal, config.sliceEventLimit, stdout)
	if err != nil {
		return err
	}
	if err := waitForBackgroundCalls(context.WithoutCancel(ctx), protocol, config.executionPolicy); err != nil {
		backgroundDrained = true
		return err
	}
	backgroundDrained = true
	closeErr := journal.close()
	for index := range domainRuns {
		domainRuns[index].ProviderCalls = journal.callsForScope(domainRuns[index].ScopeID)
	}
	journal = nil
	if closeErr != nil {
		return closeErr
	}
	report, err := extractioneval.BuildReport(
		config.runID, config.sourceRunID, config.extractorVersion, inputs.fixtures, inputs.plans, domainRuns,
	)
	if err != nil {
		return err
	}
	report.Profile = inputs.profile.Name
	report.V2Variant = config.v2Variant
	decorateDomainReports(&report, prepared)
	if err := writeArtifacts(config, report, domainRuns); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout,
		"extraction-eval-v1 (%s/%s): %d cases over %d domains, fact recall %.3f, leakage %d, primary slices %d, provider calls %d (errors %d), cache %.3f, p95 %d ms, output tokens %d\n",
		report.Extractor, report.V2Variant, report.Summary.Cases, len(report.Domains), report.Summary.FactRecall,
		report.Summary.LeakageItems, report.Telemetry.Calls, report.Telemetry.ProviderCalls, report.Telemetry.ProviderCallErrors,
		report.Telemetry.CacheHitRate, report.Telemetry.P95DurationMS, report.Telemetry.OutputTokens); err != nil {
		return fmt.Errorf("write extraction-eval-v1 summary: %w", err)
	}
	return nil
}

func decorateDomainReports(report *extractioneval.Report, prepared []preparedDomain) {
	selectionByScope := make(map[string]extractioneval.Selection, len(prepared))
	for _, domain := range prepared {
		selectionByScope[domain.plan.ScopeID] = domain.selection
	}
	for index := range report.Domains {
		selection := selectionByScope[report.Domains[index].ScopeID]
		report.Domains[index].SourceEvents = selection.SourceEvents
		report.Domains[index].SelectedEvents = selection.SelectedEvents
		report.Domains[index].ExpectedSlices = selection.ExpectedSlices
	}
}

func validateOutputAvailable(outputDir string) error {
	return validateRunDirectory(outputDir, false)
}

func validateRunDirectory(outputDir string, resume bool) error {
	_, err := os.Stat(outputDir)
	if resume && err == nil {
		if _, reportErr := os.Stat(filepath.Join(outputDir, "report.json")); reportErr == nil {
			return fmt.Errorf("validate extraction-eval-v1 resume directory %q: run is already complete", outputDir)
		} else if !errors.Is(reportErr, os.ErrNotExist) {
			return fmt.Errorf("validate extraction-eval-v1 resume report: %w", reportErr)
		}
		return nil
	}
	if !resume && err == nil {
		return fmt.Errorf("validate extraction-eval-v1 output directory %q: already exists", outputDir)
	}
	if resume && errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("validate extraction-eval-v1 resume directory %q: does not exist", outputDir)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("validate extraction-eval-v1 output directory %q: %w", outputDir, err)
	}
	return nil
}

type preparedDomain struct {
	plan      extractioneval.DomainPlan
	streams   []extractionshadow.StreamEvents
	selection extractioneval.Selection
}

func prepareDomains(
	ctx context.Context,
	pool *pgxpool.Pool,
	plans []extractioneval.DomainPlan,
	profile extractioneval.Profile,
	fixtures stageeval.FixtureSet,
	sliceEventLimit int,
	stdout io.Writer,
) ([]preparedDomain, error) {
	prepared := make([]preparedDomain, 0, len(plans))
	for _, plan := range plans {
		streams, err := extractionshadow.ExportStreams(ctx, pool, plan.ScopeID)
		if err != nil {
			return nil, err
		}
		selected, selection, err := extractioneval.PreflightAndSelectStreams(
			profile, plan.CaseIDs, fixtures, streams, sliceEventLimit,
		)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, preparedDomain{plan: plan, streams: selected, selection: selection})
		if _, err := fmt.Fprintf(
			stdout, "preflight %s: %d source events -> %d selected events, %d streams, %d expected slices\n",
			plan.ScopeID, selection.SourceEvents, selection.SelectedEvents, selection.SelectedStreams, selection.ExpectedSlices,
		); err != nil {
			return nil, fmt.Errorf("write extraction-eval-v1 preflight: %w", err)
		}
	}
	return prepared, nil
}

func replayDomains(
	ctx context.Context,
	protocol extractor.Extractor,
	prepared []preparedDomain,
	journal *runJournal,
	sliceEventLimit int,
	stdout io.Writer,
) ([]extractionshadow.CaseRun, error) {
	runs := make([]extractionshadow.CaseRun, 0, len(prepared))
	for index, domain := range prepared {
		run, err := extractionshadow.RunCaseWithOptions(
			ctx, fmt.Sprintf("domain-%d", index+1), domain.plan.ScopeID, domain.streams, protocol,
			extractionshadow.RunOptions{
				SliceEventLimit: sliceEventLimit, InitialSlices: journal.initialSlices(domain.plan.ScopeID),
				OnSlice: func(record extractionshadow.SliceRecord) error {
					return journal.appendSlice(domain.plan.ScopeID, record)
				},
			},
		)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
		if _, err := fmt.Fprintf(stdout, "domain %s: %d cases, %d streams, %d events, %d slices, %d notes\n",
			domain.plan.ScopeID, len(domain.plan.CaseIDs), run.Streams, run.Events, len(run.Slices), len(run.Notes)); err != nil {
			return nil, fmt.Errorf("write extraction-eval-v1 progress: %w", err)
		}
	}
	return runs, nil
}

func buildExtractor(
	config evalConfig,
	episodeStore extractor.EpisodeStore,
	observer extractor.ProviderCallObserver,
) (*extractor.OpenAI, error) {
	return extractor.NewOpenAI(extractor.OpenAIConfig{
		BaseURL: config.baseURL, APIKey: os.Getenv("TEAM_MEMORY_EXTRACTOR_API_KEY"), Model: config.model,
		PromptVersion: config.promptVersion, Client: &http.Client{}, ContextMode: extractor.ContextModeRolling,
		EpisodeStore: episodeStore, ExtractionVersion: config.extractorVersion, V2Variant: config.v2Variant,
		SummaryEnabled: true, ProviderCallObserver: observer,
		ExecutionPolicy: config.executionPolicy,
	})
}

func waitForBackgroundCalls(
	ctx context.Context,
	protocol interface{ WaitForBackground(context.Context) error },
	policy extractor.ExecutionPolicy,
) error {
	waitCtx, cancel := context.WithTimeout(ctx, policy.BackgroundCallTimeout())
	defer cancel()
	return protocol.WaitForBackground(waitCtx)
}

func writeArtifacts(config evalConfig, report extractioneval.Report, runs []extractionshadow.CaseRun) error {
	if err := os.MkdirAll(config.outputDir, 0o755); err != nil {
		return fmt.Errorf("create extraction-eval-v1 output directory %q: %w", config.outputDir, err)
	}
	resolved := resolvedConfig{
		SchemaVersion: extractioneval.SchemaVersion, RunID: config.runID, SourceRunID: config.sourceRunID,
		ManifestPath: config.manifestPath, FixturesPath: config.fixturesPath, ScopeSuffix: config.scopeSuffix,
		ExtractorVersion: config.extractorVersion, ExtractorBaseURL: config.baseURL,
		ExtractorModel: config.model, PromptVersion: config.promptVersion,
		ProfilePath: config.profilePath, ProfileName: report.Profile, V2Variant: config.v2Variant,
		SliceEventLimit:          config.sliceEventLimit,
		ProviderTimeoutMS:        config.executionPolicy.AttemptTimeout.Milliseconds(),
		ProviderMaxAttempts:      config.executionPolicy.MaxAttempts,
		ProviderRetryBackoffMS:   config.executionPolicy.RetryBackoff.Milliseconds(),
		ProviderMaxResponseBytes: config.executionPolicy.MaxResponseBytes,
		PrimaryMaxOutputTokens:   config.executionPolicy.PrimaryMaxOutputTokens,
	}
	writes := []struct {
		name  string
		write func(io.Writer) error
	}{
		{name: "report.json", write: func(writer io.Writer) error { return writeJSON(writer, report) }},
		{name: "config.resolved.json", write: func(writer io.Writer) error { return writeJSON(writer, resolved) }},
		{name: "cases.jsonl", write: func(writer io.Writer) error { return writeJSONL(writer, report.Cases) }},
		{name: "notes.jsonl", write: func(writer io.Writer) error { return writeNotes(writer, runs) }},
		{name: "slices.jsonl", write: func(writer io.Writer) error { return writeSlices(writer, runs) }},
		{name: "provider-calls.jsonl", write: func(writer io.Writer) error { return writeProviderCalls(writer, runs) }},
		{name: "atom-losses.jsonl", write: func(writer io.Writer) error {
			return writeJSONL(writer, report.LossLedger)
		}},
	}
	for _, artifact := range writes {
		if err := writeFile(filepath.Join(config.outputDir, artifact.name), artifact.write); err != nil {
			return err
		}
	}
	return nil
}

func writeProviderCalls(writer io.Writer, runs []extractionshadow.CaseRun) error {
	encoder := json.NewEncoder(writer)
	for _, run := range runs {
		for _, call := range run.ProviderCalls {
			if err := encoder.Encode(call); err != nil {
				return fmt.Errorf("encode extraction-eval-v1 provider call: %w", err)
			}
		}
	}
	return nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode extraction-eval-v1 JSON: %w", err)
	}
	return nil
}

func writeJSONL[T any](writer io.Writer, values []T) error {
	encoder := json.NewEncoder(writer)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			return fmt.Errorf("encode extraction-eval-v1 JSONL: %w", err)
		}
	}
	return nil
}

func writeNotes(writer io.Writer, runs []extractionshadow.CaseRun) error {
	encoder := json.NewEncoder(writer)
	for _, run := range runs {
		for _, note := range run.Notes {
			if err := encoder.Encode(struct {
				ScopeID string `json:"scope_id"`
				teamnote.Note
			}{run.ScopeID, note}); err != nil {
				return fmt.Errorf("encode extraction-eval-v1 note: %w", err)
			}
		}
	}
	return nil
}

func writeSlices(writer io.Writer, runs []extractionshadow.CaseRun) error {
	encoder := json.NewEncoder(writer)
	for _, run := range runs {
		for _, slice := range run.Slices {
			if err := encoder.Encode(struct {
				ScopeID string `json:"scope_id"`
				extractionshadow.SliceRecord
			}{run.ScopeID, slice}); err != nil {
				return fmt.Errorf("encode extraction-eval-v1 slice: %w", err)
			}
		}
	}
	return nil
}

func writeFile(path string, write func(io.Writer) error) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create extraction-eval-v1 artifact %q: %w", path, err)
	}
	writeErr := write(file)
	closeErr := file.Close()
	if writeErr != nil {
		writeErr = fmt.Errorf("write extraction-eval-v1 artifact %q: %w", path, writeErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close extraction-eval-v1 artifact %q: %w", path, closeErr)
	}
	return errors.Join(writeErr, closeErr)
}
