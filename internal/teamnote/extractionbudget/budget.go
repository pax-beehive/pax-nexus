// Package extractionbudget owns the serial execution envelope shared by the
// extraction provider, durable worker, runtime, and evaluation adapters.
package extractionbudget

import (
	"fmt"
	"math"
	"time"
)

const (
	DefaultProviderAttemptTimeout = 120 * time.Second
	DefaultProviderMaxAttempts    = 1
	DefaultProviderRetryBackoff   = 250 * time.Millisecond
	DefaultProviderMaxResponse    = 1 << 20
	DefaultPrimaryOutputTokens    = 16 * 1024
	DefaultAuxiliaryOutputTokens  = 4 * 1024
	DefaultWorkerJobTimeout       = 3 * time.Minute
	DefaultMaxSlicesPerJob        = 1
	OutcomePersistenceMargin      = 10 * time.Second
)

// ProviderPolicy bounds physical provider attempts behind one logical
// extraction call and the response accepted from each attempt.
type ProviderPolicy struct {
	AttemptTimeout            time.Duration
	MaxAttempts               int
	RetryBackoff              time.Duration
	MaxResponseBytes          int64
	PrimaryMaxOutputTokens    int
	SummaryMaxOutputTokens    int
	CompactionMaxOutputTokens int
}

// Envelope relates provider work to one durable worker job.
type Envelope struct {
	Provider          ProviderPolicy
	MaxSlicesPerJob   int
	WorkerJobTimeout  time.Duration
	CompactionEnabled bool
}

func DefaultProviderPolicy() ProviderPolicy {
	return ProviderPolicy{
		AttemptTimeout: DefaultProviderAttemptTimeout, MaxAttempts: DefaultProviderMaxAttempts,
		RetryBackoff: DefaultProviderRetryBackoff, MaxResponseBytes: DefaultProviderMaxResponse,
		PrimaryMaxOutputTokens: DefaultPrimaryOutputTokens,
		SummaryMaxOutputTokens: DefaultAuxiliaryOutputTokens, CompactionMaxOutputTokens: DefaultAuxiliaryOutputTokens,
	}
}

func DefaultEnvelope() Envelope {
	return Envelope{
		Provider: DefaultProviderPolicy(), MaxSlicesPerJob: DefaultMaxSlicesPerJob,
		WorkerJobTimeout: DefaultWorkerJobTimeout,
	}
}

func NormalizeProviderPolicy(policy ProviderPolicy) (ProviderPolicy, error) {
	defaults := DefaultProviderPolicy()
	if policy.AttemptTimeout == 0 {
		policy.AttemptTimeout = defaults.AttemptTimeout
	}
	if policy.MaxAttempts == 0 {
		policy.MaxAttempts = defaults.MaxAttempts
	}
	if policy.RetryBackoff == 0 {
		policy.RetryBackoff = defaults.RetryBackoff
	}
	if policy.MaxResponseBytes == 0 {
		policy.MaxResponseBytes = defaults.MaxResponseBytes
	}
	if policy.PrimaryMaxOutputTokens == 0 {
		policy.PrimaryMaxOutputTokens = defaults.PrimaryMaxOutputTokens
	}
	if policy.SummaryMaxOutputTokens == 0 {
		policy.SummaryMaxOutputTokens = defaults.SummaryMaxOutputTokens
	}
	if policy.CompactionMaxOutputTokens == 0 {
		policy.CompactionMaxOutputTokens = defaults.CompactionMaxOutputTokens
	}
	if err := policy.Validate(); err != nil {
		return ProviderPolicy{}, err
	}
	return policy, nil
}

func (p ProviderPolicy) LogicalCallBudget() time.Duration {
	budget, ok := p.logicalCallBudget()
	if !ok {
		return time.Duration(math.MaxInt64)
	}
	return budget
}

func (p ProviderPolicy) BackgroundCallTimeout() time.Duration {
	budget := p.LogicalCallBudget()
	if budget > time.Duration(math.MaxInt64)-OutcomePersistenceMargin {
		return time.Duration(math.MaxInt64)
	}
	return budget + OutcomePersistenceMargin
}

func (e Envelope) JobProviderBudget() time.Duration {
	budget, ok := e.jobProviderBudget()
	if !ok {
		return time.Duration(math.MaxInt64)
	}
	return budget
}

func (e Envelope) jobProviderBudget() (time.Duration, bool) {
	serialCallsPerSlice := 1
	if e.CompactionEnabled {
		serialCallsPerSlice = 3
	}
	if e.MaxSlicesPerJob < 1 || e.MaxSlicesPerJob > math.MaxInt/serialCallsPerSlice {
		return 0, false
	}
	factor := e.MaxSlicesPerJob * serialCallsPerSlice
	logical, ok := e.Provider.logicalCallBudget()
	if !ok || logical <= 0 || int64(factor) > math.MaxInt64/int64(logical) {
		return 0, false
	}
	return time.Duration(factor) * logical, true
}

func (e Envelope) Validate() error {
	if err := e.Provider.Validate(); err != nil {
		return err
	}
	if e.MaxSlicesPerJob < 1 || e.WorkerJobTimeout <= 0 {
		return fmt.Errorf("extraction execution envelope has invalid worker limits")
	}
	jobBudget, ok := e.jobProviderBudget()
	if !ok {
		return fmt.Errorf("extraction execution envelope exceeds duration limits")
	}
	if jobBudget > time.Duration(math.MaxInt64)-OutcomePersistenceMargin {
		return fmt.Errorf("extraction execution envelope leaves no outcome persistence margin")
	}
	reservedBudget := jobBudget + OutcomePersistenceMargin
	if reservedBudget >= e.WorkerJobTimeout {
		return fmt.Errorf(
			"extraction execution budget %s plus persistence margin %s for %d slices must be less than worker job deadline %s",
			jobBudget, OutcomePersistenceMargin, e.MaxSlicesPerJob, e.WorkerJobTimeout,
		)
	}
	return nil
}

func (p ProviderPolicy) Validate() error {
	if p.AttemptTimeout <= 0 || p.MaxAttempts < 1 || p.MaxAttempts > 3 || p.RetryBackoff < 0 ||
		p.MaxResponseBytes < 1 || p.PrimaryMaxOutputTokens < 1 || p.SummaryMaxOutputTokens < 1 ||
		p.CompactionMaxOutputTokens < 1 {
		return fmt.Errorf("provider execution policy has invalid deadline, attempts, backoff, response, or output budget")
	}
	logicalBudget, ok := p.logicalCallBudget()
	if !ok || logicalBudget > time.Duration(math.MaxInt64)-OutcomePersistenceMargin {
		return fmt.Errorf("provider execution policy exceeds duration limits")
	}
	return nil
}

func (p ProviderPolicy) logicalCallBudget() (time.Duration, bool) {
	attempts := int64(p.MaxAttempts)
	if attempts < 1 || int64(p.AttemptTimeout) > math.MaxInt64/attempts {
		return 0, false
	}
	attemptBudget := attempts * int64(p.AttemptTimeout)
	retries := attempts - 1
	if retries > 0 && int64(p.RetryBackoff) > math.MaxInt64/retries {
		return 0, false
	}
	retryBudget := retries * int64(p.RetryBackoff)
	if attemptBudget > math.MaxInt64-retryBudget {
		return 0, false
	}
	return time.Duration(attemptBudget + retryBudget), true
}
