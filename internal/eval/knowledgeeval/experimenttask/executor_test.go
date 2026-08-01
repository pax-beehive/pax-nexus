package experimenttask

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/demo"
	"github.com/stretchr/testify/suite"
)

type executorSuite struct {
	suite.Suite
	prepared string
	results  string
}

func TestSessionExecutor(t *testing.T) {
	suite.Run(t, new(executorSuite))
}

func (s *executorSuite) SetupTest() {
	s.prepared = s.T().TempDir()
	s.results = s.T().TempDir()
	s.T().Cleanup(func() {
		s.Require().NoError(filepath.Walk(s.results, func(
			path string,
			info os.FileInfo,
			walkErr error,
		) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				return os.Chmod(path, 0o755)
			}
			return os.Chmod(path, 0o644)
		}))
	})
	base := filepath.Join(s.prepared, "train", "locomo")
	s.Require().NoError(os.MkdirAll(filepath.Join(base, "maintainer"), 0o755))
	s.Require().NoError(os.MkdirAll(filepath.Join(base, "reader"), 0o755))
	s.Require().NoError(os.MkdirAll(filepath.Join(base, "evaluator"), 0o755))
	s.Require().NoError(os.WriteFile(
		filepath.Join(base, "maintainer", "ingest.jsonl"),
		[]byte(
			`{"schema_version":"pax-session-dataset/v1","case_id":"conv-26",`+
				`"source_kind":"long-running-conversation",`+
				`"participants":["A","B"],`+
				`"sessions":[{"session_id":"session-1","turns":[`+
				`{"dia_id":"D1:1","speaker":"A","text":"The code is cedar."}]}]}`+"\n",
		),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(base, "reader", "query.jsonl"),
		[]byte(
			`{"case_id":"question-1","source_case_id":"conv-26",`+
				`"question":"What is the code?"}`+"\n",
		),
		0o644,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(base, "evaluator", "gold.jsonl"),
		[]byte(
			`{"case_id":"question-1","answer":"cedar",`+
				`"evidence_dialog_ids":["D1:1"]}`+"\n",
		),
		0o644,
	))
}

func (s *executorSuite) TestRunsBaselineWithoutLLMCredentials() {
	executor, err := NewSessionExecutor(SessionExecutorConfig{
		PreparedRoot: s.prepared,
		ResultRoot:   s.results,
	})
	s.Require().NoError(err)
	result, err := executor.Execute(context.Background(), "task-1", Request{
		Dataset: "locomo", Partition: "train", GroupID: "conv-26",
		Mode: ModeBaseline, QuestionLimit: 1,
	})
	s.Require().NoError(err)
	s.Equal([]string{"run-task-1-locomo-conv-26-source-only"}, result.RunIDs)
	s.Require().Len(result.ArtifactIDs, 1)
	s.Equal("tasks/task-1", result.ResultPath)
	s.Require().FileExists(filepath.Join(
		s.results,
		"tasks",
		"task-1",
		"dataset-run.json",
	))
}

