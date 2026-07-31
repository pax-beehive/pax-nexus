package knowledgeeval

import (
	"fmt"
	"time"
)

type RunStatus string

const (
	RunStatusPlanned   RunStatus = "planned"
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
)

type TrialStatus string

const (
	TrialStatusPlanned    TrialStatus = "planned"
	TrialStatusRunning    TrialStatus = "running"
	TrialStatusCompleted  TrialStatus = "completed"
	TrialStatusFailed     TrialStatus = "failed"
	TrialStatusIneligible TrialStatus = "ineligible"
)

type AttemptStatus string

const (
	AttemptStatusRunning   AttemptStatus = "running"
	AttemptStatusCompleted AttemptStatus = "completed"
	AttemptStatusFailed    AttemptStatus = "failed"
)

type Stage string

const (
	StagePlanned       Stage = "planned"
	StageBuilding      Stage = "building"
	StageArtifactReady Stage = "artifact_ready"
	StageEvaluating    Stage = "evaluating"
	StagePublishing    Stage = "publishing"
	StageCompleted     Stage = "completed"
	StageFailed        Stage = "failed"
)

var stageOrder = map[Stage]int{
	StagePlanned:       0,
	StageBuilding:      1,
	StageArtifactReady: 2,
	StageEvaluating:    3,
	StagePublishing:    4,
	StageCompleted:     5,
	StageFailed:        5,
}

type Run struct {
	ID           string    `json:"id"`
	WorldID      string    `json:"world_id"`
	GroupID      string    `json:"group_id"`
	CheckpointID string    `json:"checkpoint_id"`
	ArtifactID   string    `json:"artifact_id"`
	Status       RunStatus `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	CompletedAt  time.Time `json:"completed_at,omitempty"`
}

type Trial struct {
	ID                   string           `json:"id"`
	RunID                string           `json:"run_id"`
	CaseID               string           `json:"case_id"`
	BenchmarkID          string           `json:"benchmark_id"`
	BenchmarkFingerprint string           `json:"benchmark_fingerprint"`
	Status               TrialStatus      `json:"status"`
	IneligibleReason     string           `json:"ineligible_reason,omitempty"`
	Result               *BenchmarkResult `json:"result,omitempty"`
}

type Attempt struct {
	ID          string        `json:"id"`
	TrialID     string        `json:"trial_id"`
	Number      int           `json:"number"`
	Status      AttemptStatus `json:"status"`
	Stage       Stage         `json:"stage"`
	Error       string        `json:"error,omitempty"`
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt time.Time     `json:"completed_at,omitempty"`
}

type Event struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	TrialID   string    `json:"trial_id,omitempty"`
	AttemptID string    `json:"attempt_id,omitempty"`
	Stage     Stage     `json:"stage"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type RunDetail struct {
	Run      Run       `json:"run"`
	Trials   []Trial   `json:"trials"`
	Attempts []Attempt `json:"attempts"`
	Events   []Event   `json:"events"`
}

func validateStageAdvance(current, next Stage) error {
	if current == StageCompleted || current == StageFailed {
		return fmt.Errorf("%w: attempt is already terminal", ErrInvalidTransition)
	}
	if next == StageFailed {
		return nil
	}
	if stageOrder[next] <= stageOrder[current] {
		return fmt.Errorf(
			"%w: stage %s cannot advance to %s",
			ErrInvalidTransition,
			current,
			next,
		)
	}
	return nil
}
