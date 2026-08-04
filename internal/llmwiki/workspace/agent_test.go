package workspace_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
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
	s.Require().NoError(os.Remove(
		filepath.Join(s.root, ".pax", "editorial-profile"),
	))
}

func (s *agentSuite) TestAgentUsesFilesystemToolsAndRecordsUsage() {
	page := "# Local-first Wiki\n\nSources are immutable " +
		"([source](../../" + s.sourcePath + "#" + s.anchor + ")).\n"
	client := &scriptedChatClient{responses: []llm.ChatResponse{
		{
			Message: llm.ChatMessage{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					call("read_file", `{"path":"AGENTS.md"}`),
					call("read_file", `{"path":"`+s.sourcePath+`"}`),
					call("write_file", `{"path":"wiki/index.md","content":"# Wiki\n\n- [Local-first](pages/local-first.md)\n"}`),
					call("write_file", mustArguments(map[string]string{
						"path": "wiki/pages/local-first.md", "content": page,
					})),
					call("validate", `{}`),
				},
			},
			Usage: llm.TokenUsage{InputTokens: 120, OutputTokens: 80},
		},
		{
			Message: llm.ChatMessage{Role: "assistant", Content: "Done."},
			Usage:   llm.TokenUsage{InputTokens: 30, OutputTokens: 5},
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
	s.Contains(
		client.requests[0].Messages[0].Content,
		"Write all generated Wiki",
	)
	s.Contains(client.requests[0].Messages[0].Content, "prose in English")

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
	client := &scriptedChatClient{responses: []llm.ChatResponse{
		{
			Message: llm.ChatMessage{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					call("write_file", `{"path":"`+s.sourcePath+`","content":"tampered"}`),
				},
			},
		},
		{
			Message: llm.ChatMessage{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					call("write_file", `{"path":"wiki/index.md","content":"# Wiki\n"}`),
				},
			},
		},
		{Message: llm.ChatMessage{Role: "assistant", Content: "Done."}},
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
	client := &scriptedChatClient{responses: []llm.ChatResponse{
		{
			Message: llm.ChatMessage{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					call("write_file", `{"path":"wiki/index.md","content":"# Wiki\n\n- [Missing](pages/missing.md)\n"}`),
				},
			},
		},
		{Message: llm.ChatMessage{Role: "assistant", Content: "Done."}},
		{
			Message: llm.ChatMessage{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					call("write_file", `{"path":"wiki/index.md","content":"# Wiki\n"}`),
				},
			},
		},
		{Message: llm.ChatMessage{Role: "assistant", Content: "Repaired."}},
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

func (s *agentSuite) TestWriteToolRejectsMalformedCitationBeforeReplacingPage() {
	valid := "# Cited\n\nA grounded fact ([source](../../" +
		s.sourcePath + "#" + s.anchor + ")).\n"
	malformed := "# Cited\n\nA broken fact [source](../../" +
		s.sourcePath + "#" + s.anchor + "].\n"
	client := &scriptedChatClient{responses: []llm.ChatResponse{
		{
			Message: llm.ChatMessage{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					call("write_file", `{"path":"wiki/index.md","content":"# Wiki\n\n- [Cited](pages/cited.md)\n"}`),
					call("write_file", mustArguments(map[string]string{
						"path": "wiki/pages/cited.md", "content": valid,
					})),
					call("write_file", mustArguments(map[string]string{
						"path": "wiki/pages/cited.md", "content": malformed,
					})),
				},
			},
		},
		{Message: llm.ChatMessage{Role: "assistant", Content: "Done."}},
		{
			Message: llm.ChatMessage{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					call("write_file", mustArguments(map[string]string{
						"path": "wiki/pages/cited.md", "content": valid,
					})),
				},
			},
		},
		{Message: llm.ChatMessage{Role: "assistant", Content: "Repaired."}},
	}}

	result, err := workspace.RunAgent(context.Background(), workspace.AgentConfig{
		Root: s.root, Client: client,
	}, workspace.AgentRequest{
		RunID: "citation-preflight", Instruction: "Maintain Wiki.",
	})
	s.Require().NoError(err)
	s.True(result.Validation.Valid, result.Validation.String())
	s.Require().Len(client.requests, 2)

	var toolResults string
	for _, message := range client.requests[1].Messages {
		if message.Role == "tool" {
			toolResults += message.Content
		}
	}
	s.Contains(toolResults, "malformed source citation")
	rendered, err := os.ReadFile(filepath.Join(s.root, "wiki/pages/cited.md"))
	s.Require().NoError(err)
	s.Equal(valid, string(rendered))
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
	client := &scriptedChatClient{responses: []llm.ChatResponse{
		{
			Message: llm.ChatMessage{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					call("list_files", `{}`),
					call("grep", `{"query":"immutable","path":"sources"}`),
					call("write_file", `{"path":"wiki/pages/draft.md","content":"# Draft\n"}`),
					call("replace_text", `{"path":"wiki/pages/draft.md","old_text":"# Draft","new_text":"# Edited draft"}`),
					call("move_file", `{"from":"wiki/pages/draft.md","to":"wiki/topics/moved.md"}`),
					call("read_file", `{"path":"wiki/topics/moved.md"}`),
					call("delete_file", `{"path":"wiki/topics/moved.md"}`),
					call("read_file", `{"path":"."}`),
					call("unknown_tool", `{}`),
				},
			},
		},
		{Message: llm.ChatMessage{Role: "assistant", Content: "Done."}},
	}}

	result, err := workspace.RunAgent(context.Background(), workspace.AgentConfig{
		Root: s.root, Client: client,
	}, workspace.AgentRequest{RunID: "tools", Instruction: "Inspect the workspace."})
	s.Require().NoError(err)
	s.True(result.Validation.Valid, result.Validation.String())
	s.Equal(9, result.Audit.ToolCalls)
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

func (s *agentSuite) TestRepairModeValidatesAfterEveryToolRound() {
	client := &scriptedChatClient{responses: []llm.ChatResponse{
		{
			Message: llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{
				call("write_file", `{"path":"wiki/index.md","content":"# Wiki\n\n[Broken](pages/missing.md)\n"}`),
			}},
		},
		{
			Message: llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{
				call("replace_text", `{"path":"wiki/index.md","old_text":"[Broken](pages/missing.md)","new_text":"Sources are ready for future articles."}`),
			}},
		},
	}}

	result, err := workspace.RunAgent(context.Background(), workspace.AgentConfig{
		Root: s.root, Client: client, MaxRounds: 2,
		ValidationAfterToolRound: true,
	}, workspace.AgentRequest{RunID: "repair-feedback", Instruction: "Repair the Wiki."})
	s.Require().NoError(err)
	s.True(result.Validation.Valid, result.Validation.String())
	s.Require().Len(client.requests, 2)
	s.Contains(client.requests[1].Messages[len(client.requests[1].Messages)-1].Content, "broken internal link")
}

