// Package memory provides an in-memory Page Wiki repository.
package memory

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
)

type Repository struct {
	mu              sync.RWMutex
	sourceRevisions map[string]pagewiki.SourceRevision
	pages           map[string]pagewiki.Page
	pageIDsBySlug   map[string]string
	pageRevisions   map[string]pagewiki.PageRevision
	topics          map[string]pagewiki.Topic
	placements      map[string]pagewiki.PagePlacement
	searchChunks    map[string]pagewiki.SearchChunk
	runs            map[string]pagewiki.MaintenanceRun
}

func NewRepository() *Repository {
	repository := &Repository{}
	repository.Reset()
	return repository
}

func (r *Repository) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sourceRevisions = make(map[string]pagewiki.SourceRevision)
	r.pages = make(map[string]pagewiki.Page)
	r.pageIDsBySlug = make(map[string]string)
	r.pageRevisions = make(map[string]pagewiki.PageRevision)
	r.topics = make(map[string]pagewiki.Topic)
	r.placements = make(map[string]pagewiki.PagePlacement)
	r.searchChunks = make(map[string]pagewiki.SearchChunk)
	r.runs = make(map[string]pagewiki.MaintenanceRun)
}

func (r *Repository) SaveSourceRevision(
	_ context.Context,
	revision pagewiki.SourceRevision,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, found := r.sourceRevisions[revision.ID]
	if found {
		if !sourceRevisionsEqual(existing, revision) {
			return fmt.Errorf("%w: SourceRevision %q", pagewiki.ErrImmutableConflict, revision.ID)
		}
		return nil
	}
	r.sourceRevisions[revision.ID] = cloneSourceRevision(revision)
	return nil
}

func (r *Repository) SourceRevision(
	_ context.Context,
	id string,
) (pagewiki.SourceRevision, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	revision, found := r.sourceRevisions[id]
	if !found {
		return pagewiki.SourceRevision{}, fmt.Errorf(
			"%w: SourceRevision %q",
			pagewiki.ErrNotFound,
			id,
		)
	}
	return cloneSourceRevision(revision), nil
}

func (r *Repository) PageCatalog(_ context.Context) (pagewiki.PageCatalog, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	catalog := make(pagewiki.PageCatalog, 0, len(r.pages))
	for _, page := range r.pages {
		catalog = append(catalog, pagewiki.PageCatalogEntry{
			ID:                page.ID,
			Slug:              page.Slug,
			Title:             page.Title,
			CurrentRevisionID: page.CurrentRevisionID,
			Summary:           r.pageRevisions[page.CurrentRevisionID].Summary,
		})
	}
	sort.Slice(catalog, func(i, j int) bool {
		return catalog[i].Slug < catalog[j].Slug
	})
	return catalog, nil
}

func (r *Repository) PageByID(_ context.Context, id string) (pagewiki.Page, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	page, found := r.pages[id]
	if !found {
		return pagewiki.Page{}, fmt.Errorf("%w: Page %q", pagewiki.ErrNotFound, id)
	}
	return page, nil
}

func (r *Repository) PageBySlug(_ context.Context, slug string) (pagewiki.Page, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, found := r.pageIDsBySlug[slug]
	if !found {
		return pagewiki.Page{}, fmt.Errorf("%w: Page slug %q", pagewiki.ErrNotFound, slug)
	}
	return r.pages[id], nil
}

func (r *Repository) PageRevision(
	_ context.Context,
	id string,
) (pagewiki.PageRevision, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	revision, found := r.pageRevisions[id]
	if !found {
		return pagewiki.PageRevision{}, fmt.Errorf(
			"%w: PageRevision %q",
			pagewiki.ErrNotFound,
			id,
		)
	}
	return clonePageRevision(revision), nil
}

