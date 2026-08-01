package dashboard

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/demo"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
)

type Dataset struct {
	ID              string
	Name            string
	Partition       string
	CaseID          string
	GroupKind       string
	SourceKind      string
	Status          string
	Sessions        int
	Turns           int
	Sources         int
	Trajectories    int
	Questions       int
	EvaluationCases int
	RunCount        int
	ArtifactCount   int
	ExperimentCount int
	UpdatedAt       time.Time
	CaseIDs         []string
	SourceArtifact  *Artifact
}

type DatasetPartition struct {
	Name          string
	GroupCount    int
	RunGroupCount int
}

type DatasetFamily struct {
	ID            string
	Name          string
	Revision      string
	License       string
	GroupKind     string
	GroupCount    int
	RunGroupCount int
	RunCount      int
	ArtifactCount int
	Partitions    []DatasetPartition
}

type DatasetSession struct {
	ID         string
	SourcePath string
	Turns      int
}

type SolutionVersion struct {
	ID             string
	BuilderID      string
	BuilderVersion string
	CodeRevision   string
	Model          string
	ConfigDigest   string
}

type Run struct {
	DatasetID       string
	Dataset         string
	Partition       string
	CaseID          string
	SolutionVersion SolutionVersion
	Detail          knowledgeeval.RunDetail
	ArtifactID      string
	Artifact        *Artifact
	BenchmarkIDs    []string
}

type Artifact struct {
	Record            knowledgeeval.ArtifactRecord
	Product           string
	Role              string
	DatasetID         string
	SolutionVersionID string
	Views             map[string]string
	bundleDirectory   string
}

type Benchmark struct {
	ID            string
	Name          string
	Description   string
	PrimaryMetric string
	Executed      bool
}

type Catalog struct {
	Families   []DatasetFamily
	Datasets   []Dataset
	Solutions  []SolutionVersion
	Runs       []Run
	Artifacts  []Artifact
	Benchmarks []Benchmark
}

type Registry struct {
	root         string
	preparedRoot string
	mu           sync.Mutex
	catalog      Catalog
	catalogUntil time.Time
	cacheTTL     time.Duration
	now          func() time.Time
}

type RegistryOption func(*Registry) error

const defaultCatalogCacheTTL = 2 * time.Second

func WithCatalogCacheTTL(ttl time.Duration) RegistryOption {
	return func(registry *Registry) error {
		if ttl < 0 {
			return fmt.Errorf(
				"%w: catalog cache TTL cannot be negative",
				knowledgeeval.ErrInvalidRecord,
			)
		}
		registry.cacheTTL = ttl
		return nil
	}
}

func withRegistryClock(now func() time.Time) RegistryOption {
	return func(registry *Registry) error {
		if now == nil {
			return fmt.Errorf(
				"%w: registry clock is required",
				knowledgeeval.ErrInvalidRecord,
			)
		}
		registry.now = now
		return nil
	}
}

func WithPreparedRoot(root string) RegistryOption {
	return func(registry *Registry) error {
		absolute, err := filepath.Abs(strings.TrimSpace(root))
		if err != nil {
			return fmt.Errorf("resolve prepared dataset root: %w", err)
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return fmt.Errorf("inspect prepared dataset root: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf(
				"inspect prepared dataset root: %w: root is not a directory",
				knowledgeeval.ErrInvalidRecord,
			)
		}
		registry.preparedRoot = absolute
		return nil
	}
}

