package pagewiki

import (
	"math"
	"sort"
)

// Deterministic, zero-token curation candidate detection. These functions
// surface duplicate-page and low-quality-page candidates from catalog,
// revision, and tree data alone; an LLM curator (a later stage) decides what
// to do with the candidates they emit.
const (
	curationPairSimilarityThreshold = 0.86
	curationBodyByteFloor           = 400
	curationDefaultPairLimit        = 8
	curationDefaultPageLimit        = 8
)

// pagePair is a candidate duplicate pair. AID is always lexically less than
// BID, which keeps pair identity and dedup keys stable regardless of
// discovery order.
type pagePair struct {
	AID, BID   string
	Similarity float64
}

// qualityCandidate is a page whose quality signals crossed the selection
// threshold, along with the signals that fired.
type qualityCandidate struct {
	PageID  string
	Signals []string
}

// catalogFingerprint returns a stable identity for the catalog's current
// state. It sorts "pageID@currentRevisionID" strings so the result is
// independent of catalog order, but changes whenever any page's current
// revision changes.
func catalogFingerprint(catalog PageCatalog) string {
	entries := make([]string, 0, len(catalog))
	for _, entry := range catalog {
		entries = append(entries, entry.ID+"@"+entry.CurrentRevisionID)
	}
	sort.Strings(entries)
	return StableID("curation-fingerprint", entries...)
}

// cosineSimilarity returns the cosine similarity of a and b. It returns 0
// when the vectors differ in length or either has zero magnitude, rather
// than dividing by zero.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, magA, magB float64
	for i := range a {
		av, bv := float64(a[i]), float64(b[i])
		dot += av * bv
		magA += av * av
		magB += bv * bv
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}

// curationEmbeddingText is the text embedded to detect near-duplicate pages:
// the title and summary, which is what actually distinguishes two pages
// about the same subject without requiring the full body.
func curationEmbeddingText(entry PageCatalogEntry) string {
	return entry.Title + "\n" + entry.Summary
}

// duplicatePairs finds candidate duplicate page pairs from two independent
// signals: embedding cosine similarity above curationPairSimilarityThreshold,
// and pages placed under the same tree leaf topic whose titles collapse to
// the same topicSlug (assigned Similarity 1.0, since identical-slugged
// titles in one leaf are a duplicate signal on their own, without needing
// embeddings). Results are deduped, sorted by similarity descending then AID
// ascending for determinism, and capped at limit; once a page is used in a
// pair it is not reused in another pair in the same call.
func duplicatePairs(
	catalog PageCatalog,
	vectors map[string][]float32,
	tree TopicTree,
	limit int,
) []pagePair {
	candidates := embeddingPairCandidates(catalog, vectors)
	// The same-leaf lane runs second and writes into the same map, so an
	// embedding-lane pair that also collapses to the same topicSlug ends up
	// with the same-leaf lane's Similarity 1.0, matching the pre-refactor
	// single-map two-loop behavior exactly.
	for key, pair := range sameLeafPairCandidates(catalog, tree) {
		candidates[key] = pair
	}

	// Filter out incompatible entity type pairs: curator must never propose
	// merging two pages of different entity types.
	typeByID := make(map[string]EntityType, len(catalog))
	for _, entry := range catalog {
		typeByID[entry.ID] = entry.EntityType
	}
	for key, pair := range candidates {
		if !mergeCompatibleTypes(typeByID[pair.AID], typeByID[pair.BID]) {
			delete(candidates, key)
		}
	}

	return dedupSortCapPairs(candidates, limit)
}

// mergeCompatibleTypes returns whether two entity types can be merged.
// Compatible unless both types are known, non-fallback, and different.
// "" and EntityTypeConcept are wildcards.
func mergeCompatibleTypes(a, b EntityType) bool {
	if a == "" || a == EntityTypeConcept || b == "" || b == EntityTypeConcept {
		return true
	}
	return a == b
}

// embeddingPairCandidates finds candidate duplicate pairs by embedding
// cosine similarity above curationPairSimilarityThreshold, considering only
// pages with a vector. Keyed by pairKey so a later merge can overwrite by
// unordered pair identity.
func embeddingPairCandidates(catalog PageCatalog, vectors map[string][]float32) map[string]pagePair {
	ids := make([]string, 0, len(catalog))
	for _, entry := range catalog {
		if _, ok := vectors[entry.ID]; ok {
			ids = append(ids, entry.ID)
		}
	}
	sort.Strings(ids)

	candidates := make(map[string]pagePair)
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			aID, bID := ids[i], ids[j]
			similarity := cosineSimilarity(vectors[aID], vectors[bID])
			if similarity < curationPairSimilarityThreshold {
				continue
			}
			candidates[pairKey(aID, bID)] = pagePair{AID: aID, BID: bID, Similarity: similarity}
		}
	}
	return candidates
}

