package recalladapter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/recalladapter"
	"github.com/pax-beehive/pax-nexus/internal/recall"
	"github.com/stretchr/testify/suite"
)

type adapterSuite struct {
	suite.Suite
}

func TestAdapterSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(adapterSuite))
}

func (s *adapterSuite) TestGivenMissingReaderWhenCreatedThenValidationFails() {
	adapter, err := recalladapter.New(nil)

	s.Nil(adapter)
	s.Require().Error(err)
}

func (s *adapterSuite) TestGivenReaderFailureWhenSearchedThenErrorIsWrapped() {
	want := errors.New("index unavailable")
	adapter, err := recalladapter.New(searchReaderFunc(func(
		context.Context,
		string,
	) ([]pagewiki.SearchResult, error) {
		return nil, want
	}))
	s.Require().NoError(err)

	_, err = adapter.Search(context.Background(), searchRequest(64, 0))

	s.Require().ErrorIs(err, want)
	s.Contains(err.Error(), "search PageWiki")
}

func (s *adapterSuite) TestGivenSearchLimitsWhenSearchedThenResultsArePackedDeterministically() {
	tests := []struct {
		name        string
		tokenBudget int
		maxItems    int
		wantRefs    []string
	}{
		{
			name:        "token budget skips oversized result and keeps later fit",
			tokenBudget: 2,
			wantRefs:    []string{"pagewiki:revision/revision-short"},
		},
		{
			name:        "max items stops after first packed result",
			tokenBudget: 64,
			maxItems:    1,
			wantRefs:    []string{"pagewiki:revision/revision-long"},
		},
		{
			name:        "zero max items is unlimited",
			tokenBudget: 64,
			wantRefs: []string{
				"pagewiki:revision/revision-long",
				"pagewiki:revision/revision-short",
			},
		},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			adapter, err := recalladapter.New(searchReaderFunc(func(
				context.Context,
				string,
			) ([]pagewiki.SearchResult, error) {
				return []pagewiki.SearchResult{
					searchResult("revision-long", "twelve characters"),
					searchResult("revision-short", "fit"),
				}, nil
			}))
			s.Require().NoError(err)

			hits, searchErr := adapter.Search(
				context.Background(),
				searchRequest(test.tokenBudget, test.maxItems),
			)

			s.Require().NoError(searchErr)
			refs := make([]string, len(hits))
			for index, hit := range hits {
				refs[index] = hit.Ref
			}
			s.Equal(test.wantRefs, refs)
		})
	}
}

func (s *adapterSuite) TestGetRejectsUnknownRevision() {
	adapter, err := recalladapter.New(searchReaderFunc(func(
		context.Context,
		string,
	) ([]pagewiki.SearchResult, error) {
		return nil, nil
	}))
	s.Require().NoError(err)

	_, err = adapter.Get(context.Background(), recall.GetRequest{Ref: "pagewiki:revision/revision-1"})

	s.Require().ErrorIs(err, pagewiki.ErrNotFound)
}

type searchReaderFunc func(context.Context, string) ([]pagewiki.SearchResult, error)

func (f searchReaderFunc) Search(
	ctx context.Context,
	query string,
) ([]pagewiki.SearchResult, error) {
	return f(ctx, query)
}

func (f searchReaderFunc) PageByID(
	context.Context,
	string,
) (pagewiki.Page, error) {
	return pagewiki.Page{}, pagewiki.ErrNotFound
}

func (f searchReaderFunc) PageRevision(
	context.Context,
	string,
) (pagewiki.PageRevision, error) {
	return pagewiki.PageRevision{}, pagewiki.ErrNotFound
}

func searchRequest(tokenBudget, maxItems int) recall.SearchRequest {
	return recall.SearchRequest{
		Intent: recall.IntentActive, Source: recall.SourcePageWiki,
		Query: "result", TokenBudget: tokenBudget, MaxItems: maxItems,
	}
}

func searchResult(revisionID, passage string) pagewiki.SearchResult {
	return pagewiki.SearchResult{
		Page: pagewiki.Page{
			ID: "page-" + revisionID, Slug: "slug-" + revisionID,
			Title: "Title", CurrentRevisionID: revisionID,
		},
		RevisionID: revisionID,
		SectionKey: "section",
		Passage:    passage,
		Score:      1,
	}
}
