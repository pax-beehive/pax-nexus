package experimenttask

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/dashboard"
)

const (
	ModeBaseline   = "baseline"
	ModeMaintainer = "maintainer"

	StatusQueued          = "queued"
	StatusRunning         = "running"
	StatusCompleted       = "completed"
	StatusFailed          = "failed"
	StatusCancelled       = "cancelled"
	StatusNeedsMoreRounds = "needs_more_rounds"
)

var ErrConflict = errors.New("knowledge eval task conflict")
var ErrRoundLimitReached = errors.New("knowledge eval task round limit reached")

type Request struct {
	Dataset                 string `json:"dataset"`
	Partition               string `json:"partition"`
	GroupID                 string `json:"group_id"`
	Mode                    string `json:"mode"`
	Model                   string `json:"model,omitempty"`
	ReaderModel             string `json:"reader_model,omitempty"`
	QuestionLimit           int    `json:"question_limit"`
	QuestionOffset          int    `json:"question_offset,omitempty"`
	MaxRounds               int    `json:"max_rounds"`
	ConfirmPaid             bool   `json:"confirm_paid,omitempty"`
	ContinueFromTaskID      string `json:"continue_from_task_id,omitempty"`
	ReuseArtifactFromTaskID string `json:"reuse_artifact_from_task_id,omitempty"`
}

type Preview struct {
	Eligible            bool     `json:"eligible"`
	IneligibleReason    string   `json:"ineligible_reason,omitempty"`
	Paid                bool     `json:"paid"`
	LLMConfigured       bool     `json:"llm_configured"`
	Dataset             string   `json:"dataset"`
	Partition           string   `json:"partition"`
	GroupID             string   `json:"group_id"`
	SourceKind          string   `json:"source_kind"`
	AvailableQuestions  int      `json:"available_questions"`
	SelectedQuestions   int      `json:"selected_questions"`
	CumulativeQuestions int      `json:"cumulative_questions"`
	PlannedRuns         int      `json:"planned_runs"`
	MaxLLMCalls         int      `json:"max_llm_calls"`
	Benchmarks          []string `json:"benchmarks"`
	IncludesSourceOnly  bool     `json:"includes_source_only"`
	IncludesMaintainer  bool     `json:"includes_maintainer"`
}

