package pagewiki_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

// curationAcceptanceSuite exercises RunCurationRound end to end against a
// memory repository, using ScriptedCurator/ScriptedEmbedder to script
// verdicts deterministically.
type curationAcceptanceSuite struct {
	suite.Suite
	repository *memory.Repository
}

func TestCurationAcceptanceSuite(t *testing.T) {
	suite.Run(t, new(curationAcceptanceSuite))
}

func (s *curationAcceptanceSuite) SetupTest() {
	s.repository = memory.NewRepository()
}

// seedPairPage creates one page via a standard InjectSession create run, with
// a body long enough to dodge the quality lane's short-body signal (padded
// filler after the cited quote) and no links, so it only ever surfaces as a
// pair-lane (embedding) candidate, never a quality-lane one. quote is the
// exact text carried by the page's sole citation/anchor.
func (s *curationAcceptanceSuite) seedPairPage(sourceID, slug, title, summary, quote string) pagewiki.Page {
	paragraph := "Additional background detail that keeps this page's body comfortably " +
		"above the short-body threshold so it never doubles as a quality candidate. "
	filler := paragraph + paragraph + paragraph
	return s.seedPage(sourceID, slug, title, summary, quote, quote+"\n\n"+filler)
}

// seedTypedPairPage is seedPairPage with an explicit EntityType on the
// creating brief, for tests asserting a typed page's EntityType survives a
// curation merge republish.
func (s *curationAcceptanceSuite) seedTypedPairPage(
	sourceID, slug, title, summary, quote string, entityType pagewiki.EntityType,
) pagewiki.Page {
	paragraph := "Additional background detail that keeps this page's body comfortably " +
		"above the short-body threshold so it never doubles as a quality candidate. "
	filler := paragraph + paragraph + paragraph
	return s.seedTypedPage(sourceID, slug, title, summary, quote, quote+"\n\n"+filler, entityType)
}

// seedQualityPage creates one page whose body stays short and has no links,
// so it lands in the quality lane (orphan + short-body signals at least).
func (s *curationAcceptanceSuite) seedQualityPage(sourceID, slug, title, summary, quote string) pagewiki.Page {
	return s.seedPage(sourceID, slug, title, summary, quote, quote)
}

// seedTypedQualityPage is seedQualityPage with an explicit EntityType on the
// creating brief, for tests asserting a typed page's EntityType survives a
// curation quality-lane rewrite republish.
func (s *curationAcceptanceSuite) seedTypedQualityPage(
	sourceID, slug, title, summary, quote string, entityType pagewiki.EntityType,
) pagewiki.Page {
	return s.seedTypedPage(sourceID, slug, title, summary, quote, quote, entityType)
}

func (s *curationAcceptanceSuite) seedPage(
	sourceID, slug, title, summary, quote, sectionMarkdown string,
) pagewiki.Page {
	return s.seedTypedPage(sourceID, slug, title, summary, quote, sectionMarkdown, "")
}

func (s *curationAcceptanceSuite) seedTypedPage(
	sourceID, slug, title, summary, quote, sectionMarkdown string, entityType pagewiki.EntityType,
) pagewiki.Page {
	ctx := context.Background()
	raw := []byte("event-1: " + quote)
	eventStart := len("event-1: ")
	planner := pagewiki.ScriptedPlanner{
		Briefs: []pagewiki.PageBrief{{
			Key:              slug,
			Action:           pagewiki.PageActionCreate,
			ProposedSlug:     slug,
			ProposedTitle:    title,
			EvidenceEventIDs: []string{"event-1"},
			EntityType:       entityType,
		}},
	}
	editor := pagewiki.ScriptedEditor{
		Drafts: map[string]pagewiki.PageDraft{
			slug: {
				Slug:    slug,
				Title:   title,
				Summary: summary,
				Sections: []pagewiki.SectionDraft{{
					Key:      "background",
					Heading:  "Background",
					Markdown: sectionMarkdown,
				}},
				Citations: []pagewiki.CitationDraft{{
					SectionKey: "background",
					ExactText:  quote,
					Evidence: []pagewiki.EvidenceQuoteDraft{{
						EventID:   "event-1",
						ExactText: quote,
					}},
				}},
			},
		},
	}
	service := pagewiki.NewService(s.repository, planner, editor)
	result, err := service.InjectSession(ctx, pagewiki.InjectSessionRequest{
		SourceID:       sourceID,
		IdempotencyKey: sourceID + "-injection",
		Raw:            raw,
		Events: []pagewiki.SourceEventInput{{
			ID:        "event-1",
			StartByte: eventStart,
			EndByte:   eventStart + len(quote),
		}},
	})
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	page, err := s.repository.PageBySlug(ctx, slug)
	s.Require().NoError(err)
	return page
}

func embeddingVector() []float32 { return []float32{1, 0, 0} }

