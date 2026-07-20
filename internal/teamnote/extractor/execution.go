package extractor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

var ErrProviderResponseTooLarge = errors.New("provider response too large")

const (
	defaultProviderAttemptTimeout = 90 * time.Second
	defaultProviderMaxResponse    = 1 << 20
	defaultPrimaryMaxOutputTokens = 16 * 1024
	defaultAuxiliaryOutputTokens  = 4 * 1024
)

type providerStatusError struct {
	status int
	body   string
}

func (e *providerStatusError) Error() string {
	return fmt.Sprintf("extractor response status %d: %s", e.status, e.body)
}

func normalizeExecutionPolicy(policy *ExecutionPolicy) error {
	if policy.AttemptTimeout == 0 {
		policy.AttemptTimeout = defaultProviderAttemptTimeout
	}
	if policy.MaxAttempts == 0 {
		policy.MaxAttempts = 1
	}
	if policy.RetryBackoff == 0 {
		policy.RetryBackoff = 250 * time.Millisecond
	}
	if policy.MaxResponseBytes == 0 {
		policy.MaxResponseBytes = defaultProviderMaxResponse
	}
	if policy.PrimaryMaxOutputTokens == 0 {
		policy.PrimaryMaxOutputTokens = defaultPrimaryMaxOutputTokens
	}
	if policy.SummaryMaxOutputTokens == 0 {
		policy.SummaryMaxOutputTokens = defaultAuxiliaryOutputTokens
	}
	if policy.CompactionMaxOutputTokens == 0 {
		policy.CompactionMaxOutputTokens = defaultAuxiliaryOutputTokens
	}
	if policy.AttemptTimeout < 0 || policy.MaxAttempts < 1 || policy.MaxAttempts > 3 ||
		policy.RetryBackoff < 0 || policy.MaxResponseBytes < 1 ||
		policy.PrimaryMaxOutputTokens < 1 || policy.SummaryMaxOutputTokens < 1 ||
		policy.CompactionMaxOutputTokens < 1 {
		return fmt.Errorf("provider execution policy has invalid deadline, attempts, backoff, response, or output budget")
	}
	return nil
}

func (e *OpenAI) executeProvider(
	ctx context.Context,
	messages []chatMessage,
	requestedMaxTokens int,
	callType ProviderCallType,
	validate func([]byte) error,
) ([]byte, error) {
	policy := e.config.ExecutionPolicy
	maxTokens := requestedMaxTokens
	if maxTokens == 0 {
		maxTokens = outputBudget(policy, callType)
	}
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		startedAt := time.Now()
		attemptCtx, cancel := context.WithTimeout(ctx, policy.AttemptTimeout)
		responseBody, status, err := e.providerRequest(attemptCtx, messages, maxTokens)
		if err == nil && validate != nil {
			err = validate(responseBody)
		}
		failureClass, retryable := classifyProviderFailure(attemptCtx, err)
		cancel()
		e.observeProviderAttempt(ctx, ProviderCall{
			Type: callType, Attempt: attempt, MaxAttempts: policy.MaxAttempts,
			StartedAt: startedAt.UTC(), DurationMS: time.Since(startedAt).Milliseconds(),
			HTTPStatus: status, MaxOutputTokens: maxTokens, Usage: providerUsage(responseBody),
			FailureClass: failureClass, Retryable: retryable, Error: providerErrorText(err),
		})
		if err == nil {
			return responseBody, nil
		}
		if !retryable || attempt == policy.MaxAttempts {
			return nil, fmt.Errorf("execute %s provider attempt %d: %w", callType, attempt, err)
		}
		if err := waitProviderRetry(ctx, policy.RetryBackoff); err != nil {
			return nil, fmt.Errorf("execute %s provider after attempt %d: %w", callType, attempt, err)
		}
	}
	return nil, fmt.Errorf("execute extractor provider: exhausted attempts")
}

func outputBudget(policy ExecutionPolicy, callType ProviderCallType) int {
	switch callType {
	case ProviderCallSummary:
		return policy.SummaryMaxOutputTokens
	case ProviderCallCompaction:
		return policy.CompactionMaxOutputTokens
	default:
		return policy.PrimaryMaxOutputTokens
	}
}

func classifyProviderFailure(attemptCtx context.Context, err error) (ProviderFailureClass, bool) {
	if err == nil {
		return ProviderFailureNone, false
	}
	if errors.Is(err, context.Canceled) {
		return ProviderFailureCanceled, false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
		return ProviderFailureDeadline, true
	}
	if errors.Is(err, ErrProviderResponseTooLarge) {
		return ProviderFailureResponseTooLarge, false
	}
	if errors.Is(err, ErrInvalidModelResponse) {
		return ProviderFailureInvalidResponse, false
	}
	var statusErr *providerStatusError
	if errors.As(err, &statusErr) {
		switch {
		case statusErr.status == http.StatusRequestTimeout:
			return ProviderFailureTransport, true
		case statusErr.status == http.StatusTooManyRequests:
			return ProviderFailureRateLimited, true
		case statusErr.status >= http.StatusInternalServerError:
			return ProviderFailureServer, true
		default:
			return ProviderFailureClient, false
		}
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return ProviderFailureTransport, true
	}
	return ProviderFailureTransport, true
}

func (e *OpenAI) observeProviderAttempt(ctx context.Context, record ProviderCall) {
	if e.config.ProviderCallObserver == nil {
		return
	}
	if scopeID, err := teamnote.ScopeFromContext(ctx); err == nil {
		record.ScopeID = scopeID
	}
	e.config.ProviderCallObserver(record)
}

func providerErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func waitProviderRetry(ctx context.Context, backoff time.Duration) error {
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait to retry extractor provider: %w", ctx.Err())
	}
}
