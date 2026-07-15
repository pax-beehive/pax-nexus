package v2

import "context"

type Store interface {
	Acquire(context.Context, string) (bool, error)
	Release(context.Context, string) error
	Initialize(context.Context, RunRecord, []TrialKey) error
	ResetRunning(context.Context, string) error
	HasRunnable(context.Context, string, bool, int) (bool, error)
	Claim(context.Context, TrialKey, bool, int) (bool, error)
	Complete(context.Context, TrialResult) error
	Fail(context.Context, TrialResult) error
	Results(context.Context, string) ([]TrialResult, error)
	Finish(context.Context, string) error
}