func (s *agentSuite) TestPreciseEditPreservesPageAndInvalidEditIsRejected() {
	original := "# Durable page\n\n" + strings.Repeat(
		"Established knowledge remains available while one sentence changes. ",
		8,
	) + "\n"
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.root, "wiki/pages/durable.md"),
		[]byte(original),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.root, "wiki/index.md"),
		[]byte("# Wiki\n\n[Durable](pages/durable.md)\n"),
		0o644,
	))
	client := &scriptedChatClient{responses: []llm.ChatResponse{
		{
			Message: llm.ChatMessage{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					call("write_file", `{"path":"wiki/pages/durable.md","content":"# Lost\n"}`),
					call("replace_text", `{"path":"wiki/pages/durable.md","old_text":"one sentence changes","new_text":"one precise sentence changes","expected_occurrences":8}`),
					call("replace_text", `{"path":"wiki/pages/durable.md","old_text":"not present","new_text":"invented"}`),
				},
			},
		},
		{Message: llm.ChatMessage{Role: "assistant", Content: "Done."}},
	}}

	result, err := workspace.RunAgent(context.Background(), workspace.AgentConfig{
		Root: s.root, Client: client,
	}, workspace.AgentRequest{RunID: "precise-edit", Instruction: "Update the page."})
	s.Require().NoError(err)
	s.True(result.Validation.Valid, result.Validation.String())

	rendered, err := os.ReadFile(filepath.Join(s.root, "wiki/pages/durable.md"))
	s.Require().NoError(err)
	s.Contains(string(rendered), "one precise sentence changes")
	s.NotContains(string(rendered), "# Lost")
	s.NotContains(string(rendered), "invented")

	var joined string
	for _, message := range client.requests[1].Messages {
		if message.Role == "tool" {
			joined += message.Content
		}
	}
	s.Contains(joined, "use replace_text")
	s.Contains(joined, "found 0 occurrences")
}

