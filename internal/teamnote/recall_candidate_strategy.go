package teamnote

import "fmt"

const (
	RecallCandidateStrategyPassiveV1       = "passive-v1"
	RecallCandidateStrategyHintV1Selective = "hint-v1-selective"
)

// buildDefaultRecallCandidateStrategy is injected with -ldflags -X for
// release builds. Recall policy is intentionally fixed at build time so a
// deployed candidate cannot change semantics through environment variables.
var buildDefaultRecallCandidateStrategy = RecallCandidateStrategyPassiveV1

// RecallCandidateStrategy defines the complete retrieval and active-recall
// policy distributed in one Team Memory build.
type RecallCandidateStrategy struct {
	Name   string
	Policy RecallPolicy
}

var recallCandidateStrategies = []RecallCandidateStrategy{
	{
		Name: RecallCandidateStrategyPassiveV1,
		Policy: func() RecallPolicy {
			policy := DefaultRecallPolicy()
			policy.HintThreshold = 0.65
			return policy
		}(),
	},
	{
		Name: RecallCandidateStrategyHintV1Selective,
		Policy: RecallPolicy{
			SemanticThreshold: 0.50, HintSemanticThreshold: 0.20,
			HintThreshold: 0.60, HintMinQueryRelevance: 0.10,
			HintMinMarginalUtility: 0.85, CandidateLimit: 16, EnableHintRecall: true,
			SuppressDuplicates: true, DegradeRelated: true,
		},
	},
}

// RecallCandidateStrategyNames returns the stable names accepted by build
// configuration.
func RecallCandidateStrategyNames() []string {
	names := make([]string, 0, len(recallCandidateStrategies))
	for _, strategy := range recallCandidateStrategies {
		names = append(names, strategy.Name)
	}
	return names
}

// DefaultRecallCandidateStrategy returns the release default embedded at link
// time.
func DefaultRecallCandidateStrategy() string {
	return buildDefaultRecallCandidateStrategy
}

// ResolveRecallCandidateStrategy returns a fixed distributed recall policy.
func ResolveRecallCandidateStrategy(name string) (RecallCandidateStrategy, error) {
	if name == "" {
		name = DefaultRecallCandidateStrategy()
	}
	for _, strategy := range recallCandidateStrategies {
		if strategy.Name == name {
			return strategy, nil
		}
	}
	return RecallCandidateStrategy{}, fmt.Errorf("unsupported recall candidate strategy %q", name)
}
