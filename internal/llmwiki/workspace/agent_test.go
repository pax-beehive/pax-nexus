package workspace_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	"github.com/stretchr/testify/suite"
)

type agentSuite struct {
	suite.Suite
	root       string
	sourcePath string
	anchor     string
}

func TestAgentSuite(t *testing.T) {
	suite.Run(t, new(agentSuite))
}

func (s *agentSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.T().Cleanup(func() {
		err := os.Chmod(filepath.Join(s.root, "sources"), 0o755)
		if err != nil && !os.IsNotExist(err) {
			s.Require().NoError(err)
		}
	})
	exported := workspace.SessionExport{
		SchemaVersion: workspace.PaxmSessionSchema,
		SessionID:     "agent-session",
		Turns: []workspace.SessionTurn{{
			ID:        "turn-1",
			User:      "The Wiki uses immutable sources.",
			Assistant: "The topic tree links durable pages.",
			CreatedAt: time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC),
		}},
	}
	encoded, err := json.Marshal(exported)
	s.Require().NoError(err)
	result, err := workspace.Build(context.Background(), workspace.BuildConfig{
		Root: s.root,
		ReadSession: func(context.Context, string) ([]byte, error) {
			return encoded, nil
		},
	}, workspace.BuildRequest{
		SessionID: "agent-session", TurnStart: 0, TurnEnd: 1,
	})
	s.Require().NoError(err)
	s.sourcePath = result.Source.Path
	s.anchor = result.Source.Anchors[0].ID
}

func (s *agentSuite) TestAgentUsesFilesystemToolsAndRecordsUsage() {
	page := "# Local-first Wiki\n\nSources are immutable " +
		"([source](../../" + s.sourcePath + "#" + s.anchor + ")).\n"
	client := &scriptedChatClient{responses: []workspace.ChatResponse{
		{
			Message: workspace.ChatMessage{
				Role: "assistant",
				ToolCalls: []workspace.ToolCall{
					call("read_file", `{"path":"AGENTS.md"}`),
					call("read_file", `{"path":"`+s.sourcePath+`"}`),
					call("write_file", `{"path":"wiki/index.md","content":"# Wiki\n\n- [Local-first](pages/local-first.md)\n"}`),
					call("write_file", mustArguments(map[string]string{
						"path": "wiki/pages/local-first.md", "content": page,
					})),
					call("validate", `{}`),
				},
			},
			Usage: workspace.TokenUsage{InputTokens: 120, OutputTokens: 80},
		},
		{
			Message: workspace.ChatMessage{Role: "assistant", Content: "Done."},
			Usage:   workspace.TokenUsage{InputTokens: 30, OutputTokens: 5},
		},
	}}

	result, err := workspace.RunAgent(context.Background(), workspace.AgentConfig{
		Root: s.root, Model: "deepseek-v4-pro", Client: client,
		Now: monotonicClock(time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)),
	}, workspace.AgentRequest{
		RunID: "run-one", Instruction: "Build the durable Wiki.",
	})
	s.Require().NoError(err)
	s.True(result.Validation.Valid, result.Validation.String())
	s.Equal(2, result.Audit.Calls)
	s.Equal(5, result.Audit.ToolCalls)
	s.Equal(150, result.Audit.InputTokens)
	s.Equal(85, result.Audit.OutputTokens)
	s.Empty(result.Audit.FailureReason)

	rendered, err := os.ReadFile(filepath.Join(s.root, "wiki/pages/local-first.md"))
	s.Require().NoError(err)
	s.Equal(page, string(rendered))

	auditBytes, err := os.ReadFile(filepath.Join(s.root, ".pax/runs/run-one.json"))
	s.Require().NoError(err)
	var audit workspace.RunAudit
	s.Require().NoError(json.Unmarshal(auditBytes, &audit))
	s.Equal(result.Audit, audit)
}

func (s *agentSuite) TestToolSandboxRefusesSourceMutationThenAgentRecovers() {
	sourceFile := filepath.Join(s.root, filepath.FromSlash(s.sourcePath))
	before, err := os.ReadFile(sourceFile)
	s.Require().NoError(err)
	client := &scriptedChatClient{responses: []workspace.ChatResponse{
		{
			Message: workspace.ChatMessage{
				Role: "assistant",
				ToolCalls: []workspace.ToolCall{
					call("write_file", `{"path":"`+s.sourcePath+`","content":"tampered"}`),
				},
			},
		},
		{
			Message: workspace.ChatMessage{
				Role: "assistant",
				ToolCalls: []workspace.ToolCall{
					call("write_file", `{"path":"wiki/index.md","content":"# Wiki\n"}`),
				},
			},
		},
		{Message: workspace.ChatMessage{Role: "assistant", Content: "Done."}},
	}}

	result, err := workspace.RunAgent(context.Background(), workspace.AgentConfig{
		Root: s.root, Client: client,
	}, workspace.AgentRequest{RunID: "sandbox", Instruction: "Maintain Wiki."})
	s.Require().NoError(err)
	s.True(result.Validation.Valid, result.Validation.String())

	after, err := os.ReadFile(sourceFile)
	s.Require().NoError(err)
	s.Equal(before, after)
	s.Require().Len(client.requests, 3)
	toolResult := client.requests[1].Messages[len(client.requests[1].Messages)-1]
	s.Contains(toolResult.Content, "writes are restricted to wiki/")
}

