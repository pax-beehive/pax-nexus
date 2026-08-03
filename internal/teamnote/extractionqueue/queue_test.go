package extractionqueue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/suite"
)

type queueSuite struct {
	suite.Suite
}

func TestQueueSuite(t *testing.T) {
	suite.Run(t, new(queueSuite))
}

func (s *queueSuite) TestDefaultsAndValidation() {
	defaults := withDefaults(Config{})
	s.Equal("team_note_extract", defaults.QueuePrefix)
	s.Equal(16, defaults.Shards)
	s.Equal(5, defaults.MaxAttempts)
	s.Equal(750*time.Millisecond, defaults.Debounce)
	s.Equal(30*time.Second, defaults.BatchTimeout)
	s.Equal(3*time.Minute, defaults.JobTimeout)

	tests := []Config{
		{QueuePrefix: "bad prefix", Shards: 1, MaxAttempts: 1},
		{Shards: -1, MaxAttempts: 1},
		{Shards: 65, MaxAttempts: 1},
		{Shards: 1, MaxAttempts: -1},
		{Shards: 1, MaxAttempts: 1, JobTimeout: -time.Second},
	}
	for _, config := range tests {
		s.Require().Error(validateConfig(config))
	}
}

func (s *queueSuite) TestConstructsClientAndRejectsInvalidDependencies() {
	processor := &queueProcessor{}
	queue, err := New(new(pgxpool.Pool), processor, Config{Shards: 2})
	s.Require().NoError(err)
	s.NotNil(queue)
	s.Equal(jobKind, (Args{}).Kind())

	tests := []struct {
		name      string
		pool      *pgxpool.Pool
		processor Processor
		config    Config
	}{
		{name: "missing pool", processor: processor},
		{name: "missing processor", pool: new(pgxpool.Pool)},
		{name: "invalid shards", pool: new(pgxpool.Pool), processor: processor, config: Config{Shards: 65}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, createErr := New(test.pool, test.processor, test.config)
			s.Require().Error(createErr)
		})
	}
	s.Require().Error(Migrate(context.Background(), nil))
}

func (s *queueSuite) TestShardMappingIsStableAndBounded() {
	tests := []struct {
		name string
		args Args
	}{
		{name: "first", args: Args{ScopeID: "team", UserID: "user", AgentID: "agent", SessionID: "one"}},
		{name: "second", args: Args{ScopeID: "team", UserID: "user", AgentID: "agent", SessionID: "two"}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			first := shardFor(test.args, 16)
			second := shardFor(test.args, 16)
			s.Equal(first, second)
			s.GreaterOrEqual(first, 0)
			s.Less(first, 16)
		})
	}
	s.Len(shardQueues("test_extract", 4), 4)
}

func (s *queueSuite) TestWorkerPropagatesIdentityScopeAndErrors() {
	processor := &queueProcessor{}
	var logs bytes.Buffer
	w := &worker{processor: processor, logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	args := Args{ScopeID: "team", UserID: "user", AgentID: "agent", SessionID: "session", ExpectedCursor: 3}
	s.Require().NoError(w.Work(context.Background(), &river.Job[Args]{Args: args}))
	s.Equal(teamnote.Actor{UserID: "user", AgentID: "agent", SessionID: "session"}, processor.actor)
	s.Equal("team", processor.scopeID)

	processor.err = errors.New("extract failed")
	s.Require().Error(w.Work(context.Background(), &river.Job[Args]{Args: args}))
	processor.err = nil
	processor.more = true
	s.Require().Error(w.Work(context.Background(), &river.Job[Args]{Args: args}))
	processor.more = false
	timeoutArgs := args
	timeoutArgs.RequireCurrent = true
	timeoutArgs.ExpectedCursor = 7
	s.Require().NoError(w.Work(context.Background(), &river.Job[Args]{Args: timeoutArgs}))
	s.Equal(int64(7), processor.expectedCursor)
	s.Require().Error(w.Work(context.Background(), &river.Job[Args]{Args: Args{}}))
	s.Contains(logs.String(), `"msg":"extraction job started"`)
	s.Contains(logs.String(), `"msg":"extraction job failed"`)
	s.Contains(logs.String(), `"agent_id":"agent"`)
	s.NotContains(logs.String(), "content")
}

type queueProcessor struct {
	actor          teamnote.Actor
	scopeID        string
	err            error
	more           bool
	expectedCursor int64
	requireCurrent bool
}

func (p *queueProcessor) ProcessExtraction(ctx context.Context, actor teamnote.Actor, expectedCursor int64, requireCurrent bool) (bool, error) {
	p.actor = actor
	p.expectedCursor = expectedCursor
	p.requireCurrent = requireCurrent
	scopeID, err := teamnote.ScopeFromContext(ctx)
	if err != nil {
		return false, err
	}
	p.scopeID = scopeID
	return p.more, p.err
}

func (s *queueSuite) TestEnqueueTxValidatesArgsBeforeInsert() {
	queue, err := New(new(pgxpool.Pool), &queueProcessor{}, Config{Shards: 2})
	s.Require().NoError(err)
	tests := []struct {
		name   string
		scope  string
		actor  teamnote.Actor
		cursor int64
	}{
		{name: "missing scope", actor: teamnote.Actor{UserID: "u", AgentID: "a", SessionID: "s"}, cursor: 1},
		{name: "missing actor", scope: "team", cursor: 1},
		{name: "non-positive cursor", scope: "team", actor: teamnote.Actor{UserID: "u", AgentID: "a", SessionID: "s"}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, enqueueErr := queue.EnqueueTx(context.Background(), nil, test.scope, test.actor, test.cursor, true)
			s.Require().Error(enqueueErr)
			s.Contains(enqueueErr.Error(), "scope and actor are required")
		})
	}
}

