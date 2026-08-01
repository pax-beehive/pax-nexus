package llmwiki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
)

const (
	ArtifactKind     = "llmwiki-workspace"
	ArtifactSchema   = "pax.llmwiki.workspace.v1"
	DriverID         = "llmwiki-filesystem"
	DriverVersion    = "v1"
	RendererID       = "llmwiki-html"
	RendererVersion  = "v1"
	WikiCorpusSchema = "pax.knowledge-eval.wiki-corpus.v1"
)

var (
	linkPattern     = regexp.MustCompile(`\[[^\]]+\]\(([^)#\s]+)(?:#[^)\s]+)?\)`)
	citationPattern = regexp.MustCompile(`\[[^\]]+\]\(([^)\s]*sources/[^)\s]+)\)`)
)

type Driver struct {
	store *knowledgeeval.ArtifactStore
	now   func() time.Time
}

func NewDriver(
	store *knowledgeeval.ArtifactStore,
	now func() time.Time,
) (*Driver, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: artifact store is required", knowledgeeval.ErrInvalidRecord)
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Driver{store: store, now: now}, nil
}

func (d *Driver) Descriptor() knowledgeeval.ArtifactDriverDescriptor {
	return knowledgeeval.ArtifactDriverDescriptor{
		ID: DriverID, Version: DriverVersion,
		ArtifactKinds:  []string{ArtifactKind},
		SchemaVersions: []string{ArtifactSchema},
		Capabilities: knowledgeeval.CapabilitySet{
			{Name: knowledgeeval.WikiCorpusCapability, Version: "v1"},
			{Name: knowledgeeval.SearchCapability, Version: "v1"},
			{Name: knowledgeeval.GetCapability, Version: "v1"},
			{Name: knowledgeeval.NavigateCapability, Version: "v1"},
		},
	}
}

func (d *Driver) Open(
	ctx context.Context,
	artifact knowledgeeval.ArtifactRecord,
) (knowledgeeval.Subject, error) {
	if err := artifact.Validate(); err != nil {
		return nil, err
	}
	if artifact.Kind != ArtifactKind ||
		artifact.Payload.Kind != ArtifactKind ||
		artifact.Payload.SchemaVersion != ArtifactSchema {
		return nil, fmt.Errorf(
			"%w: unsupported LLM Wiki artifact %s/%s",
			knowledgeeval.ErrInvalidRecord,
			artifact.Kind,
			artifact.Payload.SchemaVersion,
		)
	}
	root, err := d.store.OpenDirectory(ctx, artifact.Payload)
	if err != nil {
		return nil, fmt.Errorf("open LLM Wiki artifact: %w", err)
	}
	if report := workspace.Validate(root); !report.Valid {
		return nil, fmt.Errorf(
			"%w: LLM Wiki validation failed: %s",
			knowledgeeval.ErrInvalidRecord,
			report.String(),
		)
	}
	corpus, err := loadCorpus(root)
	if err != nil {
		return nil, err
	}
	return &subject{
		id: artifact.ArtifactID, root: root, store: d.store,
		corpus: corpus, capabilities: d.Descriptor().Capabilities,
	}, nil
}

