package extractor

import (
	"context"
	"fmt"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

type Usage struct {
	InputTokens           int
	OutputTokens          int
	PromptCacheHitTokens  int
	PromptCacheMissTokens int
}

type ProviderCallType string

const (
	ProviderCallPrimary    ProviderCallType = "primary"
	ProviderCallSummary    ProviderCallType = "summary"
	ProviderCallCompaction ProviderCallType = "compaction"
	ProviderCallVerifier   ProviderCallType = "verifier"
)

// ProviderCall records one physical model request. Slice usage may include
// asynchronously consumed summary or compaction usage, so evaluations use
// these records when they need an exact provider-call breakdown.
type ProviderCall struct {
	Type       ProviderCallType `json:"type"`
	ScopeID    string           `json:"scope_id,omitempty"`
	StartedAt  time.Time        `json:"started_at"`
	DurationMS int64            `json:"duration_ms"`
	Usage      Usage            `json:"usage"`
	HTTPStatus int              `json:"http_status,omitempty"`
	Error      string           `json:"error,omitempty"`
}

type ProviderCallObserver func(ProviderCall)

const (
	CandidateStrategyCurrent          = "current"
	CandidateStrategyInteractionSlim  = "interaction-slim"
	CandidateStrategyEvidenceFidelity = "evidence-fidelity-v1"
	CandidateStrategyTyped2           = "typed-2"
	CandidateStrategySourceSpanV1     = "source-span-v1"
	CandidateStrategySourceSpanV2     = "source-span-v2"
	CandidateStrategyClaimCardV1      = "claim-card-v1"
	CandidateStrategyClaimCardV2      = "claim-card-v2"

	V2VariantCurrent          = CandidateStrategyCurrent
	V2VariantInteractionSlim  = CandidateStrategyInteractionSlim
	V2VariantEvidenceFidelity = CandidateStrategyEvidenceFidelity
	V2VariantTypedCurrent     = CandidateStrategyTyped2
	V2VariantSourceSpanV1     = CandidateStrategySourceSpanV1
	V2VariantSourceSpanV2     = CandidateStrategySourceSpanV2
	V2VariantClaimCardV1      = CandidateStrategyClaimCardV1
	V2VariantClaimCardV2      = CandidateStrategyClaimCardV2
)

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
	// SourceSpans records the source-preserving metadata produced by the
	// source-span candidate. It is extraction-only provenance and does not
	// alter recall policy.
	SourceSpans []SourceSpan
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
