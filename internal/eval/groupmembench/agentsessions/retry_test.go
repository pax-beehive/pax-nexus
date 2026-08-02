package agentsessions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

type scriptedClient struct {
	mu    sync.Mutex
	errs  []error // 每次调用弹出一个;nil 表示成功
	calls int
}

func (s *scriptedClient) Complete(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if len(s.errs) == 0 {
		return llm.ChatResponse{Message: llm.ChatMessage{Content: "ok"}}, nil
	}
	err := s.errs[0]
	s.errs = s.errs[1:]
	if err == nil {
		return llm.ChatResponse{Message: llm.ChatMessage{Content: "ok"}}, nil
	}
	return llm.ChatResponse{}, err
}

func newTestRetrier(inner llm.ChatClient) (*RetryingChatClient, *[]time.Duration) {
	var slept []time.Duration
	now := time.Unix(0, 0)
	r := &RetryingChatClient{Inner: inner,
		Sleep: func(d time.Duration) { slept = append(slept, d); now = now.Add(d) },
		Rand:  func() float64 { return 1.0 },
		Now:   func() time.Time { return now }}
	return r, &slept
}

func TestRetrySucceedsAfterTransientErrors(t *testing.T) {
	inner := &scriptedClient{errs: []error{errors.New("timeout"),
		errors.New("status 503"), nil}}
	r, slept := newTestRetrier(inner)
	resp, err := r.Complete(context.Background(), llm.ChatRequest{})
	if err != nil || resp.Message.Content != "ok" {
		t.Fatalf("want ok, got %v %v", resp, err)
	}
	if inner.calls != 3 || len(*slept) != 2 {
		t.Fatalf("calls=%d sleeps=%d", inner.calls, len(*slept))
	}
	if (*slept)[0] != 1*time.Second || (*slept)[1] != 2*time.Second {
		t.Fatalf("backoff wrong: %v", *slept) // Rand=1.0 → 全额退避 1s, 2s
	}
}

func TestRetryGivesUpAfterMaxAttempts(t *testing.T) {
	inner := &scriptedClient{errs: []error{errors.New("e"), errors.New("e"),
		errors.New("e"), errors.New("e"), errors.New("e")}}
	r, _ := newTestRetrier(inner)
	_, err := r.Complete(context.Background(), llm.ChatRequest{})
	if err == nil || inner.calls != 5 {
		t.Fatalf("want failure after 5 attempts, calls=%d err=%v", inner.calls, err)
	}
}

func TestCircuitOpensAndRecovers(t *testing.T) {
	inner := &scriptedClient{}
	r, _ := newTestRetrier(inner)
	r.MaxAttempts = 1
	// 10 个样本里 3 个失败 → 30% > 20% → 开路
	for i := 0; i < 7; i++ {
		if _, err := r.Complete(context.Background(), llm.ChatRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		inner.errs = []error{errors.New("boom")}
		r.Complete(context.Background(), llm.ChatRequest{}) //nolint:errcheck
	}
	if _, err := r.Complete(context.Background(), llm.ChatRequest{}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("want ErrCircuitOpen, got %v", err)
	}
	// 5 分钟后半开恢复
	base := r.Now()
	r.Now = func() time.Time { return base.Add(5*time.Minute + time.Second) }
	if _, err := r.Complete(context.Background(), llm.ChatRequest{}); err != nil {
		t.Fatalf("want recovery after open window, got %v", err)
	}
}

func TestRetryConcurrentComplete(t *testing.T) {
	inner := &scriptedClient{}
	r, _ := newTestRetrier(inner)
	r.MaxAttempts = 1

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			resp, err := r.Complete(context.Background(), llm.ChatRequest{})
			if err != nil {
				t.Errorf("concurrent call failed: %v", err)
				return
			}
			if resp.Message.Content != "ok" {
				t.Errorf("want ok, got %s", resp.Message.Content)
			}
		}()
	}
	wg.Wait()
}

func TestRetryStopsOnContextCancel(t *testing.T) {
	inner := &scriptedClient{errs: []error{errors.New("e1"), errors.New("e2")}}
	r, _ := newTestRetrier(inner)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately before first backoff
	cancel()

	_, err := r.Complete(ctx, llm.ChatRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestRetryBackoffCapsAtMax(t *testing.T) {
	// Create 40 errors to ensure all attempts fail
	errs := make([]error, 40)
	for i := range errs {
		errs[i] = errors.New("persistent error")
	}
	inner := &scriptedClient{errs: errs}
	r, slept := newTestRetrier(inner)
	r.MaxAttempts = 40 // Large MaxAttempts to trigger overflow if not clamped

	_, err := r.Complete(context.Background(), llm.ChatRequest{})
	if err == nil {
		t.Fatalf("want error after max attempts")
	}

	// Should have 39 sleeps (before attempts 1-39)
	if len(*slept) != 39 {
		t.Fatalf("want 39 sleeps, got %d", len(*slept))
	}

	// Verify all delays are between 0 and 60s (Rand=1.0 → full jitter)
	for i, delay := range *slept {
		if delay <= 0 || delay > 60*time.Second {
			t.Fatalf("delay[%d]=%v out of bounds (0, 60s]", i, delay)
		}
	}
}

func TestRetryStopsMidBackoff(t *testing.T) {
	// Use production timer path (Sleep=nil), always-failing inner
	inner := &scriptedClient{errs: []error{errors.New("e1"), errors.New("e2"), errors.New("e3")}}

	ctx, cancel := context.WithCancel(context.Background())
	r := &RetryingChatClient{
		Inner:       inner,
		MaxAttempts: 3,
		Sleep:       nil,                           // Use production timer-based sleep
		Rand:        func() float64 { return 0.5 }, // 500ms backoff (half of 1s)
		Now:         time.Now,
	}

	// Cancel context ~50ms after Complete starts (before first backoff completes)
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	_, err := r.Complete(ctx, llm.ChatRequest{})
	elapsed := time.Since(start)

	// Should return promptly (< 2s) with context error, not block on full backoff
	if elapsed > 2*time.Second {
		t.Fatalf("Complete took too long: %v (context should have cancelled backoff)", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