func NewRegistry(root string, options ...RegistryOption) (*Registry, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return nil, fmt.Errorf("resolve knowledge eval registry root: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("inspect knowledge eval registry root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("inspect knowledge eval registry root: %w: root is not a directory", knowledgeeval.ErrInvalidRecord)
	}
	registry := &Registry{
		root: absolute, cacheTTL: defaultCatalogCacheTTL, now: time.Now,
	}
	for _, option := range options {
		if err := option(registry); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *Registry) Load(ctx context.Context) (Catalog, error) {
	if err := ctx.Err(); err != nil {
		return Catalog{}, fmt.Errorf("load knowledge eval catalog: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cacheTTL > 0 && r.now().Before(r.catalogUntil) {
		return r.catalog, nil
	}
	catalog, err := r.load(ctx)
	if err != nil {
		return Catalog{}, err
	}
	if r.cacheTTL > 0 {
		r.catalog = catalog
		r.catalogUntil = r.now().Add(r.cacheTTL)
	}
	return catalog, nil
}

func (r *Registry) load(ctx context.Context) (Catalog, error) {
	if err := ctx.Err(); err != nil {
		return Catalog{}, fmt.Errorf("load knowledge eval catalog: %w", err)
	}
	paths, err := r.bundlePaths()
	if err != nil {
		return Catalog{}, err
	}
	catalog := Catalog{
		Benchmarks: benchmarkCatalog(),
	}
	if r.preparedRoot != "" {
		if err := loadPreparedCatalog(r.preparedRoot, &catalog); err != nil {
			return Catalog{}, err
		}
	}
	solutions := make(map[string]SolutionVersion)
	executed := make(map[string]struct{})
	for _, path := range paths {
		bundle, err := loadBundle(path)
		if err != nil {
			return Catalog{}, err
		}
		appendBundle(&catalog, solutions, executed, filepath.Dir(path), bundle)
	}
	for _, solution := range solutions {
		catalog.Solutions = append(catalog.Solutions, solution)
	}
	finalizeDatasetFamilies(&catalog)
	for index := range catalog.Benchmarks {
		_, catalog.Benchmarks[index].Executed = executed[catalog.Benchmarks[index].ID]
	}
	sortCatalog(&catalog)
	return catalog, nil
}

func (r *Registry) OpenArtifactView(
	ctx context.Context,
	artifactID,
	view string,
	pagePath string,
) ([]byte, string, error) {
	catalog, err := r.Load(ctx)
	if err != nil {
		return nil, "", err
	}
	for _, artifact := range catalog.Artifacts {
		if artifact.Record.ArtifactID != artifactID {
			continue
		}
		relative, exists := artifact.Views[view]
		if !exists {
			return nil, "", fmt.Errorf("%w: artifact %s view %s", knowledgeeval.ErrNotFound, artifactID, view)
		}
		if view != "native" || strings.TrimSpace(pagePath) == "" {
			content, contentType, readErr := readView(artifact.bundleDirectory, relative)
			if readErr != nil {
				return nil, "", readErr
			}
			if view == "native" {
				content = rewriteNativeLinks(content, artifactID)
			}
			return content, contentType, nil
		}
		content, contentType, renderErr := renderArtifactPage(artifact, pagePath)
		if renderErr != nil {
			return nil, "", renderErr
		}
		return rewriteNativeLinks(content, artifactID), contentType, nil
	}
	return nil, "", fmt.Errorf("%w: artifact %s", knowledgeeval.ErrNotFound, artifactID)
}

func (r *Registry) GetDataset(
	ctx context.Context,
	dataset,
	partition,
	caseID string,
) (Dataset, []string, error) {
	catalog, err := r.Load(ctx)
	if err != nil {
		return Dataset{}, nil, err
	}
	for _, item := range catalog.Datasets {
		if item.Name != dataset || item.Partition != partition || item.CaseID != caseID {
			continue
		}
		var runIDs []string
		for _, run := range catalog.Runs {
			if run.Dataset == dataset && run.Partition == partition && run.CaseID == caseID {
				runIDs = append(runIDs, run.Detail.Run.ID)
			}
		}
		sort.Strings(runIDs)
		return item, runIDs, nil
	}
	return Dataset{}, nil, fmt.Errorf(
		"%w: dataset %s/%s/%s",
		knowledgeeval.ErrNotFound,
		dataset,
		partition,
		caseID,
	)
}

func (r *Registry) ListDatasetSessions(
	ctx context.Context,
	dataset,
	partition,
	caseID string,
) ([]DatasetSession, error) {
	item, _, err := r.GetDataset(ctx, dataset, partition, caseID)
	if err != nil {
		return nil, err
	}
	if item.SourceArtifact == nil {
		return nil, fmt.Errorf(
			"%w: dataset %s has no source artifact",
			knowledgeeval.ErrNotFound,
			item.ID,
		)
	}
	root, err := artifactTreeRoot(*item.SourceArtifact)
	if err != nil {
		return nil, err
	}
	sourceRoot := filepath.Join(root, "sources")
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: dataset %s sources", knowledgeeval.ErrNotFound, item.ID)
		}
		return nil, fmt.Errorf("list dataset %s sources: %w", item.ID, err)
	}
	sessions := make([]DatasetSession, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
			!strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		sourcePath := filepath.ToSlash(filepath.Join("sources", entry.Name()))
		content, readErr := os.ReadFile(filepath.Join(sourceRoot, entry.Name()))
		if readErr != nil {
			return nil, fmt.Errorf("read dataset source %s: %w", sourcePath, readErr)
		}
		session, parseErr := parseDatasetSession(sourcePath, content)
		if parseErr != nil {
			return nil, parseErr
		}
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(left, right int) bool {
		return sessions[left].ID < sessions[right].ID
	})
	return sessions, nil
}