func (r *Repository) PageRevisionHistory(
	_ context.Context,
	pageID string,
) ([]pagewiki.PageRevision, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	page, found := r.pages[pageID]
	if !found {
		return nil, fmt.Errorf("%w: Page %q", pagewiki.ErrNotFound, pageID)
	}
	history := make([]pagewiki.PageRevision, 0)
	revisionID := page.CurrentRevisionID
	seen := make(map[string]struct{})
	for revisionID != "" {
		if _, duplicate := seen[revisionID]; duplicate {
			return nil, fmt.Errorf(
				"%w: Page %q revision lineage is cyclic",
				pagewiki.ErrRevisionConflict,
				pageID,
			)
		}
		seen[revisionID] = struct{}{}
		revision, exists := r.pageRevisions[revisionID]
		if !exists || revision.PageID != pageID {
			return nil, fmt.Errorf(
				"%w: Page %q revision lineage is incomplete",
				pagewiki.ErrRevisionConflict,
				pageID,
			)
		}
		history = append(history, clonePageRevision(revision))
		revisionID = revision.BaseRevisionID
	}
	for left, right := 0, len(history)-1; left < right; left, right = left+1, right-1 {
		history[left], history[right] = history[right], history[left]
	}
	return history, nil
}

func (r *Repository) PublishPage(
	_ context.Context,
	publication pagewiki.PagePublication,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existingPage, pageFound := r.pages[publication.Page.ID]
	existingRevision, revisionFound := r.pageRevisions[publication.Revision.ID]
	if pageFound &&
		revisionFound &&
		reflect.DeepEqual(existingPage, publication.Page) &&
		reflect.DeepEqual(existingRevision, publication.Revision) {
		return nil
	}
	if err := r.validatePublication(publication); err != nil {
		return err
	}
	page := publication.Page
	revision := publication.Revision
	chunks := buildSearchChunks(revision)
	if previous, found := r.pages[page.ID]; found && previous.Slug != page.Slug {
		delete(r.pageIDsBySlug, previous.Slug)
	}
	r.pageRevisions[revision.ID] = clonePageRevision(revision)
	r.pages[page.ID] = page
	r.pageIDsBySlug[page.Slug] = page.ID
	for _, topic := range publication.Topics {
		r.topics[topic.ID] = topic
	}
	if publication.Placement != nil {
		r.placements[page.ID] = *publication.Placement
	}
	for id, chunk := range r.searchChunks {
		if chunk.PageID == page.ID {
			delete(r.searchChunks, id)
		}
	}
	for _, chunk := range chunks {
		r.searchChunks[chunk.ID] = chunk
	}
	return nil
}

func (r *Repository) validatePublication(publication pagewiki.PagePublication) error {
	page := publication.Page
	revision := publication.Revision
	if page.ID == "" || page.Slug == "" || revision.ID == "" {
		return fmt.Errorf("%w: Page identity is incomplete", pagewiki.ErrRevisionConflict)
	}
	if page.CurrentRevisionID != revision.ID || page.ID != revision.PageID {
		return fmt.Errorf("%w: Page and revision identities differ", pagewiki.ErrRevisionConflict)
	}
	if existingID, found := r.pageIDsBySlug[page.Slug]; found && existingID != page.ID {
		return fmt.Errorf("%w: slug %q is already published", pagewiki.ErrRevisionConflict, page.Slug)
	}
	if existing, found := r.pages[page.ID]; found {
		if existing.CurrentRevisionID != revision.BaseRevisionID {
			return fmt.Errorf("%w: Page %q base is stale", pagewiki.ErrRevisionConflict, page.ID)
		}
	} else if revision.BaseRevisionID != "" {
		return fmt.Errorf("%w: new Page %q has a base revision", pagewiki.ErrRevisionConflict, page.ID)
	}
	if existing, found := r.pageRevisions[revision.ID]; found &&
		!reflect.DeepEqual(existing, revision) {
		return fmt.Errorf(
			"%w: PageRevision %q",
			pagewiki.ErrImmutableConflict,
			revision.ID,
		)
	}
	if err := r.validateLinks(page, revision); err != nil {
		return err
	}
	topics, err := r.validateTopics(publication.Topics)
	if err != nil {
		return err
	}
	if publication.Placement != nil {
		placement := *publication.Placement
		if placement.PageID != page.ID || placement.Rank < 0 {
			return fmt.Errorf("%w: invalid Page placement", pagewiki.ErrRevisionConflict)
		}
		if _, found := topics[placement.TopicID]; !found {
			return fmt.Errorf(
				"%w: placement Topic %q is missing",
				pagewiki.ErrRevisionConflict,
				placement.TopicID,
			)
		}
	}
	return nil
}

