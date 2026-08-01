package pagewiki

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RunCurationRound runs one curation pass over the Page catalog: it detects
// duplicate-page and low-quality-page candidates, judges each with the
// configured Curator, verifies destructive verdicts, and executes the ones
// that survive. It is idempotent for an unchanged catalog: a round is keyed
// by a fingerprint of the catalog's (pageID, currentRevisionID) pairs, and a
// repeat call for the same fingerprint returns the stored run untouched,
// without invoking the curator or embedder at all — unless that stored run's
// Status is RunStatusFailed, which is re-run rather than treated as a
// permanent skip: an all-failed round (e.g. the curator was unreachable) must
// not poison its fingerprint and block every future retry against an
// unchanged catalog. All-failed runs are still persisted for audit; only the
// idempotent-skip decision treats them as not-yet-succeeded.
//
// curationMu serializes rounds on this Service instance; a round is never
// run concurrently with itself.
func (s *Service) RunCurationRound(ctx context.Context) (CurationRun, error) {
	if s.curator == nil {
		return CurationRun{}, errors.New("Page Wiki curation is not configured: use WithCurator")
	}

	s.curationMu.Lock()
	defer s.curationMu.Unlock()

	catalog, err := s.repository.PageCatalog(ctx)
	if err != nil {
		return CurationRun{}, fmt.Errorf("load Page catalog: %w", err)
	}
	fingerprint := catalogFingerprint(catalog)
	runID := stableID("curation-run", fingerprint)
	switch existing, err := s.repository.CurationRun(ctx, runID); {
	case err == nil && existing.Status != RunStatusFailed:
		return existing, nil
	case err != nil && !errors.Is(err, ErrNotFound):
		return CurationRun{}, fmt.Errorf("load CurationRun: %w", err)
	}

	directives, err := s.repository.GenerationSettings(ctx)
	if err != nil {
		return CurationRun{}, fmt.Errorf("load generation settings: %w", err)
	}
	tree, err := s.repository.TopicTree(ctx)
	if err != nil {
		return CurationRun{}, fmt.Errorf("load topic tree: %w", err)
	}
	ordinals, err := s.repository.SourceRevisionOrdinals(ctx)
	if err != nil {
		return CurationRun{}, fmt.Errorf("load Source revision ordinals: %w", err)
	}

	catalogByID := make(map[string]PageCatalogEntry, len(catalog))
	revisions := make(map[string]PageRevision, len(catalog))
	incomingLinks := make(map[string]int, len(catalog))
	for _, entry := range catalog {
		catalogByID[entry.ID] = entry
		revision, err := s.repository.PageRevision(ctx, entry.CurrentRevisionID)
		if err != nil {
			return CurationRun{}, fmt.Errorf("load PageRevision %q: %w", entry.CurrentRevisionID, err)
		}
		revisions[entry.ID] = revision
		links, err := s.repository.PageLinks(ctx, entry.ID)
		if err != nil {
			return CurationRun{}, fmt.Errorf("load PageLinks %q: %w", entry.ID, err)
		}
		incomingLinks[entry.ID] = len(links.Incoming)
	}

	vectors := s.curationVectors(ctx, catalog)
	pairs := duplicatePairs(catalog, vectors, tree, s.curationConfig.PairLimit)

	// A page the pair lane is already judging (and may merge, conflict, or
	// leave in place) is excluded from the quality lane in the same round: a
	// page the pair lane retires (as a merge loser) would otherwise still be
	// a quality-lane candidate against the round-start snapshot, and its
	// retire would pass repository CAS (retiring never changes
	// CurrentRevisionID) and clobber the successor the pair lane just set.
	touchedByPairs := make(map[string]struct{}, 2*len(pairs))
	for _, pair := range pairs {
		touchedByPairs[pair.AID] = struct{}{}
		touchedByPairs[pair.BID] = struct{}{}
	}
	qualityCatalog := catalog
	if len(touchedByPairs) > 0 {
		qualityCatalog = make(PageCatalog, 0, len(catalog))
		for _, entry := range catalog {
			if _, touched := touchedByPairs[entry.ID]; touched {
				continue
			}
			qualityCatalog = append(qualityCatalog, entry)
		}
	}
	quality := qualityCandidates(qualityCatalog, revisions, incomingLinks, s.curationConfig.PageLimit)

	changed := false
	outcomes := make([]CurationOutcome, 0, len(pairs)+len(quality))
	for _, pair := range pairs {
		outcome, didChange := s.processPairCandidate(
			ctx, runID, pair, catalogByID, revisions, incomingLinks, ordinals, directives,
		)
		outcomes = append(outcomes, outcome)
		changed = changed || didChange
	}
	for _, candidate := range quality {
		outcome, didChange := s.processQualityCandidate(
			ctx, runID, candidate, catalogByID, revisions, ordinals, directives,
		)
		outcomes = append(outcomes, outcome)
		changed = changed || didChange
	}

	run := CurationRun{
		ID:          runID,
		Fingerprint: fingerprint,
		Status:      summarizeCurationRun(outcomes),
		Outcomes:    outcomes,
	}
	if err := s.repository.SaveCurationRun(ctx, run); err != nil {
		return CurationRun{}, fmt.Errorf("save CurationRun: %w", err)
	}
	if changed {
		s.markTreeDirty()
	}
	return run, nil
}

