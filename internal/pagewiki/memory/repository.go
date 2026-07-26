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
	runs            map[string]pagewiki.MaintenanceRun
}

func NewRepository() *Repository {
	return &Repository{
		sourceRevisions: make(map[string]pagewiki.SourceRevision),
		pages:           make(map[string]pagewiki.Page),
		pageIDsBySlug:   make(map[string]string),
		pageRevisions:   make(map[string]pagewiki.PageRevision),
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
		catalog = append(catalog, pagewiki.PageCatalogEntry{
			ID:                page.ID,
			Slug:              page.Slug,
			Title:             page.Title,
			CurrentRevisionID: page.CurrentRevisionID,
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

func (r *Repository) PublishPage(
	_ context.Context,
	page pagewiki.Page,
	revision pagewiki.PageRevision,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	}
	if existing, found := r.pageRevisions[revision.ID]; found &&
		!reflect.DeepEqual(existing, revision) {
		return fmt.Errorf(
			"%w: PageRevision %q",
			pagewiki.ErrImmutableConflict,
			revision.ID,
		)
	}
	r.pageRevisions[revision.ID] = clonePageRevision(revision)
	r.pages[page.ID] = page
	r.pageIDsBySlug[page.Slug] = page.ID
	return nil
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
