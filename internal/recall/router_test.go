package recall_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/recall"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type routerSuite struct {
	suite.Suite
}

func TestRouterSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(routerSuite))
}

func (s *routerSuite) TestPassiveSearchStartsBothPathsAndCancelsWikiAfterSufficientEvidence() {
	teamStarted := make(chan struct{})
	wikiStarted := make(chan struct{})
	wikiCancelled := make(chan struct{})
	team := teamNotePathFunc(func(context.Context, teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
		close(teamStarted)
		<-wikiStarted
		return sufficientEnvelope("The release is Friday."), nil
	})
	wiki := &wikiPath{
		hint: func(ctx context.Context, _ recall.SearchRequest) (recall.MemoryHit, error) {
			close(wikiStarted)
			<-ctx.Done()
			close(wikiCancelled)
			return recall.MemoryHit{}, ctx.Err()
		},
	}
	router, err := recall.NewRouter(team, wiki, recall.Config{EnablePassiveWikiHint: true})
	s.Require().NoError(err)

	result, err := router.Search(context.Background(), passiveRequest())

	s.Require().NoError(err)
	s.Require().Len(result.Hits, 1)
	s.Equal(recall.DispositionEvidence, result.Hits[0].Disposition)
	s.True(result.EvidenceSufficient)
	s.Equal(recall.PathCompleted, result.Trace.TeamNote.Status)
	s.Equal(recall.PathCancelled, result.Trace.WikiHint.Status)
	s.Eventually(func() bool {
		select {
		case <-wikiCancelled:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	select {
	case <-teamStarted:
	default:
		s.Fail("team note path did not start")
	}
}

func (s *routerSuite) TestPassiveSearchWaitsForWikiWhenEvidenceIsInsufficient() {
	wikiRelease := make(chan struct{})
	team := teamNotePathFunc(func(context.Context, teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
		return insufficientEnvelope("The release date is not recorded."), nil
	})
	wiki := &wikiPath{hint: func(context.Context, recall.SearchRequest) (recall.MemoryHit, error) {
		<-wikiRelease
		return recall.MemoryHit{Text: "Search the release runbook.", Tokens: 6}, nil
	}}
	router, err := recall.NewRouter(team, wiki, recall.Config{EnablePassiveWikiHint: true})
	s.Require().NoError(err)

	done := make(chan searchOutcome, 1)
	go func() {
		result, searchErr := router.Search(context.Background(), passiveRequest())
		done <- searchOutcome{result: result, err: searchErr}
	}()
	s.Never(func() bool { return len(done) > 0 }, 20*time.Millisecond, time.Millisecond)
	close(wikiRelease)
	outcome := <-done
	s.Require().NoError(outcome.err)
	result := outcome.result

	s.Require().Len(result.Hits, 2)
	s.Equal(recall.DispositionEvidence, result.Hits[0].Disposition)
	s.Equal(recall.DispositionHint, result.Hits[1].Disposition)
	s.False(result.EvidenceSufficient)
	s.Equal(recall.PathCompleted, result.Trace.WikiHint.Status)
}

func (s *routerSuite) TestWikiResultCannotReturnBeforeTeamNote() {
	teamRelease := make(chan struct{})
	team := teamNotePathFunc(func(context.Context, teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
		<-teamRelease
		return insufficientEnvelope("No final decision."), nil
	})
	wiki := &wikiPath{hint: func(context.Context, recall.SearchRequest) (recall.MemoryHit, error) {
		return recall.MemoryHit{Text: "Search the decision log.", Tokens: 5}, nil
	}}
	router, err := recall.NewRouter(team, wiki, recall.Config{EnablePassiveWikiHint: true})
	s.Require().NoError(err)

	done := make(chan struct{})
	searchErrors := make(chan error, 1)
	go func() {
		_, searchErr := router.Search(context.Background(), passiveRequest())
		searchErrors <- searchErr
		close(done)
	}()
	s.Never(func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 20*time.Millisecond, time.Millisecond)
	close(teamRelease)
	s.Eventually(func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	s.Require().NoError(<-searchErrors)
}

func (s *routerSuite) TestCompletedWikiHintIsDiscardedWhenTeamNoteIsSufficient() {
	teamRelease := make(chan struct{})
	team := teamNotePathFunc(func(context.Context, teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
		<-teamRelease
		return sufficientEnvelope("The release is Friday."), nil
	})
	wikiCompleted := make(chan struct{})
	wiki := &wikiPath{hint: func(context.Context, recall.SearchRequest) (recall.MemoryHit, error) {
		close(wikiCompleted)
		return recall.MemoryHit{Text: "Search the release runbook.", Tokens: 6}, nil
	}}
	router, err := recall.NewRouter(team, wiki, recall.Config{EnablePassiveWikiHint: true})
	s.Require().NoError(err)

	done := make(chan searchOutcome, 1)
	go func() {
		result, searchErr := router.Search(context.Background(), passiveRequest())
		done <- searchOutcome{result: result, err: searchErr}
	}()
	<-wikiCompleted
	close(teamRelease)
	outcome := <-done
	s.Require().NoError(outcome.err)
	result := outcome.result

	s.Require().Len(result.Hits, 1)
	s.Equal(recall.DispositionEvidence, result.Hits[0].Disposition)
	s.True(result.Trace.EarlyReturn)
	s.Equal(recall.PathCompleted, result.Trace.WikiHint.Status)
	s.Equal(1, result.Trace.WikiHint.Candidates)
	s.Equal("discarded_sufficient_team_note_evidence", result.Trace.WikiHint.Reason)
}

func (s *routerSuite) TestPassiveWikiFailureDegradesToTeamNoteEvidence() {
	team := teamNotePathFunc(func(context.Context, teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
		return insufficientEnvelope("The owner is Riley."), nil
	})
	wiki := &wikiPath{hint: func(context.Context, recall.SearchRequest) (recall.MemoryHit, error) {
		return recall.MemoryHit{}, errors.New("wiki unavailable")
	}}
	router, err := recall.NewRouter(team, wiki, recall.Config{EnablePassiveWikiHint: true})
	s.Require().NoError(err)

	result, err := router.Search(context.Background(), passiveRequest())

	s.Require().NoError(err)
	s.Require().Len(result.Hits, 1)
	s.Equal(recall.PathFailed, result.Trace.WikiHint.Status)
	s.Equal("wiki unavailable", result.Trace.WikiHint.Error)
}

func (s *routerSuite) TestActiveSearchAndGetUseOnlyTheWikiPath() {
	teamCalls := 0
	team := teamNotePathFunc(func(context.Context, teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
		teamCalls++
		return teamnote.NoteEnvelope{}, nil
	})
	wiki := &wikiPath{
		search: func(_ context.Context, request recall.SearchRequest) ([]recall.MemoryHit, error) {
			s.Equal(recall.IntentActive, request.Intent)
			return []recall.MemoryHit{{Ref: "wiki:release", Text: "Release runbook"}}, nil
		},
		get: func(_ context.Context, request recall.GetRequest) (recall.MemoryDocument, error) {
			s.Equal("wiki:release", request.Ref)
			return recall.MemoryDocument{Ref: request.Ref, Text: "Full runbook"}, nil
		},
	}
	router, err := recall.NewRouter(team, wiki, recall.Config{})
	s.Require().NoError(err)

	result, err := router.Search(context.Background(), recall.SearchRequest{
		Intent: recall.IntentActive, Source: recall.SourceLLMWiki, Query: "release", TokenBudget: 64,
	})
	s.Require().NoError(err)
	s.Require().Len(result.Hits, 1)
	s.Equal(recall.DispositionReference, result.Hits[0].Disposition)
	document, err := router.Get(context.Background(), recall.GetRequest{Ref: "wiki:release"})
	s.Require().NoError(err)
	s.Equal("Full runbook", document.Text)
	s.Zero(teamCalls)
}

func (s *routerSuite) TestRejectsInvalidConfigurationAndRequests() {
	_, err := recall.NewRouter(nil, &wikiPath{}, recall.Config{})
	s.Require().Error(err)
	team := teamNotePathFunc(func(context.Context, teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
		return teamnote.NoteEnvelope{}, nil
	})
	router, err := recall.NewRouter(team, nil, recall.Config{})
	s.Require().NoError(err)

	_, err = router.Search(context.Background(), recall.SearchRequest{})
	s.Require().Error(err)
	_, err = router.Get(context.Background(), recall.GetRequest{Ref: "wiki:any"})
	s.Require().Error(err)
}

type searchOutcome struct {
	result recall.SearchResult
	err    error
}

type teamNotePathFunc func(context.Context, teamnote.RecallRequest) (teamnote.NoteEnvelope, error)

func (f teamNotePathFunc) RecallNotes(ctx context.Context, request teamnote.RecallRequest) (teamnote.NoteEnvelope, error) {
	return f(ctx, request)
}

type wikiPath struct {
	hint   func(context.Context, recall.SearchRequest) (recall.MemoryHit, error)
	search func(context.Context, recall.SearchRequest) ([]recall.MemoryHit, error)
	get    func(context.Context, recall.GetRequest) (recall.MemoryDocument, error)
}

func (w *wikiPath) Hint(ctx context.Context, request recall.SearchRequest) (recall.MemoryHit, error) {
	return w.hint(ctx, request)
}

func (w *wikiPath) Search(ctx context.Context, request recall.SearchRequest) ([]recall.MemoryHit, error) {
	return w.search(ctx, request)
}

func (w *wikiPath) Get(ctx context.Context, request recall.GetRequest) (recall.MemoryDocument, error) {
	return w.get(ctx, request)
}

func passiveRequest() recall.SearchRequest {
	return recall.SearchRequest{
		Intent: recall.IntentPassive, Query: "When is the release?", TokenBudget: 64,
		Actor: teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "session"},
	}
}

func sufficientEnvelope(text string) teamnote.NoteEnvelope {
	return teamnote.NoteEnvelope{
		Items: []string{text}, Tokens: 5,
		Details:  []teamnote.RecalledNote{{NoteID: "note", Text: text, Relevance: 0.8}},
		Decision: teamnote.RecallDecisionSummary{EvidenceSufficient: true},
	}
}

func insufficientEnvelope(text string) teamnote.NoteEnvelope {
	result := sufficientEnvelope(text)
	result.Decision = teamnote.RecallDecisionSummary{
		ReasonCodes: []teamnote.RecallReasonCode{teamnote.RecallReasonFactCoverage},
	}
	return result
}
