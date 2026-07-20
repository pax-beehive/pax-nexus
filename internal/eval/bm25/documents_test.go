package bm25_test

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/bm25"
	"github.com/stretchr/testify/suite"
)

func (s *rawSuite) TestRankDocumentsPrefersExactEvidence() {
	ranked, err := bm25.RankDocuments([]bm25.Document{
		{ID: "later", Text: "Rollback evidence has an owner."},
		{ID: "gold", Text: "Ops Lead owns the rollback evidence pack if July 26 slips."},
	}, "Who owns the rollback evidence pack if July 26 slips?")

	s.Require().NoError(err)
	s.Require().Len(ranked, 2)
	s.Equal("gold", ranked[0].ID)
	s.Positive(ranked[0].Score)
}

func TestDocuments(t *testing.T) { suite.Run(t, new(rawSuite)) }
