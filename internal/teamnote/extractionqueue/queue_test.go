package extractionqueue

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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
