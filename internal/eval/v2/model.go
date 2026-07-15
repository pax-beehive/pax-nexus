// Package v2 owns durable, configuration-driven evaluation runs.
package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/harness"
)

const ConfigVersion = "v2"

type Config struct {
	Version          string       `json:"version" yaml:"version"`
	Run              RunConfig    `json:"run" yaml:"run"`
	Store            StoreConfig  `json:"store" yaml:"store"`
	BaselineArm      string       `json:"baseline_arm" yaml:"baseline_arm"`
	Arms             []ArmConfig  `json:"arms" yaml:"arms"`
	RetryFailed      bool         `json:"retry_failed" yaml:"retry_failed"`
	RetryMaxAttempts int          `json:"retry_max_attempts,omitempty" yaml:"retry_max_attempts,omitempty"`
	TrialTimeout     string       `json:"trial_timeout" yaml:"trial_timeout"`
	RuntimeEnv       []string     `json:"runtime_env,omitempty" yaml:"runtime_env,omitempty"`
	Output           OutputConfig `json:"output,omitempty" yaml:"output,omitempty"`
	BeforeRun        *CommandSpec `json:"before_run,omitempty" yaml:"before_run,omitempty"`
	Preflight        *CommandSpec `json:"preflight,omitempty" yaml:"preflight,omitempty"`
	SharedProducer   *CommandSpec `json:"shared_producer,omitempty" yaml:"shared_producer,omitempty"`
	AfterRun         *CommandSpec `json:"after_run,omitempty" yaml:"after_run,omitempty"`
}

type RunConfig struct {
	ID          string `json:"id" yaml:"id"`
	Dataset     string `json:"dataset" yaml:"dataset"`
	Manifest    string `json:"manifest" yaml:"manifest"`
	OutputDir   string `json:"output_dir" yaml:"output_dir"`
	Parallelism int    `json:"parallelism" yaml:"parallelism"`
}

type StoreConfig struct {
	DSNEnv string `json:"dsn_env" yaml:"dsn_env"`
}

type OutputConfig struct {
	Formats []string `json:"formats,omitempty" yaml:"formats,omitempty"`
}

type ArmConfig struct {
	Name          string       `json:"name" yaml:"name"`
	Producer      *CommandSpec `json:"producer,omitempty" yaml:"producer,omitempty"`
	Ingest        *CommandSpec `json:"ingest,omitempty" yaml:"ingest,omitempty"`
	AfterProducer *CommandSpec `json:"after_producer,omitempty" yaml:"after_producer,omitempty"`
	Consumer      CommandSpec  `json:"consumer" yaml:"consumer"`
}

type CommandSpec struct {
	Program       string            `json:"program" yaml:"program"`
	Args          []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	WorkingDir    string            `json:"working_dir,omitempty" yaml:"working_dir,omitempty"`
	SuccessMarker string            `json:"success_marker,omitempty" yaml:"success_marker,omitempty"`
}

type Case struct {
	ID                string `json:"id"`
	Category          string `json:"category"`
	Question          string `json:"question"`
	Expected          string `json:"expected"`
	AskingUserID      string `json:"asking_user_id"`
	ScopeID           string `json:"scope_id"`
	ProducerWorkspace string `json:"producer_workspace"`
	ConsumerWorkspace string `json:"consumer_workspace"`
}

type TrialKey struct {
	RunID  string
	CaseID string
	Arm    string
}

type RunRecord struct {
	ID              string
	Dataset         string
	DatasetRevision string
	ConfigHash      string
	Config          Config
	Runtime         map[string]string
}

type TrialResult struct {
	RunID                 string    `json:"run_id"`
	Dataset               string    `json:"dataset"`
	DatasetRevision       string    `json:"dataset_revision"`
	CaseID                string    `json:"case_id"`
	Category              string    `json:"category"`
	Question              string    `json:"question"`
	Arm                   string    `json:"arm"`
	AskingUserID          string    `json:"asking_user_id"`
	Status                string    `json:"status"`
	MemoryIngestProvider  string    `json:"memory_ingest_provider,omitempty"`
	MemoryIngestAccepted  int       `json:"memory_ingest_accepted,omitempty"`
	MemoryIngestDuplicate int       `json:"memory_ingest_duplicate,omitempty"`
	MemoryIngestCreated   int       `json:"memory_ingest_created,omitempty"`
	MemoryIngestUpdated   int       `json:"memory_ingest_updated,omitempty"`
	MemoryIngestDeleted   int       `json:"memory_ingest_deleted,omitempty"`
	MemoryIngestNoOp      bool      `json:"memory_ingest_noop"`
	Expected              string    `json:"expected"`
	Answer                string    `json:"answer"`
	Exact                 bool      `json:"exact"`
	SafeSuccess           bool      `json:"safe_success"`
	TokenF1               float64   `json:"token_f1"`
	SessionID             string    `json:"session_id"`
	InputTokens           int       `json:"input_tokens"`
	OutputTokens          int       `json:"output_tokens"`
	Cost                  float64   `json:"cost"`
	CostScope             string    `json:"cost_scope"`
	ProducerInputTokens   int       `json:"producer_input_tokens"`
	ProducerOutputTokens  int       `json:"producer_output_tokens"`
	ProducerCost          float64   `json:"producer_cost"`
	ConsumerInputTokens   int       `json:"consumer_input_tokens"`
	ConsumerOutputTokens  int       `json:"consumer_output_tokens"`
	ConsumerCost          float64   `json:"consumer_cost"`
	ProducerDurationMS    int64     `json:"producer_duration_ms"`
	ReadinessDurationMS   int64     `json:"readiness_duration_ms"`
	ConsumerDurationMS    int64     `json:"consumer_duration_ms"`
	TotalDurationMS       int64     `json:"total_duration_ms"`
	StartedAt             time.Time `json:"started_at"`
	CompletedAt           time.Time `json:"completed_at"`
	Error                 string    `json:"error,omitempty"`
}

