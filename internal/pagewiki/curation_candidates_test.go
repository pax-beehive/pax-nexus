package pagewiki

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGivenCatalogWhenFingerprintedThenOrderIsIrrelevantButRevisionIsSignificant(t *testing.T) {
	t.Parallel()

	a := PageCatalogEntry{ID: "page-1", CurrentRevisionID: "rev-1"}
	b := PageCatalogEntry{ID: "page-2", CurrentRevisionID: "rev-1"}

	forward := catalogFingerprint(PageCatalog{a, b})
	reversed := catalogFingerprint(PageCatalog{b, a})
	assert.Equal(t, forward, reversed, "fingerprint must not depend on catalog order")

	b.CurrentRevisionID = "rev-2"
	changed := catalogFingerprint(PageCatalog{a, b})
	assert.NotEqual(t, forward, changed, "fingerprint must change when a revision changes")
}

func TestGivenVectorsWhenCosineSimilarityIsComputedThenIdenticalIsOneAndOrthogonalIsZero(t *testing.T) {
	t.Parallel()

	identical := cosineSimilarity([]float32{1, 2, 3}, []float32{1, 2, 3})
	assert.InDelta(t, 1.0, identical, 1e-9)

	orthogonal := cosineSimilarity([]float32{1, 0}, []float32{0, 1})
	assert.InDelta(t, 0.0, orthogonal, 1e-9)

	assert.Equal(t, 0.0, cosineSimilarity(nil, nil))
	assert.Equal(t, 0.0, cosineSimilarity([]float32{1}, []float32{1, 2}))
	assert.Equal(t, 0.0, cosineSimilarity([]float32{0, 0}, []float32{1, 1}))
}

func TestGivenEntryWhenEmbeddingTextIsBuiltThenItJoinsTitleAndSummary(t *testing.T) {
	t.Parallel()

	entry := PageCatalogEntry{Title: "Deploy Runbook", Summary: "How we deploy."}
	assert.Equal(t, "Deploy Runbook\nHow we deploy.", curationEmbeddingText(entry))
}

func TestGivenNearDuplicateVectorsWhenPairsAreDetectedThenAboveThresholdPairsAreCappedAndOrdered(t *testing.T) {
	t.Parallel()

	catalog := PageCatalog{
		{ID: "page-a", Title: "A"},
		{ID: "page-b", Title: "B"},
		{ID: "page-c", Title: "C"},
		{ID: "page-d", Title: "D"},
	}
	vectors := map[string][]float32{
		"page-a": {1, 0, 0},
		"page-b": {0.9, 0.43589, 0}, // exact cosine 0.9 with page-a: above threshold
		"page-c": {0, 1, 0},         // orthogonal to page-a: below threshold
		"page-d": {0, 0, 1},         // orthogonal to both a and b: below threshold
	}

	pairs := duplicatePairs(catalog, vectors, TopicTree{}, 8)
	require.Len(t, pairs, 1)
	assert.Equal(t, "page-a", pairs[0].AID)
	assert.Equal(t, "page-b", pairs[0].BID)
	assert.Greater(t, pairs[0].Similarity, curationPairSimilarityThreshold)

	capped := duplicatePairs(catalog, vectors, TopicTree{}, 0)
	assert.Empty(t, capped)
}

func TestGivenOverlappingCandidatesWhenPairsAreDetectedThenNoPageAppearsTwice(t *testing.T) {
	t.Parallel()

	catalog := PageCatalog{
		{ID: "page-a", Title: "A"},
		{ID: "page-b", Title: "B"},
		{ID: "page-c", Title: "C"},
	}
	// All three vectors are mutually near-identical, so all three pairwise
	// combinations clear the threshold; only one pair may be emitted since
	// each page can be used at most once.
	vectors := map[string][]float32{
		"page-a": {1, 0, 0},
		"page-b": {1, 0.001, 0},
		"page-c": {1, 0, 0.001},
	}

	pairs := duplicatePairs(catalog, vectors, TopicTree{}, 8)

	seen := make(map[string]struct{})
	for _, pair := range pairs {
		_, aDup := seen[pair.AID]
		_, bDup := seen[pair.BID]
		require.False(t, aDup, "page %s reused across pairs", pair.AID)
		require.False(t, bDup, "page %s reused across pairs", pair.BID)
		seen[pair.AID] = struct{}{}
		seen[pair.BID] = struct{}{}
	}
	assert.Len(t, pairs, 1)
}

