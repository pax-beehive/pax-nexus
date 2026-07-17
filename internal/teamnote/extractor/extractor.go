package extractor

import (
	"context"
	"fmt"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

type Usage struct {
	InputTokens           int
	OutputTokens          int
	PromptCacheHitTokens  int
	PromptCacheMissTokens int
}

type Result struct {
	Candidates    []teamnote.Candidate
	Usage         Usage
	Model         string
	PromptVersion string
	// ExtractionVersion identifies the response protocol that produced this
	// result. Runtime idempotency uses it to keep v2 shadow or rollout results
	// from replaying a v1 extraction run for the same Session Slice.
	ExtractionVersion string
	// Rejections records candidates dropped during deterministic
	// post-decode validation, with reasons.
	Rejections []teamnote.CandidateRejection
	// Trace carries the extraction v2 internal products (claims, decisions,
	// observations). It is nil under the v1 protocol and never enters passive
	// agent context.
	Trace *TraceV2
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
