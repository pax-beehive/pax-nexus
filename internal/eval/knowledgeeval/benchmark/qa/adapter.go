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
	BenchmarkVersion = "v2"
)

type Case struct {
	ID              string     `json:"id"`
	Question        string     `json:"question"`
	Expected        string     `json:"expected"`
	AnswerKind      AnswerKind `json:"answer_kind,omitempty"`
	DatasetCategory string     `json:"dataset_category,omitempty"`
	SupportRefs     []string   `json:"support_refs,omitempty"`
}

type Config struct {
	Cases       []Case      `json:"cases"`
	MaxItems    int         `json:"max_items"`
	TokenBudget int         `json:"token_budget"`
	Reader      Reader      `json:"-"`
	Judge       AnswerJudge `json:"-"`
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
	if config.Reader == nil {
		config.Reader = ContextReader{}
	}
	if config.Judge == nil {
		config.Judge = DeterministicAnswerJudge{}
	}
	for _, testCase := range config.Cases {
		if strings.TrimSpace(testCase.ID) == "" ||
			strings.TrimSpace(testCase.Question) == "" ||
			strings.TrimSpace(testCase.Expected) == "" {
			return nil, fmt.Errorf("%w: QA case ID, question, and expected answer are required", knowledgeeval.ErrInvalidRecord)
		}
	}
	encoded, err := json.Marshal(struct {
		Cases       []Case `json:"cases"`
		MaxItems    int    `json:"max_items"`
		TokenBudget int    `json:"token_budget"`
		ReaderID    string `json:"reader_id"`
		JudgeID     string `json:"judge_id"`
	}{
		Cases: config.Cases, MaxItems: config.MaxItems,
		TokenBudget: config.TokenBudget, ReaderID: config.Reader.ID(),
		JudgeID: config.Judge.ID(),
	})
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
	corpus, corpusAvailable, err := a.loadCorpus(ctx, subject)
	if err != nil {
		return knowledgeeval.BenchmarkResult{}, err
	}
	var correctCount, artifactFoundCount, supportFoundCount, readerSuccessCount int
	var disputedCount int
	var answerScoreTotal, judgeConfidenceTotal float64
	for _, testCase := range a.config.Cases {
		result, supportFound, judgment, err := a.runCase(
			ctx,
			searcher,
			getter,
			testCase,
		)
		if err != nil {
			return knowledgeeval.BenchmarkResult{}, err
		}
		artifactFound := true
		if corpusAvailable {
			artifactFound = corpusSupports(corpus, testCase)
		}
		result.FailureStage = failureStage(result.Correct, artifactFound, supportFound)
		result.Metrics = observationMetrics(artifactFound, supportFound, judgment)
		results = append(results, result)
		answerScoreTotal += judgment.Score
		judgeConfidenceTotal += judgment.Confidence
		if judgment.Disputed {
			disputedCount++
		}
		if result.Correct {
			correctCount++
		}
		if artifactFound {
			artifactFoundCount++
		}
		if supportFound {
			supportFoundCount++
		}
		if supportFound && result.Correct {
			readerSuccessCount++
		}
	}
	metrics := []knowledgeeval.Metric{
		{Name: "answer_accuracy", Value: ratio(correctCount, len(results)), Unit: "ratio"},
		{Name: "answer_score", Value: answerScoreTotal / float64(len(results)), Unit: "ratio"},
		{Name: "artifact_support_rate", Value: ratio(artifactFoundCount, len(results)), Unit: "ratio"},
		{Name: "retrieval_hit_rate", Value: ratio(supportFoundCount, len(results)), Unit: "ratio"},
		{
			Name:  "conditional_retrieval_rate",
			Value: ratio(supportFoundCount, artifactFoundCount),
			Unit:  "ratio",
		},
		{
			Name:  "conditional_reader_accuracy",
			Value: ratio(readerSuccessCount, supportFoundCount),
			Unit:  "ratio",
		},
		{
			Name: "judge_mean_confidence", Value: judgeConfidenceTotal / float64(len(results)),
			Unit: "ratio",
		},
		{Name: "judge_disputed_rate", Value: ratio(disputedCount, len(results)), Unit: "ratio"},
		{Name: "case_count", Value: float64(len(results)), Unit: "count"},
	}
	return a.publish(ctx, correctCount == len(results), metrics, results)
}