func TestGivenSameLeafTopicWhenTitlesShareASlugThenPairIsDetectedWithoutEmbeddings(t *testing.T) {
	t.Parallel()

	catalog := PageCatalog{
		{ID: "page-a", Title: "Deploy Runbook"},
		{ID: "page-b", Title: "Deploy Runbook"},
		{ID: "page-c", Title: "Unrelated Page"},
	}
	tree := TopicTree{
		Topics: []Topic{{ID: "topic-leaf", ParentID: "", Slug: "ops"}},
		Placements: []PagePlacement{
			{PageID: "page-a", TopicID: "topic-leaf"},
			{PageID: "page-b", TopicID: "topic-leaf"},
			{PageID: "page-c", TopicID: "topic-leaf"},
		},
	}

	pairs := duplicatePairs(catalog, nil, tree, 8)
	require.Len(t, pairs, 1)
	assert.Equal(t, "page-a", pairs[0].AID)
	assert.Equal(t, "page-b", pairs[0].BID)
	assert.Equal(t, 1.0, pairs[0].Similarity)
}

// Regression test for a Critical review finding: with three pages sharing
// a topicSlug in one leaf, all three pairwise combinations tie at
// Similarity 1.0, and two of those pairs also tie on AID (page-a is the
// lexically-smaller member of both (page-a,page-b) and (page-a,page-c)).
// Before the fix, the final sort broke ties only by Similarity then AID,
// leaving the choice between those two pairs to Go's randomized map
// iteration order, so capping at limit=1 was nondeterministic across
// calls with identical input. Adding BID as a third sort key makes the
// comparator a total order and the capped result stable.
func TestGivenTiedSimilarityAndAIDWhenPairsAreCappedThenTheResultIsDeterministicAcrossCalls(t *testing.T) {
	t.Parallel()

	catalog := PageCatalog{
		{ID: "page-a", Title: "Deploy Runbook"},
		{ID: "page-b", Title: "Deploy Runbook"},
		{ID: "page-c", Title: "Deploy Runbook"},
	}
	tree := TopicTree{
		Topics: []Topic{{ID: "topic-leaf", ParentID: "", Slug: "ops"}},
		Placements: []PagePlacement{
			{PageID: "page-a", TopicID: "topic-leaf"},
			{PageID: "page-b", TopicID: "topic-leaf"},
			{PageID: "page-c", TopicID: "topic-leaf"},
		},
	}

	var first []pagePair
	for i := 0; i < 100; i++ {
		pairs := duplicatePairs(catalog, nil, tree, 1)
		require.Len(t, pairs, 1)
		if first == nil {
			first = pairs
			continue
		}
		require.Equal(t, first, pairs, "capped result must be identical across repeated calls with identical input")
	}
	assert.Equal(t, "page-a", first[0].AID)
	assert.Equal(t, "page-b", first[0].BID)
}

func TestGivenThreeIndependentPairsWhenCappedThenTheTwoHighestSimilarityPairsAreKeptInOrder(t *testing.T) {
	t.Parallel()

	catalog := PageCatalog{
		{ID: "page-p1", Title: "P1"},
		{ID: "page-p2", Title: "P2"},
		{ID: "page-p3", Title: "P3"},
		{ID: "page-p4", Title: "P4"},
		{ID: "page-p5", Title: "P5"},
		{ID: "page-p6", Title: "P6"},
	}
	// Three independent, non-overlapping pairs, each isolated to its own
	// pair of dimensions so cross-pair cosine similarity is exactly 0:
	// (p1,p2) ~0.95, (p3,p4) ~0.90, (p5,p6) ~0.87 — all above threshold.
	vectors := map[string][]float32{
		"page-p1": {1, 0, 0, 0, 0, 0},
		"page-p2": {0.95, 0.31225, 0, 0, 0, 0},
		"page-p3": {0, 0, 1, 0, 0, 0},
		"page-p4": {0, 0, 0.90, 0.43589, 0, 0},
		"page-p5": {0, 0, 0, 0, 1, 0},
		"page-p6": {0, 0, 0, 0, 0.87, 0.49305},
	}

	pairs := duplicatePairs(catalog, vectors, TopicTree{}, 2)

	require.Len(t, pairs, 2)
	assert.Equal(t, "page-p1", pairs[0].AID)
	assert.Equal(t, "page-p2", pairs[0].BID)
	assert.Equal(t, "page-p3", pairs[1].AID)
	assert.Equal(t, "page-p4", pairs[1].BID)
	assert.Greater(t, pairs[0].Similarity, pairs[1].Similarity, "highest-similarity pair sorts first")
}