func (s *curationAcceptanceSuite) TestGivenNearDuplicatePagesWhenMergedThenSurvivorGetsUnionedCitationsAndLoserIsRetired() {
	ctx := context.Background()
	titleA, summaryA, quoteA := "Deploy Pipeline", "How the deploy pipeline works.", "The team chose Buildkite for deploys."
	titleB, summaryB, quoteB := "Deployment Pipeline", "How the deployment pipeline works.", "The team later confirmed Buildkite for prod releases."
	pageA := s.seedPairPage("session-a", "deploy-pipeline", titleA, summaryA, quoteA)
	pageB := s.seedPairPage("session-b", "deployment-pipeline", titleB, summaryB, quoteB)

	pairKey := pagewiki.PairKey(pageA.ID, pageB.ID)
	curator := pagewiki.ScriptedCurator{
		PairVerdicts: map[string]pagewiki.PairVerdict{
			pairKey: {
				Verdict:   pagewiki.CurationVerdictMerge,
				Rationale: "near-duplicate deploy pipeline pages",
				Draft: &pagewiki.CurationDraft{
					Title:   "Deploy Pipeline",
					Summary: "Unified deploy pipeline page.",
					Sections: []pagewiki.SectionDraft{{
						Key: "overview", Heading: "Overview",
						Markdown: "The deploy pipeline covers build and release.",
					}},
				},
			},
		},
		Verifies: map[string]pagewiki.VerifyVerdict{pairKey: {Refuted: false}},
	}
	embedder := pagewiki.ScriptedEmbedder{Vectors: map[string][]float32{
		titleA + "\n" + summaryA: embeddingVector(),
		titleB + "\n" + summaryB: embeddingVector(),
	}}
	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, embedder, pagewiki.CurationConfig{}, nil),
	)

	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, run.Status)
	s.Require().Len(run.Outcomes, 1)
	outcome := run.Outcomes[0]
	s.Require().Equal(pagewiki.CurationVerdictMerge, outcome.Verdict)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, outcome.Status)
	s.Require().False(outcome.Refuted)

	survivorID, loserID := pageA.ID, pageB.ID
	if pageB.ID < pageA.ID {
		survivorID, loserID = pageB.ID, pageA.ID
	}

	catalog, err := s.repository.PageCatalog(ctx)
	s.Require().NoError(err)
	catalogIDs := make([]string, 0, len(catalog))
	for _, entry := range catalog {
		catalogIDs = append(catalogIDs, entry.ID)
	}
	s.Require().Contains(catalogIDs, survivorID)
	s.Require().NotContains(catalogIDs, loserID)

	loser, err := s.repository.PageByID(ctx, loserID)
	s.Require().NoError(err)
	s.Require().True(loser.Retired())
	s.Require().Equal(survivorID, loser.SuccessorPageID)
	s.Require().Equal(run.ID, loser.RetiredByRunID)

	survivor, err := s.repository.PageByID(ctx, survivorID)
	s.Require().NoError(err)
	newRevision, err := s.repository.PageRevision(ctx, survivor.CurrentRevisionID)
	s.Require().NoError(err)
	citationTexts := make([]string, 0, len(newRevision.Citations))
	for _, citation := range newRevision.Citations {
		citationTexts = append(citationTexts, citation.ExactText)
	}
	s.Require().ElementsMatch([]string{quoteA, quoteB}, citationTexts)

	storedRun, err := s.repository.CurationRun(ctx, run.ID)
	s.Require().NoError(err)
	s.Require().Equal(run, storedRun)

	s.Require().Positive(
		service.PendingTreeTasksForTest(),
		"a merge must queue the republished page for re-placement in the topic tree",
	)
}

// TestGivenMergeLeavesTopicUnderfullThenCurationDissolvesIt pins the round's
// tree hygiene: retiring the merge loser leaves its topic with one active
// page, and the same round dissolves the folder and queues the survivor for
// re-placement.
func (s *curationAcceptanceSuite) TestGivenMergeLeavesTopicUnderfullThenCurationDissolvesIt() {
	ctx := context.Background()
	titleA, summaryA, quoteA := "Deploy Pipeline", "How the deploy pipeline works.", "The team chose Buildkite for deploys."
	titleB, summaryB, quoteB := "Deployment Pipeline", "How the deployment pipeline works.", "The team later confirmed Buildkite for prod releases."
	pageA := s.seedPairPage("session-a", "deploy-pipeline", titleA, summaryA, quoteA)
	pageB := s.seedPairPage("session-b", "deployment-pipeline", titleB, summaryB, quoteB)
	s.Require().NoError(s.repository.ReplaceTopicTree(ctx, pagewiki.TopicTree{
		Topics: []pagewiki.Topic{{ID: "topic-deploys", Slug: "deploys", Title: "Deploys"}},
		Placements: []pagewiki.PagePlacement{
			{PageID: pageA.ID, TopicID: "topic-deploys", Rank: 0},
			{PageID: pageB.ID, TopicID: "topic-deploys", Rank: 1},
		},
	}))

	pairKey := pagewiki.PairKey(pageA.ID, pageB.ID)
	curator := pagewiki.ScriptedCurator{
		PairVerdicts: map[string]pagewiki.PairVerdict{
			pairKey: {
				Verdict:   pagewiki.CurationVerdictMerge,
				Rationale: "near-duplicate deploy pipeline pages",
				Draft: &pagewiki.CurationDraft{
					Title:   "Deploy Pipeline",
					Summary: "Unified deploy pipeline page.",
					Sections: []pagewiki.SectionDraft{{
						Key: "overview", Heading: "Overview",
						Markdown: "The deploy pipeline covers build and release.",
					}},
				},
			},
		},
		Verifies: map[string]pagewiki.VerifyVerdict{pairKey: {Refuted: false}},
	}
	embedder := pagewiki.ScriptedEmbedder{Vectors: map[string][]float32{
		titleA + "\n" + summaryA: embeddingVector(),
		titleB + "\n" + summaryB: embeddingVector(),
	}}
	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, embedder, pagewiki.CurationConfig{}, nil),
	)

	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, run.Status)

	tree, err := s.repository.TopicTree(ctx)
	s.Require().NoError(err)
	s.Require().Empty(tree.Topics,
		"the merge leaves the topic with one active page, so the round must dissolve it")
	s.Require().Empty(tree.Placements)
	s.Require().Positive(
		service.PendingTreeTasksForTest(),
		"the promoted survivor is queued for re-placement",
	)
}

