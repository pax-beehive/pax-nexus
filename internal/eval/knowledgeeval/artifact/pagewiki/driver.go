package pagewiki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	domain "github.com/pax-beehive/pax-nexus/internal/pagewiki"
)

const (
	ArtifactKind   = "pagewiki-snapshot"
	ArtifactSchema = "pax.pagewiki.snapshot.v1"
	DriverID       = "pagewiki"
	DriverVersion  = "v1"
)

type Snapshot struct {
	Pages     []domain.Page         `json:"pages"`
	Revisions []domain.PageRevision `json:"revisions"`
	TopicTree domain.TopicTree      `json:"topic_tree"`
}

type Driver struct {
	store *knowledgeeval.ArtifactStore
	now   func() time.Time
}

func NewDriver(store *knowledgeeval.ArtifactStore, now func() time.Time) (*Driver, error) {
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
		ArtifactKinds: []string{ArtifactKind}, SchemaVersions: []string{ArtifactSchema},
		Capabilities: knowledgeeval.CapabilitySet{
			{Name: knowledgeeval.WikiCorpusCapability, Version: "v1"},
			{Name: knowledgeeval.SearchCapability, Version: "v1"},
			{Name: knowledgeeval.GetCapability, Version: "v1"},
			{Name: knowledgeeval.NavigateCapability, Version: "v1"},
		},
	}
}

func (d *Driver) Publish(
	ctx context.Context,
	snapshot Snapshot,
	group knowledgeeval.BenchmarkGroup,
	provenance knowledgeeval.Provenance,
) (knowledgeeval.ArtifactRecord, error) {
	if err := validateSnapshot(snapshot); err != nil {
		return knowledgeeval.ArtifactRecord{}, err
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, fmt.Errorf("encode PageWiki snapshot: %w", err)
	}
	payload, err := d.store.PutBytes(ctx, ArtifactKind, ArtifactSchema, encoded)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, fmt.Errorf("store PageWiki snapshot: %w", err)
	}
	return knowledgeeval.ArtifactRecord{
		ArtifactID: stableID(group.GroupID, group.CheckpointID, payload.SHA256),
		Kind:       ArtifactKind, WorldID: group.WorldID, GroupID: group.GroupID,
		CheckpointID: group.CheckpointID, Payload: payload,
		Provenance: provenance, CreatedAt: d.now(),
	}, nil
}

