package quality

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
)

const (
	BenchmarkID      = "wiki-artifact-quality"
	BenchmarkVersion = "v1"
)

type Config struct {
	MinimumScore float64 `json:"minimum_score"`
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
	if config.MinimumScore == 0 {
		config.MinimumScore = 0.75
	}
	if config.MinimumScore < 0 || config.MinimumScore > 1 {
		return nil, fmt.Errorf("%w: minimum score must be between zero and one", knowledgeeval.ErrInvalidRecord)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode quality benchmark config: %w", err)
	}
	return &Adapter{
		store:  store,
		config: config,
		descriptor: knowledgeeval.BenchmarkDescriptor{
			ID: BenchmarkID, Version: BenchmarkVersion,
			RequiredCapabilities: knowledgeeval.CapabilitySet{{
				Name: knowledgeeval.WikiCorpusCapability, Version: "v1",
			}},
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
	projector, ok := subject.(knowledgeeval.Projector)
	if !ok {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf(
			"%w: subject does not implement Wiki projection",
			knowledgeeval.ErrCapabilityMissing,
		)
	}
	projected, err := projector.Project(ctx, knowledgeeval.ProjectionRequest{
		Name: knowledgeeval.WikiCorpusCapability, Version: "v1",
	})
	if err != nil {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf("project Wiki corpus: %w", err)
	}
	encoded, err := a.store.OpenBytes(ctx, projected.Payload)
	if err != nil {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf("open Wiki corpus projection: %w", err)
	}
	var corpus knowledgeeval.WikiCorpus
	if err := json.Unmarshal(encoded, &corpus); err != nil {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf("decode Wiki corpus projection: %w", err)
	}
	return a.evaluate(ctx, corpus)
}

func (a *Adapter) evaluate(
	ctx context.Context,
	corpus knowledgeeval.WikiCorpus,
) (knowledgeeval.BenchmarkResult, error) {
	refs := make(map[string]struct{}, len(corpus.Documents))
	for _, document := range corpus.Documents {
		refs[document.Ref] = struct{}{}
	}
	var titlePass, bodyPass, citationPass, validLinks, totalLinks int
	cases := make([]knowledgeeval.CaseResult, 0, len(corpus.Documents))
	for _, document := range corpus.Documents {
		hasTitle := strings.TrimSpace(document.Title) != ""
		hasBody := strings.TrimSpace(document.Body) != ""
		hasCitation := len(document.Citations) > 0
		if hasTitle {
			titlePass++
		}
		if hasBody {
			bodyPass++
		}
		if hasCitation {
			citationPass++
		}
		for _, link := range document.Links {
			totalLinks++
			if _, exists := refs[link]; exists {
				validLinks++
			}
		}
		correct := hasTitle && hasBody && hasCitation
		cases = append(cases, knowledgeeval.CaseResult{
			CaseID: document.Ref, Status: passStatus(correct), Correct: correct,
			FailureStage: failureStage(correct),
			Evidence: []string{
				fmt.Sprintf("title=%t", hasTitle),
				fmt.Sprintf("body=%t", hasBody),
				fmt.Sprintf("citations=%d", len(document.Citations)),
			},
		})
	}
	documentCount := len(corpus.Documents)
	titleCoverage := ratio(titlePass, documentCount)
	bodyCoverage := ratio(bodyPass, documentCount)
	citationCoverage := ratio(citationPass, documentCount)
	linkIntegrity := 1.0
	if totalLinks > 0 {
		linkIntegrity = ratio(validLinks, totalLinks)
	}
	score := (titleCoverage + bodyCoverage + citationCoverage + linkIntegrity) / 4
	metrics := []knowledgeeval.Metric{
		{Name: "artifact_quality_score", Value: score, Unit: "ratio"},
		{Name: "title_coverage", Value: titleCoverage, Unit: "ratio"},
		{Name: "body_coverage", Value: bodyCoverage, Unit: "ratio"},
		{Name: "citation_coverage", Value: citationCoverage, Unit: "ratio"},
		{Name: "link_integrity", Value: linkIntegrity, Unit: "ratio"},
		{Name: "document_count", Value: float64(documentCount), Unit: "count"},
	}
	return a.publish(ctx, score >= a.config.MinimumScore, metrics, cases)
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
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf("encode quality report: %w", err)
	}
	observations, err := a.store.PutBytes(
		ctx, "benchmark-observations", "pax.knowledge-eval.quality.v1", encoded,
	)
	if err != nil {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf("store quality observations: %w", err)
	}
	rawReport, err := a.store.PutBytes(
		ctx, "benchmark-report", "pax.knowledge-eval.report.v1", encoded,
	)
	if err != nil {
		return knowledgeeval.BenchmarkResult{}, fmt.Errorf("store quality report: %w", err)
	}
	return knowledgeeval.BenchmarkResult{
		Status: passStatus(passed), Metrics: metrics, CaseResults: cases,
		Observations: observations, RawReport: rawReport,
	}, nil
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func passStatus(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}

func failureStage(passed bool) string {
	if passed {
		return ""
	}
	return "artifact_quality"
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