func (c Config) Validate() error {
	if c.Version != ConfigVersion {
		return fmt.Errorf("validate eval config: version must be %q", ConfigVersion)
	}
	if strings.TrimSpace(c.Run.ID) == "" || strings.TrimSpace(c.Run.Dataset) == "" || strings.TrimSpace(c.Run.Manifest) == "" || strings.TrimSpace(c.Run.OutputDir) == "" {
		return fmt.Errorf("validate eval config: run id, dataset, manifest, and output directory are required")
	}
	if c.Run.Parallelism <= 0 {
		return fmt.Errorf("validate eval config: parallelism must be positive")
	}
	if strings.TrimSpace(c.Store.DSNEnv) == "" {
		return fmt.Errorf("validate eval config: store dsn_env is required")
	}
	if _, err := c.Timeout(); err != nil {
		return err
	}
	if err := validateRetry(c); err != nil {
		return err
	}
	if err := validateRuntimeEnvironment(c.RuntimeEnv); err != nil {
		return err
	}
	if err := validateOutputFormats(c.Output.Formats); err != nil {
		return err
	}
	if err := validateLifecycleCommands(c); err != nil {
		return err
	}
	return validateArms(c)
}

func validateRetry(config Config) error {
	if config.RetryFailed && config.RetryMaxAttempts < 2 {
		return fmt.Errorf("validate eval config: retry_failed requires retry_max_attempts of at least 2")
	}
	if !config.RetryFailed && config.RetryMaxAttempts > 1 {
		return fmt.Errorf("validate eval config: retry_max_attempts requires retry_failed")
	}
	return nil
}

func validateLifecycleCommands(config Config) error {
	commands := map[string]*CommandSpec{
		"before_run": config.BeforeRun, "preflight": config.Preflight,
		"shared_producer": config.SharedProducer, "after_run": config.AfterRun,
	}
	for label, command := range commands {
		if command == nil {
			continue
		}
		if err := validateCommand(label, *command); err != nil {
			return err
		}
	}
	return nil
}

func validateArms(config Config) error {
	if len(config.Arms) < 2 {
		return fmt.Errorf("validate eval config: at least two arms are required")
	}
	names := make(map[string]struct{}, len(config.Arms))
	ingestArms := 0
	for _, arm := range config.Arms {
		if err := validateArm(arm); err != nil {
			return err
		}
		if _, exists := names[arm.Name]; exists {
			return fmt.Errorf("validate eval config: duplicate arm %q", arm.Name)
		}
		names[arm.Name] = struct{}{}
		if arm.Ingest != nil {
			ingestArms++
		}
	}
	if (config.SharedProducer == nil) != (ingestArms == 0) {
		return fmt.Errorf("validate eval config: shared_producer and at least one ingest arm are required together")
	}
	if config.SharedProducer != nil && strings.TrimSpace(config.SharedProducer.SuccessMarker) == "" {
		return fmt.Errorf("validate eval config: shared_producer success_marker is required")
	}
	if _, exists := names[config.BaselineArm]; !exists {
		return fmt.Errorf("validate eval config: baseline arm %q is not configured", config.BaselineArm)
	}
	return nil
}

func (c Config) MaxAttempts() int {
	if !c.RetryFailed {
		return 1
	}
	return c.RetryMaxAttempts
}

func validateRuntimeEnvironment(names []string) error {
	for _, name := range names {
		upper := strings.ToUpper(strings.TrimSpace(name))
		if upper == "" || strings.ContainsAny(upper, " =") {
			return fmt.Errorf("validate eval config: runtime_env contains an invalid name")
		}
		for _, secretMarker := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD"} {
			if strings.Contains(upper, secretMarker) {
				return fmt.Errorf("validate eval config: runtime_env must not expose secret variable %q", name)
			}
		}
	}
	return nil
}