func (d *Driver) Open(
	ctx context.Context,
	artifact knowledgeeval.ArtifactRecord,
) (knowledgeeval.Subject, error) {
	if err := artifact.Validate(); err != nil {
		return nil, err
	}
	if artifact.Kind != ArtifactKind || artifact.Payload.SchemaVersion != ArtifactSchema {
		return nil, fmt.Errorf("%w: unsupported PageWiki artifact", knowledgeeval.ErrInvalidRecord)
	}
	encoded, err := d.store.OpenBytes(ctx, artifact.Payload)
	if err != nil {
		return nil, fmt.Errorf("open PageWiki snapshot: %w", err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return nil, fmt.Errorf("decode PageWiki snapshot: %w", err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return nil, err
	}
	return &subject{
		id: artifact.ArtifactID, store: d.store, snapshot: snapshot,
		capabilities: d.Descriptor().Capabilities,
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
			"%w: PageWiki driver returned an unexpected subject",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	var content []byte
	switch request.Kind {
	case "native", "canonical":
		content = renderSnapshot(current.snapshot)
	case "raw":
		content, err = json.MarshalIndent(current.snapshot, "", "  ")
		if err != nil {
			return knowledgeeval.ArtifactViewRecord{}, fmt.Errorf("encode raw PageWiki view: %w", err)
		}
		content = []byte("<!doctype html><html><body><pre>" +
			html.EscapeString(string(content)) + "</pre></body></html>")
	default:
		return knowledgeeval.ArtifactViewRecord{}, fmt.Errorf(
			"%w: unsupported PageWiki view %q",
			knowledgeeval.ErrInvalidRecord,
			request.Kind,
		)
	}
	payload, err := d.store.PutBytes(
		ctx, "artifact-view/html", "pax.knowledge-eval.artifact-view.v1", content,
	)
	if err != nil {
		return knowledgeeval.ArtifactViewRecord{}, fmt.Errorf("store PageWiki view: %w", err)
	}
	return knowledgeeval.ArtifactViewRecord{
		ViewID:     stableID(request.Artifact.ArtifactID, request.Kind, payload.SHA256),
		ArtifactID: request.Artifact.ArtifactID, Kind: request.Kind,
		RendererID: DriverID, RendererVersion: DriverVersion,
		Payload: payload, EntryPoint: payload.URI, CreatedAt: d.now(),
	}, nil
}

type subject struct {
	id           string
	store        *knowledgeeval.ArtifactStore
	snapshot     Snapshot
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
	corpus := s.corpus()
	encoded, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		return knowledgeeval.ProjectionResponse{}, fmt.Errorf("encode PageWiki corpus: %w", err)
	}
	payload, err := s.store.PutBytes(ctx, "wiki-corpus", "pax.knowledge-eval.wiki-corpus.v1", encoded)
	if err != nil {
		return knowledgeeval.ProjectionResponse{}, fmt.Errorf("store PageWiki corpus: %w", err)
	}
	return knowledgeeval.ProjectionResponse{Payload: payload}, nil
}

func (s *subject) Search(
	_ context.Context,
	request knowledgeeval.SearchRequest,
) (knowledgeeval.SearchResponse, error) {
	queryTerms := terms(request.Query)
	if len(queryTerms) == 0 {
		return knowledgeeval.SearchResponse{}, fmt.Errorf("%w: query is required", knowledgeeval.ErrInvalidRecord)
	}
	hits := make([]knowledgeeval.SearchHit, 0)
	for _, document := range s.corpus().Documents {
		score := overlap(queryTerms, terms(document.Title+" "+document.Body))
		if score == 0 {
			continue
		}
		text := document.Title + "\n" + document.Body
		hits = append(hits, knowledgeeval.SearchHit{
			Ref: document.Ref, Text: text, Score: score, Tokens: estimateTokens(text),
		})
	}
	sort.Slice(hits, func(left, right int) bool {
		if hits[left].Score == hits[right].Score {
			return hits[left].Ref < hits[right].Ref
		}
		return hits[left].Score > hits[right].Score
	})
	hits = selectHits(hits, request.MaxItems, request.TokenBudget)
	trace, err := json.Marshal(map[string]int{"selected": len(hits)})
	if err != nil {
		return knowledgeeval.SearchResponse{}, fmt.Errorf("encode PageWiki search trace: %w", err)
	}
	return knowledgeeval.SearchResponse{Hits: hits, Trace: trace}, nil
}

func (s *subject) Get(
	_ context.Context,
	request knowledgeeval.GetRequest,
) (knowledgeeval.GetResponse, error) {
	for _, document := range s.corpus().Documents {
		if document.Ref == request.Ref {
			return knowledgeeval.GetResponse{
				Ref: document.Ref, Text: document.Title + "\n" + document.Body,
			}, nil
		}
	}
	return knowledgeeval.GetResponse{}, fmt.Errorf("%w: PageWiki page %s", knowledgeeval.ErrNotFound, request.Ref)
}

func (s *subject) Navigate(
	_ context.Context,
	request knowledgeeval.NavigateRequest,
) (knowledgeeval.NavigateResponse, error) {
	pages := make(map[string]domain.Page, len(s.snapshot.Pages))
	for _, page := range s.snapshot.Pages {
		pages[page.ID] = page
	}
	roots := make([]knowledgeeval.NavigationNode, 0)
	for _, topic := range s.snapshot.TopicTree.Topics {
		if request.Ref != "" && topic.ID != request.Ref {
			continue
		}
		node := knowledgeeval.NavigationNode{Ref: topic.ID, Title: topic.Title}
		for _, placement := range s.snapshot.TopicTree.Placements {
			if placement.TopicID != topic.ID {
				continue
			}
			page := pages[placement.PageID]
			node.Children = append(node.Children, knowledgeeval.NavigationNode{
				Ref: page.Slug, Title: page.Title,
			})
		}
		roots = append(roots, node)
	}
	if request.Ref != "" && len(roots) == 0 {
		return knowledgeeval.NavigateResponse{}, fmt.Errorf("%w: PageWiki topic %s", knowledgeeval.ErrNotFound, request.Ref)
	}
	return knowledgeeval.NavigateResponse{Roots: roots}, nil
}

func (s *subject) corpus() knowledgeeval.WikiCorpus {
	revisions := make(map[string]domain.PageRevision, len(s.snapshot.Revisions))
	for _, revision := range s.snapshot.Revisions {
		revisions[revision.ID] = revision
	}
	pages := make(map[string]domain.Page, len(s.snapshot.Pages))
	for _, page := range s.snapshot.Pages {
		pages[page.ID] = page
	}
	documents := make([]knowledgeeval.WikiDocument, 0, len(s.snapshot.Pages))
	for _, page := range s.snapshot.Pages {
		revision := revisions[page.CurrentRevisionID]
		links := make([]string, 0, len(revision.Links))
		for _, link := range revision.Links {
			links = append(links, pages[link.TargetPageID].Slug)
		}
		citations := make([]string, 0, len(revision.Citations))
		for _, citation := range revision.Citations {
			for _, anchor := range citation.SourceAnchors {
				citations = append(citations, anchor.SourceRevisionID+"#"+anchor.ID)
			}
		}
		documents = append(documents, knowledgeeval.WikiDocument{
			Ref: page.Slug, Title: revision.Title, Body: revision.Markdown,
			Links: links, Citations: citations,
			Metadata: map[string]string{"page_id": page.ID, "revision_id": revision.ID},
		})
	}
	sort.Slice(documents, func(left, right int) bool {
		return documents[left].Ref < documents[right].Ref
	})
	return knowledgeeval.WikiCorpus{
		SchemaVersion: "pax.knowledge-eval.wiki-corpus.v1",
		Documents:     documents,
	}
}

func validateSnapshot(snapshot Snapshot) error {
	if len(snapshot.Pages) == 0 {
		return fmt.Errorf("%w: PageWiki snapshot requires pages", knowledgeeval.ErrInvalidRecord)
	}
	revisions := make(map[string]domain.PageRevision, len(snapshot.Revisions))
	for _, revision := range snapshot.Revisions {
		revisions[revision.ID] = revision
	}
	for _, page := range snapshot.Pages {
		if strings.TrimSpace(page.ID) == "" || strings.TrimSpace(page.Slug) == "" {
			return fmt.Errorf("%w: PageWiki page ID and slug are required", knowledgeeval.ErrInvalidRecord)
		}
		revision, exists := revisions[page.CurrentRevisionID]
		if !exists || revision.PageID != page.ID {
			return fmt.Errorf("%w: PageWiki page %s has no current revision", knowledgeeval.ErrInvalidRecord, page.ID)
		}
	}
	return nil
}

func renderSnapshot(snapshot Snapshot) []byte {
	var output strings.Builder
	output.WriteString("<!doctype html><html><body><h1>PageWiki</h1>")
	revisions := make(map[string]domain.PageRevision, len(snapshot.Revisions))
	for _, revision := range snapshot.Revisions {
		revisions[revision.ID] = revision
	}
	for _, page := range snapshot.Pages {
		revision := revisions[page.CurrentRevisionID]
		fmt.Fprintf(
			&output,
			"<article><h2>%s</h2><p>%s</p><pre>%s</pre></article>",
			html.EscapeString(revision.Title),
			html.EscapeString(revision.Summary),
			html.EscapeString(revision.Markdown),
		)
	}
	output.WriteString("</body></html>")
	return []byte(output.String())
}

func terms(value string) map[string]struct{} {
	result := make(map[string]struct{})
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			result[current.String()] = struct{}{}
			current.Reset()
		}
	}
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			current.WriteRune(character)
		} else {
			flush()
		}
	}
	flush()
	return result
}

func overlap(query, document map[string]struct{}) float64 {
	matches := 0
	for term := range query {
		if _, exists := document[term]; exists {
			matches++
		}
	}
	if len(query) == 0 {
		return 0
	}
	return float64(matches) / float64(len(query))
}

func estimateTokens(value string) int {
	count := len(strings.Fields(value))
	if count == 0 {
		return 1
	}
	return count
}

func selectHits(
	hits []knowledgeeval.SearchHit,
	maxItems,
	tokenBudget int,
) []knowledgeeval.SearchHit {
	if maxItems <= 0 || maxItems > len(hits) {
		maxItems = len(hits)
	}
	selected := make([]knowledgeeval.SearchHit, 0, maxItems)
	used := 0
	for _, hit := range hits {
		if len(selected) == maxItems {
			break
		}
		if tokenBudget > 0 && used+hit.Tokens > tokenBudget {
			continue
		}
		selected = append(selected, hit)
		used += hit.Tokens
	}
	return selected
}

func stableID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}