func (s *curationAcceptanceSuite) TestGivenSkepticVerifyRefutesMergeThenNoWritesOccur() {
	ctx := context.Background()
	titleA, summaryA, quoteA := "Deploy Pipeline", "How the deploy pipeline works.", "The team chose Buildkite for deploys."
	titleB, summaryB, quoteB := "Deployment Pipeline", "How the deployment pipeline works.", "The team later confirmed Buildkite for prod releases."
	pageA := s.seedPairPage("session-a", "deploy-pipeline", titleA, summaryA, quoteA)
	pageB := s.seedPairPage("session-b", "deployment-pipeline", titleB, summaryB, quoteB)

	pairKey := pagewiki.PairKey(pageA.ID, pageB.ID)
	curator := pagewiki.ScriptedCurator{
		PairVerdicts: map[string]pagewiki.PairVerdict{
			pairKey: {
				Verdict: pagewiki.CurationVerdictMerge,
				Draft: &pagewiki.CurationDraft{
					Title: "Deploy Pipeline", Summary: "Unified.",
					Sections: []pagewiki.SectionDraft{{Key: "overview", Heading: "Overview", Markdown: "Body."}},
				},
			},
		},
		Verifies: map[string]pagewiki.VerifyVerdict{pairKey: {Refuted: true, Rationale: "not actually duplicates"}},
	}
	embedder := pagewiki.ScriptedEmbedder{Vectors: map[string][]float32{
		titleA + "\n" + summaryA: embeddingVector(),
		titleB + "\n" + summaryB: embeddingVector(),
	}}
	beforePages := s.repository.PageCount()
	beforeRevisions := s.repository.PageRevisionCount()

	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, embedder, pagewiki.CurationConfig{}, nil),
	)
	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Len(run.Outcomes, 1)
	outcome := run.Outcomes[0]
	s.Require().True(outcome.Refuted)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, outcome.Status)
	s.Require().Equal(pagewiki.CurationVerdictMerge, outcome.Verdict)

	s.Require().Equal(beforePages, s.repository.PageCount())
	s.Require().Equal(beforeRevisions, s.repository.PageRevisionCount())
	s.Require().Zero(
		service.PendingTreeTasksForTest(),
		"a refuted verdict changes nothing, so no re-placement is queued",
	)
}

func (s *curationAcceptanceSuite) TestGivenQualityCandidateWhenRetiredThenRevisionsStayIntact() {
	ctx := context.Background()
	badTitle := "This orphaned page has a title that reads like a full sentence instead of a concept"
	page := s.seedQualityPage("session-orphan", "orphan-stub", badTitle, "A short orphaned stub.", "The stub was never linked from anywhere.")

	curator := pagewiki.ScriptedCurator{
		PageVerdicts: map[string]pagewiki.PageVerdict{
			page.ID: {Verdict: pagewiki.CurationVerdictRetire, Rationale: "orphaned low-quality stub"},
		},
		Verifies: map[string]pagewiki.VerifyVerdict{page.ID: {Refuted: false}},
	}
	beforeRevisions := s.repository.PageRevisionCount()

	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, pagewiki.ScriptedEmbedder{}, pagewiki.CurationConfig{}, nil),
	)
	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Len(run.Outcomes, 1)
	outcome := run.Outcomes[0]
	s.Require().Equal(pagewiki.CurationVerdictRetire, outcome.Verdict)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, outcome.Status)
	s.Require().False(outcome.Refuted)

	retired, err := s.repository.PageByID(ctx, page.ID)
	s.Require().NoError(err)
	s.Require().True(retired.Retired())
	s.Require().Empty(retired.SuccessorPageID)
	s.Require().Equal(run.ID, retired.RetiredByRunID)
	s.Require().Equal(beforeRevisions, s.repository.PageRevisionCount(), "retiring must not touch revision history")

	// The retired page left the catalog, so there is nothing left to place.
	s.Require().Zero(service.PendingTreeTasksForTest())
}

// TestGivenNilEmbedderThenQualityLaneStillRunsWithEmptyPairLane covers the
// "embedding service unavailable" degradation: WithCurator is given a nil
// TextEmbedder (what buildEmbedder returns when embedding env is unset), so
// the pair lane has no vectors to work with and produces no pair candidates,
// but the quality lane — which needs no embeddings — still judges and
// retires a low-quality orphan page, and the round is saved normally.
func (s *curationAcceptanceSuite) TestGivenNilEmbedderThenQualityLaneStillRunsWithEmptyPairLane() {
	ctx := context.Background()
	badTitle := "This orphaned page has a title that reads like a full sentence instead of a concept"
	page := s.seedQualityPage("session-no-embedder", "no-embedder-stub", badTitle, "A short orphaned stub.", "The stub was never linked from anywhere.")

	curator := pagewiki.ScriptedCurator{
		PageVerdicts: map[string]pagewiki.PageVerdict{
			page.ID: {Verdict: pagewiki.CurationVerdictRetire, Rationale: "orphaned low-quality stub"},
		},
		Verifies: map[string]pagewiki.VerifyVerdict{page.ID: {Refuted: false}},
	}

	s.Require().NotPanics(func() {
		service := pagewiki.NewService(
			s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
			pagewiki.WithCurator(curator, nil, pagewiki.CurationConfig{}, nil),
		)
		run, err := service.RunCurationRound(ctx)
		s.Require().NoError(err)
		s.Require().Equal(pagewiki.RunStatusSucceeded, run.Status)
		s.Require().Len(run.Outcomes, 1)
		outcome := run.Outcomes[0]
		s.Require().Equal(pagewiki.CurationVerdictRetire, outcome.Verdict)
		s.Require().Equal(pagewiki.TargetStatusSucceeded, outcome.Status)
		s.Require().False(outcome.Refuted)

		retired, err := s.repository.PageByID(ctx, page.ID)
		s.Require().NoError(err)
		s.Require().True(retired.Retired())

		storedRun, err := s.repository.CurationRun(ctx, run.ID)
		s.Require().NoError(err)
		s.Require().Equal(run, storedRun)
	})
}

