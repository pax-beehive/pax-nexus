// Package memory provides an in-memory Page Wiki repository.
package memory

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sort"
	"sync"

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
	runs            map[string]pagewiki.MaintenanceRun
}

func NewRepository() *Repository {
	return &Repository{
		sourceRevisions: make(map[string]pagewiki.SourceRevision),
		pages:           make(map[string]pagewiki.Page),
		pageIDsBySlug:   make(map[string]string),
		pageRevisions:   make(map[string]pagewiki.PageRevision),
		topics:          make(map[string]pagewiki.Topic),
		placements:      make(map[string]pagewiki.PagePlacement),
		runs:            make(map[string]pagewiki.MaintenanceRun),
	}
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
		catalog = append(catalog, pagewiki.PageCatalogEntry(page))
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
	if err := r.validatePublication(publication); err != nil {
		return err
	}
	page := publication.Page
	revision := publication.Revision
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
	return nil
}

func (r *Repository) validatePublication(publication pagewiki.PagePublication) error {
	page := publication.Page
	revision := publication.Revision
	if page.CurrentRevisionID != revision.ID || page.ID != revision.PageID {
		return fmt.Errorf("%w: Page and revision identities differ", pagewiki.ErrRevisionConflict)
	}
	if existingID, found := r.pageIDsBySlug[page.Slug]; found && existingID != page.ID {
		return fmt.Errorf("%w: slug %q is already published", pagewiki.ErrRevisionConflict, page.Slug)
	}
	if existing, found := r.pages[page.ID]; found {
		if existing.CurrentRevisionID != revision.BaseRevisionID {
			if reflect.DeepEqual(existing, page) &&
				reflect.DeepEqual(r.pageRevisions[revision.ID], revision) {
				return nil
			}
			return fmt.Errorf("%w: Page %q base is stale", pagewiki.ErrRevisionConflict, page.ID)
		}
	} else if revision.BaseRevisionID != "" {
		return fmt.Errorf("%w: new Page %q has a base revision", pagewiki.ErrRevisionConflict, page.ID)
	} else if publication.Placement == nil {
		return fmt.Errorf("%w: new Page %q requires placement", pagewiki.ErrRevisionConflict, page.ID)
	}
	if existing, found := r.pageRevisions[revision.ID]; found &&
		!reflect.DeepEqual(existing, revision) {
		return fmt.Errorf(
			"%w: PageRevision %q",
			pagewiki.ErrImmutableConflict,
			revision.ID,
		)
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
	return pagewiki.Navigation{
		Roots: buildNavigationTopics("", children, pages),
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