func (d *Driver) RenderView(
	ctx context.Context,
	request knowledgeeval.ArtifactViewRequest,
) (knowledgeeval.ArtifactViewRecord, error) {
	opened, err := d.Open(ctx, request.Artifact)
	if err != nil {
		return knowledgeeval.ArtifactViewRecord{}, err
	}
	current, ok := opened.(*subject)
	if !ok {
		return knowledgeeval.ArtifactViewRecord{}, fmt.Errorf(
			"%w: LLM Wiki driver returned an unexpected subject",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	var content []byte
	switch request.Kind {
	case "native":
		recorder := httptest.NewRecorder()
		workspace.NewViewer(current.root).ServeHTTP(
			recorder,
			httptest.NewRequest("GET", "/", nil),
		)
		if recorder.Code != 200 {
			return knowledgeeval.ArtifactViewRecord{}, fmt.Errorf(
				"render native LLM Wiki view: status %d",
				recorder.Code,
			)
		}
		content = recorder.Body.Bytes()
	case "canonical":
		content = renderCorpus(current.corpus)
	case "raw":
		files, err := listFiles(current.root)
		if err != nil {
			return knowledgeeval.ArtifactViewRecord{}, err
		}
		content = renderFileList(files)
	case "diff":
		if request.BaseArtifact == nil {
			return knowledgeeval.ArtifactViewRecord{}, fmt.Errorf(
				"%w: diff view requires a base artifact",
				knowledgeeval.ErrInvalidRecord,
			)
		}
		baseOpened, err := d.Open(ctx, *request.BaseArtifact)
		if err != nil {
			return knowledgeeval.ArtifactViewRecord{}, fmt.Errorf("open diff base: %w", err)
		}
		base, ok := baseOpened.(*subject)
		if !ok {
			return knowledgeeval.ArtifactViewRecord{}, fmt.Errorf(
				"%w: LLM Wiki driver returned an unexpected base subject",
				knowledgeeval.ErrInvalidRecord,
			)
		}
		content, err = renderDiff(base.root, current.root)
		if err != nil {
			return knowledgeeval.ArtifactViewRecord{}, err
		}
	default:
		return knowledgeeval.ArtifactViewRecord{}, fmt.Errorf(
			"%w: unsupported LLM Wiki view %q",
			knowledgeeval.ErrInvalidRecord,
			request.Kind,
		)
	}
	payload, err := d.store.PutBytes(
		ctx,
		"artifact-view/html",
		"pax.knowledge-eval.artifact-view.v1",
		content,
	)
	if err != nil {
		return knowledgeeval.ArtifactViewRecord{}, err
	}
	return knowledgeeval.ArtifactViewRecord{
		ViewID:          stableID(request.Artifact.ArtifactID, request.Kind, payload.SHA256),
		ArtifactID:      request.Artifact.ArtifactID,
		Kind:            request.Kind,
		RendererID:      RendererID,
		RendererVersion: RendererVersion,
		Payload:         payload,
		EntryPoint:      payload.URI,
		CreatedAt:       d.now(),
	}, nil
}

type subject struct {
	id           string
	root         string
	store        *knowledgeeval.ArtifactStore
	corpus       knowledgeeval.WikiCorpus
	capabilities knowledgeeval.CapabilitySet
}

func (s *subject) ID() string {
	return s.id
}

func (s *subject) Capabilities() knowledgeeval.CapabilitySet {
	return s.capabilities.Clone()
}

func (s *subject) Project(
	ctx context.Context,
	request knowledgeeval.ProjectionRequest,
) (knowledgeeval.ProjectionResponse, error) {
	if request.Name != knowledgeeval.WikiCorpusCapability || request.Version != "v1" {
		return knowledgeeval.ProjectionResponse{}, fmt.Errorf(
			"%w: projection %s:%s",
			knowledgeeval.ErrCapabilityMissing,
			request.Name,
			request.Version,
		)
	}
	encoded, err := json.MarshalIndent(s.corpus, "", "  ")
	if err != nil {
		return knowledgeeval.ProjectionResponse{}, fmt.Errorf("encode Wiki corpus: %w", err)
	}
	ref, err := s.store.PutBytes(ctx, "wiki-corpus", WikiCorpusSchema, encoded)
	if err != nil {
		return knowledgeeval.ProjectionResponse{}, err
	}
	return knowledgeeval.ProjectionResponse{Payload: ref}, nil
}

func (s *subject) Search(
	_ context.Context,
	request knowledgeeval.SearchRequest,
) (knowledgeeval.SearchResponse, error) {
	queryTerms := terms(request.Query)
	if len(queryTerms) == 0 {
		return knowledgeeval.SearchResponse{}, fmt.Errorf(
			"%w: search query is required",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	hits := make([]knowledgeeval.SearchHit, 0, len(s.corpus.Documents))
	for _, document := range s.corpus.Documents {
		documentTerms := terms(document.Title + " " + document.Body)
		matches := 0
		for term := range queryTerms {
			if _, exists := documentTerms[term]; exists {
				matches++
			}
		}
		if matches == 0 {
			continue
		}
		text := document.Title + "\n" + document.Body
		metadata := map[string]string{"title": document.Title}
		for key, value := range document.Metadata {
			metadata[key] = value
		}
		hits = append(hits, knowledgeeval.SearchHit{
			Ref: document.Ref, Text: text,
			Score:    float64(matches) / float64(len(queryTerms)),
			Tokens:   estimateTokens(text),
			Metadata: metadata,
		})
	}
	sort.Slice(hits, func(left, right int) bool {
		if hits[left].Score == hits[right].Score {
			return hits[left].Ref < hits[right].Ref
		}
		return hits[left].Score > hits[right].Score
	})
	limit := request.MaxItems
	if limit <= 0 || limit > len(hits) {
		limit = len(hits)
	}
	selected := make([]knowledgeeval.SearchHit, 0, limit)
	used := 0
	for _, hit := range hits[:limit] {
		if request.TokenBudget > 0 && used+hit.Tokens > request.TokenBudget {
			continue
		}
		selected = append(selected, hit)
		used += hit.Tokens
	}
	trace, err := json.Marshal(map[string]int{
		"candidates": len(hits),
		"selected":   len(selected),
		"tokens":     used,
	})
	if err != nil {
		return knowledgeeval.SearchResponse{}, fmt.Errorf("encode search trace: %w", err)
	}
	return knowledgeeval.SearchResponse{Hits: selected, Trace: trace}, nil
}

func (s *subject) Get(
	_ context.Context,
	request knowledgeeval.GetRequest,
) (knowledgeeval.GetResponse, error) {
	for _, document := range s.corpus.Documents {
		if document.Ref == request.Ref {
			return knowledgeeval.GetResponse{
				Ref: document.Ref, Text: document.Body,
				Provenance: cloneMetadata(document.Title, document.Metadata),
			}, nil
		}
	}
	return knowledgeeval.GetResponse{}, fmt.Errorf(
		"%w: Wiki document %s",
		knowledgeeval.ErrNotFound,
		request.Ref,
	)
}

func (s *subject) Navigate(
	_ context.Context,
	_ knowledgeeval.NavigateRequest,
) (knowledgeeval.NavigateResponse, error) {
	roots := make([]knowledgeeval.NavigationNode, 0, len(s.corpus.Documents))
	for _, document := range s.corpus.Documents {
		roots = append(roots, knowledgeeval.NavigationNode{
			Ref: document.Ref, Title: document.Title,
		})
	}
	return knowledgeeval.NavigateResponse{Roots: roots}, nil
}

func loadCorpus(root string) (knowledgeeval.WikiCorpus, error) {
	turnByAnchor, err := loadTurnByAnchor(root)
	if err != nil {
		return knowledgeeval.WikiCorpus{}, err
	}
	wikiRoot := filepath.Join(root, "wiki")
	corpus := knowledgeeval.WikiCorpus{SchemaVersion: WikiCorpusSchema}
	err = filepath.WalkDir(wikiRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") ||
			entry.Name() == "log.md" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read Wiki page: %w", err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("resolve Wiki page ref: %w", err)
		}
		document := knowledgeeval.WikiDocument{
			Ref:   filepath.ToSlash(relative),
			Title: markdownTitle(content, entry.Name()),
			Body:  string(content),
		}
		for _, match := range linkPattern.FindAllSubmatch(content, -1) {
			link := string(match[1])
			if normalized, ok := normalizeWikiLink(document.Ref, link); ok {
				document.Links = append(document.Links, normalized)
			}
		}
		for _, match := range citationPattern.FindAllSubmatch(content, -1) {
			citation := string(match[1])
			document.Citations = append(document.Citations, citation)
			fragment := citationFragment(citation)
			if turnID := turnByAnchor[fragment]; turnID != "" {
				if document.Metadata == nil {
					document.Metadata = make(map[string]string)
				}
				document.Metadata["support_refs"] = appendCSV(
					document.Metadata["support_refs"],
					turnID,
				)
			}
		}
		corpus.Documents = append(corpus.Documents, document)
		return nil
	})
	if err != nil {
		return knowledgeeval.WikiCorpus{}, fmt.Errorf("load LLM Wiki corpus: %w", err)
	}
	sort.Slice(corpus.Documents, func(left, right int) bool {
		return corpus.Documents[left].Ref < corpus.Documents[right].Ref
	})
	return corpus, nil
}

func normalizeWikiLink(documentRef, link string) (string, bool) {
	lower := strings.ToLower(link)
	if strings.Contains(link, "sources/") ||
		strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:") {
		return "", false
	}
	normalized := path.Clean(path.Join(path.Dir(documentRef), link))
	if normalized == "wiki" || !strings.HasPrefix(normalized, "wiki/") {
		return "", false
	}
	return normalized, true
}

func loadTurnByAnchor(root string) (map[string]string, error) {
	encoded, err := os.ReadFile(filepath.Join(root, ".pax", "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read LLM Wiki manifest: %w", err)
	}
	var manifest workspace.Manifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return nil, fmt.Errorf("decode LLM Wiki manifest: %w", err)
	}
	result := make(map[string]string)
	for _, source := range manifest.Sources {
		for _, anchor := range source.Anchors {
			result[anchor.ID] = anchor.TurnID
		}
	}
	return result, nil
}