type enqueuedJobRow struct {
	queue       string
	kind        string
	maxAttempts int
	scheduledAt time.Time
	args        Args
}

func (s *queueSuite) TestEnqueueTxSchedulesShardStableJobs() {
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		s.T().Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	s.Require().NoError(err)
	defer pool.Close()
	s.Require().NoError(Migrate(ctx, pool))

	prefix := fmt.Sprintf("enqueue_test_%d", time.Now().UnixNano())
	const shards = 4
	queue, err := New(pool, &queueProcessor{}, Config{
		QueuePrefix: prefix, Shards: shards, MaxAttempts: 3,
		Debounce: 2 * time.Second, BatchTimeout: 90 * time.Second,
	})
	s.Require().NoError(err)
	tx, err := pool.Begin(ctx)
	s.Require().NoError(err)
	defer func() { s.Require().NoError(tx.Rollback(ctx)) }()
	actor := teamnote.Actor{UserID: "owner", AgentID: "agent", SessionID: "session"}
	enqueuedAt := time.Now()

	fetch := func(jobID string) enqueuedJobRow {
		id, parseErr := strconv.ParseInt(jobID, 10, 64)
		s.Require().NoError(parseErr)
		var row enqueuedJobRow
		var rawArgs []byte
		s.Require().NoError(tx.QueryRow(ctx,
			`SELECT queue, kind, max_attempts, scheduled_at, args FROM river_job WHERE id = $1`, id,
		).Scan(&row.queue, &row.kind, &row.maxAttempts, &row.scheduledAt, &rawArgs))
		s.Require().NoError(json.Unmarshal(rawArgs, &row.args))
		return row
	}

	completeID, err := queue.EnqueueTx(ctx, tx, "scope-enqueue", actor, 7, true)
	s.Require().NoError(err)
	complete := fetch(completeID)
	s.Equal(jobKind, complete.kind)
	s.Equal(3, complete.maxAttempts)
	s.False(complete.args.RequireCurrent, "a complete batch runs without a currency gate")
	s.Equal(int64(7), complete.args.ExpectedCursor)
	s.Equal("scope-enqueue", complete.args.ScopeID)
	s.Equal(queueName(prefix, shardFor(complete.args, shards)), complete.queue,
		"the job must land on its shard-stable queue")
	completeDelay := complete.scheduledAt.Sub(enqueuedAt)
	s.Greater(completeDelay, time.Duration(0))
	s.Less(completeDelay, 30*time.Second, "a complete batch is debounced, not batch-timed-out")

	incompleteID, err := queue.EnqueueTx(ctx, tx, "scope-enqueue", actor, 8, false)
	s.Require().NoError(err)
	incomplete := fetch(incompleteID)
	s.True(incomplete.args.RequireCurrent, "an incomplete batch must require a current cursor")
	s.Equal(int64(8), incomplete.args.ExpectedCursor)
	incompleteDelay := incomplete.scheduledAt.Sub(enqueuedAt)
	s.Greater(incompleteDelay, 60*time.Second, "an incomplete batch waits for the batch timeout")

	repeatID, err := queue.EnqueueTx(ctx, tx, "scope-enqueue", actor, 9, true)
	s.Require().NoError(err)
	repeat := fetch(repeatID)
	s.Equal(complete.queue, repeat.queue, "the same actor stream must keep its shard queue")
}