func (r *Repository) validateLinks(
	page pagewiki.Page,
	revision pagewiki.PageRevision,
) error {
	sections := make(map[string]pagewiki.PageSection, len(revision.Sections))
	for _, section := range revision.Sections {
		sections[section.Key] = section
	}
	for _, link := range revision.Links {
		if link.PageRevisionID != revision.ID {
			return fmt.Errorf(
				"%w: Link revision identity differs",
				pagewiki.ErrInvalidLink,
			)
		}
		section, found := sections[link.SectionKey]
		if !found {
			return fmt.Errorf(
				"%w: Link Section %q is missing",
				pagewiki.ErrInvalidLink,
				link.SectionKey,
			)
		}
		start, end, valid := exactTextRange(section.Markdown, link.ExactText)
		if !valid || start != link.StartByte || end != link.EndByte {
			return fmt.Errorf(
				"%w: Link exact text is not uniquely grounded",
				pagewiki.ErrInvalidLink,
			)
		}
		if link.TargetPageID != page.ID {
			if _, found := r.pages[link.TargetPageID]; !found {
				return fmt.Errorf(
					"%w: target Page %q is missing",
					pagewiki.ErrInvalidLink,
					link.TargetPageID,
				)
			}
		}
	}
	return nil
}

func exactTextRange(content, exactText string) (int, int, bool) {
	if exactText == "" || strings.Count(content, exactText) != 1 {
		return 0, 0, false
	}
	start := strings.Index(content, exactText)
	return start, start + len(exactText), true
}

func (r *Repository) validateTopics(
	publicationTopics []pagewiki.Topic,
) (map[string]pagewiki.Topic, error) {
	topics := make(map[string]pagewiki.Topic, len(r.topics)+len(publicationTopics))
	for id, topic := range r.topics {
		topics[id] = topic
	}
	seen := make(map[string]struct{}, len(publicationTopics))
	for _, topic := range publicationTopics {
		if topic.ID == "" || topic.Slug == "" || topic.Title == "" {
			return nil, fmt.Errorf("%w: invalid Topic", pagewiki.ErrRevisionConflict)
		}
		if _, duplicate := seen[topic.ID]; duplicate {
			return nil, fmt.Errorf(
				"%w: duplicate Topic %q",
				pagewiki.ErrRevisionConflict,
				topic.ID,
			)
		}
		seen[topic.ID] = struct{}{}
		if existing, found := r.topics[topic.ID]; found && !reflect.DeepEqual(existing, topic) {
			return nil, fmt.Errorf(
				"%w: Topic %q",
				pagewiki.ErrImmutableConflict,
				topic.ID,
			)
		}
		topics[topic.ID] = topic
	}
	for _, topic := range publicationTopics {
		if topic.ParentID != "" {
			if _, found := topics[topic.ParentID]; !found {
				return nil, fmt.Errorf(
					"%w: parent Topic %q is missing",
					pagewiki.ErrRevisionConflict,
					topic.ParentID,
				)
			}
		}
		if topicDepth(topic.ID, topics) > 2 {
			return nil, fmt.Errorf(
				"%w: Topic %q exceeds two levels",
				pagewiki.ErrRevisionConflict,
				topic.ID,
			)
		}
	}
	return topics, nil
}

