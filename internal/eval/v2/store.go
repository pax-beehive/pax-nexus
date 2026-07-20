package v2

import "context"

type Store interface {
	Acquire(context.Context, string) (bool, error)
	Release(context.Context, string) error
	Initialize(context.Context, RunRecord, []TrialKey) error
	ResetRunning(context.Context, string) error
	HasRunnable(context.Context, string, bool, int) (bool, error)
	Claim(context.Context, TrialKey, bool, int) (TrialAttemptHandle, bool, error)
	ClaimRejudge(context.Context, TrialKey) (TrialAttemptHandle, bool, error)
	UpdateAttempt(context.Context, TrialAttemptHandle, TrialStage, map[string]string) error
	Complete(context.Context, TrialAttemptHandle, TrialResult) error
	Fail(context.Context, TrialAttemptHandle, TrialResult) error
	Attempts(context.Context, string) ([]TrialAttempt, error)
	Results(context.Context, string) ([]TrialResult, error)
	Finish(context.Context, string) error
}
