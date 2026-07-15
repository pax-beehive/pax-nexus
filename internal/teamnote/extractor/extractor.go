package extractor

import (
	"context"
	"fmt"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

type Usage struct {
	InputTokens  int
	OutputTokens int
}

type Result struct {
	Candidates []teamnote.Candidate
	Usage      Usage
}

type Extractor interface {
	Extract(context.Context, sessionlake.Slice) (Result, error)
}

type Fixture struct {
	ByChecksum map[string]Result
}

func (f Fixture) Extract(ctx context.Context, slice sessionlake.Slice) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("fixture extraction context: %w", err)
	}
	result, ok := f.ByChecksum[slice.InputChecksum]
	if !ok {
		return Result{}, fmt.Errorf("fixture extraction %q: no result", slice.InputChecksum)
	}
	return result, nil
}

type Noop struct{}

func (Noop) Extract(ctx context.Context, _ sessionlake.Slice) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("noop extraction context: %w", err)
	}
	return Result{}, nil
}