func topicDepth(id string, topics map[string]pagewiki.Topic) int {
	visited := make(map[string]struct{})
	depth := 0
	for id != "" {
		if _, seen := visited[id]; seen {
			return 3
		}
		visited[id] = struct{}{}
		depth++
		id = topics[id].ParentID
	}
	return depth
}

func (r *Repository) Navigation(_ context.Context) (pagewiki.Navigation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	children := make(map[string][]pagewiki.Topic)
	for _, topic := range r.topics {
		children[topic.ParentID] = append(children[topic.ParentID], topic)
	}
	pages := make(map[string][]pagewiki.NavigationPage)
	for pageID, placement := range r.placements {
		page, found := r.pages[pageID]
		if !found {
			continue
		}
		pages[placement.TopicID] = append(pages[placement.TopicID], pagewiki.NavigationPage{
			ID:    page.ID,
			Slug:  page.Slug,
			Title: page.Title,
			Rank:  placement.Rank,
		})
	}
	rootPages := make([]pagewiki.NavigationPage, 0)
	for id, page := range r.pages {
		if _, placed := r.placements[id]; placed {
			continue
		}
		rootPages = append(rootPages, pagewiki.NavigationPage{
			ID: page.ID, Slug: page.Slug, Title: page.Title,
		})
	}
	sort.Slice(rootPages, func(i, j int) bool {
		return rootPages[i].Slug < rootPages[j].Slug
	})
	return pagewiki.Navigation{
		Roots: buildNavigationTopics("", children, pages),
		Pages: rootPages,
	}, nil
}

func buildNavigationTopics(
	parentID string,
	children map[string][]pagewiki.Topic,
	pages map[string][]pagewiki.NavigationPage,
) []pagewiki.NavigationTopic {
	topics := append([]pagewiki.Topic(nil), children[parentID]...)
	sort.Slice(topics, func(i, j int) bool {
		if topics[i].Slug == topics[j].Slug {
			return topics[i].ID < topics[j].ID
		}
		return topics[i].Slug < topics[j].Slug
	})
	result := make([]pagewiki.NavigationTopic, 0, len(topics))
	for _, topic := range topics {
		topicPages := append([]pagewiki.NavigationPage(nil), pages[topic.ID]...)
		sort.Slice(topicPages, func(i, j int) bool {
			if topicPages[i].Rank == topicPages[j].Rank {
				return topicPages[i].Slug < topicPages[j].Slug
			}
			return topicPages[i].Rank < topicPages[j].Rank
		})
		result = append(result, pagewiki.NavigationTopic{
			ID:       topic.ID,
			Slug:     topic.Slug,
			Title:    topic.Title,
			Children: buildNavigationTopics(topic.ID, children, pages),
			Pages:    topicPages,
		})
	}
	return result
}

func (r *Repository) Search(
	_ context.Context,
	query string,
) ([]pagewiki.SearchResult, error) {
	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return nil, fmt.Errorf("%w: at least one token is required", pagewiki.ErrInvalidSearch)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	results := make([]pagewiki.SearchResult, 0)
	for _, chunk := range r.searchChunks {
		score := lexicalScore(queryTokens, tokenize(chunk.Passage))
		if score == 0 {
			continue
		}
		page, pageFound := r.pages[chunk.PageID]
		revision, revisionFound := r.pageRevisions[chunk.PageRevisionID]
		if !pageFound || !revisionFound || page.CurrentRevisionID != revision.ID {
			continue
		}
		results = append(results, pagewiki.SearchResult{
			Page:       page,
			RevisionID: revision.ID,
			SectionKey: chunk.SectionKey,
			Passage:    chunk.Passage,
			Score:      score,
			Citations:  citationsForSection(revision, chunk.SectionKey),
			Links:      linksForSection(revision, chunk.SectionKey),
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Page.Slug != results[j].Page.Slug {
			return results[i].Page.Slug < results[j].Page.Slug
		}
		return results[i].SectionKey < results[j].SectionKey
	})
	return results, nil
}

func tokenize(value string) []string {
	return strings.FieldsFunc(strings.ToLower(value), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsNumber(character)
	})
}