func (s *executorSuite) TestResolvesCompletedMaintainedArtifactForReuse() {
	executor, err := NewSessionExecutor(SessionExecutorConfig{
		PreparedRoot: s.prepared,
		ResultRoot:   s.results,
	})
	s.Require().NoError(err)
	_, err = executor.Execute(context.Background(), "source-task", Request{
		Dataset: "locomo", Partition: "train", GroupID: "conv-26",
		Mode: ModeBaseline, QuestionLimit: 1,
	})
	s.Require().NoError(err)
	bundlePath := filepath.Join(s.results, "tasks", "source-task", "dataset-run.json")
	encoded, err := os.ReadFile(bundlePath)
	s.Require().NoError(err)
	var bundle demo.SessionDatasetBundle
	s.Require().NoError(json.Unmarshal(encoded, &bundle))
	s.Require().Len(bundle.Arms, 1)
	bundle.Arms[0].ID = "maintained"
	bundle.Arms[0].Role = "candidate"
	encoded, err = json.Marshal(bundle)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(bundlePath, encoded, 0o644))

	artifact, root, err := executor.reusableArtifact("source-task")
	s.Require().NoError(err)
	s.NotEmpty(artifact.ArtifactID)
	s.DirExists(root)

	_, _, err = executor.reusableArtifact("missing-task")
	s.Require().ErrorContains(err, "read reused artifact task bundle")

	invalidTaskRoot := filepath.Join(s.results, "tasks", "invalid-task")
	s.Require().NoError(os.MkdirAll(invalidTaskRoot, 0o755))
	s.Require().NoError(os.WriteFile(
		filepath.Join(invalidTaskRoot, "dataset-run.json"),
		[]byte("not-json"),
		0o644,
	))
	_, _, err = executor.reusableArtifact("invalid-task")
	s.Require().ErrorContains(err, "decode reused artifact task bundle")

	emptyTaskRoot := filepath.Join(s.results, "tasks", "empty-reuse-task")
	s.Require().NoError(os.MkdirAll(emptyTaskRoot, 0o755))
	s.Require().NoError(os.WriteFile(
		filepath.Join(emptyTaskRoot, "dataset-run.json"),
		[]byte(`{"arms":[]}`),
		0o644,
	))
	_, _, err = executor.reusableArtifact("empty-reuse-task")
	s.Require().ErrorIs(err, knowledgeeval.ErrNotFound)
}

func (s *executorSuite) TestRejectsUnsafeOrMissingArtifactReuseTask() {
	executor, err := NewSessionExecutor(SessionExecutorConfig{
		PreparedRoot: s.prepared,
		ResultRoot:   s.results,
		APIKey:       "configured-for-resolution-only",
	})
	s.Require().NoError(err)
	request := Request{
		Dataset: "locomo", Partition: "train", GroupID: "conv-26",
		Mode: ModeMaintainer, Model: "model", ReaderModel: "reader",
		QuestionLimit: 1, QuestionOffset: 1,
	}
	request.ReuseArtifactFromTaskID = "../unsafe"
	_, err = executor.Execute(context.Background(), "task-reuse", request)
	s.Require().ErrorContains(err, "invalid reused artifact task ID")

	request.ReuseArtifactFromTaskID = "missing-task"
	_, err = executor.Execute(context.Background(), "task-reuse", request)
	s.Require().ErrorContains(err, "read reused artifact task bundle")
}

func (s *executorSuite) TestRejectsUnsafePathsAndMissingPaidConfiguration() {
	executor, err := NewSessionExecutor(SessionExecutorConfig{
		PreparedRoot: s.prepared,
		ResultRoot:   s.results,
	})
	s.Require().NoError(err)
	_, err = executor.Execute(context.Background(), "../task", baselineRequest())
	s.Require().Error(err)
	_, err = executor.Execute(context.Background(), "task-2", Request{
		Dataset: "locomo", Partition: "train", GroupID: "conv-26",
		Mode: ModeMaintainer, Model: "model", ReaderModel: "reader", QuestionLimit: 1,
	})
	s.Require().ErrorContains(err, "DEEPSEEK_API_KEY")

	s.T().Setenv("DEEPSEEK_API_KEY", "")
	s.False(APIKeyConfigured())
	s.T().Setenv("DEEPSEEK_API_KEY", "configured")
	s.True(APIKeyConfigured())
}

func (s *executorSuite) TestConfiguresSemanticJudgeForPaidRuns() {
	executor, err := NewSessionExecutor(SessionExecutorConfig{
		PreparedRoot: s.prepared,
		ResultRoot:   s.results,
		APIKey:       "configured-for-construction",
	})
	s.Require().NoError(err)
	config := demo.SessionDatasetConfig{}
	err = executor.configureMaintainer(&config, Request{
		Mode: ModeMaintainer, Model: "maintainer-model", ReaderModel: "judge-model",
	})
	s.Require().NoError(err)
	s.Require().NotNil(config.Reader)
	s.Require().NotNil(config.Judge)
	s.Equal("chat-reader:v1:judge-model", config.Reader.ID())
	s.Equal("semantic-answer-judge:v1:judge-model", config.Judge.ID())
}