func (s *agentSuite) TestAgentFeedsValidationFailureBackForRepair() {
	client := &scriptedChatClient{responses: []workspace.ChatResponse{
		{
			Message: workspace.ChatMessage{
				Role: "assistant",
				ToolCalls: []workspace.ToolCall{
					call("write_file", `{"path":"wiki/index.md","content":"# Wiki\n\n- [Missing](pages/missing.md)\n"}`),
				},
			},
		},
		{Message: workspace.ChatMessage{Role: "assistant", Content: "Done."}},
		{
			Message: workspace.ChatMessage{
				Role: "assistant",
				ToolCalls: []workspace.ToolCall{
					call("write_file", `{"path":"wiki/index.md","content":"# Wiki\n"}`),
				},
			},
		},
		{Message: workspace.ChatMessage{Role: "assistant", Content: "Repaired."}},
	}}

	result, err := workspace.RunAgent(context.Background(), workspace.AgentConfig{
		Root: s.root, Client: client,
	}, workspace.AgentRequest{RunID: "repair", Instruction: "Maintain Wiki."})
	s.Require().NoError(err)
	s.True(result.Validation.Valid, result.Validation.String())
	s.Require().Len(client.requests, 4)
	s.Contains(
		client.requests[2].Messages[len(client.requests[2].Messages)-1].Content,
		"broken internal link",
	)
}

func (s *agentSuite) TestRecordsClientFailureReason() {
	client := &scriptedChatClient{err: errors.New("provider unavailable")}
	_, err := workspace.RunAgent(context.Background(), workspace.AgentConfig{
		Root: s.root, Client: client,
	}, workspace.AgentRequest{RunID: "failed", Instruction: "Maintain Wiki."})
	s.Require().ErrorContains(err, "provider unavailable")

	auditBytes, readErr := os.ReadFile(filepath.Join(s.root, ".pax/runs/failed.json"))
	s.Require().NoError(readErr)
	var audit workspace.RunAudit
	s.Require().NoError(json.Unmarshal(auditBytes, &audit))
	s.Contains(audit.FailureReason, "provider unavailable")
	s.Equal(1, audit.Calls)
}

func (s *agentSuite) TestFilesystemToolMatrixIsBoundedAndComposable() {
	client := &scriptedChatClient{responses: []workspace.ChatResponse{
		{
			Message: workspace.ChatMessage{
				Role: "assistant",
				ToolCalls: []workspace.ToolCall{
					call("list_files", `{}`),
					call("grep", `{"query":"immutable","path":"sources"}`),
					call("write_file", `{"path":"wiki/pages/draft.md","content":"# Draft\n"}`),
					call("move_file", `{"from":"wiki/pages/draft.md","to":"wiki/topics/moved.md"}`),
					call("read_file", `{"path":"wiki/topics/moved.md"}`),
					call("delete_file", `{"path":"wiki/topics/moved.md"}`),
					call("read_file", `{"path":"."}`),
					call("unknown_tool", `{}`),
				},
			},
		},
		{Message: workspace.ChatMessage{Role: "assistant", Content: "Done."}},
	}}

	result, err := workspace.RunAgent(context.Background(), workspace.AgentConfig{
		Root: s.root, Client: client,
	}, workspace.AgentRequest{RunID: "tools", Instruction: "Inspect the workspace."})
	s.Require().NoError(err)
	s.True(result.Validation.Valid, result.Validation.String())
	s.Equal(8, result.Audit.ToolCalls)
	s.NoFileExists(filepath.Join(s.root, "wiki/topics/moved.md"))
	s.Require().Len(client.requests, 2)

	toolMessages := client.requests[1].Messages
	var joined string
	for _, message := range toolMessages {
		if message.Role == "tool" {
			joined += message.Content
		}
	}
	s.Contains(joined, "AGENTS.md")
	s.Contains(joined, "sources/")
	s.Contains(joined, "read_file requires a file")
	s.Contains(joined, "unknown tool")
}

type scriptedChatClient struct {
	responses []workspace.ChatResponse
	requests  []workspace.ChatRequest
	err       error
}

func (c *scriptedChatClient) Complete(
	_ context.Context,
	request workspace.ChatRequest,
) (workspace.ChatResponse, error) {
	c.requests = append(c.requests, request)
	if c.err != nil {
		return workspace.ChatResponse{}, c.err
	}
	response := c.responses[0]
	c.responses = c.responses[1:]
	return response, nil
}

func call(name, arguments string) workspace.ToolCall {
	return workspace.ToolCall{
		ID:   "call-" + name,
		Type: "function",
		Function: workspace.ToolFunction{
			Name: name, Arguments: arguments,
		},
	}
}

func mustArguments(value map[string]string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func monotonicClock(start time.Time) func() time.Time {
	current := start.Add(-time.Millisecond)
	return func() time.Time {
		current = current.Add(time.Millisecond)
		return current
	}
}