func lexicalScore(queryTokens, passageTokens []string) float64 {
	counts := make(map[string]int, len(passageTokens))
	for _, token := range passageTokens {
		counts[token]++
	}
	seen := make(map[string]struct{}, len(queryTokens))
	score := 0
	for _, token := range queryTokens {
		if _, duplicate := seen[token]; duplicate {
			continue
		}
		seen[token] = struct{}{}
		score += counts[token]
	}
	return float64(score)
}

func citationsForSection(
	revision pagewiki.PageRevision,
	sectionKey string,
) []pagewiki.PageCitation {
	result := make([]pagewiki.PageCitation, 0)
	for _, citation := range revision.Citations {
		if citation.SectionKey != sectionKey {
			continue
		}
		citation.SourceAnchors = append(
			[]pagewiki.SourceAnchor(nil),
			citation.SourceAnchors...,
		)
		result = append(result, citation)
	}
	return result
}

func linksForSection(
	revision pagewiki.PageRevision,
	sectionKey string,
) []pagewiki.PageLink {
	result := make([]pagewiki.PageLink, 0)
	for _, link := range revision.Links {
		if link.SectionKey == sectionKey {
			result = append(result, link)
		}
	}
	return result
}

func (r *Repository) PageLinks(
	_ context.Context,
	pageID string,
) (pagewiki.PageLinkSet, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	page, found := r.pages[pageID]
	if !found {
		return pagewiki.PageLinkSet{}, fmt.Errorf("%w: Page %q", pagewiki.ErrNotFound, pageID)
	}
	result := pagewiki.PageLinkSet{}
	currentRevision := r.pageRevisions[page.CurrentRevisionID]
	for _, link := range currentRevision.Links {
		result.Outgoing = append(result.Outgoing, pagewiki.ResolvedPageLink{
			Link:             link,
			SourcePage:       page,
			SourceRevisionID: currentRevision.ID,
			TargetPage:       r.pages[link.TargetPageID],
		})
	}
	for _, sourcePage := range r.pages {
		sourceRevision := r.pageRevisions[sourcePage.CurrentRevisionID]
		for _, link := range sourceRevision.Links {
			if link.TargetPageID != pageID {
				continue
			}
			result.Incoming = append(result.Incoming, pagewiki.ResolvedPageLink{
				Link:             link,
				SourcePage:       sourcePage,
				SourceRevisionID: sourceRevision.ID,
				TargetPage:       page,
			})
		}
	}
	sortResolvedLinks(result.Outgoing, true)
	sortResolvedLinks(result.Incoming, false)
	return result, nil
}

func sortResolvedLinks(links []pagewiki.ResolvedPageLink, outgoing bool) {
	sort.Slice(links, func(i, j int) bool {
		leftPage := links[i].SourcePage
		rightPage := links[j].SourcePage
		if outgoing {
			leftPage = links[i].TargetPage
			rightPage = links[j].TargetPage
		}
		if leftPage.Slug != rightPage.Slug {
			return leftPage.Slug < rightPage.Slug
		}
		return links[i].Link.ExactText < links[j].Link.ExactText
	})
}