func (s *curationAcceptanceSuite) TestGivenQualityCandidateWhenRewrittenThenNewRevisionCarriesAnchorsAndSlugStaysStable() {
	ctx := context.Background()
	badTitle := "This orphaned page has a title that reads like a full sentence instead of a concept"
	quote := "The stub was never linked from anywhere."
	page := s.seedQualityPage("session-rewrite", "rewrite-stub", badTitle, "A short stub awaiting rewrite.", quote)

	originalRevision, err := s.repository.PageRevision(ctx, page.CurrentRevisionID)
	s.Require().NoError(err)
	s.Require().Len(originalRevision.Citations, 1)
	s.Require().Len(originalRevision.Citations[0].SourceAnchors, 1)
	originalAnchorID := originalRevision.Citations[0].SourceAnchors[0].ID

	curator := pagewiki.ScriptedCurator{
		PageVerdicts: map[string]pagewiki.PageVerdict{
			page.ID: {
				Verdict: pagewiki.CurationVerdictRewrite,
				Draft: &pagewiki.CurationDraft{
					Title:   "Rewrite Stub Concept",
					Summary: "A properly concept-shaped rewrite of the stub.",
					Sections: []pagewiki.SectionDraft{{
						Key: "overview", Heading: "Overview", Markdown: "A rewritten overview.",
					}},
				},
			},
		},
	}
	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, pagewiki.ScriptedEmbedder{}, pagewiki.CurationConfig{}, nil),
	)
	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Len(run.Outcomes, 1)
	outcome := run.Outcomes[0]
	s.Require().Equal(pagewiki.CurationVerdictRewrite, outcome.Verdict)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, outcome.Status)

	rewritten, err := s.repository.PageByID(ctx, page.ID)
	s.Require().NoError(err)
	s.Require().Equal(page.Slug, rewritten.Slug, "rewrite must keep the page's slug unchanged")
	s.Require().NotEqual(page.CurrentRevisionID, rewritten.CurrentRevisionID)

	newRevision, err := s.repository.PageRevision(ctx, rewritten.CurrentRevisionID)
	s.Require().NoError(err)
	s.Require().Equal("Rewrite Stub Concept", newRevision.Title)
	s.Require().Len(newRevision.Citations, 1)
	s.Require().Len(newRevision.Citations[0].SourceAnchors, 1)
	s.Require().Equal(originalAnchorID, newRevision.Citations[0].SourceAnchors[0].ID, "anchors must carry forward verbatim")
}

// Regression test for a Critical final-review finding: pageFromCatalogEntry
// (which buildCurationPublication's Page value is seeded from) did not copy
// entry.EntityType, so every quality-lane rewrite silently wiped a typed
// page's EntityType back to "" (read as concept forever) on republish.
func (s *curationAcceptanceSuite) TestGivenTypedQualityCandidateWhenRewrittenThenEntityTypeSurvivesRepublish() {
	ctx := context.Background()
	badTitle := "This orphaned page has a title that reads like a full sentence instead of a concept"
	quote := "The stub was never linked from anywhere."
	page := s.seedTypedQualityPage(
		"session-typed-rewrite", "typed-rewrite-stub", badTitle,
		"A short stub awaiting rewrite.", quote, pagewiki.EntityTypeSystem,
	)
	s.Require().Equal(pagewiki.EntityTypeSystem, page.EntityType, "sanity: seeded page must carry the type under test")

	curator := pagewiki.ScriptedCurator{
		PageVerdicts: map[string]pagewiki.PageVerdict{
			page.ID: {
				Verdict: pagewiki.CurationVerdictRewrite,
				Draft: &pagewiki.CurationDraft{
					Title:   "Rewrite Stub Concept",
					Summary: "A properly concept-shaped rewrite of the stub.",
					Sections: []pagewiki.SectionDraft{{
						Key: "overview", Heading: "Overview", Markdown: "A rewritten overview.",
					}},
				},
			},
		},
	}
	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, pagewiki.ScriptedEmbedder{}, pagewiki.CurationConfig{}, nil),
	)
	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Len(run.Outcomes, 1)
	s.Require().Equal(pagewiki.CurationVerdictRewrite, run.Outcomes[0].Verdict)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, run.Outcomes[0].Status)

	rewritten, err := s.repository.PageByID(ctx, page.ID)
	s.Require().NoError(err)
	s.Require().Equal(
		pagewiki.EntityTypeSystem, rewritten.EntityType,
		"a curation rewrite republish must not wipe the page's established EntityType",
	)
}

// Regression test for the same Critical finding's merge/pair-lane path: the
// survivor Page a merge republishes is also built via pageFromCatalogEntry,
// so a typed survivor's type was silently wiped the same way.
func (s *curationAcceptanceSuite) TestGivenTypedSurvivorWhenMergedThenEntityTypeSurvivesRepublish() {
	ctx := context.Background()
	titleA, summaryA, quoteA := "Deploy Pipeline", "How the deploy pipeline works.", "The team chose Buildkite for deploys."
	titleB, summaryB, quoteB := "Deployment Pipeline", "How the deployment pipeline works.", "The team later confirmed Buildkite for prod releases."
	pageA := s.seedTypedPairPage("session-a", "deploy-pipeline-typed", titleA, summaryA, quoteA, pagewiki.EntityTypeSystem)
	pageB := s.seedTypedPairPage("session-b", "deployment-pipeline-typed", titleB, summaryB, quoteB, pagewiki.EntityTypeSystem)
	s.Require().Equal(pagewiki.EntityTypeSystem, pageA.EntityType, "sanity: both merge inputs must carry the type under test")
	s.Require().Equal(pagewiki.EntityTypeSystem, pageB.EntityType, "sanity: both merge inputs must carry the type under test")

	pairKey := pagewiki.PairKey(pageA.ID, pageB.ID)
	curator := pagewiki.ScriptedCurator{
		PairVerdicts: map[string]pagewiki.PairVerdict{
			pairKey: {
				Verdict:   pagewiki.CurationVerdictMerge,
				Rationale: "near-duplicate typed deploy pipeline pages",
				Draft: &pagewiki.CurationDraft{
					Title:   "Deploy Pipeline",
					Summary: "Unified deploy pipeline page.",
					Sections: []pagewiki.SectionDraft{{
						Key: "overview", Heading: "Overview",
						Markdown: "The deploy pipeline covers build and release.",
					}},
				},
			},
		},
		Verifies: map[string]pagewiki.VerifyVerdict{pairKey: {Refuted: false}},
	}
	embedder := pagewiki.ScriptedEmbedder{Vectors: map[string][]float32{
		titleA + "\n" + summaryA: embeddingVector(),
		titleB + "\n" + summaryB: embeddingVector(),
	}}
	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, embedder, pagewiki.CurationConfig{}, nil),
	)

	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Len(run.Outcomes, 1)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, run.Outcomes[0].Status)
	s.Require().False(run.Outcomes[0].Refuted)

	survivorID := pageA.ID
	if pageB.ID < pageA.ID {
		survivorID = pageB.ID
	}
	survivor, err := s.repository.PageByID(ctx, survivorID)
	s.Require().NoError(err)
	s.Require().Equal(
		pagewiki.EntityTypeSystem, survivor.EntityType,
		"a curation merge republish must not wipe the survivor's established EntityType",
	)
}

