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

var candidateStrategies = []candidateStrategy{
	{
		name: CandidateStrategyCurrent, protocolVersion: extractionProtocolV2RevisionCurrent,
		protocol:  extractionProtocol{rollingSystemPromptV2, decodeExtractionResponseV2, decodeExtractionContentV2},
		mapResult: mapExtractionV2,
	},
	{
		name: CandidateStrategyInteractionSlim, protocolVersion: extractionProtocolV2RevisionInteractionSlim,
		protocol:  extractionProtocol{rollingSystemPromptV2InteractionSlim, decodeExtractionResponseV2, decodeExtractionContentV2},
		mapResult: mapExtractionV2,
	},
	{
		name: CandidateStrategyEvidenceFidelity, protocolVersion: extractionProtocolV2RevisionEvidenceFidelity,
		protocol: extractionProtocol{rollingSystemPromptV2EvidenceFidelity, decodeExtractionResponseV2,
			decodeExtractionContentV2},
		mapResult: mapExtractionV2,
	},
	{
		name: CandidateStrategySourceClause, protocolVersion: extractionProtocolV2RevisionSourceClause,
		protocol: extractionProtocol{rollingSystemPromptV2SourceClause, decodeExtractionResponseV2,
			decodeExtractionContentV2},
		mapResult: mapExtractionSourceClauseV1,
	},
	{
		name: CandidateStrategyTyped2, protocolVersion: extractionProtocolV2RevisionTypedCurrent,
		protocol:  extractionProtocol{rollingSystemPromptV2Typed, decodeExtractionResponseV2Typed, decodeExtractionContentV2Typed},
		mapResult: mapExtractionV2,
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
		name: CandidateStrategyClaimCardV1, protocolVersion: extractionProtocolV2RevisionClaimCardV1,
		protocol: extractionProtocol{rollingSystemPromptClaimCardV1, decodeExtractionResponseV2,
			decodeExtractionContentV2},
		mapResult: mapExtractionClaimCardV1,
	},
	{
		name: CandidateStrategyClaimCardV2, protocolVersion: extractionProtocolV2RevisionClaimCardV2,
		protocol: extractionProtocol{rollingSystemPromptClaimCardV2, decodeExtractionResponseV2,
			decodeExtractionContentV2},
		mapResult: mapExtractionClaimCardV1,
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