func (s *agentSuite) TestRejectsInvalidAgentConfigurationAndToolInputs() {
	tests := []struct {
		name    string
		config  workspace.AgentConfig
		request workspace.AgentRequest
		message string
	}{
		{
			name: "root", config: workspace.AgentConfig{Client: &scriptedChatClient{}},
			request: workspace.AgentRequest{RunID: "run", Instruction: "work"},
			message: "root",
		},
		{
			name: "client", config: workspace.AgentConfig{Root: s.root},
			request: workspace.AgentRequest{RunID: "run", Instruction: "work"},
			message: "client",
		},
		{
			name: "run ID", config: workspace.AgentConfig{
				Root: s.root, Client: &scriptedChatClient{},
			},
			request: workspace.AgentRequest{RunID: "../bad", Instruction: "work"},
			message: "run ID",
		},
		{
			name: "instruction", config: workspace.AgentConfig{
				Root: s.root, Client: &scriptedChatClient{},
			},
			request: workspace.AgentRequest{RunID: "run"},
			message: "instruction",
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := workspace.RunAgent(
				context.Background(), test.config, test.request,
			)
			s.Require().ErrorContains(err, test.message)
		})
	}

	large := strings.Repeat("x", 2<<20+1)
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.root, "wiki/log.md"), []byte(large), 0o644,
	))
	outside := s.T().TempDir()
	s.Require().NoError(os.Symlink(outside, filepath.Join(s.root, "wiki/link")))
	client := &scriptedChatClient{responses: []llm.ChatResponse{
		{
			Message: llm.ChatMessage{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					call("grep", `{"query":""}`),
					call("read_file", `{"path":"wiki/log.md"}`),
					call("write_file", `{"path":"/tmp/escape.md","content":"x"}`),
					call("write_file", `{"path":"wiki/link/escape.md","content":"x"}`),
					call("move_file", `{"from":"wiki/missing.md","to":"wiki/new.md"}`),
					call("delete_file", `{"path":"wiki/missing.md"}`),
					call("list_files", `{"path":"../"}`),
					call("read_file", `{bad json}`),
				},
			},
		},
		{Message: llm.ChatMessage{Role: "assistant", Content: "Done."}},
	}}
	result, err := workspace.RunAgent(context.Background(), workspace.AgentConfig{
		Root: s.root, Client: client,
	}, workspace.AgentRequest{RunID: "tool-errors", Instruction: "Inspect."})
	s.Require().NoError(err)
	s.True(result.Validation.Valid, result.Validation.String())
	var joined string
	for _, message := range client.requests[1].Messages {
		if message.Role == "tool" {
			joined += message.Content
		}
	}
	s.Contains(joined, "grep query is required")
	s.Contains(joined, "exceeds 2 MiB")
	s.Contains(joined, "absolute tool paths")
	s.Contains(joined, "symlinks are forbidden")
	s.Contains(joined, "move wiki file")
	s.Contains(joined, "delete wiki file")
	s.Contains(joined, "escapes workspace")
	s.Contains(joined, "decode tool arguments")
}