// StartCurationMaintenance runs curation rounds in the background on a
// fixed tick: WithCurator must have configured a Curator/embedder and a
// positive CurationConfig.Interval, or this is a no-op — curation stays
// entirely opt-in, and an Interval of 0 (the zero value) disables the loop
// without starting a busy ticker. Each tick calls RunCurationRound; any
// error is logged and the loop keeps ticking rather than stopping, so one
// bad round (e.g. the curator briefly unreachable) never wedges curation
// until process restart. It stops when ctx is cancelled.
func (s *Service) StartCurationMaintenance(ctx context.Context) {
	if s.curator == nil || s.curationConfig.Interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(s.curationConfig.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.RunCurationRound(ctx); err != nil {
					s.logger.Warn("Page Wiki curation round failed", "error", err)
				}
			}
		}
	}()
}

// curationVectors resolves an embedding vector for every catalog page: a
// cached PageEmbedding is reused when its RevisionID matches the page's
// current revision; every other page's title+summary text is embedded in one
// batch call. On any load or embed failure, the pair lane is disabled for
// this round: it logs a warning and returns nil rather than a partial map, so
// duplicatePairs never mixes freshly embedded and stale-but-cached vectors
// with vectors it never got a chance to refresh. When no embedder was
// configured (WithCurator was called with a nil embedder), the pair lane is
// disabled the same way: this is the "embedding service unavailable" case,
// and only the quality lane needs to keep running.
func (s *Service) curationVectors(ctx context.Context, catalog PageCatalog) map[string][]float32 {
	if s.curationEmbedder == nil {
		s.logger.Warn("Page Wiki curation embeddings skipped", "stage", "embed", "error", "no embedder configured")
		return nil
	}
	cached, err := s.repository.PageEmbeddings(ctx)
	if err != nil {
		s.logger.Warn("Page Wiki curation embeddings skipped", "stage", "load cache", "error", err)
		return nil
	}
	cachedByID := make(map[string]PageEmbedding, len(cached))
	for _, embedding := range cached {
		cachedByID[embedding.PageID] = embedding
	}

	vectors := make(map[string][]float32, len(catalog))
	stale := make([]PageCatalogEntry, 0, len(catalog))
	for _, entry := range catalog {
		if embedding, ok := cachedByID[entry.ID]; ok && embedding.RevisionID == entry.CurrentRevisionID {
			vectors[entry.ID] = embedding.Vector
			continue
		}
		stale = append(stale, entry)
	}
	if len(stale) == 0 {
		return vectors
	}

	texts := make([]string, len(stale))
	for index, entry := range stale {
		texts[index] = curationEmbeddingText(entry)
	}
	embedded, err := s.curationEmbedder.Embed(ctx, texts)
	if err != nil {
		s.logger.Warn("Page Wiki curation embeddings skipped", "stage", "embed", "error", err)
		return nil
	}
	if len(embedded) != len(texts) {
		s.logger.Warn(
			"Page Wiki curation embeddings skipped", "stage", "embed",
			"error", fmt.Errorf("expected %d vectors, got %d", len(texts), len(embedded)),
		)
		return nil
	}
	for index, entry := range stale {
		vector := embedded[index]
		vectors[entry.ID] = vector
		embedding := PageEmbedding{PageID: entry.ID, RevisionID: entry.CurrentRevisionID, Vector: vector}
		if err := s.repository.SavePageEmbedding(ctx, embedding); err != nil {
			s.logger.Warn("Page Wiki curation embedding save failed", "page", entry.ID, "error", err)
		}
	}
	return vectors
}