// sameLeafPairCandidates finds candidate duplicate pairs among pages placed
// under the same tree leaf topic whose titles collapse to the same
// topicSlug, assigned Similarity 1.0. Keyed by pairKey so a later merge can
// overwrite by unordered pair identity.
func sameLeafPairCandidates(catalog PageCatalog, tree TopicTree) map[string]pagePair {
	titleByID := make(map[string]string, len(catalog))
	for _, entry := range catalog {
		titleByID[entry.ID] = entry.Title
	}

	candidates := make(map[string]pagePair)
	for _, pageIDs := range sameLeafGroups(tree) {
		sort.Strings(pageIDs)
		for i := 0; i < len(pageIDs); i++ {
			for j := i + 1; j < len(pageIDs); j++ {
				aID, bID := pageIDs[i], pageIDs[j]
				aSlug, bSlug := topicSlug(titleByID[aID]), topicSlug(titleByID[bID])
				if aSlug == "" || aSlug != bSlug {
					continue
				}
				candidates[pairKey(aID, bID)] = pagePair{AID: aID, BID: bID, Similarity: 1.0}
			}
		}
	}
	return candidates
}

// dedupSortCapPairs sorts candidates (Similarity descending, then AID, then
// BID ascending) for determinism and caps the result at limit, enforcing the
// one-pair-per-page rule: once a page is used in a pair it is not reused in
// another pair in the same call.
func dedupSortCapPairs(candidates map[string]pagePair, limit int) []pagePair {
	pairs := make([]pagePair, 0, len(candidates))
	for _, pair := range candidates {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Similarity != pairs[j].Similarity {
			return pairs[i].Similarity > pairs[j].Similarity
		}
		if pairs[i].AID != pairs[j].AID {
			return pairs[i].AID < pairs[j].AID
		}
		return pairs[i].BID < pairs[j].BID
	})

	taken := make(map[string]struct{}, 2*len(pairs))
	result := make([]pagePair, 0, limit)
	for _, pair := range pairs {
		if len(result) >= limit {
			break
		}
		if _, used := taken[pair.AID]; used {
			continue
		}
		if _, used := taken[pair.BID]; used {
			continue
		}
		taken[pair.AID] = struct{}{}
		taken[pair.BID] = struct{}{}
		result = append(result, pair)
	}
	return result
}

// pairKey returns a dedup key stable regardless of argument order.
func pairKey(aID, bID string) string {
	if aID > bID {
		aID, bID = bID, aID
	}
	return aID + "|" + bID
}

// sameLeafGroups groups page IDs by the leaf topic (a topic with no child
// topics) they are placed under. Pages with no placement — root-unplaced
// pages — never appear in any group.
func sameLeafGroups(tree TopicTree) map[string][]string {
	hasChildren := make(map[string]struct{}, len(tree.Topics))
	for _, topic := range tree.Topics {
		if topic.ParentID != "" {
			hasChildren[topic.ParentID] = struct{}{}
		}
	}
	groups := make(map[string][]string)
	for _, placement := range tree.Placements {
		if _, notLeaf := hasChildren[placement.TopicID]; notLeaf {
			continue
		}
		groups[placement.TopicID] = append(groups[placement.TopicID], placement.PageID)
	}
	return groups
}

// qualityCandidates scores pages on three signals — orphan (no incoming
// links and the current revision has no outgoing links), short body
// (revision Markdown under curationBodyByteFloor bytes), and sentence-shaped
// title (normalizeProposedTitle returns "") — and selects pages whose score
// is at least 2. Results are sorted by score descending then PageID
// ascending, and capped at limit.
func qualityCandidates(
	catalog PageCatalog,
	revisions map[string]PageRevision,
	incomingLinks map[string]int,
	limit int,
) []qualityCandidate {
	candidates := make([]qualityCandidate, 0, len(catalog))
	for _, entry := range catalog {
		revision, ok := revisions[entry.ID]
		if !ok {
			continue
		}
		var signals []string
		if incomingLinks[entry.ID] == 0 && len(revision.Links) == 0 {
			signals = append(signals, "orphan")
		}
		if len(revision.Markdown) < curationBodyByteFloor {
			signals = append(signals, "short-body")
		}
		if normalizeProposedTitle(entry.Title) == "" {
			signals = append(signals, "sentence-title")
		}
		if len(signals) < 2 {
			continue
		}
		candidates = append(candidates, qualityCandidate{PageID: entry.ID, Signals: signals})
	}
	sort.Slice(candidates, func(i, j int) bool {
		si, sj := len(candidates[i].Signals), len(candidates[j].Signals)
		if si != sj {
			return si > sj
		}
		return candidates[i].PageID < candidates[j].PageID
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}