type Event struct {
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type Task struct {
	ID                    string     `json:"id"`
	Request               Request    `json:"request"`
	Preview               Preview    `json:"preview"`
	Status                string     `json:"status"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	StartedAt             *time.Time `json:"started_at,omitempty"`
	CompletedAt           *time.Time `json:"completed_at,omitempty"`
	CancellationRequested bool       `json:"cancellation_requested,omitempty"`
	Error                 string     `json:"error,omitempty"`
	RunIDs                []string   `json:"run_ids,omitempty"`
	ArtifactIDs           []string   `json:"artifact_ids,omitempty"`
	ResultPath            string     `json:"result_path,omitempty"`
	Events                []Event    `json:"events"`
	IdempotencyKey        string     `json:"idempotency_key"`
	RequestDigest         string     `json:"request_digest"`
	RetryOfTaskID         string     `json:"retry_of_task_id,omitempty"`
	ContinuedFromTaskID   string     `json:"continued_from_task_id,omitempty"`
}

type ExecutionResult struct {
	RunIDs      []string
	ArtifactIDs []string
	ResultPath  string
}

type Executor interface {
	Execute(context.Context, string, Request) (ExecutionResult, error)
}

type DatasetCatalog interface {
	GetDataset(
		context.Context,
		string,
		string,
		string,
	) (dashboard.Dataset, []string, error)
}

type Planner struct {
	catalog       DatasetCatalog
	llmConfigured bool
}

func NewPlanner(catalog DatasetCatalog, llmConfigured bool) (*Planner, error) {
	if catalog == nil {
		return nil, fmt.Errorf("%w: task dataset catalog is required", knowledgeeval.ErrInvalidRecord)
	}
	return &Planner{catalog: catalog, llmConfigured: llmConfigured}, nil
}

func NormalizeRequest(request Request) Request {
	request.Dataset = strings.TrimSpace(request.Dataset)
	request.Partition = strings.TrimSpace(request.Partition)
	request.GroupID = strings.TrimSpace(request.GroupID)
	request.Mode = strings.TrimSpace(strings.ToLower(request.Mode))
	request.Model = strings.TrimSpace(request.Model)
	request.ReaderModel = strings.TrimSpace(request.ReaderModel)
	request.ContinueFromTaskID = strings.TrimSpace(request.ContinueFromTaskID)
	request.ReuseArtifactFromTaskID = strings.TrimSpace(request.ReuseArtifactFromTaskID)
	if request.Mode == "" {
		request.Mode = ModeBaseline
	}
	if request.QuestionLimit <= 0 {
		request.QuestionLimit = 5
	}
	if request.MaxRounds <= 0 && request.ReuseArtifactFromTaskID == "" {
		request.MaxRounds = 30
	}
	if request.QuestionOffset < 0 {
		request.QuestionOffset = 0
	}
	if request.Mode == ModeMaintainer {
		if request.Model == "" {
			request.Model = "deepseek-v4-pro"
		}
		if request.ReaderModel == "" {
			request.ReaderModel = request.Model
		}
	}
	return request
}

func (p *Planner) Preview(ctx context.Context, request Request) (Preview, error) {
	request = NormalizeRequest(request)
	if request.Dataset == "" || request.Partition == "" || request.GroupID == "" {
		return Preview{}, fmt.Errorf(
			"%w: dataset, partition, and group ID are required",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	if request.Mode != ModeBaseline && request.Mode != ModeMaintainer {
		return Preview{}, fmt.Errorf(
			"%w: unsupported task mode %q",
			knowledgeeval.ErrInvalidRecord,
			request.Mode,
		)
	}
	group, _, err := p.catalog.GetDataset(
		ctx,
		request.Dataset,
		request.Partition,
		request.GroupID,
	)
	if err != nil {
		return Preview{}, err
	}
	remainingQuestions := max(0, group.EvaluationCases-request.QuestionOffset)
	selectedQuestions := min(request.QuestionLimit, remainingQuestions)
	preview := Preview{
		Eligible: true, Paid: request.Mode == ModeMaintainer,
		LLMConfigured: p.llmConfigured,
		Dataset:       group.Name, Partition: group.Partition, GroupID: group.CaseID,
		SourceKind: group.SourceKind, AvailableQuestions: group.EvaluationCases,
		SelectedQuestions:   selectedQuestions,
		CumulativeQuestions: request.QuestionOffset + selectedQuestions,
		PlannedRuns:         1, Benchmarks: []string{
			"wiki-artifact-quality",
			"knowledge-search-get-qa",
		},
		IncludesSourceOnly: true,
	}
	switch group.SourceKind {
	case "long-running-conversation", "chat-session-history":
	case "agent-trajectory-haystack":
		preview.Eligible = false
		preview.IneligibleReason = "LongMemEval-V2 needs an agent trajectory dataset adapter."
	default:
		preview.Eligible = false
		preview.IneligibleReason = "This prepared source kind has no experiment adapter."
	}
	if len(group.CaseIDs) != 1 {
		preview.Eligible = false
		preview.IneligibleReason = "This group contains multiple cases and needs a grouped runner."
	}
	if group.EvaluationCases == 0 {
		preview.Eligible = false
		preview.IneligibleReason = "This group has no evaluation questions."
	}
	if request.Mode == ModeMaintainer {
		preview.PlannedRuns = 2
		preview.IncludesMaintainer = true
		preview.MaxLLMCalls = request.MaxRounds + 4*selectedQuestions
		if request.ReuseArtifactFromTaskID != "" {
			preview.PlannedRuns = 1
			preview.Benchmarks = []string{"knowledge-search-get-qa"}
			preview.IncludesSourceOnly = false
			preview.MaxLLMCalls = 2 * selectedQuestions
			if selectedQuestions == 0 {
				preview.Eligible = false
				preview.IneligibleReason = "This artifact has already evaluated all questions."
			}
		}
		if !p.llmConfigured {
			preview.Eligible = false
			preview.IneligibleReason = "DEEPSEEK_API_KEY is not configured in the API process."
		}
	}
	return preview, nil
}