func (r *Registry) OpenDatasetSession(
	ctx context.Context,
	dataset,
	partition,
	caseID,
	sessionID string,
) ([]byte, string, error) {
	item, _, err := r.GetDataset(ctx, dataset, partition, caseID)
	if err != nil {
		return nil, "", err
	}
	if item.SourceArtifact == nil {
		return nil, "", fmt.Errorf("%w: dataset %s source artifact", knowledgeeval.ErrNotFound, item.ID)
	}
	sessions, err := r.ListDatasetSessions(ctx, dataset, partition, caseID)
	if err != nil {
		return nil, "", err
	}
	for _, session := range sessions {
		if session.ID != sessionID {
			continue
		}
		content, contentType, renderErr := renderArtifactPage(*item.SourceArtifact, session.SourcePath)
		if renderErr != nil {
			return nil, "", renderErr
		}
		return rewriteNativeLinks(
			content,
			item.SourceArtifact.Record.ArtifactID,
		), contentType, nil
	}
	return nil, "", fmt.Errorf(
		"%w: dataset %s session %s",
		knowledgeeval.ErrNotFound,
		item.ID,
		sessionID,
	)
}

func (r *Registry) bundlePaths() ([]string, error) {
	var paths []string
	err := filepath.WalkDir(r.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || entry.Name() != "dataset-run.json" {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan knowledge eval registry: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}

func loadBundle(path string) (demo.SessionDatasetBundle, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return demo.SessionDatasetBundle{}, fmt.Errorf("read knowledge eval bundle %s: %w", path, err)
	}
	var bundle demo.SessionDatasetBundle
	if err := json.Unmarshal(content, &bundle); err != nil {
		return demo.SessionDatasetBundle{}, fmt.Errorf("decode knowledge eval bundle %s: %w", path, err)
	}
	if strings.TrimSpace(bundle.Dataset) == "" ||
		strings.TrimSpace(bundle.Partition) == "" ||
		strings.TrimSpace(bundle.CaseID) == "" {
		return demo.SessionDatasetBundle{}, fmt.Errorf(
			"decode knowledge eval bundle %s: %w: dataset identity is incomplete",
			path,
			knowledgeeval.ErrInvalidRecord,
		)
	}
	return bundle, nil
}

func appendBundle(
	catalog *Catalog,
	solutions map[string]SolutionVersion,
	executed map[string]struct{},
	bundleDirectory string,
	bundle demo.SessionDatasetBundle,
) {
	datasetID := datasetID(bundle.Dataset, bundle.Partition, bundle.CaseID)
	sourceSummary := bundle.Artifact
	if sourceSummary.Artifact.ArtifactID == "" {
		for _, arm := range bundle.Arms {
			if arm.Artifact != nil {
				sourceSummary = *arm.Artifact
				if arm.Role == "baseline" {
					break
				}
			}
		}
	}
	var sourceArtifact *Artifact
	if sourceSummary.Artifact.ArtifactID != "" {
		sourceArtifact = &Artifact{
			Record: sourceSummary.Artifact, Product: sourceSummary.Product,
			Role: "dataset-source", DatasetID: datasetID,
			Views: cloneViews(sourceSummary.Views), bundleDirectory: bundleDirectory,
		}
	}
	group := Dataset{
		ID: datasetID, Name: bundle.Dataset, Partition: bundle.Partition,
		CaseID: bundle.CaseID, GroupKind: inferGroupKind(bundle.Dataset, ""),
		Status:   bundle.BuildStatus,
		Sessions: bundle.Ingest.Sessions, Turns: bundle.Ingest.Turns,
		Sources: bundle.Ingest.Sources, Questions: bundle.Questions,
		EvaluationCases: bundle.Questions,
		RunCount:        len(bundle.Query.Runs), ArtifactCount: artifactCount(bundle.Arms),
		ExperimentCount: len(bundle.Arms), UpdatedAt: bundle.GeneratedAt,
		CaseIDs:        []string{bundle.CaseID},
		SourceArtifact: sourceArtifact,
	}
	upsertDatasetGroup(catalog, group)
	details := make(map[string]knowledgeeval.RunDetail, len(bundle.Query.Runs))
	for _, detail := range bundle.Query.Runs {
		details[detail.Run.ID] = detail
		for _, trial := range detail.Trials {
			executed[trial.BenchmarkID] = struct{}{}
		}
	}
	for _, arm := range bundle.Arms {
		if arm.Artifact == nil {
			continue
		}
		provenance := arm.Artifact.Artifact.Provenance
		solution := solutionVersion(provenance)
		solutions[solution.ID] = solution
		artifact := Artifact{
			Record: arm.Artifact.Artifact, Product: arm.Artifact.Product,
			Role: arm.Role, DatasetID: datasetID, SolutionVersionID: solution.ID,
			Views: cloneViews(arm.Artifact.Views), bundleDirectory: bundleDirectory,
		}
		catalog.Artifacts = append(catalog.Artifacts, artifact)
		detail, exists := details[arm.RunID]
		if !exists {
			continue
		}
		if detail.Run.Metadata == nil {
			detail.Run.Metadata = make(map[string]string)
		}
		if model := provenance.Metadata["model"]; model != "" {
			if _, exists := detail.Run.Metadata["model"]; !exists {
				detail.Run.Metadata["model"] = model
			}
		}
		catalog.Runs = append(catalog.Runs, Run{
			DatasetID: datasetID, Dataset: bundle.Dataset, Partition: bundle.Partition,
			CaseID: bundle.CaseID, SolutionVersion: solution, Detail: detail,
			ArtifactID: artifact.Record.ArtifactID, Artifact: &artifact,
			BenchmarkIDs: benchmarkIDs(detail.Trials),
		})
	}
}

func artifactCount(arms []demo.SessionDatasetArm) int {
	count := 0
	for _, arm := range arms {
		if arm.Artifact != nil {
			count++
		}
	}
	return count
}

func solutionVersion(provenance knowledgeeval.Provenance) SolutionVersion {
	model := provenance.Metadata["model"]
	idParts := []string{
		provenance.BuilderID,
		provenance.BuilderVersion,
		provenance.CodeRevision,
		provenance.ConfigDigest,
	}
	return SolutionVersion{
		ID: strings.Join(idParts, ":"), BuilderID: provenance.BuilderID,
		BuilderVersion: provenance.BuilderVersion, CodeRevision: provenance.CodeRevision,
		Model: model, ConfigDigest: provenance.ConfigDigest,
	}
}

func benchmarkIDs(trials []knowledgeeval.Trial) []string {
	ids := make([]string, 0, len(trials))
	for _, trial := range trials {
		ids = append(ids, trial.BenchmarkID)
	}
	sort.Strings(ids)
	return ids
}

func benchmarkCatalog() []Benchmark {
	return []Benchmark{
		{
			ID: "knowledge-search-get-qa", Name: "Search / Get QA",
			Description:   "Measures artifact support, retrieval, reader, and final answer accuracy.",
			PrimaryMetric: "answer_accuracy",
		},
		{
			ID: "wiki-artifact-quality", Name: "Wiki artifact quality",
			Description:   "Measures document structure, citations, links, and coverage.",
			PrimaryMetric: "artifact_quality_score",
		},
		{
			ID: "knowledge-tester-agent", Name: "Tester agent",
			Description:   "Measures task completion through subject tool use.",
			PrimaryMetric: "task_success_rate",
		},
	}
}

func readView(bundleDirectory, relative string) ([]byte, string, error) {
	if strings.TrimSpace(relative) == "" || filepath.IsAbs(relative) {
		return nil, "", fmt.Errorf("%w: artifact view path is invalid", knowledgeeval.ErrInvalidRecord)
	}
	target := filepath.Join(bundleDirectory, filepath.FromSlash(relative))
	within, err := filepath.Rel(bundleDirectory, target)
	if err != nil {
		return nil, "", fmt.Errorf("resolve artifact view: %w", err)
	}
	if within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("%w: artifact view escapes bundle", knowledgeeval.ErrInvalidRecord)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("%w: artifact view", knowledgeeval.ErrNotFound)
		}
		return nil, "", fmt.Errorf("read artifact view: %w", err)
	}
	contentType := mime.TypeByExtension(filepath.Ext(target))
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	return content, contentType, nil
}