// TestGrepHonorsReadAllowList proves grep over "." surfaces nothing from
// .git/ or .pax/runs/: search must honor exactly the same allow-list as
// read_file instead of walking everything under the resolved path.
func (s *agentSuite) TestGrepHonorsReadAllowList() {
	// A real Git checkout gives the workspace a .git directory with a HEAD,
	// so the validator's Git change budget stays green.
	store := filepath.Join(s.T().TempDir(), "wiki.git")
	_, err := workspace.InitStore(context.Background(), store, s.root)
	s.Require().NoError(err)
	root := filepath.Join(s.T().TempDir(), "checkout")
	s.Require().NoError(workspace.Checkout(context.Background(), store, root))
	s.T().Cleanup(func() {
		err := os.Chmod(filepath.Join(root, "sources"), 0o755)
		if err != nil && !os.IsNotExist(err) {
			s.Require().NoError(err)
		}
	})

	const marker = "grep-leak-marker"
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, ".git", "leak.txt"), []byte(marker+" in git internals\n"), 0o644,
	))
	s.Require().NoError(os.MkdirAll(filepath.Join(root, ".pax", "runs"), 0o755))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, ".pax", "runs", "previous.json"), []byte(`{"note":"`+marker+`"}`), 0o644,
	))
	s.Require().NoError(os.MkdirAll(filepath.Join(root, "wiki", "pages"), 0o755))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, "wiki", "pages", "durable.md"),
		[]byte("# Durable page\n\nEstablished "+marker+" knowledge stays readable.\n"),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, "wiki", "index.md"),
		[]byte("# Wiki\n\n[Durable](pages/durable.md)\n"),
		0o644,
	))

	client := &scriptedChatClient{responses: []llm.ChatResponse{
		{
			Message: llm.ChatMessage{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					call("grep", `{"query":"`+marker+`","path":"."}`),
				},
			},
		},
		{Message: llm.ChatMessage{Role: "assistant", Content: "Done."}},
	}}
	result, err := workspace.RunAgent(context.Background(), workspace.AgentConfig{
		Root: root, Client: client,
	}, workspace.AgentRequest{RunID: "grep-allowlist", Instruction: "Search the workspace."})
	s.Require().NoError(err)
	s.True(result.Validation.Valid, result.Validation.String())

	s.Require().Len(client.requests, 2)
	var joined string
	for _, message := range client.requests[1].Messages {
		if message.Role == "tool" {
			joined += message.Content
		}
	}
	s.Contains(joined, "wiki/pages/durable.md")
	s.NotContains(joined, ".git")
	s.NotContains(joined, ".pax/runs")
	s.NotContains(joined, "git internals")
}

// TestMoveRefusesToOverwriteExistingDestination proves move_file cannot
// clobber an existing page (which would sidestep write_file's replace_text
// guard), while a move to a fresh destination still succeeds.
func (s *agentSuite) TestMoveRefusesToOverwriteExistingDestination() {
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.root, "wiki", "pages", "one.md"),
		[]byte("# Page one\n\nContent one.\n"),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.root, "wiki", "pages", "two.md"),
		[]byte("# Page two\n\nContent two.\n"),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.root, "wiki", "index.md"),
		[]byte("# Wiki\n\n[One](pages/renamed.md)\n[Two](pages/two.md)\n"),
		0o644,
	))
	client := &scriptedChatClient{responses: []llm.ChatResponse{
		{
			Message: llm.ChatMessage{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					call("move_file", `{"from":"wiki/pages/one.md","to":"wiki/pages/two.md"}`),
					call("move_file", `{"from":"wiki/pages/one.md","to":"wiki/pages/renamed.md"}`),
				},
			},
		},
		{Message: llm.ChatMessage{Role: "assistant", Content: "Done."}},
	}}

	result, err := workspace.RunAgent(context.Background(), workspace.AgentConfig{
		Root: s.root, Client: client,
	}, workspace.AgentRequest{RunID: "move-guard", Instruction: "Reorganize the Wiki."})
	s.Require().NoError(err)
	s.True(result.Validation.Valid, result.Validation.String())

	// The refused overwrite left page two intact; the allowed move landed.
	two, err := os.ReadFile(filepath.Join(s.root, "wiki", "pages", "two.md"))
	s.Require().NoError(err)
	s.Equal("# Page two\n\nContent two.\n", string(two))
	renamed, err := os.ReadFile(filepath.Join(s.root, "wiki", "pages", "renamed.md"))
	s.Require().NoError(err)
	s.Equal("# Page one\n\nContent one.\n", string(renamed))
	s.NoFileExists(filepath.Join(s.root, "wiki", "pages", "one.md"))

	var joined string
	for _, message := range client.requests[1].Messages {
		if message.Role == "tool" {
			joined += message.Content
		}
	}
	s.Contains(joined, "move destination already exists")
}

type scriptedChatClient struct {
	responses []llm.ChatResponse
	requests  []llm.ChatRequest
	err       error
}

func (c *scriptedChatClient) Complete(
	_ context.Context,
	request llm.ChatRequest,
) (llm.ChatResponse, error) {
	c.requests = append(c.requests, request)
	if c.err != nil {
		return llm.ChatResponse{}, c.err
	}
	response := c.responses[0]
	c.responses = c.responses[1:]
	return response, nil
}

func call(name, arguments string) llm.ToolCall {
	return llm.ToolCall{
		ID:   "call-" + name,
		Type: "function",
		Function: llm.ToolFunction{
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
