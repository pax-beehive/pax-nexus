package extractionqueue

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

const jobKind = "team_note_extract"

type Config struct {
	QueuePrefix     string
	Shards          int
	MaxAttempts     int
	Debounce        time.Duration
	BatchTimeout    time.Duration
	JobTimeout      time.Duration
	SoftStopTimeout time.Duration
	Logger          *slog.Logger
}

type Processor interface {
	ProcessExtraction(context.Context, teamnote.Actor, int64, bool) (bool, error)
}

type Args struct {
	ScopeID        string `json:"scope_id"`
	UserID         string `json:"user_id"`
	AgentID        string `json:"agent_id"`
	SessionID      string `json:"session_id"`
	ExpectedCursor int64  `json:"expected_cursor"`
	RequireCurrent bool   `json:"require_current"`
}

func (Args) Kind() string {
	return jobKind
}

type Queue struct {
	client       *river.Client[pgx.Tx]
	queuePrefix  string
	shards       int
	maxAttempts  int
	debounce     time.Duration
	batchTimeout time.Duration
}

func New(pool *pgxpool.Pool, processor Processor, config Config) (*Queue, error) {
	if pool == nil || processor == nil {
		return nil, fmt.Errorf("create extraction queue: pool and processor are required")
	}
	config = withDefaults(config)
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely(workers, &worker{processor: processor, logger: config.Logger}); err != nil {
		return nil, fmt.Errorf("register extraction worker: %w", err)
	}
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Workers: workers, Queues: shardQueues(config.QueuePrefix, config.Shards), MaxAttempts: config.MaxAttempts,
		JobTimeout: config.JobTimeout, SoftStopTimeout: config.SoftStopTimeout, Logger: config.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("create River client: %w", err)
	}
	return &Queue{
		client: client, queuePrefix: config.QueuePrefix,
		shards: config.Shards, maxAttempts: config.MaxAttempts, debounce: config.Debounce, batchTimeout: config.BatchTimeout,
	}, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("migrate extraction queue: pool is required")
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("create River migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("migrate River schema: %w", err)
	}
	return nil
}

func (q *Queue) Start(ctx context.Context) error {
	if err := q.client.Start(ctx); err != nil {
		return fmt.Errorf("start extraction queue: %w", err)
	}
	return nil
}

func (q *Queue) Stop(ctx context.Context) error {
	if err := q.client.Stop(ctx); err != nil {
		return fmt.Errorf("stop extraction queue: %w", err)
	}
	return nil
}

func (q *Queue) EnqueueTx(ctx context.Context, tx pgx.Tx, scopeID string, actor teamnote.Actor, cursor int64, complete bool) (string, error) {
	args := Args{ScopeID: scopeID, UserID: actor.UserID, AgentID: actor.AgentID, SessionID: actor.SessionID}
	delay := q.debounce
	args.ExpectedCursor = cursor
	if !complete {
		args.RequireCurrent = true
		delay = q.batchTimeout
	}
	if err := validateArgs(args); err != nil {
		return "", err
	}
	result, err := q.client.InsertTx(ctx, tx, args, &river.InsertOpts{
		Queue: queueName(q.queuePrefix, shardFor(args, q.shards)), MaxAttempts: q.maxAttempts,
		ScheduledAt: time.Now().Add(delay),
	})
	if err != nil {
		return "", fmt.Errorf("insert River extraction job: %w", err)
	}
	return strconv.FormatInt(result.Job.ID, 10), nil
}

type worker struct {
	river.WorkerDefaults[Args]
	processor Processor
	logger    *slog.Logger
}