// curationPageView projects a catalog entry and its current revision into
// the view the Curator judges. Quotes are one CurationQuote per SourceAnchor
// across every citation on the revision; SourceOrdinal looks up the anchor's
// SourceRevisionID in ordinals, defaulting to 0 when absent.
func (s *Service) curationPageView(
	entry PageCatalogEntry,
	revision PageRevision,
	ordinals map[string]int,
) CurationPageView {
	quotes := make([]CurationQuote, 0, len(revision.Citations))
	for _, citation := range revision.Citations {
		for _, anchor := range citation.SourceAnchors {
			quotes = append(quotes, CurationQuote{
				ExactText:     anchor.ExactQuote,
				SourceOrdinal: ordinals[anchor.SourceRevisionID],
			})
		}
	}
	return CurationPageView{
		PageID:   entry.ID,
		Title:    entry.Title,
		Summary:  entry.Summary,
		Markdown: revision.Markdown,
		Quotes:   quotes,
	}
}

// processPairCandidate judges one duplicate-pair candidate and, for an
// accepted merge/conflict verdict, executes it. It returns the recorded
// outcome and whether it actually changed a Page (published a revision or
// retired a Page), which the caller uses to decide whether to mark the topic
// tree dirty.
func (s *Service) processPairCandidate(
	ctx context.Context,
	runID string,
	pair pagePair,
	catalogByID map[string]PageCatalogEntry,
	revisions map[string]PageRevision,
	incomingLinks map[string]int,
	ordinals map[string]int,
	directives GenerationDirectives,
) (CurationOutcome, bool) {
	outcome := CurationOutcome{Kind: "pair", PageIDs: []string{pair.AID, pair.BID}}
	entryA, entryB := catalogByID[pair.AID], catalogByID[pair.BID]
	revisionA, revisionB := revisions[pair.AID], revisions[pair.BID]
	viewA := s.curationPageView(entryA, revisionA, ordinals)
	viewB := s.curationPageView(entryB, revisionB, ordinals)

	verdict, err := s.curator.JudgePair(ctx, PairJudgeInput{A: viewA, B: viewB, Directives: directives})
	if err != nil {
		return degradeToKeep(outcome, err), false
	}
	outcome.Verdict = verdict.Verdict
	outcome.Rationale = verdict.Rationale

	switch verdict.Verdict {
	case CurationVerdictDistinct:
		outcome.Status = TargetStatusSucceeded
		return outcome, false
	case CurationVerdictMerge, CurationVerdictConflict:
		// fall through to execution below
	default:
		return degradeToKeep(outcome, fmt.Errorf("unexpected pair verdict %q", verdict.Verdict)), false
	}

	if verdict.Draft == nil {
		return degradeToKeep(outcome, errors.New("curator merge/conflict verdict missing draft")), false
	}
	if s.verifyDestructive(ctx, verdict.Verdict, verdict.Rationale, []CurationPageView{viewA, viewB}, directives) {
		outcome.Refuted = true
		outcome.Status = TargetStatusSucceeded
		return outcome, false
	}

	survivorEntry, survivorRevision, loserEntry := selectSurvivor(
		entryA, revisionA, entryB, revisionB, incomingLinks,
	)
	related := relatedPages(
		catalogByID,
		map[string]struct{}{entryA.ID: {}, entryB.ID: {}},
		revisionA.Links, revisionB.Links,
	)
	carried := make([]PageCitation, 0, len(revisionA.Citations)+len(revisionB.Citations))
	carried = append(carried, revisionA.Citations...)
	carried = append(carried, revisionB.Citations...)

	draft, err := buildCurationDraft(survivorEntry.Slug, *verdict.Draft, carried, related)
	if err != nil {
		return degradeToKeep(outcome, err), false
	}
	newPage, newRevision, err := s.buildCurationPublication(ctx, pageFromCatalogEntry(survivorEntry), survivorRevision, draft)
	if err != nil {
		outcome.Status = TargetStatusFailed
		outcome.Error = err.Error()
		return outcome, false
	}
	if err := s.repository.PublishPage(ctx, PagePublication{Page: newPage, Revision: newRevision}); err != nil {
		outcome.Status = TargetStatusFailed
		outcome.Error = err.Error()
		return outcome, false
	}
	if err := s.repository.RetirePage(ctx, RetireRequest{
		PageID:                 loserEntry.ID,
		ExpectedBaseRevisionID: loserEntry.CurrentRevisionID,
		SuccessorPageID:        survivorEntry.ID,
		RunID:                  runID,
	}); err != nil {
		// The survivor's new revision already landed; no compensation is
		// attempted here — the next round re-detects the (now-updated)
		// survivor and the still-active loser and self-heals.
		outcome.Status = TargetStatusFailed
		outcome.Error = err.Error()
		return outcome, true
	}
	outcome.Status = TargetStatusSucceeded
	return outcome, true
}