func validateOutputFormats(formats []string) error {
	seen := make(map[string]struct{}, len(formats))
	for _, format := range formats {
		if format != "csv" && format != "jsonl" && format != "html" {
			return fmt.Errorf("validate eval config: unsupported output format %q", format)
		}
		if _, exists := seen[format]; exists {
			return fmt.Errorf("validate eval config: duplicate output format %q", format)
		}
		seen[format] = struct{}{}
	}
	return nil
}

func (c Config) OutputFormats() []string {
	if len(c.Output.Formats) == 0 {
		return []string{"csv", "jsonl", "html"}
	}
	return slices.Clone(c.Output.Formats)
}

func (c Config) Timeout() (time.Duration, error) {
	duration, err := time.ParseDuration(c.TrialTimeout)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("validate eval config: trial_timeout must be a positive duration")
	}
	return duration, nil
}

func (c Config) Hash() (string, error) {
	return c.HashWithRuntime(nil)
}

func (c Config) HashWithRuntime(runtime map[string]string) (string, error) {
	hashConfig := c
	hashConfig.Output.Formats = slices.DeleteFunc(slices.Clone(c.Output.Formats), func(format string) bool {
		return format == "html"
	})
	encoded, err := json.Marshal(struct {
		Config  Config            `json:"config"`
		Runtime map[string]string `json:"runtime,omitempty"`
	}{Config: hashConfig, Runtime: runtime})
	if err != nil {
		return "", fmt.Errorf("hash eval config: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (c Config) ResolveRuntime(getenv func(string) string) (map[string]string, error) {
	values := make(map[string]string, len(c.RuntimeEnv))
	for _, name := range c.RuntimeEnv {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			return nil, fmt.Errorf("resolve eval runtime: environment variable %s is required", name)
		}
		values[name] = value
	}
	return values, nil
}

func ScoreResult(run RunRecord, evalCase Case, arm string, producer, consumer harness.AgentOutput, started time.Time, durations [3]time.Duration) TrialResult {
	score := harness.ScoreExact(arm, evalCase.Expected, consumer)
	completed := time.Now().UTC()
	return TrialResult{
		RunID: run.ID, Dataset: run.Dataset, DatasetRevision: run.DatasetRevision,
		CaseID: evalCase.ID, Category: evalCase.Category, Question: evalCase.Question, Arm: arm, AskingUserID: evalCase.AskingUserID,
		Status: "completed", Expected: score.Expected, Answer: score.Answer, Exact: score.Exact,
		SafeSuccess: score.SafeSuccess, TokenF1: score.TokenF1, SessionID: score.SessionID,
		InputTokens: producer.InputTokens + score.InputTokens, OutputTokens: producer.OutputTokens + score.OutputTokens,
		Cost: producer.Cost + score.Cost, CostScope: "opencode_reported",
		ProducerInputTokens: producer.InputTokens, ProducerOutputTokens: producer.OutputTokens, ProducerCost: producer.Cost,
		ConsumerInputTokens: score.InputTokens, ConsumerOutputTokens: score.OutputTokens, ConsumerCost: score.Cost,
		ProducerDurationMS: durations[0].Milliseconds(), ReadinessDurationMS: durations[1].Milliseconds(),
		ConsumerDurationMS: durations[2].Milliseconds(), TotalDurationMS: completed.Sub(started).Milliseconds(),
		StartedAt: started, CompletedAt: completed,
	}
}

func validateArm(arm ArmConfig) error {
	if strings.TrimSpace(arm.Name) == "" {
		return fmt.Errorf("validate eval config: arm name is required")
	}
	if err := validateCommand(arm.Name+" consumer", arm.Consumer); err != nil {
		return err
	}
	for label, command := range map[string]*CommandSpec{
		"producer": arm.Producer, "ingest": arm.Ingest, "after_producer": arm.AfterProducer,
	} {
		if command != nil {
			if err := validateCommand(arm.Name+" "+label, *command); err != nil {
				return err
			}
		}
	}
	if arm.Producer != nil && arm.Ingest != nil {
		return fmt.Errorf("validate eval config: arm %q cannot configure both producer and ingest", arm.Name)
	}
	if arm.Producer == nil && arm.Ingest == nil && arm.AfterProducer != nil {
		return fmt.Errorf("validate eval config: arm %q cannot wait without a producer", arm.Name)
	}
	return nil
}

func validateCommand(name string, command CommandSpec) error {
	if strings.TrimSpace(command.Program) == "" {
		return fmt.Errorf("validate eval config: %s program is required", name)
	}
	return nil
}