func (s *executorSuite) TestValidatesMaintainerBundleStatus() {
	tests := []struct {
		name    string
		request Request
		bundle  demo.SessionDatasetBundle
		errText string
		errIs   error
	}{
		{
			name:    "maintainer reports round limit as continuable",
			request: Request{Mode: ModeMaintainer},
			bundle: demo.SessionDatasetBundle{
				BuildStatus: StatusNeedsMoreRounds,
				Blocker:     "agent exhausted its configured rounds",
			},
			errText: "agent exhausted its configured rounds",
			errIs:   ErrRoundLimitReached,
		},
		{
			name:    "baseline accepts baseline-only bundle",
			request: Request{Mode: ModeBaseline},
			bundle:  demo.SessionDatasetBundle{BuildStatus: "baseline_only"},
		},
		{
			name:    "maintainer accepts completed bundle",
			request: Request{Mode: ModeMaintainer},
			bundle:  demo.SessionDatasetBundle{BuildStatus: "completed"},
		},
		{
			name:    "maintainer rejects partial bundle with blocker",
			request: Request{Mode: ModeMaintainer},
			bundle: demo.SessionDatasetBundle{
				BuildStatus: "partial",
				Blocker:     "provider request was canceled",
			},
			errText: "maintainer bundle is partial: provider request was canceled",
		},
		{
			name:    "maintainer supplies missing blocker",
			request: Request{Mode: ModeMaintainer},
			bundle:  demo.SessionDatasetBundle{BuildStatus: "partial"},
			errText: "maintainer arm did not complete",
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			err := validateExecutionBundle(test.request, test.bundle)
			if test.errText == "" {
				s.NoError(err)
				return
			}
			s.Require().ErrorContains(err, test.errText)
			if test.errIs != nil {
				s.ErrorIs(err, test.errIs)
			}
		})
	}
}

func (s *executorSuite) TestResolvesPersistedMaintainerFailureWorkspace() {
	executor, err := NewSessionExecutor(SessionExecutorConfig{
		PreparedRoot: s.prepared,
		ResultRoot:   s.results,
	})
	s.Require().NoError(err)
	digest := strings.Repeat("a", 64)
	root := filepath.Join(s.results, "tasks", "failed-task", "artifacts", digest, "tree")
	s.Require().NoError(os.MkdirAll(root, 0o755))
	bundle := demo.SessionDatasetBundle{Arms: []demo.SessionDatasetArm{{
		ID: "maintained", BuildStatus: "failed",
		FailurePayload: &knowledgeeval.OpaqueRef{
			Kind:          "llmwiki-maintainer-failure",
			SchemaVersion: "pax.knowledge-eval.llmwiki-maintainer-failure.v1",
			URI:           "artifact://sha256/" + digest, SHA256: digest,
		},
	}}}
	encoded, err := json.Marshal(bundle)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.results, "tasks", "failed-task", "dataset-run.json"),
		encoded,
		0o644,
	))

	resolved, err := executor.failureWorkspacePath("failed-task")
	s.Require().NoError(err)
	s.Equal(root, resolved)

	_, err = executor.failureWorkspacePath("missing-task")
	s.Require().ErrorContains(err, "read resume task bundle")
	emptyTaskRoot := filepath.Join(s.results, "tasks", "empty-task")
	s.Require().NoError(os.MkdirAll(emptyTaskRoot, 0o755))
	s.Require().NoError(os.WriteFile(
		filepath.Join(emptyTaskRoot, "dataset-run.json"),
		[]byte(`{"arms":[]}`),
		0o644,
	))
	_, err = executor.failureWorkspacePath("empty-task")
	s.Require().ErrorContains(err, "has no maintained failure payload")
}
