// Package knowledgeeval provides product-neutral orchestration for knowledge
// artifact evaluation.
package knowledgeeval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	WikiCorpusCapability       = "projection/wiki-corpus"
	TeamNoteSnapshotCapability = "projection/team-note-snapshot"
	SearchCapability           = "recall/search"
	GetCapability              = "recall/get"
	NavigateCapability         = "recall/navigate"
	PassiveRecallCapability    = "recall/passive"
)

var (
	ErrInvalidRecord     = errors.New("invalid knowledge eval record")
	ErrNotFound          = errors.New("knowledge eval record not found")
	ErrConflict          = errors.New("knowledge eval record conflict")
	ErrInvalidTransition = errors.New("invalid knowledge eval transition")
	ErrCapabilityMissing = errors.New("required capability is missing")
	ErrCapabilityVersion = errors.New("capability version is incompatible")
	digestPattern        = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type OpaqueRef struct {
	Kind          string `json:"kind"`
	SchemaVersion string `json:"schema_version"`
	URI           string `json:"uri"`
	SHA256        string `json:"sha256"`
}

func (r OpaqueRef) Validate() error {
	if strings.TrimSpace(r.Kind) == "" {
		return fmt.Errorf("%w: opaque ref kind is required", ErrInvalidRecord)
	}
	if strings.TrimSpace(r.SchemaVersion) == "" {
		return fmt.Errorf("%w: opaque ref schema version is required", ErrInvalidRecord)
	}
	parsed, err := url.Parse(r.URI)
	if err != nil || parsed.Scheme == "" {
		return fmt.Errorf("%w: opaque ref URI is invalid", ErrInvalidRecord)
	}
	if !digestPattern.MatchString(r.SHA256) {
		return fmt.Errorf("%w: opaque ref SHA-256 is invalid", ErrInvalidRecord)
	}
	return nil
}

type Provenance struct {
	BuilderID      string            `json:"builder_id"`
	BuilderVersion string            `json:"builder_version"`
	CodeRevision   string            `json:"code_revision"`
	ConfigDigest   string            `json:"config_digest"`
	Seed           int64             `json:"seed"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type ArtifactRecord struct {
	ArtifactID   string     `json:"artifact_id"`
	Kind         string     `json:"kind"`
	WorldID      string     `json:"world_id"`
	GroupID      string     `json:"group_id"`
	CheckpointID string     `json:"checkpoint_id"`
	BaseID       string     `json:"base_id,omitempty"`
	Payload      OpaqueRef  `json:"payload"`
	Provenance   Provenance `json:"provenance"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (r ArtifactRecord) Validate() error {
	for name, value := range map[string]string{
		"artifact ID":   r.ArtifactID,
		"kind":          r.Kind,
		"world ID":      r.WorldID,
		"group ID":      r.GroupID,
		"checkpoint ID": r.CheckpointID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: %s is required", ErrInvalidRecord, name)
		}
	}
	if err := r.Payload.Validate(); err != nil {
		return fmt.Errorf("validate artifact payload: %w", err)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: artifact creation time is required", ErrInvalidRecord)
	}
	return nil
}

type Capability struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (c Capability) Validate() error {
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Version) == "" {
		return fmt.Errorf("%w: capability name and version are required", ErrInvalidRecord)
	}
	return nil
}

type CapabilitySet []Capability

func (s CapabilitySet) Supports(required Capability) error {
	if err := required.Validate(); err != nil {
		return err
	}
	for _, candidate := range s {
		if candidate.Name != required.Name {
			continue
		}
		if candidate.Version != required.Version {
			return fmt.Errorf(
				"%w: %s requires %s, subject provides %s",
				ErrCapabilityVersion,
				required.Name,
				required.Version,
				candidate.Version,
			)
		}
		return nil
	}
	return fmt.Errorf("%w: %s:%s", ErrCapabilityMissing, required.Name, required.Version)
}

func (s CapabilitySet) Clone() CapabilitySet {
	return slices.Clone(s)
}

type Subject interface {
	ID() string
	Capabilities() CapabilitySet
}

type WikiDocument struct {
	Ref       string            `json:"ref"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Links     []string          `json:"links,omitempty"`
	Citations []string          `json:"citations,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type WikiCorpus struct {
	SchemaVersion string         `json:"schema_version"`
	Documents     []WikiDocument `json:"documents"`
}

type ProjectionRequest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ProjectionResponse struct {
	Payload OpaqueRef `json:"payload"`
}

type Projector interface {
	Project(context.Context, ProjectionRequest) (ProjectionResponse, error)
}

type SearchRequest struct {
	Query       string `json:"query"`
	MaxItems    int    `json:"max_items"`
	TokenBudget int    `json:"token_budget"`
}

