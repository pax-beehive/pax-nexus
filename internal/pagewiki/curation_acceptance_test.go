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

// seedQualityPage creates one page whose body stays short and has no links,
// so it lands in the quality lane (orphan + short-body signals at least).
func (s *curationAcceptanceSuite) seedQualityPage(sourceID, slug, title, summary, quote string) pagewiki.Page {
	return s.seedPage(sourceID, slug, title, summary, quote, quote)
}

func (s *curationAcceptanceSuite) seedPage(
	sourceID, slug, title, summary, quote, sectionMarkdown string,
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

	s.Require().True(service.TreeDirtyForTest(), "a merge must mark the topic tree dirty")
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

	s.Require().True(service.TreeDirtyForTest())
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
	s.Require().NotEmpty(outcome.Error)

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