func (r *Repository) SourceBacklinks(
	_ context.Context,
	sourceRevisionID string,
) ([]pagewiki.SourceBacklink, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, found := r.sourceRevisions[sourceRevisionID]; !found {
		return nil, fmt.Errorf(
			"%w: SourceRevision %q",
			pagewiki.ErrNotFound,
			sourceRevisionID,
		)
	}
	result := make([]pagewiki.SourceBacklink, 0)
	for _, page := range r.pages {
		revision := r.pageRevisions[page.CurrentRevisionID]
		citations := citationsForSource(revision, sourceRevisionID)
		if len(citations) == 0 {
			continue
		}
		result = append(result, pagewiki.SourceBacklink{
			Page:      page,
			Revision:  clonePageRevision(revision),
			Citations: citations,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Page.Slug < result[j].Page.Slug
	})
	return result, nil
}

func citationsForSource(
	revision pagewiki.PageRevision,
	sourceRevisionID string,
) []pagewiki.PageCitation {
	result := make([]pagewiki.PageCitation, 0)
	for _, citation := range revision.Citations {
		matches := false
		for _, anchor := range citation.SourceAnchors {
			if anchor.SourceRevisionID == sourceRevisionID {
				matches = true
				break
			}
		}
		if matches {
			citation.SourceAnchors = append(
				[]pagewiki.SourceAnchor(nil),
				citation.SourceAnchors...,
			)
			result = append(result, citation)
		}
	}
	return result
}

func (r *Repository) RebuildSearchIndex(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	chunks := make(map[string]pagewiki.SearchChunk)
	for _, page := range r.pages {
		revision, found := r.pageRevisions[page.CurrentRevisionID]
		if !found {
			return fmt.Errorf(
				"%w: current PageRevision %q",
				pagewiki.ErrRevisionConflict,
				page.CurrentRevisionID,
			)
		}
		for _, chunk := range buildSearchChunks(revision) {
			chunks[chunk.ID] = chunk
		}
	}
	r.searchChunks = chunks
	return nil
}

func buildSearchChunks(revision pagewiki.PageRevision) []pagewiki.SearchChunk {
	chunks := make([]pagewiki.SearchChunk, 0, len(revision.Sections))
	for _, section := range revision.Sections {
		chunks = append(chunks, pagewiki.SearchChunk{
			ID:             revision.ID + ":" + section.Key,
			PageID:         revision.PageID,
			PageRevisionID: revision.ID,
			SectionKey:     section.Key,
			Passage:        section.Markdown,
		})
	}
	return chunks
}

func (r *Repository) SaveMaintenanceRun(
	_ context.Context,
	run pagewiki.MaintenanceRun,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, found := r.runs[run.ID]; found && !reflect.DeepEqual(existing, run) {
		return fmt.Errorf("%w: MaintenanceRun %q", pagewiki.ErrImmutableConflict, run.ID)
	}
	r.runs[run.ID] = cloneRun(run)
	return nil
}

func (r *Repository) MaintenanceRun(
	_ context.Context,
	id string,
) (pagewiki.MaintenanceRun, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	run, found := r.runs[id]
	if !found {
		return pagewiki.MaintenanceRun{}, fmt.Errorf(
			"%w: MaintenanceRun %q",
			pagewiki.ErrNotFound,
			id,
		)
	}
	return cloneRun(run), nil
}

func (r *Repository) PageCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.pages)
}

func (r *Repository) PageRevisionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.pageRevisions)
}

func (r *Repository) TopicCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.topics)
}

func (r *Repository) PlacementCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.placements)
}

func (r *Repository) SourceRevisionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sourceRevisions)
}

func (r *Repository) MaintenanceRunCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.runs)
}

func (r *Repository) ClearSearchChunks() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.searchChunks = make(map[string]pagewiki.SearchChunk)
}

func (r *Repository) SearchChunkCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.searchChunks)
}

func (r *Repository) TopicTree(_ context.Context) (pagewiki.TopicTree, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tree := pagewiki.TopicTree{
		Topics:     make([]pagewiki.Topic, 0, len(r.topics)),
		Placements: make([]pagewiki.PagePlacement, 0, len(r.placements)),
	}
	for _, topic := range r.topics {
		tree.Topics = append(tree.Topics, topic)
	}
	sort.Slice(tree.Topics, func(i, j int) bool {
		if tree.Topics[i].ParentID != tree.Topics[j].ParentID {
			return tree.Topics[i].ParentID < tree.Topics[j].ParentID
		}
		return tree.Topics[i].Slug < tree.Topics[j].Slug
	})
	for _, placement := range r.placements {
		tree.Placements = append(tree.Placements, placement)
	}
	sort.Slice(tree.Placements, func(i, j int) bool {
		return tree.Placements[i].PageID < tree.Placements[j].PageID
	})
	return tree, nil
}