// processQualityCandidate judges one low-quality-page candidate and, for an
// accepted retire/rewrite verdict, executes it.
func (s *Service) processQualityCandidate(
	ctx context.Context,
	runID string,
	candidate qualityCandidate,
	catalogByID map[string]PageCatalogEntry,
	revisions map[string]PageRevision,
	ordinals map[string]int,
	directives GenerationDirectives,
) (CurationOutcome, bool) {
	outcome := CurationOutcome{Kind: "page", PageIDs: []string{candidate.PageID}}
	entry := catalogByID[candidate.PageID]
	revision := revisions[candidate.PageID]
	view := s.curationPageView(entry, revision, ordinals)

	verdict, err := s.curator.JudgePage(ctx, PageJudgeInput{Page: view, Signals: candidate.Signals, Directives: directives})
	if err != nil {
		return degradeToKeep(outcome, err), false
	}
	outcome.Verdict = verdict.Verdict
	outcome.Rationale = verdict.Rationale

	switch verdict.Verdict {
	case CurationVerdictKeep:
		outcome.Status = TargetStatusSucceeded
		return outcome, false
	case CurationVerdictRetire:
		if s.verifyDestructive(ctx, verdict.Verdict, verdict.Rationale, []CurationPageView{view}, directives) {
			outcome.Refuted = true
			outcome.Status = TargetStatusSucceeded
			return outcome, false
		}
		if err := s.repository.RetirePage(ctx, RetireRequest{
			PageID:                 entry.ID,
			ExpectedBaseRevisionID: entry.CurrentRevisionID,
			RunID:                  runID,
		}); err != nil {
			outcome.Status = TargetStatusFailed
			outcome.Error = err.Error()
			return outcome, false
		}
		outcome.Status = TargetStatusSucceeded
		return outcome, true
	case CurationVerdictRewrite:
		if verdict.Draft == nil {
			return degradeToKeep(outcome, errors.New("curator rewrite verdict missing draft")), false
		}
		related := relatedPages(catalogByID, map[string]struct{}{entry.ID: {}}, revision.Links)
		draft, err := buildCurationDraft(entry.Slug, *verdict.Draft, revision.Citations, related)
		if err != nil {
			return degradeToKeep(outcome, err), false
		}
		newPage, newRevision, err := s.buildCurationPublication(ctx, pageFromCatalogEntry(entry), revision, draft)
		if err != nil {
			outcome.Status = TargetStatusFailed
			outcome.Error = err.Error()
			return outcome, false
		}
		if err := s.repository.PublishPage(ctx, PagePublication{Page: newPage, Revision: newRevision}); err != nil {
			outcome.Status = TargetStatusFailed
			outcome.Error = err.Error()
			return outcome, false
		}
		outcome.Status = TargetStatusSucceeded
		return outcome, true
	default:
		return degradeToKeep(outcome, fmt.Errorf("unexpected page verdict %q", verdict.Verdict)), false
	}
}

