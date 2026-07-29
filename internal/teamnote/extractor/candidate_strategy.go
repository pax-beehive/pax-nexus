package extractor

import (
	"fmt"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
)

// buildDefaultCandidateStrategy is injected with -ldflags -X for release
// builds. An explicit runtime strategy still takes precedence.
var buildDefaultCandidateStrategy = CandidateStrategySourceClause

type extractionProtocol struct {
	systemPrompt string
	decodeFresh  func(body []byte) (Result, string, error)
	decodeSaved  func(content string) (Result, error)
}

type candidateStrategy struct {
	name            string
	protocolVersion string
	protocol        extractionProtocol
	mapResult       func(*Result, sessionlake.Slice)
	candidateLimit  int
}

// Retirement policy (see docs/decisions/2026-07-28-extraction-strategy-retirement.md):
// a strategy enters this table with a stated experiment goal and an eval exit
// condition. When the experiment concludes it is either promoted to the build
// default or deleted in the same change that records the conclusion. Git
// history is the archive — no dormant entries.
var candidateStrategies = []candidateStrategy{
	{
		name: CandidateStrategyInteractionSlim, protocolVersion: extractionProtocolV2RevisionInteractionSlim,
		protocol:  extractionProtocol{rollingSystemPromptV2InteractionSlim, decodeExtractionResponseV2, decodeExtractionContentV2},
		mapResult: mapExtractionV2,
	},
	{
		name: CandidateStrategySourceClause, protocolVersion: extractionProtocolV2RevisionSourceClause,
		protocol: extractionProtocol{rollingSystemPromptV2SourceClause, decodeExtractionResponseV2,
			decodeExtractionContentV2},
		mapResult: mapExtractionSourceClauseV1,
	},
	{
		name: CandidateStrategySourceSpanV1, protocolVersion: extractionProtocolV2RevisionSourceSpanV1,
		protocol:  extractionProtocol{rollingSystemPromptSourceSpanV1, decodeExtractionResponseSourceSpanV1, decodeExtractionContentSourceSpanV1},
		mapResult: mapSourceSpanV1,
	},
	{
		name: CandidateStrategySourceSpanV2, protocolVersion: extractionProtocolV2RevisionSourceSpanV2,
		protocol: extractionProtocol{rollingSystemPromptSourceSpanV1, decodeExtractionResponseSourceSpanV1,
			decodeExtractionContentSourceSpanV1},
		mapResult: mapSourceSpanV2,
		// Deterministic source shards preserve all bounded slice events; they
		// are not subject to the model-output candidate cap.
		candidateLimit: -1,
	},
	{
		name: CandidateStrategyClaimCardV2, protocolVersion: extractionProtocolV2RevisionClaimCardV2,
		protocol: extractionProtocol{rollingSystemPromptClaimCardV2, decodeExtractionResponseV2,
			decodeExtractionContentV2},
		mapResult: mapExtractionClaimCard,
	},
}

// CandidateStrategyNames returns the stable names accepted by build and
// runtime configuration.
func CandidateStrategyNames() []string {
	names := make([]string, 0, len(candidateStrategies))
	for _, strategy := range candidateStrategies {
		names = append(names, strategy.name)
	}
	return names
}

// DefaultCandidateStrategy returns the release default embedded at link time.
func DefaultCandidateStrategy() string {
	return buildDefaultCandidateStrategy
}

func resolveCandidateStrategy(name string) (candidateStrategy, error) {
	if name == "" {
		name = DefaultCandidateStrategy()
	}
	for _, strategy := range candidateStrategies {
		if strategy.name == name {
			return strategy, nil
		}
	}
	return candidateStrategy{}, fmt.Errorf("unsupported extraction candidate strategy %q", name)
}
