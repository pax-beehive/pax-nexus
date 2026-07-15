package postgresstore_test

import (
	"context"
	"os"
	"testing"
	"time"

	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	"github.com/pax-beehive/pax-nexus/internal/eval/v2/postgresstore"
	"github.com/stretchr/testify/suite"
)

type storeSuite struct {
	suite.Suite
	store *postgresstore.Store
}

func TestStoreSuite(t *testing.T) { suite.Run(t, new(storeSuite)) }

func (s *storeSuite) SetupSuite() {
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		s.T().Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not set")
	}
	store, err := postgresstore.Open(context.Background(), dsn)
	s.Require().NoError(err)
	s.Require().NoError(store.Migrate(context.Background()))
	s.store = store
}

func (s *storeSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *storeSuite) TestLifecycleResumeAndRetry() {
	ctx := context.Background()
	runID := "eval-store-" + time.Now().UTC().Format("20060102150405.000000000")
	config := v2.Config{Version: v2.ConfigVersion}
	run := v2.RunRecord{ID: runID, Dataset: "suite", DatasetRevision: "rev", ConfigHash: "hash", Config: config}
	keys := []v2.TrialKey{{RunID: runID, CaseID: "case-1", Arm: "control"}, {RunID: runID, CaseID: "case-1", Arm: "memory"}}
	acquired, err := s.store.Acquire(ctx, runID)
	s.Require().NoError(err)
	s.True(acquired)
	acquired, err = s.store.Acquire(ctx, runID)
	s.Require().NoError(err)
	s.False(acquired)
	s.Require().NoError(s.store.Release(ctx, runID))
	s.Require().NoError(s.store.Initialize(ctx, run, keys))
	s.Require().NoError(s.store.Initialize(ctx, run, keys))

	steps := []struct {
		name string
		run  func() (bool, error)
		want bool
	}{
		{name: "claim pending", run: func() (bool, error) { return s.store.Claim(ctx, keys[0], false, 1) }, want: true},
		{name: "reject running", run: func() (bool, error) { return s.store.Claim(ctx, keys[0], false, 1) }},
		{name: "reset interrupted", run: func() (bool, error) { return false, s.store.ResetRunning(ctx, runID) }},
		{name: "reclaim reset", run: func() (bool, error) { return s.store.Claim(ctx, keys[0], false, 1) }, want: true},
		{name: "complete", run: func() (bool, error) { return false, s.store.Complete(ctx, result(runID, "control", "completed")) }},
		{name: "do not retry complete", run: func() (bool, error) { return s.store.Claim(ctx, keys[0], true, 2) }},
		{name: "claim second", run: func() (bool, error) { return s.store.Claim(ctx, keys[1], false, 1) }, want: true},
		{name: "fail", run: func() (bool, error) { return false, s.store.Fail(ctx, result(runID, "memory", "failed")) }},
		{name: "skip failed", run: func() (bool, error) { return s.store.Claim(ctx, keys[1], false, 1) }},
		{name: "retry failed", run: func() (bool, error) { return s.store.Claim(ctx, keys[1], true, 2) }, want: true},
		{name: "fail retry", run: func() (bool, error) { return false, s.store.Fail(ctx, result(runID, "memory", "failed")) }},
		{name: "cap failed retry", run: func() (bool, error) { return s.store.Claim(ctx, keys[1], true, 2) }},
	}
	for _, step := range steps {
		s.Run(step.name, func() {
			claimed, err := step.run()
			s.Require().NoError(err)
			s.Equal(step.want, claimed)
		})
	}

	results, err := s.store.Results(ctx, runID)
	s.Require().NoError(err)
	s.Len(results, 2)
	runnable, err := s.store.HasRunnable(ctx, runID, true, 2)
	s.Require().NoError(err)
	s.False(runnable)
	s.Require().NoError(s.store.Finish(ctx, runID))
}

func (s *storeSuite) TestRejectsRunIDConfigCollisionAndInvalidTransition() {
	ctx := context.Background()
	runID := "eval-collision-" + time.Now().UTC().Format("20060102150405.000000000")
	key := v2.TrialKey{RunID: runID, CaseID: "case", Arm: "control"}
	run := v2.RunRecord{ID: runID, Dataset: "suite", DatasetRevision: "rev", ConfigHash: "first", Config: v2.Config{Version: v2.ConfigVersion}}
	s.Require().NoError(s.store.Initialize(ctx, run, []v2.TrialKey{key}))
	run.ConfigHash = "second"
	s.Require().Error(s.store.Initialize(ctx, run, []v2.TrialKey{key}))
	s.Require().Error(s.store.Complete(ctx, result(runID, "control", "completed")))
	s.Require().Error(s.store.Finish(ctx, runID))
}

func TestOpenRejectsEmptyDSN(t *testing.T) {
	store, err := postgresstore.Open(context.Background(), "")
	if err == nil || store != nil {
		t.Fatal("expected empty DSN error")
	}
}

func result(runID, arm, status string) v2.TrialResult {
	return v2.TrialResult{
		RunID: runID, Dataset: "suite", DatasetRevision: "rev", CaseID: "case-1", Category: "temporal",
		Arm: arm, AskingUserID: "user", Status: status, Expected: "answer", Answer: "answer",
		Exact: status == "completed", SafeSuccess: status == "completed", TokenF1: 1,
		StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(),
	}
}