type SearchHit struct {
	Ref      string            `json:"ref"`
	Text     string            `json:"text"`
	Score    float64           `json:"score"`
	Tokens   int               `json:"tokens"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type SearchResponse struct {
	Hits  []SearchHit     `json:"hits"`
	Trace json.RawMessage `json:"trace,omitempty"`
}

type Searcher interface {
	Search(context.Context, SearchRequest) (SearchResponse, error)
}

type GetRequest struct {
	Ref string `json:"ref"`
}

type GetResponse struct {
	Ref        string            `json:"ref"`
	Text       string            `json:"text"`
	Provenance map[string]string `json:"provenance,omitempty"`
}

type Getter interface {
	Get(context.Context, GetRequest) (GetResponse, error)
}

type NavigateRequest struct {
	Ref string `json:"ref,omitempty"`
}

type NavigationNode struct {
	Ref      string           `json:"ref"`
	Title    string           `json:"title"`
	Children []NavigationNode `json:"children,omitempty"`
}

type NavigateResponse struct {
	Roots []NavigationNode `json:"roots"`
}

type Navigator interface {
	Navigate(context.Context, NavigateRequest) (NavigateResponse, error)
}

type PassiveRecallRequest struct {
	Query       string `json:"query"`
	MaxItems    int    `json:"max_items"`
	TokenBudget int    `json:"token_budget"`
}

type PassiveRecallResponse struct {
	Items []SearchHit     `json:"items"`
	Trace json.RawMessage `json:"trace,omitempty"`
}

type PassiveRecaller interface {
	Recall(context.Context, PassiveRecallRequest) (PassiveRecallResponse, error)
}

type ArtifactViewRequest struct {
	Artifact     ArtifactRecord  `json:"artifact"`
	BaseArtifact *ArtifactRecord `json:"base_artifact,omitempty"`
	Kind         string          `json:"kind"`
	ViewConfig   *OpaqueRef      `json:"view_config,omitempty"`
}

type ArtifactViewRecord struct {
	ViewID          string    `json:"view_id"`
	ArtifactID      string    `json:"artifact_id"`
	Kind            string    `json:"kind"`
	RendererID      string    `json:"renderer_id"`
	RendererVersion string    `json:"renderer_version"`
	Payload         OpaqueRef `json:"payload"`
	EntryPoint      string    `json:"entry_point"`
	CreatedAt       time.Time `json:"created_at"`
}

type ArtifactViewProvider interface {
	RenderView(context.Context, ArtifactViewRequest) (ArtifactViewRecord, error)
}

type BenchmarkGroup struct {
	GroupID         string    `json:"group_id"`
	WorldID         string    `json:"world_id"`
	CheckpointID    string    `json:"checkpoint_id"`
	BuildInput      OpaqueRef `json:"build_input"`
	EvaluationInput OpaqueRef `json:"evaluation_input"`
}

type BuildRequest struct {
	Group         BenchmarkGroup  `json:"group"`
	BaseArtifact  *ArtifactRecord `json:"base_artifact,omitempty"`
	BuilderConfig *OpaqueRef      `json:"builder_config,omitempty"`
}

type BuilderDescriptor struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type BuilderDriver interface {
	Descriptor() BuilderDescriptor
	Build(context.Context, BuildRequest) (ArtifactRecord, error)
}

type ArtifactDriverDescriptor struct {
	ID             string        `json:"id"`
	Version        string        `json:"version"`
	ArtifactKinds  []string      `json:"artifact_kinds"`
	Capabilities   CapabilitySet `json:"capabilities"`
	SchemaVersions []string      `json:"schema_versions"`
}

type ArtifactDriver interface {
	Descriptor() ArtifactDriverDescriptor
	Open(context.Context, ArtifactRecord) (Subject, error)
}

type Metric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

type CaseResult struct {
	CaseID       string            `json:"case_id"`
	Status       string            `json:"status"`
	Correct      bool              `json:"correct"`
	Expected     string            `json:"expected,omitempty"`
	Actual       string            `json:"actual,omitempty"`
	FailureStage string            `json:"failure_stage,omitempty"`
	Metrics      []Metric          `json:"metrics,omitempty"`
	Evidence     []string          `json:"evidence,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type BenchmarkResult struct {
	Status       string       `json:"status"`
	Metrics      []Metric     `json:"metrics"`
	CaseResults  []CaseResult `json:"case_results"`
	Observations OpaqueRef    `json:"observations"`
	RawReport    OpaqueRef    `json:"raw_report"`
}

type BenchmarkDescriptor struct {
	ID                   string        `json:"id"`
	Version              string        `json:"version"`
	RequiredCapabilities CapabilitySet `json:"required_capabilities"`
	BundleDigest         string        `json:"bundle_digest"`
	ConfigDigest         string        `json:"config_digest"`
}

func (d BenchmarkDescriptor) Fingerprint() string {
	return strings.Join([]string{
		d.ID,
		d.Version,
		d.BundleDigest,
		d.ConfigDigest,
	}, ":")
}

type BenchmarkAdapter interface {
	Descriptor() BenchmarkDescriptor
	Run(context.Context, Subject) (BenchmarkResult, error)
}