// verifyDestructive asks the Curator to double-check a merge/conflict/retire
// verdict before it is executed. A verify error is treated the same as an
// explicit refutation: conservative, so an unreachable or misbehaving
// skeptic never lets a destructive verdict through unchecked.
func (s *Service) verifyDestructive(
	ctx context.Context,
	action CurationVerdict,
	rationale string,
	pages []CurationPageView,
	directives GenerationDirectives,
) bool {
	verdict, err := s.curator.Verify(ctx, VerifyInput{
		Action: action, Rationale: rationale, Pages: pages, Directives: directives,
	})
	if err != nil {
		return true
	}
	return verdict.Refuted
}

// degradeToKeep records a candidate that could not be judged or could not
// produce a usable draft as a failed keep: the LLM's decision is unusable,
// so no change is enacted, exactly as if the verdict had been keep.
func degradeToKeep(outcome CurationOutcome, err error) CurationOutcome {
	outcome.Verdict = CurationVerdictKeep
	outcome.Status = TargetStatusFailed
	outcome.Error = err.Error()
	return outcome
}

// selectSurvivor picks the merge/conflict survivor: the page with more
// incoming links wins; ties break on the lexicographically smaller PageID.
func selectSurvivor(
	entryA PageCatalogEntry, revisionA PageRevision,
	entryB PageCatalogEntry, revisionB PageRevision,
	incomingLinks map[string]int,
) (survivorEntry PageCatalogEntry, survivorRevision PageRevision, loserEntry PageCatalogEntry) {
	linksA, linksB := incomingLinks[entryA.ID], incomingLinks[entryB.ID]
	switch {
	case linksA > linksB:
		return entryA, revisionA, entryB
	case linksB > linksA:
		return entryB, revisionB, entryA
	case entryA.ID < entryB.ID:
		return entryA, revisionA, entryB
	default:
		return entryB, revisionB, entryA
	}
}

// relatedPages unions the outgoing-link targets of one or more link sets into
// RelatedPage values resolved against the catalog, skipping any target in
// exclude and deduping by target ID. Order follows first appearance across
// linkSets in argument order.
func relatedPages(
	catalogByID map[string]PageCatalogEntry,
	exclude map[string]struct{},
	linkSets ...[]PageLink,
) []RelatedPage {
	seen := make(map[string]struct{})
	var related []RelatedPage
	for _, links := range linkSets {
		for _, link := range links {
			if _, excluded := exclude[link.TargetPageID]; excluded {
				continue
			}
			if _, duplicate := seen[link.TargetPageID]; duplicate {
				continue
			}
			entry, found := catalogByID[link.TargetPageID]
			if !found {
				continue
			}
			seen[link.TargetPageID] = struct{}{}
			related = append(related, RelatedPage{ID: entry.ID, Title: entry.Title})
		}
	}
	return related
}

// pageFromCatalogEntry builds the Page value a curation publication targets.
// Catalog entries are always active pages (PageCatalog hides retired ones),
// so the zero-value Status/SuccessorPageID/RetiredByRunID fields are correct
// as-is.
func pageFromCatalogEntry(entry PageCatalogEntry) Page {
	return Page{
		ID:                entry.ID,
		Slug:              entry.Slug,
		Title:             entry.Title,
		CurrentRevisionID: entry.CurrentRevisionID,
	}
}