// Regression test for an Important final-review finding: relatedPages (which
// rebuilds RelatedPage values from an existing revision's typed outgoing
// links for a curation republish) dropped link.RelationType, so
// relatedKnowledgeSection downstream stamped every carried-forward link
// relates-to even when the prior revision had a typed relation such as
// depends-on.
func (s *curationAcceptanceSuite) TestGivenTypedLinkQualityCandidateWhenRewrittenThenRelationTypeSurvivesRepublish() {
	ctx := context.Background()
	target := s.seedPairPage(
		"session-typed-link-target", "typed-link-target",
		"SQLite Storage", "How SQLite stores data.", "The team chose SQLite for storage.",
	)

	badTitle := "This orphaned page has a title that reads like a full sentence instead of a concept"
	quote := "The stub links out to another page for storage."
	slug := "typed-link-stub"
	raw := []byte("event-1: " + quote)
	eventStart := len("event-1: ")
	planner := pagewiki.ScriptedPlanner{
		Briefs: []pagewiki.PageBrief{{
			Key:              slug,
			Action:           pagewiki.PageActionCreate,
			ProposedSlug:     slug,
			ProposedTitle:    badTitle,
			EvidenceEventIDs: []string{"event-1"},
			RelatedPages: []pagewiki.RelatedPage{
				{ID: target.ID, Title: target.Title, Relation: pagewiki.RelationTypeDependsOn},
			},
		}},
	}
	editor := pagewiki.ScriptedEditor{
		Drafts: map[string]pagewiki.PageDraft{
			slug: {
				Slug:    slug,
				Title:   badTitle,
				Summary: "A short stub that depends on another page.",
				Sections: []pagewiki.SectionDraft{
					{Key: "background", Heading: "Background", Markdown: quote},
					{Key: "related-knowledge", Heading: "Related knowledge", Markdown: "See also: " + target.Title + "."},
				},
				Citations: []pagewiki.CitationDraft{{
					SectionKey: "background",
					ExactText:  quote,
					Evidence: []pagewiki.EvidenceQuoteDraft{{
						EventID:   "event-1",
						ExactText: quote,
					}},
				}},
				Links: []pagewiki.LinkDraft{{
					SectionKey:   "related-knowledge",
					ExactText:    target.Title,
					TargetPageID: target.ID,
					RelationType: pagewiki.RelationTypeDependsOn,
				}},
			},
		},
	}
	service := pagewiki.NewService(s.repository, planner, editor)
	result, err := service.InjectSession(ctx, pagewiki.InjectSessionRequest{
		SourceID:       "session-typed-link",
		IdempotencyKey: "session-typed-link-injection",
		Raw:            raw,
		Events: []pagewiki.SourceEventInput{{
			ID:        "event-1",
			StartByte: eventStart,
			EndByte:   eventStart + len(quote),
		}},
	})
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	page, err := s.repository.PageBySlug(ctx, slug)
	s.Require().NoError(err)

	originalRevision, err := s.repository.PageRevision(ctx, page.CurrentRevisionID)
	s.Require().NoError(err)
	s.Require().Len(originalRevision.Links, 1)
	s.Require().Equal(
		pagewiki.RelationTypeDependsOn, originalRevision.Links[0].RelationType,
		"sanity: seeded page must carry the typed link under test",
	)

	curator := pagewiki.ScriptedCurator{
		PageVerdicts: map[string]pagewiki.PageVerdict{
			page.ID: {
				Verdict: pagewiki.CurationVerdictRewrite,
				Draft: &pagewiki.CurationDraft{
					Title:   "Typed Link Stub Concept",
					Summary: "A properly concept-shaped rewrite that still depends on the target.",
					Sections: []pagewiki.SectionDraft{{
						Key: "overview", Heading: "Overview", Markdown: "A rewritten overview.",
					}},
				},
			},
		},
	}
	curationService := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, pagewiki.ScriptedEmbedder{}, pagewiki.CurationConfig{}, nil),
	)
	run, err := curationService.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Len(run.Outcomes, 1)
	s.Require().Equal(pagewiki.CurationVerdictRewrite, run.Outcomes[0].Verdict)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, run.Outcomes[0].Status)

	rewritten, err := s.repository.PageByID(ctx, page.ID)
	s.Require().NoError(err)
	newRevision, err := s.repository.PageRevision(ctx, rewritten.CurrentRevisionID)
	s.Require().NoError(err)
	s.Require().Len(newRevision.Links, 1)
	s.Require().Equal(
		pagewiki.RelationTypeDependsOn, newRevision.Links[0].RelationType,
		"republish must preserve the prior revision's typed relation, not downgrade it to relates-to",
	)
}

func (s *curationAcceptanceSuite) TestGivenUnresolvableConflictWithNoDraftThenCandidateDegradesToKeepAndPagesStayUntouched() {
	ctx := context.Background()
	titleA, summaryA, quoteA := "Deploy Pipeline", "How the deploy pipeline works.", "The team chose Buildkite for deploys."
	titleB, summaryB, quoteB := "Deployment Pipeline", "How the deployment pipeline works.", "The team later confirmed Buildkite for prod releases."
	pageA := s.seedPairPage("session-a", "deploy-pipeline", titleA, summaryA, quoteA)
	pageB := s.seedPairPage("session-b", "deployment-pipeline", titleB, summaryB, quoteB)

	pairKey := pagewiki.PairKey(pageA.ID, pageB.ID)
	curator := pagewiki.ScriptedCurator{
		PairVerdicts: map[string]pagewiki.PairVerdict{
			pairKey: {Verdict: pagewiki.CurationVerdictConflict, Rationale: "irreconcilable", Draft: nil},
		},
	}
	embedder := pagewiki.ScriptedEmbedder{Vectors: map[string][]float32{
		titleA + "\n" + summaryA: embeddingVector(),
		titleB + "\n" + summaryB: embeddingVector(),
	}}
	beforePages := s.repository.PageCount()
	beforeRevisions := s.repository.PageRevisionCount()

	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, embedder, pagewiki.CurationConfig{}, nil),
	)
	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Len(run.Outcomes, 1)
	outcome := run.Outcomes[0]
	s.Require().Equal(pagewiki.CurationVerdictKeep, outcome.Verdict)
	s.Require().Equal(pagewiki.TargetStatusFailed, outcome.Status)
	s.Require().NotEmpty(outcome.Error)

	s.Require().Equal(beforePages, s.repository.PageCount())
	s.Require().Equal(beforeRevisions, s.repository.PageRevisionCount())
	s.Require().Zero(
		service.PendingTreeTasksForTest(),
		"a degraded-to-keep candidate changes nothing, so no re-placement is queued",
	)
}