func renderArtifactPage(artifact Artifact, pagePath string) ([]byte, string, error) {
	if artifact.Record.Kind != "llmwiki-workspace" {
		return nil, "", fmt.Errorf(
			"%w: artifact %s does not support page navigation",
			knowledgeeval.ErrInvalidRecord,
			artifact.Record.ArtifactID,
		)
	}
	treeRoot, err := artifactTreeRoot(artifact)
	if err != nil {
		return nil, "", err
	}
	targetURL := (&url.URL{Path: "/" + strings.TrimPrefix(pagePath, "/")}).String()
	recorder := httptest.NewRecorder()
	workspace.NewViewer(treeRoot).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, targetURL, nil),
	)
	switch recorder.Code {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, "", fmt.Errorf(
			"%w: artifact %s page %s",
			knowledgeeval.ErrNotFound,
			artifact.Record.ArtifactID,
			pagePath,
		)
	default:
		return nil, "", fmt.Errorf(
			"%w: artifact %s page %s returned status %d",
			knowledgeeval.ErrInvalidRecord,
			artifact.Record.ArtifactID,
			pagePath,
			recorder.Code,
		)
	}
	contentType := recorder.Header().Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(recorder.Body.Bytes())
	}
	return recorder.Body.Bytes(), contentType, nil
}

