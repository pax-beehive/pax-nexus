package cohorttask

import (
	"errors"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/experimenttask"
)

const (
	StatusQueued            = "queued"
	StatusRunning           = "running"
	StatusCompleted         = "completed"
	StatusCompletedWithGaps = "completed_with_gaps"
	StatusCancelled         = "cancelled"

	ExecutionPlanned       = "planned"
	ExecutionQueued        = "queued"
	ExecutionRunning       = "running"
	ExecutionResultPending = "result_pending"
	ExecutionCompleted     = "completed"
	ExecutionFailed        = "failed"
	ExecutionCancelled     = "cancelled"
	ExecutionIneligible    = "ineligible"
)

var ErrConflict = errors.New("knowledge eval cohort conflict")

type DatasetSelection struct {
	Dataset   string `json:"dataset"`
	Partition string `json:"partition"`
}

type Recipe struct {
	Mode        string `json:"mode"`
	Model       string `json:"model,omitempty"`
	ReaderModel string `json:"reader_model,omitempty"`
	MaxRounds   int    `json:"max_rounds"`
}

type Request struct {
	Name         string             `json:"name"`
	Selections   []DatasetSelection `json:"selections"`
	Recipe       Recipe             `json:"recipe"`
	ConfirmPaid  bool               `json:"confirm_paid,omitempty"`
	LLMCallLimit int                `json:"llm_call_limit,omitempty"`
}

type Issue struct {
	Dataset   string `json:"dataset"`
	Partition string `json:"partition"`
	GroupID   string `json:"group_id,omitempty"`
	Reason    string `json:"reason"`
}

type DatasetPreview struct {
	Dataset          string `json:"dataset"`
	Partition        string `json:"partition"`
	TotalGroups      int    `json:"total_groups"`
	EligibleGroups   int    `json:"eligible_groups"`
	IneligibleGroups int    `json:"ineligible_groups"`
	TotalQuestions   int    `json:"total_questions"`
	PlannedQuestions int    `json:"planned_questions"`
	MaxLLMCalls      int    `json:"max_llm_calls"`
}

type Preview struct {
	Eligible         bool             `json:"eligible"`
	Paid             bool             `json:"paid"`
	TotalGroups      int              `json:"total_groups"`
	EligibleGroups   int              `json:"eligible_groups"`
	IneligibleGroups int              `json:"ineligible_groups"`
	TotalQuestions   int              `json:"total_questions"`
	PlannedQuestions int              `json:"planned_questions"`
	PlannedTasks     int              `json:"planned_tasks"`
	MaxLLMCalls      int              `json:"max_llm_calls"`
	Datasets         []DatasetPreview `json:"datasets"`
	Issues           []Issue          `json:"issues,omitempty"`
}

type Execution struct {
	Dataset            string                 `json:"dataset"`
	Partition          string                 `json:"partition"`
	GroupID            string                 `json:"group_id"`
	Questions          int                    `json:"questions"`
	Status             string                 `json:"status"`
	IneligibleReason   string                 `json:"ineligible_reason,omitempty"`
	TaskID             string                 `json:"task_id,omitempty"`
	TaskRequest        experimenttask.Request `json:"task_request"`
	Error              string                 `json:"error,omitempty"`
	RunID              string                 `json:"run_id,omitempty"`
	EvaluatedQuestions int                    `json:"evaluated_questions,omitempty"`
	CorrectQuestions   int                    `json:"correct_questions,omitempty"`
	Accuracy           float64                `json:"accuracy,omitempty"`
}

type Summary struct {
	TotalGroups        int     `json:"total_groups"`
	EligibleGroups     int     `json:"eligible_groups"`
	EvaluatedGroups    int     `json:"evaluated_groups"`
	FailedGroups       int     `json:"failed_groups"`
	TotalQuestions     int     `json:"total_questions"`
	EvaluatedQuestions int     `json:"evaluated_questions"`
	CorrectQuestions   int     `json:"correct_questions"`
	MicroAccuracy      float64 `json:"micro_accuracy"`
	MacroAccuracy      float64 `json:"macro_accuracy"`
	GroupCoverage      float64 `json:"group_coverage"`
	QuestionCoverage   float64 `json:"question_coverage"`
}

type Campaign struct {
	ID                    string      `json:"id"`
	Request               Request     `json:"request"`
	Preview               Preview     `json:"preview"`
	Status                string      `json:"status"`
	Summary               Summary     `json:"summary"`
	Executions            []Execution `json:"executions"`
	CreatedAt             time.Time   `json:"created_at"`
	UpdatedAt             time.Time   `json:"updated_at"`
	CompletedAt           *time.Time  `json:"completed_at,omitempty"`
	CancellationRequested bool        `json:"cancellation_requested,omitempty"`
	IdempotencyKey        string      `json:"idempotency_key"`
	RequestDigest         string      `json:"request_digest"`
}
