package llmwiki

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/stretchr/testify/suite"
)

type AgentBuilderSuite struct {
	suite.Suite
	ctx   context.Context
	store *knowledgeeval.ArtifactStore
	now   func() time.Time
}

func TestAgentBuilderSuite(t *testing.T) {
	suite.Run(t, new(AgentBuilderSuite))
}

func (s *AgentBuilderSuite) SetupTest() {
	s.ctx = context.Background()
	var err error
	storeRoot := s.T().TempDir()
	s.T().Cleanup(func() {
		s.Require().NoError(filepath.WalkDir(
			storeRoot,
			func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					return os.Chmod(path, 0o755)
				}
				return nil
			},
		))
	})
	s.store, err = knowledgeeval.NewArtifactStore(storeRoot)
	s.Require().NoError(err)
	current := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		value := current
		current = current.Add(time.Millisecond)
		return value
	}
}

func (s *AgentBuilderSuite) TestRunsMaintainerAndRecordsProvenance() {
	request := s.buildRequest()
	client := &builderChatClient{responses: []llm.ChatResponse{{
		Message: llm.ChatMessage{Role: "assistant", Content: "Wiki is current."},
		Usage:   llm.TokenUsage{InputTokens: 12, OutputTokens: 3},
	}}}
	builder, err := NewAgentBuilder(s.store, s.now, "revision-2", AgentBuilderConfig{
		Model: "deepseek-v4-pro", MaxRounds: 3, Client: client,
	})
	s.Require().NoError(err)
	artifact, err := builder.Build(s.ctx, request)
	s.Require().NoError(err)
	s.Equal("llmwiki-maintainer", artifact.Provenance.BuilderID)
	s.Equal("deepseek-v4-pro", artifact.Provenance.Metadata["model"])
	s.Equal("12", artifact.Provenance.Metadata["input_tokens"])
	s.Equal("true", artifact.Provenance.Metadata["validation"])
	s.NotEmpty(artifact.Provenance.ConfigDigest)
	s.Require().Len(client.requests, 1)

	driver, err := NewDriver(s.store, s.now)
	s.Require().NoError(err)
	_, err = driver.Open(s.ctx, artifact)
	s.Require().NoError(err)
}

func (s *AgentBuilderSuite) TestPreservesFailedMaintainerWorkspace() {
	request := s.buildRequest()
	client := &builderChatClient{err: errors.New("provider unavailable")}
	builder, err := NewAgentBuilder(s.store, s.now, "revision-2", AgentBuilderConfig{
		Client: client,
	})
	s.Require().NoError(err)
	_, err = builder.Build(s.ctx, request)
	var buildErr *AgentBuildError
	s.Require().ErrorAs(err, &buildErr)
	s.Equal("world-group-checkpoint", buildErr.RunID)
	s.Contains(buildErr.Error(), "provider unavailable")
	s.Require().NoError(buildErr.FailurePayload.Validate())
	root, openErr := s.store.OpenDirectory(s.ctx, buildErr.FailurePayload)
	s.Require().NoError(openErr)
	s.FileExists(root + "/.pax/runs/world-group-checkpoint.json")

	resumer, resumeErr := NewAgentBuilder(
		s.store,
		s.now,
		"revision-2",
		AgentBuilderConfig{Resume: &buildErr.FailurePayload},
	)
	s.Require().NoError(resumeErr)
	artifact, resumeErr := resumer.Build(s.ctx, request)
	s.Require().NoError(resumeErr)
	s.Equal(
		buildErr.FailurePayload.SHA256,
		artifact.Provenance.Metadata["continued_from_failure_sha256"],
	)
	s.Contains(artifact.Provenance.Metadata["initial_failure"], "provider unavailable")
}

func (s *AgentBuilderSuite) TestContinuesInvalidFailureWorkspaceWithValidationFeedback() {
	request := s.buildRequest()
	broken := "[Missing](pages/missing.md)"
	initialCall := llm.ToolCall{
		ID: "break-link", Type: "function", Function: llm.ToolFunction{
			Name: "replace_text",
			Arguments: `{"path":"wiki/index.md","old_text":"The maintained Wiki uses durable citations.",` +
				`"new_text":"` + broken + `"}`,
		},
	}
	initialResponse := llm.ChatResponse{
		Message: llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{initialCall}},
	}
	initialClient := &builderChatClient{responses: []llm.ChatResponse{initialResponse}}
	initial, err := NewAgentBuilder(s.store, s.now, "revision-2", AgentBuilderConfig{
		Client: initialClient, MaxRounds: 1,
	})
	s.Require().NoError(err)
	_, err = initial.Build(s.ctx, request)
	var buildErr *AgentBuildError
	s.Require().ErrorAs(err, &buildErr)
	var roundLimitErr *workspace.RoundLimitError
	s.Require().ErrorAs(err, &roundLimitErr)
	s.Contains(buildErr.Error(), "broken internal link")

	repairCall := llm.ToolCall{
		ID: "repair-link", Type: "function", Function: llm.ToolFunction{
			Name: "replace_text",
			Arguments: `{"path":"wiki/index.md","old_text":"` + broken + `",` +
				`"new_text":"The maintained Wiki uses durable citations."}`,
		},
	}
	repairResponse := llm.ChatResponse{
		Message: llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{repairCall}},
	}
	repairClient := &builderChatClient{responses: []llm.ChatResponse{repairResponse}}
	resumer, err := NewAgentBuilder(s.store, s.now, "revision-2", AgentBuilderConfig{
		Client: repairClient, MaxRounds: 3, Resume: &buildErr.FailurePayload,
	})
	s.Require().NoError(err)
	artifact, err := resumer.Build(s.ctx, request)
	s.Require().NoError(err)
	s.Equal(buildErr.FailurePayload.SHA256, artifact.Provenance.Metadata["continued_from_failure_sha256"])
	s.Contains(artifact.Provenance.Metadata["initial_failure"], "broken internal link")
	s.Contains(artifact.Provenance.Metadata["continuation_run_id"], "-continue-")
	s.Require().Len(repairClient.requests, 1)
}

func (s *AgentBuilderSuite) TestValidatesDependencies() {
	_, err := NewAgentBuilder(nil, nil, "", AgentBuilderConfig{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
	_, err = NewAgentBuilder(s.store, nil, "", AgentBuilderConfig{})
	s.Require().ErrorIs(err, knowledgeeval.ErrInvalidRecord)
}

func (s *AgentBuilderSuite) buildRequest() knowledgeeval.BuildRequest {
	driverSuite := &DriverSuite{
		Suite: suite.Suite{}, ctx: s.ctx, store: s.store, now: s.now,
	}
	driverSuite.SetT(s.T())
	root := driverSuite.validWorkspace("The maintained Wiki uses durable citations.")
	input, err := s.store.PutDirectory(s.ctx, "benchmark-build-input", "v1", root)
	s.Require().NoError(err)
	hidden, err := s.store.PutBytes(s.ctx, "benchmark-eval-input", "v1", []byte("{}"))
	s.Require().NoError(err)
	return knowledgeeval.BuildRequest{Group: knowledgeeval.BenchmarkGroup{
		GroupID: "world/group", WorldID: "world", CheckpointID: "checkpoint",
		BuildInput: input, EvaluationInput: hidden,
	}}
}

type builderChatClient struct {
	responses []llm.ChatResponse
	requests  []llm.ChatRequest
	err       error
}

func (c *builderChatClient) Complete(
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