func artifactTreeRoot(artifact Artifact) (string, error) {
	digest := artifact.Record.Payload.SHA256
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 {
		return "", fmt.Errorf(
			"%w: artifact %s payload digest is invalid",
			knowledgeeval.ErrInvalidRecord,
			artifact.Record.ArtifactID,
		)
	}
	treeRoot := filepath.Join(artifact.bundleDirectory, "artifacts", digest, "tree")
	info, err := os.Stat(treeRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: artifact %s tree", knowledgeeval.ErrNotFound, artifact.Record.ArtifactID)
		}
		return "", fmt.Errorf("inspect artifact %s tree: %w", artifact.Record.ArtifactID, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf(
			"%w: artifact %s tree is not a directory",
			knowledgeeval.ErrInvalidRecord,
			artifact.Record.ArtifactID,
		)
	}
	return treeRoot, nil
}

var (
	sessionIDPattern = regexp.MustCompile("(?m)^- Session: `([^`]+)`$")
	turnRangePattern = regexp.MustCompile("(?m)^- Turn range: `\\[(\\d+),(\\d+)\\)`$")
)

func parseDatasetSession(sourcePath string, content []byte) (DatasetSession, error) {
	sessionMatch := sessionIDPattern.FindSubmatch(content)
	turnMatch := turnRangePattern.FindSubmatch(content)
	if len(sessionMatch) != 2 || len(turnMatch) != 3 {
		return DatasetSession{}, fmt.Errorf(
			"%w: dataset source %s header is invalid",
			knowledgeeval.ErrInvalidRecord,
			sourcePath,
		)
	}
	start, startErr := strconv.Atoi(string(turnMatch[1]))
	end, endErr := strconv.Atoi(string(turnMatch[2]))
	if startErr != nil || endErr != nil || end < start {
		return DatasetSession{}, fmt.Errorf(
			"%w: dataset source %s turn range is invalid",
			knowledgeeval.ErrInvalidRecord,
			sourcePath,
		)
	}
	return DatasetSession{
		ID: string(sessionMatch[1]), SourcePath: sourcePath, Turns: end - start,
	}, nil
}

