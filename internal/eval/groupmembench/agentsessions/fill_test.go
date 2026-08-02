package agentsessions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

type cannedClient struct {
	content string
	err     error
	calls   int
	lastReq llm.ChatRequest
}

func (c *cannedClient) Complete(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	c.calls++
	c.lastReq = req
	if c.err != nil {
		return llm.ChatResponse{}, c.err
	}
	return llm.ChatResponse{Message: llm.ChatMessage{Content: c.content},
		FinishReason: "stop"}, nil
}

func fillWindow() (Window, []ActionSpec) {
	m, _, _ := Normalize([]groupmembench.Message{{NodeID: "Msg_1", Channel: "A",
		Author: "User_2", Timestamp: "2025-07-19T01:00:00",
		Content: "assessment due 2025-07-16, User_7 owns it"}})
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1, Msgs: m}
	specs := []ActionSpec{{Type: "memory_write", SourceMsg: "Msg_1"}}
	return w, specs
}

func TestFillSessionParsesActions(t *testing.T) {
	client := &cannedClient{
		content: `{"actions":[{"index":0,"content":"User_7 owns the assessment, due 2025-07-16"}]}`}
	f := &Filler{Client: client, Model: "deepseek-v4-flash"}
	w, specs := fillWindow()
	actions, err := f.FillSession(context.Background(), w, Persona{UserID: "User_1"}, specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Fallback ||
		actions[0].Content != "User_7 owns the assessment, due 2025-07-16" ||
		actions[0].SourceMsgs[0] != "Msg_1" {
		t.Fatalf("bad actions: %+v", actions)
	}
	if client.lastReq.Model != "deepseek-v4-flash" {
		t.Fatalf("model not set: %q", client.lastReq.Model)
	}
	if !strings.Contains(client.lastReq.Messages[1].Content, "Msg_1") {
		t.Fatal("prompt must include source message")
	}
}

func TestFillSessionFallsBackOnFailure(t *testing.T) {
	f := &Filler{Client: &cannedClient{err: errors.New("boom")}, Model: "m"}
	w, specs := fillWindow()
	actions, err := f.FillSession(context.Background(), w, Persona{UserID: "User_1"}, specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || !actions[0].Fallback ||
		!strings.Contains(actions[0].Content, "assessment due 2025-07-16") {
		t.Fatalf("fallback missing source excerpt: %+v", actions)
	}
	if f.Stats().Fallbacks != 1 {
		t.Fatalf("stats: %+v", f.Stats())
	}
}

func TestFillSessionUsesCache(t *testing.T) {
	client := &cannedClient{content: `{"actions":[{"index":0,"content":"cached"}]}`}
	f := &Filler{Client: client, Model: "m", Cache: &DiskCache{Dir: t.TempDir()}}
	w, specs := fillWindow()
	if _, err := f.FillSession(context.Background(), w, Persona{}, specs); err != nil {
		t.Fatal(err)
	}
	if _, err := f.FillSession(context.Background(), w, Persona{}, specs); err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 {
		t.Fatalf("cache not used, calls=%d", client.calls)
	}
	if f.Stats().CacheHits != 1 {
		t.Fatalf("stats: %+v", f.Stats())
	}
}
