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
	var controlAttempt v2.TrialAttemptHandle
	var memoryAttempt v2.TrialAttemptHandle
	claim := func(key v2.TrialKey, retry bool, maxAttempts int, target *v2.TrialAttemptHandle) (bool, error) {
		handle, claimed, err := s.store.Claim(ctx, key, retry, maxAttempts)
		if claimed {
			*target = handle
		}
		return claimed, err
	}

	steps := []struct {
		name string
		run  func() (bool, error)
		want bool
	}{
		{name: "claim pending", run: func() (bool, error) { return claim(keys[0], false, 1, &controlAttempt) }, want: true},
		{name: "reject running", run: func() (bool, error) { return claim(keys[0], false, 1, &controlAttempt) }},
		{name: "reset interrupted", run: func() (bool, error) { return false, s.store.ResetRunning(ctx, runID) }},
		{name: "reclaim reset", run: func() (bool, error) { return claim(keys[0], false, 1, &controlAttempt) }, want: true},
		{name: "complete", run: func() (bool, error) {
			return false, s.store.Complete(ctx, controlAttempt, result(runID, "control", "completed"))
		}},
		{name: "do not retry complete", run: func() (bool, error) { return claim(keys[0], true, 2, &controlAttempt) }},
		{name: "claim second", run: func() (bool, error) { return claim(keys[1], false, 1, &memoryAttempt) }, want: true},
		{name: "fail", run: func() (bool, error) {
			return false, s.store.Fail(ctx, memoryAttempt, result(runID, "memory", "failed"))
		}},
		{name: "skip failed", run: func() (bool, error) { return claim(keys[1], false, 1, &memoryAttempt) }},
		{name: "retry failed", run: func() (bool, error) { return claim(keys[1], true, 2, &memoryAttempt) }, want: true},
		{name: "fail retry", run: func() (bool, error) {
			return false, s.store.Fail(ctx, memoryAttempt, result(runID, "memory", "failed"))
		}},
		{name: "cap failed retry", run: func() (bool, error) { return claim(keys[1], true, 2, &memoryAttempt) }},
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
	attempts, err := s.store.Attempts(ctx, runID)
	s.Require().NoError(err)
	s.Require().Len(attempts, 4)
	s.Equal("interrupted", attempts[0].Status)
	s.Equal(v2.FailureClassInterrupted, attempts[0].FailureClass)
	s.NotNil(attempts[0].CompletedAt)
}

func (s *storeSuite) TestRejectsRunIDConfigCollisionAndInvalidTransition() {
	ctx := context.Background()
	runID := "eval-collision-" + time.Now().UTC().Format("20060102150405.000000000")
	key := v2.TrialKey{RunID: runID, CaseID: "case", Arm: "control"}
	run := v2.RunRecord{ID: runID, Dataset: "suite", DatasetRevision: "rev", ConfigHash: "first", Config: v2.Config{Version: v2.ConfigVersion}}
	s.Require().NoError(s.store.Initialize(ctx, run, []v2.TrialKey{key}))
	run.ConfigHash = "second"
	s.Require().Error(s.store.Initialize(ctx, run, []v2.TrialKey{key}))
	s.Require().Error(s.store.Complete(ctx, v2.TrialAttemptHandle{RunID: runID, CaseID: "case", Arm: "control", Number: 1}, result(runID, "control", "completed")))
	handle, claimed, err := s.store.Claim(ctx, key, false, 1)
	s.Require().NoError(err)
	s.True(claimed)
	mismatched := result(runID, "other-arm", "completed")
	s.Require().ErrorContains(s.store.Complete(ctx, handle, mismatched), "identity do not match")
	s.Require().Error(s.store.Finish(ctx, runID))
}

