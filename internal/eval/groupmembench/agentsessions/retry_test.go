package agentsessions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

type scriptedClient struct {
	errs  []error // 每次调用弹出一个;nil 表示成功
	calls int
}

func (s *scriptedClient) Complete(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
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
