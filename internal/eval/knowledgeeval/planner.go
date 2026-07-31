package knowledgeeval

import (
	"fmt"
	"strings"
)

type TrialPlan struct {
	TrialID          string              `json:"trial_id"`
	Benchmark        BenchmarkDescriptor `json:"benchmark"`
	Eligible         bool                `json:"eligible"`
	IneligibleReason string              `json:"ineligible_reason,omitempty"`
}

func PlanTrials(runID string, subject Subject, benchmarks []BenchmarkAdapter) []TrialPlan {
	result := make([]TrialPlan, 0, len(benchmarks))
	for _, benchmark := range benchmarks {
		descriptor := benchmark.Descriptor()
		plan := TrialPlan{
			TrialID:   runID + "-" + descriptor.ID,
			Benchmark: descriptor,
			Eligible:  true,
		}
		for _, required := range descriptor.RequiredCapabilities {
			if err := subject.Capabilities().Supports(required); err != nil {
				plan.Eligible = false
				plan.IneligibleReason = err.Error()
				break
			}
		}
		result = append(result, plan)
	}
	return result
}

type Registry struct {
	builders   map[string]BuilderDriver
	artifacts  map[string]ArtifactDriver
	benchmarks map[string]BenchmarkAdapter
}

func NewRegistry() *Registry {
	return &Registry{
		builders:   make(map[string]BuilderDriver),
		artifacts:  make(map[string]ArtifactDriver),
		benchmarks: make(map[string]BenchmarkAdapter),
	}
}

func (r *Registry) RegisterBuilder(builder BuilderDriver) error {
	descriptor := builder.Descriptor()
	return register(r.builders, descriptor.ID, builder)
}

func (r *Registry) RegisterArtifact(driver ArtifactDriver) error {
	descriptor := driver.Descriptor()
	return register(r.artifacts, descriptor.ID, driver)
}

func (r *Registry) RegisterBenchmark(benchmark BenchmarkAdapter) error {
	descriptor := benchmark.Descriptor()
	return register(r.benchmarks, descriptor.ID, benchmark)
}

func register[T any](values map[string]T, id string, value T) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: registry ID is required", ErrInvalidRecord)
	}
	if _, exists := values[id]; exists {
		return fmt.Errorf("%w: registry ID %s already exists", ErrConflict, id)
	}
	values[id] = value
	return nil
}

func (r *Registry) Builder(id string) (BuilderDriver, error) {
	value, exists := r.builders[id]
	if !exists {
		return nil, fmt.Errorf("%w: builder %s", ErrNotFound, id)
	}
	return value, nil
}

func (r *Registry) Artifact(id string) (ArtifactDriver, error) {
	value, exists := r.artifacts[id]
	if !exists {
		return nil, fmt.Errorf("%w: artifact driver %s", ErrNotFound, id)
	}
	return value, nil
}

func (r *Registry) Benchmark(id string) (BenchmarkAdapter, error) {
	value, exists := r.benchmarks[id]
	if !exists {
		return nil, fmt.Errorf("%w: benchmark %s", ErrNotFound, id)
	}
	return value, nil
}