func (s *curationAcceptanceSuite) TestGivenUnchangedCatalogWhenRunTwiceThenSecondRunIsAnIdempotentSkip() {
	ctx := context.Background()
	page := s.seedQualityPage("session-keep", "keep-stub", "Keep Stub", "A stub that should just be kept.", "Nothing about this stub needs to change.")

	var judgeCalls int
	curator := pagewiki.ScriptedCurator{
		PageVerdicts: map[string]pagewiki.PageVerdict{
			page.ID: {Verdict: pagewiki.CurationVerdictKeep},
		},
		JudgeCalls: &judgeCalls,
	}
	embedder := &countingEmbedder{inner: pagewiki.ScriptedEmbedder{}}
	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, embedder, pagewiki.CurationConfig{}, nil),
	)

	first, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, first.Status)
	s.Require().Equal(1, judgeCalls)
	firstEmbedCalls := embedder.calls.Load()

	second, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Equal(first, second)
	s.Require().Equal(1, judgeCalls, "idempotent skip must not re-judge")
	s.Require().Equal(firstEmbedCalls, embedder.calls.Load(), "idempotent skip must not re-embed")
}

func (s *curationAcceptanceSuite) TestGivenCASRaceOnSurvivorWhenExecutingMergeThenPublishFailsAndLoserStaysActive() {
	ctx := context.Background()
	titleA, summaryA, quoteA := "Deploy Pipeline", "How the deploy pipeline works.", "The team chose Buildkite for deploys."
	titleB, summaryB, quoteB := "Deployment Pipeline", "How the deployment pipeline works.", "The team later confirmed Buildkite for prod releases."
	pageA := s.seedPairPage("session-a", "deploy-pipeline", titleA, summaryA, quoteA)
	pageB := s.seedPairPage("session-b", "deployment-pipeline", titleB, summaryB, quoteB)

	survivorID, loserID := pageA.ID, pageB.ID
	survivorSlug, survivorTitle, survivorOriginalRevisionID := pageA.Slug, pageA.Title, pageA.CurrentRevisionID
	if pageB.ID < pageA.ID {
		survivorID, loserID = pageB.ID, pageA.ID
		survivorSlug, survivorTitle, survivorOriginalRevisionID = pageB.Slug, pageB.Title, pageB.CurrentRevisionID
	}

	pairKey := pagewiki.PairKey(pageA.ID, pageB.ID)
	scripted := pagewiki.ScriptedCurator{
		PairVerdicts: map[string]pagewiki.PairVerdict{
			pairKey: {
				Verdict: pagewiki.CurationVerdictMerge,
				Draft: &pagewiki.CurationDraft{
					Title: "Deploy Pipeline", Summary: "Unified.",
					Sections: []pagewiki.SectionDraft{{Key: "overview", Heading: "Overview", Markdown: "Body."}},
				},
			},
		},
		Verifies: map[string]pagewiki.VerifyVerdict{pairKey: {Refuted: false}},
	}
	racedRevisionID := "raced-revision-" + survivorID
	curator := &racingCurator{
		Curator: scripted,
		trigger: func(ctx context.Context) error {
			return s.repository.PublishPage(ctx, pagewiki.PagePublication{
				Page: pagewiki.Page{
					ID: survivorID, Slug: survivorSlug, Title: survivorTitle,
					CurrentRevisionID: racedRevisionID,
				},
				Revision: pagewiki.PageRevision{
					ID:             racedRevisionID,
					PageID:         survivorID,
					BaseRevisionID: survivorOriginalRevisionID,
					Title:          survivorTitle,
					Summary:        "Raced summary change.",
					Sections:       []pagewiki.PageSection{{Key: "raced", Heading: "Raced", Markdown: "Raced content."}},
					Markdown:       "# " + survivorTitle + "\n\nRaced summary change.",
				},
			})
		},
	}
	embedder := pagewiki.ScriptedEmbedder{Vectors: map[string][]float32{
		titleA + "\n" + summaryA: embeddingVector(),
		titleB + "\n" + summaryB: embeddingVector(),
	}}
	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, embedder, pagewiki.CurationConfig{}, nil),
	)

	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Len(run.Outcomes, 1)
	outcome := run.Outcomes[0]
	s.Require().Equal(pagewiki.TargetStatusFailed, outcome.Status)
	s.Require().Contains(outcome.Error, pagewiki.ErrRevisionConflict.Error(), "the failure must specifically be a CAS/revision conflict")

	loser, err := s.repository.PageByID(ctx, loserID)
	s.Require().NoError(err)
	s.Require().False(loser.Retired(), "loser must remain active: publish CAS failure must abort before retire is attempted")

	survivor, err := s.repository.PageByID(ctx, survivorID)
	s.Require().NoError(err)
	s.Require().Equal(racedRevisionID, survivor.CurrentRevisionID, "the race's revision must remain in place")
}

func (s *curationAcceptanceSuite) TestGivenEmbedderFailureThenPairLaneIsEmptyButQualityLaneIsStillJudged() {
	ctx := context.Background()
	titleA, summaryA, quoteA := "Deploy Pipeline", "How the deploy pipeline works.", "The team chose Buildkite for deploys."
	titleB, summaryB, quoteB := "Deployment Pipeline", "How the deployment pipeline works.", "The team later confirmed Buildkite for prod releases."
	s.seedPairPage("session-a", "deploy-pipeline", titleA, summaryA, quoteA)
	s.seedPairPage("session-b", "deployment-pipeline", titleB, summaryB, quoteB)
	qualityPage := s.seedQualityPage("session-orphan", "orphan-stub", "Orphan Stub", "A short orphaned stub.", "The stub was never linked from anywhere.")

	var judgeCalls int
	curator := pagewiki.ScriptedCurator{
		PageVerdicts: map[string]pagewiki.PageVerdict{
			qualityPage.ID: {Verdict: pagewiki.CurationVerdictKeep},
		},
		JudgeCalls: &judgeCalls,
	}
	embedder := pagewiki.ScriptedEmbedder{Err: errors.New("embedding backend unavailable")}
	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, embedder, pagewiki.CurationConfig{}, nil),
	)

	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Len(run.Outcomes, 1, "the pair lane must be empty; only the quality candidate is judged")
	outcome := run.Outcomes[0]
	s.Require().Equal("page", outcome.Kind)
	s.Require().Equal([]string{qualityPage.ID}, outcome.PageIDs)
	s.Require().Equal(pagewiki.CurationVerdictKeep, outcome.Verdict)
	s.Require().Equal(1, judgeCalls)
}