func TestGivenPlacementUnderATopicWithChildrenWhenPairsAreDetectedThenItIsNotALeafGroup(t *testing.T) {
	t.Parallel()

	catalog := PageCatalog{
		{ID: "page-a", Title: "Deploy Runbook"},
		{ID: "page-b", Title: "Deploy Runbook"},
	}
	tree := TopicTree{
		Topics: []Topic{
			{ID: "topic-parent", ParentID: ""},
			{ID: "topic-child", ParentID: "topic-parent"},
		},
		Placements: []PagePlacement{
			{PageID: "page-a", TopicID: "topic-parent"},
			{PageID: "page-b", TopicID: "topic-parent"},
		},
	}

	pairs := duplicatePairs(catalog, nil, tree, 8)
	assert.Empty(t, pairs, "a topic with children is not a leaf, so its direct placements do not count")
}

func TestGivenLowQualitySignalsWhenScoredThenOnlyPagesWithAtLeastTwoSignalsAreSelected(t *testing.T) {
	t.Parallel()

	catalog := PageCatalog{
		{ID: "page-orphan-short", Title: "Deploy Runbook"},
		{ID: "page-merely-short", Title: "Deploy Runbook"},
		{ID: "page-healthy", Title: "Deploy Runbook"},
	}
	revisions := map[string]PageRevision{
		"page-orphan-short": {Markdown: "short"},
		"page-merely-short": {Markdown: "short"},
		"page-healthy":      {Markdown: longMarkdown()},
	}
	incomingLinks := map[string]int{
		"page-merely-short": 3,
		"page-healthy":      3,
	}

	candidates := qualityCandidates(catalog, revisions, incomingLinks, 8)

	require.Len(t, candidates, 1)
	assert.Equal(t, "page-orphan-short", candidates[0].PageID)
	assert.ElementsMatch(t, []string{"orphan", "short-body"}, candidates[0].Signals)
}

func TestGivenAllThreeSignalsWhenScoredThenSentenceTitleSignalIsRecorded(t *testing.T) {
	t.Parallel()

	sentenceTitle := "This is a very long sentence-shaped title that goes way beyond what a concept title should ever be"
	catalog := PageCatalog{
		{ID: "page-1", Title: sentenceTitle},
	}
	revisions := map[string]PageRevision{
		"page-1": {Markdown: "short"},
	}

	candidates := qualityCandidates(catalog, revisions, map[string]int{}, 8)

	require.Len(t, candidates, 1)
	assert.ElementsMatch(t, []string{"orphan", "short-body", "sentence-title"}, candidates[0].Signals)
}

func TestGivenMoreCandidatesThanLimitWhenScoredThenResultsAreCappedAndOrdered(t *testing.T) {
	t.Parallel()

	sentenceTitle := "This is a very long sentence-shaped title that goes way beyond what a concept title should ever be"
	catalog := PageCatalog{
		{ID: "page-b-two-signals", Title: "Deploy Runbook"},
		{ID: "page-a-three-signals", Title: sentenceTitle},
		{ID: "page-c-two-signals", Title: "Deploy Runbook"},
	}
	revisions := map[string]PageRevision{
		"page-b-two-signals":   {Markdown: "short"},
		"page-a-three-signals": {Markdown: "short"},
		"page-c-two-signals":   {Markdown: "short"},
	}

	candidates := qualityCandidates(catalog, revisions, map[string]int{}, 2)

	require.Len(t, candidates, 2)
	assert.Equal(t, "page-a-three-signals", candidates[0].PageID, "three-signal page sorts first")
	assert.Equal(t, "page-b-two-signals", candidates[1].PageID, "ties broken by PageID ascending")
}

func longMarkdown() string {
	body := make([]byte, curationBodyByteFloor+50)
	for i := range body {
		body[i] = 'x'
	}
	return string(body)
}