func citationFragment(citation string) string {
	_, fragment, found := strings.Cut(citation, "#")
	if !found {
		return ""
	}
	return fragment
}

func appendCSV(current, value string) string {
	for _, existing := range strings.Split(current, ",") {
		if existing == value {
			return current
		}
	}
	if current == "" {
		return value
	}
	return current + "," + value
}

func cloneMetadata(title string, metadata map[string]string) map[string]string {
	result := make(map[string]string, len(metadata)+1)
	result["title"] = title
	for key, value := range metadata {
		result[key] = value
	}
	return result
}

func markdownTitle(content []byte, fallback string) string {
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "# ") {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "# "))
		}
	}
	return fallback
}

func terms(value string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, term := range strings.FieldsFunc(strings.ToLower(value), func(current rune) bool {
		return !unicode.IsLetter(current) && !unicode.IsNumber(current)
	}) {
		if term != "" {
			result[term] = struct{}{}
		}
	}
	return result
}

func estimateTokens(value string) int {
	if value == "" {
		return 0
	}
	return (len([]rune(value)) + 3) / 4
}

func stableID(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:16])
}

func listFiles(root string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result = append(result, filepath.ToSlash(relative))
		return nil
	})
	sort.Strings(result)
	return result, err
}

func renderCorpus(corpus knowledgeeval.WikiCorpus) []byte {
	var content strings.Builder
	content.WriteString("<!doctype html><meta charset=\"utf-8\"><title>Canonical Wiki Corpus</title>")
	content.WriteString("<h1>Canonical Wiki Corpus</h1>")
	for _, document := range corpus.Documents {
		content.WriteString("<article><h2>")
		content.WriteString(html.EscapeString(document.Title))
		content.WriteString("</h2><pre>")
		content.WriteString(html.EscapeString(document.Body))
		content.WriteString("</pre></article>")
	}
	return []byte(content.String())
}