func (s *curationAcceptanceSuite) TestGivenTwoOrphanNearDuplicateStubsWhenMergedThenQualityLaneDoesNotRetireTheLoserAgain() {
	ctx := context.Background()
	titleA, summaryA, quoteA := "Deploy Stub", "A short deploy stub.", "The team picked Buildkite."
	titleB, summaryB, quoteB := "Deployment Stub", "A short deployment stub.", "The team later confirmed Buildkite."
	// Both pages are short-bodied and orphaned (no links either way), so
	// each independently clears the quality lane's >=2-signal threshold —
	// exactly the reviewer's repro: a page merged by the pair lane must not
	// also be judged, and retired again, by the quality lane in the same
	// round.
	pageA := s.seedQualityPage("session-a", "deploy-stub", titleA, summaryA, quoteA)
	pageB := s.seedQualityPage("session-b", "deployment-stub", titleB, summaryB, quoteB)

	pairKey := pagewiki.PairKey(pageA.ID, pageB.ID)
	var judgeCalls int
	curator := pagewiki.ScriptedCurator{
		PairVerdicts: map[string]pagewiki.PairVerdict{
			pairKey: {
				Verdict:   pagewiki.CurationVerdictMerge,
				Rationale: "near-duplicate orphaned stubs",
				Draft: &pagewiki.CurationDraft{
					Title:   "Deploy Stub",
					Summary: "Unified deploy stub.",
					Sections: []pagewiki.SectionDraft{{
						Key: "overview", Heading: "Overview", Markdown: "Unified stub body.",
					}},
				},
			},
		},
		Verifies: map[string]pagewiki.VerifyVerdict{
			pairKey:  {Refuted: false},
			pageA.ID: {Refuted: false},
			pageB.ID: {Refuted: false},
		},
		// Both pages also carry a Retire page-verdict: if the quality-lane
		// exclusion regresses, the quality lane would judge and retire the
		// already-merged loser a second time, wiping its SuccessorPageID.
		PageVerdicts: map[string]pagewiki.PageVerdict{
			pageA.ID: {Verdict: pagewiki.CurationVerdictRetire, Rationale: "would incorrectly re-retire after merge"},
			pageB.ID: {Verdict: pagewiki.CurationVerdictRetire, Rationale: "would incorrectly re-retire after merge"},
		},
		JudgeCalls: &judgeCalls,
	}
	embedder := pagewiki.ScriptedEmbedder{Vectors: map[string][]float32{
		titleA + "\n" + summaryA: embeddingVector(),
		titleB + "\n" + summaryB: embeddingVector(),
	}}
	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, embedder, pagewiki.CurationConfig{}, nil),
	)

	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Len(run.Outcomes, 1, "the quality lane must not independently judge either merged page")
	outcome := run.Outcomes[0]
	s.Require().Equal("pair", outcome.Kind)
	s.Require().Equal(pagewiki.CurationVerdictMerge, outcome.Verdict)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, outcome.Status)
	s.Require().Equal(1, judgeCalls, "only the pair judgement must have run")

	survivorID, loserID := pageA.ID, pageB.ID
	if pageB.ID < pageA.ID {
		survivorID, loserID = pageB.ID, pageA.ID
	}
	loser, err := s.repository.PageByID(ctx, loserID)
	s.Require().NoError(err)
	s.Require().True(loser.Retired())
	s.Require().Equal(survivorID, loser.SuccessorPageID, "the successor must survive a would-be conflicting second retire")
	s.Require().Equal(run.ID, loser.RetiredByRunID)
}

func (s *curationAcceptanceSuite) TestGivenJudgeErrorThenCandidateDegradesImmediatelyWithExactlyOneJudgeCallAndNoWrites() {
	ctx := context.Background()
	page := s.seedQualityPage(
		"session-judge-error", "judge-error-stub", "Judge Error Stub",
		"A stub whose curator call always errors.", "Nothing about this stub needs to change.",
	)

	var judgeCalls int
	curator := pagewiki.ScriptedCurator{
		Errs:       map[string]error{page.ID: errors.New("curator backend unavailable")},
		JudgeCalls: &judgeCalls,
	}
	beforePages := s.repository.PageCount()
	beforeRevisions := s.repository.PageRevisionCount()

	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, pagewiki.ScriptedEmbedder{}, pagewiki.CurationConfig{}, nil),
	)
	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Len(run.Outcomes, 1)
	outcome := run.Outcomes[0]
	s.Require().Equal(pagewiki.CurationVerdictKeep, outcome.Verdict)
	s.Require().Equal(pagewiki.TargetStatusFailed, outcome.Status)
	s.Require().NotEmpty(outcome.Error)
	s.Require().Equal(1, judgeCalls, "a judge error must degrade immediately: no adapter-owned retry belongs here")

	s.Require().Equal(beforePages, s.repository.PageCount())
	s.Require().Equal(beforeRevisions, s.repository.PageRevisionCount())
}

