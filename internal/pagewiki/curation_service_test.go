package pagewiki

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGivenPageAWithMoreIncomingLinksWhenSurvivorIsSelectedThenAWins(t *testing.T) {
	t.Parallel()

	entryA := PageCatalogEntry{ID: "page-a", Slug: "a"}
	entryB := PageCatalogEntry{ID: "page-b", Slug: "b"}
	revisionA := PageRevision{ID: "rev-a"}
	revisionB := PageRevision{ID: "rev-b"}
	incomingLinks := map[string]int{"page-a": 3, "page-b": 1}

	survivor, survivorRevision, loser := selectSurvivor(entryA, revisionA, entryB, revisionB, incomingLinks)

	assert.Equal(t, entryA, survivor)
	assert.Equal(t, revisionA, survivorRevision)
	assert.Equal(t, entryB, loser)
}

func TestGivenPageBWithMoreIncomingLinksWhenSurvivorIsSelectedThenBWins(t *testing.T) {
	t.Parallel()

	entryA := PageCatalogEntry{ID: "page-a", Slug: "a"}
	entryB := PageCatalogEntry{ID: "page-b", Slug: "b"}
	revisionA := PageRevision{ID: "rev-a"}
	revisionB := PageRevision{ID: "rev-b"}
	incomingLinks := map[string]int{"page-a": 1, "page-b": 3}

	survivor, survivorRevision, loser := selectSurvivor(entryA, revisionA, entryB, revisionB, incomingLinks)

	assert.Equal(t, entryB, survivor)
	assert.Equal(t, revisionB, survivorRevision)
	assert.Equal(t, entryA, loser)
}

func TestGivenTiedIncomingLinksWhenSurvivorIsSelectedThenLexicallySmallerIDWins(t *testing.T) {
	t.Parallel()

	entryA := PageCatalogEntry{ID: "page-a", Slug: "a"}
	entryB := PageCatalogEntry{ID: "page-b", Slug: "b"}
	revisionA := PageRevision{ID: "rev-a"}
	revisionB := PageRevision{ID: "rev-b"}
	incomingLinks := map[string]int{"page-a": 2, "page-b": 2}

	survivor, _, loser := selectSurvivor(entryA, revisionA, entryB, revisionB, incomingLinks)
	assert.Equal(t, entryA, survivor)
	assert.Equal(t, entryB, loser)

	// Reversing argument order must not change the outcome: the smaller ID
	// still wins regardless of which argument position it arrives in.
	survivor, _, loser = selectSurvivor(entryB, revisionB, entryA, revisionA, incomingLinks)
	assert.Equal(t, entryA, survivor)
	assert.Equal(t, entryB, loser)
}

func TestGivenOutgoingLinksAcrossBothMergedPagesWhenRelatedPagesAreUnionedThenExcludedAndDuplicateTargetsAreDropped(t *testing.T) {
	t.Parallel()

	catalogByID := map[string]PageCatalogEntry{
		"page-a":   {ID: "page-a", Title: "A"},
		"page-b":   {ID: "page-b", Title: "B"},
		"page-foo": {ID: "page-foo", Title: "Foo"},
		"page-bar": {ID: "page-bar", Title: "Bar"},
	}
	exclude := map[string]struct{}{"page-a": {}, "page-b": {}}
	linksA := []PageLink{
		{TargetPageID: "page-foo"},
		{TargetPageID: "page-b"}, // one merged page links to the other: must be excluded
	}
	linksB := []PageLink{
		{TargetPageID: "page-foo"}, // duplicate target across the two link sets: kept once
		{TargetPageID: "page-bar"},
		{TargetPageID: "page-missing"}, // not in the catalog: silently skipped
	}

	related := relatedPages(catalogByID, exclude, linksA, linksB)

	assert.Equal(t, []RelatedPage{
		{ID: "page-foo", Title: "Foo"},
		{ID: "page-bar", Title: "Bar"},
	}, related)
}

func TestGivenNoLinksWhenRelatedPagesAreUnionedThenResultIsEmpty(t *testing.T) {
	t.Parallel()

	related := relatedPages(map[string]PageCatalogEntry{}, map[string]struct{}{})
	assert.Empty(t, related)
}