func rewriteNativeLinks(content []byte, artifactID string) []byte {
	base := "/v1/knowledge-eval/artifacts/" +
		url.PathEscape(artifactID) +
		"/views/native"
	rewritten := bytes.ReplaceAll(content, []byte(`href="/"`), []byte(`href="`+base+`"`))
	rewritten = bytes.ReplaceAll(
		rewritten,
		[]byte(`href="/wiki/`),
		[]byte(`href="`+base+`?path=wiki/`),
	)
	return bytes.ReplaceAll(
		rewritten,
		[]byte(`href="/sources/`),
		[]byte(`href="`+base+`?path=sources/`),
	)
}

func datasetID(dataset, partition, caseID string) string {
	return strings.Join([]string{dataset, partition, caseID}, "/")
}

func cloneViews(views map[string]string) map[string]string {
	cloned := make(map[string]string, len(views))
	for key, value := range views {
		cloned[key] = value
	}
	return cloned
}

func sortCatalog(catalog *Catalog) {
	sort.Slice(catalog.Families, func(left, right int) bool {
		return catalog.Families[left].ID < catalog.Families[right].ID
	})
	sort.Slice(catalog.Datasets, func(left, right int) bool {
		leftGroup := catalog.Datasets[left]
		rightGroup := catalog.Datasets[right]
		if leftGroup.Name != rightGroup.Name {
			return leftGroup.Name < rightGroup.Name
		}
		if leftGroup.Partition != rightGroup.Partition {
			return leftGroup.Partition < rightGroup.Partition
		}
		return leftGroup.CaseID < rightGroup.CaseID
	})
	sort.Slice(catalog.Runs, func(left, right int) bool {
		return catalog.Runs[left].Detail.Run.CreatedAt.After(catalog.Runs[right].Detail.Run.CreatedAt)
	})
	sort.Slice(catalog.Artifacts, func(left, right int) bool {
		return catalog.Artifacts[left].Record.CreatedAt.After(catalog.Artifacts[right].Record.CreatedAt)
	})
	sort.Slice(catalog.Solutions, func(left, right int) bool {
		return catalog.Solutions[left].ID < catalog.Solutions[right].ID
	})
}