func (w *worker) Work(ctx context.Context, job *river.Job[Args]) error {
	startedAt := time.Now()
	w.logger.InfoContext(ctx, "extraction job started", jobLogAttrs(job)...)
	if err := validateArgs(job.Args); err != nil {
		return w.fail(ctx, job, startedAt, fmt.Errorf("validate extraction job: %w", err))
	}
	actor := teamnote.Actor{UserID: job.Args.UserID, AgentID: job.Args.AgentID, SessionID: job.Args.SessionID}
	more, err := w.processor.ProcessExtraction(
		teamnote.WithScope(ctx, job.Args.ScopeID), actor, job.Args.ExpectedCursor, job.Args.RequireCurrent,
	)
	if err != nil {
		return w.fail(ctx, job, startedAt, fmt.Errorf("process extraction stream: %w", err))
	}
	if more {
		w.logger.InfoContext(ctx, "extraction job snoozed", append(jobLogAttrs(job), slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()))...)
		return river.JobSnooze(0)
	}
	w.logger.InfoContext(ctx, "extraction job completed", append(jobLogAttrs(job), slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()))...)
	return nil
}

func (w *worker) fail(ctx context.Context, job *river.Job[Args], startedAt time.Time, err error) error {
	attrs := append(jobLogAttrs(job), slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()), slog.Any("error", err))
	w.logger.ErrorContext(ctx, "extraction job failed", attrs...)
	return err
}

func jobLogAttrs(job *river.Job[Args]) []any {
	var jobID int64
	var attempt int
	if job.JobRow != nil {
		jobID = job.ID
		attempt = job.Attempt
	}
	return []any{
		"job_id", jobID, "scope_id", job.Args.ScopeID, "user_id", job.Args.UserID,
		"agent_id", job.Args.AgentID, "session_id", job.Args.SessionID,
		"expected_cursor", job.Args.ExpectedCursor, "attempt", attempt,
	}
}

func withDefaults(config Config) Config {
	if config.Logger == nil {
		config.Logger = observability.DiscardLogger()
	}
	if config.QueuePrefix == "" {
		config.QueuePrefix = "team_note_extract"
	}
	if config.Shards == 0 {
		config.Shards = 16
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = 5
	}
	if config.Debounce == 0 {
		config.Debounce = 750 * time.Millisecond
	}
	if config.BatchTimeout == 0 {
		config.BatchTimeout = 30 * time.Second
	}
	if config.JobTimeout == 0 {
		config.JobTimeout = 2 * time.Minute
	}
	if config.SoftStopTimeout == 0 {
		config.SoftStopTimeout = 30 * time.Second
	}
	return config
}

func validateConfig(config Config) error {
	if strings.TrimSpace(config.QueuePrefix) == "" || strings.ContainsAny(config.QueuePrefix, " ,") {
		return fmt.Errorf("create extraction queue: queue prefix is invalid")
	}
	if config.Shards < 1 || config.Shards > 64 {
		return fmt.Errorf("create extraction queue: shards must be between 1 and 64")
	}
	if config.MaxAttempts < 1 || config.Debounce < 0 || config.BatchTimeout < 0 || config.JobTimeout < 0 || config.SoftStopTimeout < 0 {
		return fmt.Errorf("create extraction queue: attempts and timeouts are invalid")
	}
	return nil
}

func validateArgs(args Args) error {
	if strings.TrimSpace(args.ScopeID) == "" || strings.TrimSpace(args.UserID) == "" ||
		strings.TrimSpace(args.AgentID) == "" || strings.TrimSpace(args.SessionID) == "" ||
		args.ExpectedCursor <= 0 {
		return fmt.Errorf("enqueue extraction: scope and actor are required")
	}
	return nil
}

func shardQueues(prefix string, shards int) map[string]river.QueueConfig {
	queues := make(map[string]river.QueueConfig, shards)
	for shard := range shards {
		queues[queueName(prefix, shard)] = river.QueueConfig{MaxWorkers: 1}
	}
	return queues
}

func shardFor(args Args, shards int) int {
	const offset32 = uint32(2166136261)
	const prime32 = uint32(16777619)
	hash := offset32
	for _, value := range []string{args.ScopeID, args.AgentID, args.SessionID} {
		for _, current := range []byte(value) {
			hash ^= uint32(current)
			hash *= prime32
		}
		hash ^= 0
		hash *= prime32
	}
	return int(hash % uint32(shards))
}

func queueName(prefix string, shard int) string {
	return fmt.Sprintf("%s_%02d", prefix, shard)
}