func renderFileList(files []string) []byte {
	var content strings.Builder
	content.WriteString("<!doctype html><meta charset=\"utf-8\"><title>Raw Artifact</title>")
	content.WriteString("<h1>Raw Artifact</h1><ul>")
	for _, file := range files {
		content.WriteString("<li><code>")
		content.WriteString(html.EscapeString(file))
		content.WriteString("</code></li>")
	}
	content.WriteString("</ul>")
	return []byte(content.String())
}

func renderDiff(baseRoot, currentRoot string) ([]byte, error) {
	base, err := fileDigests(filepath.Join(baseRoot, "wiki"))
	if err != nil {
		return nil, err
	}
	current, err := fileDigests(filepath.Join(currentRoot, "wiki"))
	if err != nil {
		return nil, err
	}
	paths := make(map[string]struct{}, len(base)+len(current))
	for path := range base {
		paths[path] = struct{}{}
	}
	for path := range current {
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	var content strings.Builder
	content.WriteString("<!doctype html><meta charset=\"utf-8\"><title>Wiki Diff</title>")
	content.WriteString("<h1>Wiki Diff</h1><ul>")
	for _, path := range ordered {
		status := "unchanged"
		switch {
		case base[path] == "":
			status = "added"
		case current[path] == "":
			status = "deleted"
		case base[path] != current[path]:
			status = "modified"
		}
		content.WriteString("<li><strong>")
		content.WriteString(status)
		content.WriteString("</strong> ")
		content.WriteString(html.EscapeString(path))
		content.WriteString("</li>")
	}
	content.WriteString("</ul>")
	return []byte(content.String()), nil
}

func fileDigests(root string) (map[string]string, error) {
	result := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(content)
		result[filepath.ToSlash(relative)] = hex.EncodeToString(digest[:])
		return nil
	})
	return result, err
}
