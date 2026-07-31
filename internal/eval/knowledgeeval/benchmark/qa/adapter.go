package qa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
)

const (
	BenchmarkID      = "knowledge-search-get-qa"
	BenchmarkVersion = "v1"
)

type Case struct {
	ID          string   `json:"id"`
	Question    string   `json:"question"`
	Expected    string   `json:"expected"`
	SupportRefs []string `json:"support_refs,omitempty"`
}

type Config struct {
	Cases       []Case `json:"cases"`
	MaxItems    int    `json:"max_items"`
	TokenBudget int    `json:"token_budget"`
}

type Adapter struct {
	store      *knowledgeeval.ArtifactStore
	config     Config
	descriptor knowledgeeval.BenchmarkDescriptor
}

func NewAdapter(store *knowledgeeval.ArtifactStore, config Config) (*Adapter, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: artifact store is required", knowledgeeval.ErrInvalidRecord)
	}
	if len(config.Cases) == 0 {
		return nil, fmt.Errorf("%w: QA cases are required", knowledgeeval.ErrInvalidRecord)
	}
	if config.MaxItems <= 0 {
		config.MaxItems = 5
	}
	if config.TokenBudget <= 0 {
		config.TokenBudget = 2000
	}
	for _, testCase := range config.Cases {
		if strings.TrimSpace(testCase.ID) == "" ||
			strings.TrimSpace(testCase.Question) == "" ||
			strings.TrimSpace(testCase.Expected) == "" {
			return nil, fmt.Errorf("%w: QA case ID, question, and expected answer are required", knowledgeeval.ErrInvalidRecord)
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode QA benchmark config: %w", err)
	}
	return &Adapter{
		store: store, config: config,
		descriptor: knowledgeeval.BenchmarkDescriptor{
			ID: BenchmarkID, Version: BenchmarkVersion,
			RequiredCapabilities: knowledgeeval.CapabilitySet{
				{Name: knowledgeeval.SearchCapability, Version: "v1"},
				{Name: knowledgeeval.GetCapability, Version: "v1"},
			},
			BundleDigest: digest([]byte(BenchmarkID + ":" + BenchmarkVersion)),
			ConfigDigest: digest(encoded),
		},
	}, nil
}

func (a *Adapter) Descriptor() knowledgeeval.BenchmarkDescriptor {
	return a.descriptor
}

func (a *Adapter) Run(
	ctx context.Context,
	subject knowledgeeval.Subject,
) (knowledgeeval.BenchmarkResult, error) {
	searcher, searchOK := subject.(knowledgeeval.Searcher)
	getter, getOK := subject.(knowledgeeval.Getter)
	if !searchOK || !getOK {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf(
			"%w: QA requires Search and Get",
			knowledgeeval.ErrCapabilityMissing,
		)
	}
	results := make([]knowledgeeval.CaseResult, 0, len(a.config.Cases))
	var correctCount, supportFound int
	for _, testCase := range a.config.Cases {
		result, found, err := a.runCase(ctx, searcher, getter, testCase)
		if err != nil {
			return knowledgeeval.BenchmarkResult{}, err
		}
		results = append(results, result)
		if result.Correct {
			correctCount++
		}
		if found {
			supportFound++
		}
	}
	metrics := []knowledgeeval.Metric{
		{Name: "answer_accuracy", Value: ratio(correctCount, len(results)), Unit: "ratio"},
		{Name: "retrieval_hit_rate", Value: ratio(supportFound, len(results)), Unit: "ratio"},
		{Name: "case_count", Value: float64(len(results)), Unit: "count"},
	}
	return a.publish(ctx, correctCount == len(results), metrics, results)
}

func (a *Adapter) runCase(
	ctx context.Context,
	searcher knowledgeeval.Searcher,
	getter knowledgeeval.Getter,
	testCase Case,
) (knowledgeeval.CaseResult, bool, error) {
	search, err := searcher.Search(ctx, knowledgeeval.SearchRequest{
		Query: testCase.Question, MaxItems: a.config.MaxItems,
		TokenBudget: a.config.TokenBudget,
	})
	if err != nil {
		return knowledgeeval.CaseResult{}, false, fmt.Errorf("search QA case %s: %w", testCase.ID, err)
	}
	retrieved := make([]string, 0, len(search.Hits))
	passages := make([]string, 0, len(search.Hits))
	for _, hit := range search.Hits {
		retrieved = append(retrieved, hit.Ref)
		document, err := getter.Get(ctx, knowledgeeval.GetRequest{Ref: hit.Ref})
		if err != nil {
			return knowledgeeval.CaseResult{}, false, fmt.Errorf("get QA hit %s: %w", hit.Ref, err)
		}
		passages = append(passages, document.Text)
	}
	supportFound := supportsAny(retrieved, testCase.SupportRefs)
	if len(testCase.SupportRefs) == 0 {
		supportFound = len(retrieved) > 0
	}
	actual := strings.Join(passages, "\n")
	correct := containsNormalized(actual, testCase.Expected)
	stage := ""
	if !supportFound {
		stage = "retrieval"
	} else if !correct {
		stage = "reader"
	}
	sort.Strings(retrieved)
	return knowledgeeval.CaseResult{
		CaseID: testCase.ID, Status: status(correct), Correct: correct,
		Expected: testCase.Expected, Actual: actual, FailureStage: stage,
		Evidence: retrieved,
		Metrics: []knowledgeeval.Metric{{
			Name: "support_retrieved", Value: boolValue(supportFound), Unit: "binary",
		}},
	}, supportFound, nil
}

func (a *Adapter) publish(
	ctx context.Context,
	passed bool,
	metrics []knowledgeeval.Metric,
	cases []knowledgeeval.CaseResult,
) (knowledgeeval.BenchmarkResult, error) {
	report := struct {
		Benchmark string                     `json:"benchmark"`
		Metrics   []knowledgeeval.Metric     `json:"metrics"`
		Cases     []knowledgeeval.CaseResult `json:"cases"`
	}{Benchmark: BenchmarkID, Metrics: metrics, Cases: cases}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf("encode QA report: %w", err)
	}
	observations, err := a.store.PutBytes(
		ctx, "benchmark-observations", "pax.knowledge-eval.qa.v1", encoded,
	)
	if err != nil {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf("store QA observations: %w", err)
	}
	rawReport, err := a.store.PutBytes(
		ctx, "benchmark-report", "pax.knowledge-eval.report.v1", encoded,
	)
	if err != nil {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf("store QA report: %w", err)
	}
	return knowledgeeval.BenchmarkResult{
		Status: status(passed), Metrics: metrics, CaseResults: cases,
		Observations: observations, RawReport: rawReport,
	}, nil
}

func supportsAny(retrieved, expected []string) bool {
	for _, candidate := range retrieved {
		for _, support := range expected {
			if candidate == support {
				return true
			}
		}
	}
	return false
}

func containsNormalized(haystack, needle string) bool {
	normalize := func(value string) string {
		return strings.Join(strings.Fields(strings.ToLower(value)), " ")
	}
	return strings.Contains(normalize(haystack), normalize(needle))
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func boolValue(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func status(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