func (s *curationAcceptanceSuite) TestGivenAllFailedRoundWhenRerunWithWorkingCuratorThenItRejudgesAndSucceeds() {
	ctx := context.Background()
	page := s.seedQualityPage(
		"session-retry", "retry-stub", "Retry Stub",
		"A stub whose curator call fails once.", "Nothing about this stub needs to change yet.",
	)

	erroringCurator := pagewiki.ScriptedCurator{
		Errs: map[string]error{page.ID: errors.New("curator backend unavailable")},
	}
	firstService := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(erroringCurator, pagewiki.ScriptedEmbedder{}, pagewiki.CurationConfig{}, nil),
	)
	firstRun, err := firstService.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusFailed, firstRun.Status)
	s.Require().Len(firstRun.Outcomes, 1)
	s.Require().Equal(pagewiki.TargetStatusFailed, firstRun.Outcomes[0].Status)

	storedFailed, err := s.repository.CurationRun(ctx, firstRun.ID)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusFailed, storedFailed.Status, "an all-failed round must still be persisted for audit")

	workingCurator := pagewiki.ScriptedCurator{
		PageVerdicts: map[string]pagewiki.PageVerdict{page.ID: {Verdict: pagewiki.CurationVerdictKeep}},
	}
	secondService := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(workingCurator, pagewiki.ScriptedEmbedder{}, pagewiki.CurationConfig{}, nil),
	)
	secondRun, err := secondService.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Equal(firstRun.ID, secondRun.ID, "the fingerprint is unchanged, so the run ID must match")
	s.Require().Equal(pagewiki.RunStatusSucceeded, secondRun.Status)
	s.Require().Len(secondRun.Outcomes, 1)
	s.Require().Equal(pagewiki.TargetStatusSucceeded, secondRun.Outcomes[0].Status)

	storedSucceeded, err := s.repository.CurationRun(ctx, secondRun.ID)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusSucceeded, storedSucceeded.Status, "the successful re-run must overwrite the stored failed record")
}

func (s *curationAcceptanceSuite) TestGivenServiceWithoutCuratorWhenRunCurationRoundThenErrorNotPanic() {
	ctx := context.Background()
	service := pagewiki.NewService(s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{})

	s.Require().NotPanics(func() {
		_, err := service.RunCurationRound(ctx)
		s.Require().Error(err)
	})
}

func (s *curationAcceptanceSuite) TestGivenEmbedderReturningWrongVectorCountThenPairLaneIsEmpty() {
	ctx := context.Background()
	titleA, summaryA, quoteA := "Deploy Pipeline", "How the deploy pipeline works.", "The team chose Buildkite for deploys."
	titleB, summaryB, quoteB := "Deployment Pipeline", "How the deployment pipeline works.", "The team later confirmed Buildkite for prod releases."
	s.seedPairPage("session-a", "deploy-pipeline", titleA, summaryA, quoteA)
	s.seedPairPage("session-b", "deployment-pipeline", titleB, summaryB, quoteB)

	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(pagewiki.ScriptedCurator{}, shortEmbedder{}, pagewiki.CurationConfig{}, nil),
	)

	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Empty(run.Outcomes, "a vector-count mismatch must disable the pair lane entirely, not partially")
}

func (s *curationAcceptanceSuite) TestGivenVerifyErrorThenVerdictIsTreatedAsRefuted() {
	ctx := context.Background()
	page := s.seedQualityPage(
		"session-verify-err", "verify-err-stub", "Verify Err Stub",
		"A stub the curator wants retired.", "Nothing here needs to change but the curator wants it retired.",
	)

	scripted := pagewiki.ScriptedCurator{
		PageVerdicts: map[string]pagewiki.PageVerdict{
			page.ID: {Verdict: pagewiki.CurationVerdictRetire, Rationale: "looks retireable"},
		},
	}
	curator := erroringVerifyCurator{Curator: scripted, err: errors.New("skeptic unreachable")}
	beforePages := s.repository.PageCount()

	service := pagewiki.NewService(
		s.repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{},
		pagewiki.WithCurator(curator, pagewiki.ScriptedEmbedder{}, pagewiki.CurationConfig{}, nil),
	)
	run, err := service.RunCurationRound(ctx)
	s.Require().NoError(err)
	s.Require().Len(run.Outcomes, 1)
	outcome := run.Outcomes[0]
	s.Require().True(outcome.Refuted, "a Verify error must be treated as a conservative refutation")
	s.Require().Equal(pagewiki.TargetStatusSucceeded, outcome.Status)
	s.Require().Equal(pagewiki.CurationVerdictRetire, outcome.Verdict)
	s.Require().Equal(beforePages, s.repository.PageCount())
	s.Require().Zero(service.PendingTreeTasksForTest())
}

// shortEmbedder always returns fewer vectors than texts requested,
// regardless of input, to exercise curationVectors' length-mismatch guard.
type shortEmbedder struct{}

func (shortEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	return [][]float32{{1, 0, 0}}, nil
}

// erroringVerifyCurator delegates JudgePair/JudgePage to the embedded
// Curator and always fails Verify, to test that a Verify error is treated as
// a conservative refutation independent of the underlying judge verdict.
type erroringVerifyCurator struct {
	pagewiki.Curator
	err error
}

func (c erroringVerifyCurator) Verify(context.Context, pagewiki.VerifyInput) (pagewiki.VerifyVerdict, error) {
	return pagewiki.VerifyVerdict{}, c.err
}

// racingCurator delegates JudgePair/JudgePage to the embedded Curator and
// overrides Verify to fire a one-shot side effect first: it simulates a
// concurrent publish landing between judgment and execution.
type racingCurator struct {
	pagewiki.Curator
	trigger    func(context.Context) error
	fired      bool
	mu         sync.Mutex
	triggerErr error
}

func (c *racingCurator) Verify(ctx context.Context, input pagewiki.VerifyInput) (pagewiki.VerifyVerdict, error) {
	c.mu.Lock()
	if !c.fired {
		c.fired = true
		c.triggerErr = c.trigger(ctx)
	}
	c.mu.Unlock()
	if c.triggerErr != nil {
		return pagewiki.VerifyVerdict{}, c.triggerErr
	}
	return c.Curator.Verify(ctx, input)
}

// countingEmbedder wraps a TextEmbedder to count Embed calls, letting the
// idempotency test assert an idempotent skip makes zero embedder calls.
type countingEmbedder struct {
	inner pagewiki.TextEmbedder
	calls atomic.Int32
}

func (e *countingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	e.calls.Add(1)
	return e.inner.Embed(ctx, texts)
}
