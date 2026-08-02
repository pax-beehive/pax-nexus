package agentsessions

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

const (
	retryBaseDelay      = 1 * time.Second
	retryMaxDelay       = 60 * time.Second
	defaultMaxAttempts  = 5
	breakerWindow       = 50
	breakerMinSamples   = 10
	breakerFailureRate  = 0.20
	breakerOpenDuration = 5 * time.Minute
)

var ErrCircuitOpen = errors.New("llm circuit open")

// RetryingChatClient 实现 llm.ChatClient。传输错误按指数退避+全抖动重试
// (base 1s, cap 60s, 最多 MaxAttempts 次);滑动窗口 50 个样本中失败率
// >20%(且样本 ≥10)时开路 5 分钟。Sleep/Rand/Now 可注入以便测试。
type RetryingChatClient struct {
	Inner       llm.ChatClient
	MaxAttempts int                 // 0 → 5
	Sleep       func(time.Duration) // nil → time.Sleep
	Rand        func() float64      // nil → rand.Float64
	Now         func() time.Time    // nil → time.Now
	// 内部熔断状态,零值可用
	mu        sync.Mutex
	outcomes  []bool // 最近 50 次,true=失败
	openUntil time.Time
}

func (r *RetryingChatClient) Complete(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	random, now := r.Rand, r.Now
	if random == nil {
		random = rand.Float64
	}
	if now == nil {
		now = time.Now
	}
	attempts := r.MaxAttempts
	if attempts <= 0 {
		attempts = defaultMaxAttempts
	}
	if err := r.checkBreaker(now()); err != nil {
		return llm.ChatResponse{}, err
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := r.waitBackoff(ctx, attempt, random); err != nil {
				return llm.ChatResponse{}, err
			}
		}
		// Check context before attempting
		if ctx.Err() != nil {
			return llm.ChatResponse{}, ctx.Err()
		}
		resp, err := r.Inner.Complete(ctx, req)
		r.record(err != nil, now())
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return llm.ChatResponse{}, ctx.Err()
		}
	}
	return llm.ChatResponse{}, lastErr
}

// waitBackoff 按全抖动指数退避等待重试:优先用注入的 Sleep(测试),否则用
// timer+select 支持 ctx 取消。
func (r *RetryingChatClient) waitBackoff(ctx context.Context, attempt int, random func() float64) error {
	// Clamp exponent to prevent overflow
	exp := attempt - 1
	if exp > 6 { // 1s<<6 = 64s, already above the 60s ceiling
		exp = 6
	}
	delay := retryBaseDelay << exp
	if delay > retryMaxDelay {
		delay = retryMaxDelay
	}
	jitteredDelay := time.Duration(random() * float64(delay)) // 全抖动

	// Context-aware sleep
	if r.Sleep != nil {
		// Injected sleep (tests): call it, then check context
		r.Sleep(jitteredDelay)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	// Real sleep: use timer + select for cancellation
	timer := time.NewTimer(jitteredDelay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
		// Timer fired, continue to next attempt
	}
	return nil
}

func (r *RetryingChatClient) checkBreaker(now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Before(r.openUntil) {
		return ErrCircuitOpen
	}
	return nil
}

func (r *RetryingChatClient) record(failed bool, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcomes = append(r.outcomes, failed)
	if len(r.outcomes) > breakerWindow {
		r.outcomes = r.outcomes[len(r.outcomes)-breakerWindow:]
	}
	if len(r.outcomes) < breakerMinSamples {
		return
	}
	failures := 0
	for _, f := range r.outcomes {
		if f {
			failures++
		}
	}
	if float64(failures)/float64(len(r.outcomes)) > breakerFailureRate {
		r.openUntil = now.Add(breakerOpenDuration)
		r.outcomes = nil // 半开:窗口清零重新计数
	}
}
