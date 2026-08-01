package memory_test

import (
	"context"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type CurationRecordsSuite struct {
	suite.Suite
	ctx        context.Context
	repository *memory.Repository
}

func TestCurationRecordsSuite(t *testing.T) {
	suite.Run(t, new(CurationRecordsSuite))
}

func (s *CurationRecordsSuite) SetupTest() {
	s.ctx = context.Background()
	s.repository = memory.NewRepository()
}

func (s *CurationRecordsSuite) TestGivenCurationRunWhenSavedThenLoadRoundTrips() {
	run := pagewiki.CurationRun{
		ID:          "curation-run-1",
		Fingerprint: "fingerprint-1",
		Status:      pagewiki.RunStatusSucceeded,
		Outcomes: []pagewiki.CurationOutcome{
			{
				Kind:      "pair",
				PageIDs:   []string{"page-1", "page-2"},
				Verdict:   pagewiki.CurationVerdictMerge,
				Rationale: "overlapping content",
				Status:    pagewiki.TargetStatusSucceeded,
			},
		},
	}

	s.Require().NoError(s.repository.SaveCurationRun(s.ctx, run))

	stored, err := s.repository.CurationRun(s.ctx, run.ID)
	s.Require().NoError(err)
	s.Require().Equal(run, stored)
}

func (s *CurationRecordsSuite) TestGivenUnknownCurationRunIDWhenLoadedThenNotFound() {
	_, err := s.repository.CurationRun(s.ctx, "missing")

	s.Require().ErrorIs(err, pagewiki.ErrNotFound)
}

func (s *CurationRecordsSuite) TestGivenCurationRunWhenLoadedThenOutcomesAreIndependentCopies() {
	run := pagewiki.CurationRun{
		ID:     "curation-run-1",
		Status: pagewiki.RunStatusSucceeded,
		Outcomes: []pagewiki.CurationOutcome{
			{Kind: "page", PageIDs: []string{"page-1"}, Verdict: pagewiki.CurationVerdictKeep},
		},
	}
	s.Require().NoError(s.repository.SaveCurationRun(s.ctx, run))

	stored, err := s.repository.CurationRun(s.ctx, run.ID)
	s.Require().NoError(err)
	stored.Outcomes[0].Verdict = pagewiki.CurationVerdictRetire
	stored.Outcomes[0].PageIDs[0] = "mutated"

	again, err := s.repository.CurationRun(s.ctx, run.ID)
	s.Require().NoError(err)
	s.Require().Equal(pagewiki.CurationVerdictKeep, again.Outcomes[0].Verdict)
	s.Require().Equal("page-1", again.Outcomes[0].PageIDs[0])
}

func (s *CurationRecordsSuite) TestGivenSamePageWhenNewEmbeddingSavedThenOldRowIsReplaced() {
	first := pagewiki.PageEmbedding{
		PageID:     "page-1",
		RevisionID: "rev-1",
		Vector:     []float32{0.1, 0.2, 0.3},
	}
	second := pagewiki.PageEmbedding{
		PageID:     "page-1",
		RevisionID: "rev-2",
		Vector:     []float32{0.4, 0.5, 0.6},
	}

	s.Require().NoError(s.repository.SavePageEmbedding(s.ctx, first))
	s.Require().NoError(s.repository.SavePageEmbedding(s.ctx, second))

	embeddings, err := s.repository.PageEmbeddings(s.ctx)
	s.Require().NoError(err)
	s.Require().Len(embeddings, 1)
	s.Require().Equal(second, embeddings[0])
}

func (s *CurationRecordsSuite) TestGivenStoredEmbeddingWhenLoadedThenVectorIsIndependentCopy() {
	embedding := pagewiki.PageEmbedding{
		PageID:     "page-1",
		RevisionID: "rev-1",
		Vector:     []float32{0.1, 0.2, 0.3},
	}
	s.Require().NoError(s.repository.SavePageEmbedding(s.ctx, embedding))

	embeddings, err := s.repository.PageEmbeddings(s.ctx)
	s.Require().NoError(err)
	s.Require().Len(embeddings, 1)
	embeddings[0].Vector[0] = 9.9

	again, err := s.repository.PageEmbeddings(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(float32(0.1), again[0].Vector[0])
}

func (s *CurationRecordsSuite) TestGivenSourceRevisionsSavedInOrderWhenOrdinalsReadThenOrderIsReflected() {
	first := pagewiki.SourceRevision{ID: "rev-a", SourceID: "source-1", SHA256: "sha-a"}
	second := pagewiki.SourceRevision{ID: "rev-b", SourceID: "source-1", SHA256: "sha-b"}
	third := pagewiki.SourceRevision{ID: "rev-c", SourceID: "source-1", SHA256: "sha-c"}

	s.Require().NoError(s.repository.SaveSourceRevision(s.ctx, first))
	s.Require().NoError(s.repository.SaveSourceRevision(s.ctx, second))
	s.Require().NoError(s.repository.SaveSourceRevision(s.ctx, third))

	ordinals, err := s.repository.SourceRevisionOrdinals(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(0, ordinals["rev-a"])
	s.Require().Equal(1, ordinals["rev-b"])
	s.Require().Equal(2, ordinals["rev-c"])
}

func (s *CurationRecordsSuite) TestGivenSameRevisionResavedWhenOrdinalsReadThenOriginalOrdinalIsKept() {
	revision := pagewiki.SourceRevision{ID: "rev-a", SourceID: "source-1", SHA256: "sha-a"}
	other := pagewiki.SourceRevision{ID: "rev-b", SourceID: "source-1", SHA256: "sha-b"}

	s.Require().NoError(s.repository.SaveSourceRevision(s.ctx, revision))
	s.Require().NoError(s.repository.SaveSourceRevision(s.ctx, other))
	// Re-saving the same revision must be a no-op idempotent save that keeps
	// its original ordinal rather than being reassigned to the end.
	s.Require().NoError(s.repository.SaveSourceRevision(s.ctx, revision))

	ordinals, err := s.repository.SourceRevisionOrdinals(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(0, ordinals["rev-a"])
	s.Require().Equal(1, ordinals["rev-b"])
}