// buildCurationCitations builds PageCitations directly from CitationDraft
// anchors carried forward by buildCurationDraft, rather than re-resolving
// Evidence against a single source revision the way buildCitations does:
// curation evidence is a union across the pages being rewritten or merged.
func buildCurationCitations(
	revisionID string,
	sections map[string]SectionDraft,
	drafts []CitationDraft,
) ([]PageCitation, error) {
	citations := make([]PageCitation, 0, len(drafts))
	for index, draft := range drafts {
		section, found := sections[draft.SectionKey]
		if !found {
			return nil, fmt.Errorf("%w: unknown Section key %q", ErrInvalidCitation, draft.SectionKey)
		}
		start, end, err := uniqueTextRange(section.Markdown, draft.ExactText)
		if err != nil {
			return nil, fmt.Errorf("%w: page text: %w", ErrInvalidCitation, err)
		}
		if len(draft.Anchors) == 0 {
			return nil, fmt.Errorf("%w: anchors are required", ErrInvalidCitation)
		}
		citations = append(citations, PageCitation{
			ID:             stableID("citation", revisionID, fmt.Sprint(index)),
			PageRevisionID: revisionID,
			SectionKey:     draft.SectionKey,
			StartByte:      start,
			EndByte:        end,
			ExactText:      draft.ExactText,
			SourceAnchors:  append([]SourceAnchor(nil), draft.Anchors...),
		})
	}
	return citations, nil
}

// buildCurationPublication builds the Page and PageRevision for a curation
// rewrite/merge, mirroring buildPublication but sourcing citations from
// CitationDraft.Anchors (buildCurationCitations) instead of re-resolving
// Evidence against a brief and source revision. linkable is always nil: a
// curation draft's links only ever target already-published catalog pages.
func (s *Service) buildCurationPublication(
	ctx context.Context,
	page Page,
	currentRevision PageRevision,
	draft PageDraft,
) (Page, PageRevision, error) {
	sections, sectionByKey, err := validateDraft(PageBrief{Action: PageActionUpdate}, draft)
	if err != nil {
		return Page{}, PageRevision{}, err
	}
	baseRevisionID := currentRevision.ID
	revisionID, err := revisionIdentity(page.ID, baseRevisionID, draft)
	if err != nil {
		return Page{}, PageRevision{}, err
	}
	citations, err := buildCurationCitations(revisionID, sectionByKey, draft.Citations)
	if err != nil {
		return Page{}, PageRevision{}, err
	}
	links, err := s.buildLinks(ctx, revisionID, sectionByKey, draft.Links, nil)
	if err != nil {
		return Page{}, PageRevision{}, err
	}
	revision := PageRevision{
		ID:             revisionID,
		PageID:         page.ID,
		BaseRevisionID: baseRevisionID,
		Title:          draft.Title,
		Summary:        draft.Summary,
		Sections:       sections,
		Markdown:       renderMarkdown(draft),
		Citations:      citations,
		Links:          links,
	}
	pageValue := page
	pageValue.Slug = draft.Slug
	pageValue.Title = draft.Title
	pageValue.CurrentRevisionID = revision.ID
	return pageValue, revision, nil
}

// summarizeCurationRun mirrors summarizeRun's RunStatus semantics for
// CurationOutcomes: all succeeded → succeeded, all failed → failed, a mix →
// partial_success, no candidates → succeeded (vacuously, since 0 == 0).
func summarizeCurationRun(outcomes []CurationOutcome) RunStatus {
	succeeded := 0
	failed := 0
	for _, outcome := range outcomes {
		switch outcome.Status {
		case TargetStatusSucceeded:
			succeeded++
		case TargetStatusFailed:
			failed++
		}
	}
	switch {
	case succeeded == len(outcomes):
		return RunStatusSucceeded
	case failed == len(outcomes):
		return RunStatusFailed
	case succeeded > 0 && failed > 0:
		return RunStatusPartialSuccess
	default:
		return RunStatusPending
	}
}
