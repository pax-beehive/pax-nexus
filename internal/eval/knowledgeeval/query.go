package knowledgeeval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type QueryService struct {
	store RunStore
	now   func() time.Time
}

type QuerySnapshot struct {
	SchemaVersion string      `json:"schema_version"`
	GeneratedAt   time.Time   `json:"generated_at"`
	Runs          []RunDetail `json:"runs"`
}

func NewQueryService(store RunStore, now func() time.Time) (*QueryService, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: query store is required", ErrInvalidRecord)
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &QueryService{store: store, now: now}, nil
}

func (s *QueryService) ListRuns(ctx context.Context) ([]Run, error) {
	return s.store.ListRuns(ctx)
}

func (s *QueryService) GetRun(ctx context.Context, runID string) (RunDetail, error) {
	return s.store.GetRun(ctx, runID)
}

func (s *QueryService) Export(ctx context.Context, writer io.Writer) error {
	runs, err := s.store.ListRuns(ctx)
	if err != nil {
		return fmt.Errorf("list runs for query snapshot: %w", err)
	}
	snapshot := QuerySnapshot{
		SchemaVersion: "pax.knowledge-eval.query.v1",
		GeneratedAt:   s.now(),
		Runs:          make([]RunDetail, 0, len(runs)),
	}
	for _, run := range runs {
		detail, err := s.store.GetRun(ctx, run.ID)
		if err != nil {
			return fmt.Errorf("load run %s for query snapshot: %w", run.ID, err)
		}
		snapshot.Runs = append(snapshot.Runs, detail)
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		return fmt.Errorf("encode query snapshot: %w", err)
	}
	return nil
}