func (r *Repository) ReplaceTopicTree(_ context.Context, tree pagewiki.TopicTree) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	topics := make(map[string]pagewiki.Topic, len(tree.Topics))
	for _, topic := range tree.Topics {
		if topic.ID == "" || topic.Slug == "" || topic.Title == "" {
			return fmt.Errorf("%w: invalid Topic", pagewiki.ErrRevisionConflict)
		}
		if _, duplicate := topics[topic.ID]; duplicate {
			return fmt.Errorf("%w: duplicate Topic %q", pagewiki.ErrRevisionConflict, topic.ID)
		}
		topics[topic.ID] = topic
	}
	for _, topic := range tree.Topics {
		if topic.ParentID != "" {
			if _, found := topics[topic.ParentID]; !found {
				return fmt.Errorf(
					"%w: parent Topic %q is missing",
					pagewiki.ErrRevisionConflict, topic.ParentID,
				)
			}
		}
		if topicDepth(topic.ID, topics) > 2 {
			return fmt.Errorf(
				"%w: Topic %q exceeds two levels",
				pagewiki.ErrRevisionConflict, topic.ID,
			)
		}
	}
	placements := make(map[string]pagewiki.PagePlacement, len(tree.Placements))
	for _, placement := range tree.Placements {
		if _, found := r.pages[placement.PageID]; !found {
			return fmt.Errorf(
				"%w: placed Page %q is missing",
				pagewiki.ErrRevisionConflict, placement.PageID,
			)
		}
		if _, found := topics[placement.TopicID]; !found {
			return fmt.Errorf(
				"%w: placement Topic %q is missing",
				pagewiki.ErrRevisionConflict, placement.TopicID,
			)
		}
		if _, duplicate := placements[placement.PageID]; duplicate {
			return fmt.Errorf(
				"%w: Page %q is placed twice",
				pagewiki.ErrRevisionConflict, placement.PageID,
			)
		}
		if placement.Rank < 0 {
			return fmt.Errorf("%w: invalid Page placement", pagewiki.ErrRevisionConflict)
		}
		placements[placement.PageID] = placement
	}
	r.topics = topics
	r.placements = placements
	return nil
}

func sourceRevisionsEqual(left, right pagewiki.SourceRevision) bool {
	return left.ID == right.ID &&
		left.SourceID == right.SourceID &&
		left.SHA256 == right.SHA256 &&
		bytes.Equal(left.Raw, right.Raw) &&
		reflect.DeepEqual(left.Events, right.Events)
}

func cloneSourceRevision(revision pagewiki.SourceRevision) pagewiki.SourceRevision {
	revision.Raw = append([]byte(nil), revision.Raw...)
	revision.Events = append([]pagewiki.SourceEvent(nil), revision.Events...)
	return revision
}

func clonePageRevision(revision pagewiki.PageRevision) pagewiki.PageRevision {
	revision.Sections = append([]pagewiki.PageSection(nil), revision.Sections...)
	revision.Citations = append([]pagewiki.PageCitation(nil), revision.Citations...)
	for index := range revision.Citations {
		revision.Citations[index].SourceAnchors = append(
			[]pagewiki.SourceAnchor(nil),
			revision.Citations[index].SourceAnchors...,
		)
	}
	revision.Links = append([]pagewiki.PageLink(nil), revision.Links...)
	return revision
}

func cloneRun(run pagewiki.MaintenanceRun) pagewiki.MaintenanceRun {
	run.Targets = append([]pagewiki.MaintenanceTarget(nil), run.Targets...)
	return run
}
