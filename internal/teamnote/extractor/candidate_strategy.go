package extractor

import "fmt"

// buildDefaultCandidateStrategy is injected with -ldflags -X for release
// builds. An explicit runtime strategy still takes precedence.
var buildDefaultCandidateStrategy = CandidateStrategyCurrent

type extractionProtocol struct {
	systemPrompt string
	decodeFresh  func(body []byte) (Result, string, error)
	decodeSaved  func(content string) (Result, error)
}

type candidateStrategy struct {
	name            string
	protocolVersion string
	protocol        extractionProtocol
}

var candidateStrategies = []candidateStrategy{
	{
		name: CandidateStrategyCurrent, protocolVersion: extractionProtocolV2RevisionCurrent,
		protocol: extractionProtocol{rollingSystemPromptV2, decodeExtractionResponseV2, decodeExtractionContentV2},
	},
	{
		name: CandidateStrategyInteractionSlim, protocolVersion: extractionProtocolV2RevisionInteractionSlim,
		protocol: extractionProtocol{rollingSystemPromptV2InteractionSlim, decodeExtractionResponseV2, decodeExtractionContentV2},
	},
	{
		name: CandidateStrategyTyped2, protocolVersion: extractionProtocolV2RevisionTypedCurrent,
		protocol: extractionProtocol{rollingSystemPromptV2Typed, decodeExtractionResponseV2Typed, decodeExtractionContentV2Typed},
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