func (s *storeSuite) TestAttemptLedgerPreservesFailedAttemptAcrossRetry() {
	ctx := context.Background()
	runID := "eval-attempt-ledger-" + time.Now().UTC().Format("20060102150405.000000000")
	key := v2.TrialKey{RunID: runID, CaseID: "case-1", Arm: "memory"}
	run := v2.RunRecord{
		ID: runID, Dataset: "suite", DatasetRevision: "rev", ConfigHash: "hash",
		Config: v2.Config{Version: v2.ConfigVersion},
	}
	s.Require().NoError(s.store.Initialize(ctx, run, []v2.TrialKey{key}))

	first, claimed, err := s.store.Claim(ctx, key, false, 2)
	s.Require().NoError(err)
	s.True(claimed)
	s.Equal(1, first.Number)
	s.Require().NoError(s.store.UpdateAttempt(ctx, first, v2.TrialStageConsumer, map[string]string{
		"artifact_dir": "trials/case-1/memory/attempts/001",
	}))
	failed := result(runID, "memory", "failed")
	failed.FailureStage = v2.TrialStageConsumer
	failed.FailureClass = v2.FailureClassExit
	failed.Error = "run consumer: exit status 1"
	s.Require().NoError(s.store.Fail(ctx, first, failed))

	second, claimed, err := s.store.Claim(ctx, key, true, 2)
	s.Require().NoError(err)
	s.True(claimed)
	s.Equal(2, second.Number)
	completed := result(runID, "memory", "completed")
	s.Require().NoError(s.store.Complete(ctx, second, completed))

	attempts, err := s.store.Attempts(ctx, runID)
	s.Require().NoError(err)
	s.Require().Len(attempts, 2)
	s.Equal(1, attempts[0].Number)
	s.Equal("failed", attempts[0].Status)
	s.Equal(v2.TrialStageConsumer, attempts[0].Stage)
	s.Equal(v2.FailureClassExit, attempts[0].FailureClass)
	s.Equal("run consumer: exit status 1", attempts[0].Error)
	s.Equal("trials/case-1/memory/attempts/001", attempts[0].ArtifactRefs["artifact_dir"])
	s.NotNil(attempts[0].CompletedAt)
	s.Equal(2, attempts[1].Number)
	s.Equal("completed", attempts[1].Status)
	s.Equal(v2.TrialStageCompleted, attempts[1].Stage)
	s.Empty(attempts[1].FailureClass)
	s.NotNil(attempts[1].CompletedAt)
}

func (s *storeSuite) TestClaimRejudgeAppendsAttemptWithoutDiscardingCompletedResult() {
	ctx := context.Background()
	runID := "eval-rejudge-" + time.Now().UTC().Format("20060102150405.000000000")
	key := v2.TrialKey{RunID: runID, CaseID: "case-1", Arm: "memory"}
	run := v2.RunRecord{
		ID: runID, Dataset: "suite", DatasetRevision: "rev", ConfigHash: "hash",
		Config: v2.Config{Version: v2.ConfigVersion},
	}
	s.Require().NoError(s.store.Initialize(ctx, run, []v2.TrialKey{key}))
	first, claimed, err := s.store.Claim(ctx, key, false, 1)
	s.Require().NoError(err)
	s.True(claimed)
	unjudged := result(runID, "memory", "completed")
	unjudged.Judged = false
	s.Require().NoError(s.store.Complete(ctx, first, unjudged))

	second, claimed, err := s.store.ClaimRejudge(ctx, key)

	s.Require().NoError(err)
	s.True(claimed)
	s.Equal(2, second.Number)
	results, err := s.store.Results(ctx, runID)
	s.Require().NoError(err)
	s.Empty(results)
	unjudged.Judged = true
	s.Require().NoError(s.store.Complete(ctx, second, unjudged))
	_, claimed, err = s.store.ClaimRejudge(ctx, key)
	s.Require().NoError(err)
	s.False(claimed)
	attempts, err := s.store.Attempts(ctx, runID)
	s.Require().NoError(err)
	s.Require().Len(attempts, 2)
	s.Equal("completed", attempts[1].Status)
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