func (a *Adapter) runCase(
	ctx context.Context,
	searcher knowledgeeval.Searcher,
	getter knowledgeeval.Getter,
	testCase Case,
) (knowledgeeval.CaseResult, bool, AnswerJudgment, error) {
	search, err := searcher.Search(ctx, knowledgeeval.SearchRequest{
		Query: testCase.Question, MaxItems: a.config.MaxItems,
		TokenBudget: a.config.TokenBudget,
	})
	if err != nil {
		return knowledgeeval.CaseResult{}, false, AnswerJudgment{}, fmt.Errorf("search QA case %s: %w", testCase.ID, err)
	}
	retrieved := make([]string, 0, len(search.Hits))
	passages := make([]string, 0, len(search.Hits))
	retrievedSupportRefs := make([]string, 0, len(search.Hits))
	documents := make([]knowledgeeval.GetResponse, 0, len(search.Hits))
	for _, hit := range search.Hits {
		retrieved = append(retrieved, hit.Ref)
		retrievedSupportRefs = append(
			retrievedSupportRefs,
			strings.Split(hit.Metadata["support_refs"], ",")...,
		)
		document, err := getter.Get(ctx, knowledgeeval.GetRequest{Ref: hit.Ref})
		if err != nil {
			return knowledgeeval.CaseResult{}, false, AnswerJudgment{}, fmt.Errorf("get QA hit %s: %w", hit.Ref, err)
		}
		documents = append(documents, document)
		passages = append(passages, document.Text)
	}
	supportFound := supportsAny(
		append(retrieved, retrievedSupportRefs...),
		testCase.SupportRefs,
	)
	if len(testCase.SupportRefs) == 0 {
		supportFound = containsNormalized(strings.Join(passages, "\n"), testCase.Expected)
	}
	actual, err := a.config.Reader.Answer(ctx, testCase.Question, documents)
	if err != nil {
		return knowledgeeval.CaseResult{}, false, AnswerJudgment{}, fmt.Errorf(
			"read QA case %s: %w",
			testCase.ID,
			err,
		)
	}
	judgment, err := a.config.Judge.Judge(ctx, AnswerJudgmentRequest{
		Question: testCase.Question, Expected: testCase.Expected, Actual: actual,
		Kind: answerKindOrDefault(testCase.AnswerKind), DatasetCategory: testCase.DatasetCategory,
	})
	if err != nil {
		return knowledgeeval.CaseResult{}, false, AnswerJudgment{}, fmt.Errorf(
			"judge QA case %s: %w", testCase.ID, err,
		)
	}
	sort.Strings(retrieved)
	return knowledgeeval.CaseResult{
		CaseID: testCase.ID, Status: status(judgment.Correct), Correct: judgment.Correct,
		Expected: testCase.Expected, Actual: actual,
		Evidence: retrieved,
		Metadata: map[string]string{
			"reader_id":         a.config.Reader.ID(),
			"judge_id":          a.config.Judge.ID(),
			"judge_verdict":     judgment.Verdict,
			"judge_confidence":  fmt.Sprintf("%.4f", judgment.Confidence),
			"judge_disputed":    fmt.Sprint(judgment.Disputed),
			"judge_reason_code": judgment.ReasonCode,
			"judge_reason":      judgment.Reason,
			"answer_kind":       string(answerKindOrDefault(testCase.AnswerKind)),
			"dataset_category":  testCase.DatasetCategory,
		},
	}, supportFound, judgment, nil
}

func failureStage(correct, artifactFound, supportFound bool) string {
	if correct {
		return ""
	}
	if !artifactFound {
		return "artifact"
	}
	if !supportFound {
		return "retrieval"
	}
	return "reader"
}

func observationMetrics(
	artifactFound bool,
	supportFound bool,
	judgment AnswerJudgment,
) []knowledgeeval.Metric {
	return []knowledgeeval.Metric{
		{Name: "answer_score", Value: judgment.Score, Unit: "ratio"},
		{Name: "judge_confidence", Value: judgment.Confidence, Unit: "ratio"},
		{Name: "judge_disputed", Value: boolValue(judgment.Disputed), Unit: "binary"},
		{
			Name:  "artifact_support_available",
			Value: boolValue(artifactFound),
			Unit:  "binary",
		},
		{
			Name:  "support_retrieved",
			Value: boolValue(supportFound),
			Unit:  "binary",
		},
	}
}

func (a *Adapter) loadCorpus(
	ctx context.Context,
	subject knowledgeeval.Subject,
) (knowledgeeval.WikiCorpus, bool, error) {
	projector, ok := subject.(knowledgeeval.Projector)
	if !ok {
		return knowledgeeval.WikiCorpus{}, false, nil
	}
	projected, err := projector.Project(ctx, knowledgeeval.ProjectionRequest{
		Name: knowledgeeval.WikiCorpusCapability, Version: "v1",
	})
	if err != nil {
		return knowledgeeval.WikiCorpus{}, false, fmt.Errorf("project QA artifact corpus: %w", err)
	}
	encoded, err := a.store.OpenBytes(ctx, projected.Payload)
	if err != nil {
		return knowledgeeval.WikiCorpus{}, false, fmt.Errorf("open QA artifact corpus: %w", err)
	}
	var corpus knowledgeeval.WikiCorpus
	if err := json.Unmarshal(encoded, &corpus); err != nil {
		return knowledgeeval.WikiCorpus{}, false, fmt.Errorf("decode QA artifact corpus: %w", err)
	}
	return corpus, true, nil
}

func corpusSupports(corpus knowledgeeval.WikiCorpus, testCase Case) bool {
	for _, document := range corpus.Documents {
		if len(testCase.SupportRefs) == 0 &&
			containsNormalized(document.Body, testCase.Expected) {
			return true
		}
		if supportsAny(
			strings.Split(document.Metadata["support_refs"], ","),
			testCase.SupportRefs,
		) {
			return true
		}
	}
	return false
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
