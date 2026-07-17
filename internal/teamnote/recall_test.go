package teamnote_test

import (
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type recallSuite struct {
	suite.Suite
}

func TestRecallSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(recallSuite))
}

func (s *recallSuite) TestPlanRecallTraceRecordsStageRejections() {
	request := teamnote.RecallRequest{
		Actor: teamnote.Actor{UserID: "owner", AgentID: "consumer", SessionID: "consumer-session"},
		Query: "alpha beta gamma", TokenBudget: 1000,
	}
	policy := teamnote.RecallPolicy{SemanticThreshold: 0.65, CandidateLimit: 10}

	s.Run("fusion limit and relevance gate", func() {
		candidates := []teamnote.RecallCandidate{
			recallCandidate("note-relevant", "alpha beta", "alpha beta release notes", 0.9, nil),
			recallCandidate("note-no-lane", "delta epsilon", "delta epsilon zeta", 0, nil),
			recallCandidate("note-weak-overlap", "alpha", "alpha single overlap only", 0.4, nil),
		}
		planned, trace := teamnote.PlanRecall(candidates, request, policy)
		s.Require().Len(planned, 1)
		s.Equal(3, trace.Candidates)
		s.Equal(1, trace.FusionKept)
		s.Equal(1, trace.PlannedNotes)
		s.Equal(planned[0].Tokens, trace.PlannedTokens)
		s.Require().Len(trace.Rejections, 2)
		s.Equal(teamnote.RecallRejection{NoteID: "note-no-lane", Reason: teamnote.RejectFusionLimit}, trace.Rejections[0])
		s.Equal(teamnote.RecallRejection{NoteID: "note-weak-overlap", Reason: teamnote.RejectRelevanceGate}, trace.Rejections[1])
	})

	s.Run("semantic lane union without lexical support", func() {
		semantic := 0.9
		candidates := []teamnote.RecallCandidate{
			recallCandidate("note-semantic", "delta epsilon", "delta epsilon zeta", 0, &semantic),
		}
		planned, trace := teamnote.PlanRecall(candidates, request, policy)
		s.Require().Len(planned, 1)
		s.Equal("note-semantic", planned[0].Note.ID)
		s.Equal(1, trace.FusionKept)
		s.Empty(trace.Rejections)
	})

	s.Run("token budget rejection records tokens", func() {
		candidates := []teamnote.RecallCandidate{
			recallCandidate("note-first", "alpha beta", "alpha beta release notes", 0.9, nil),
			recallCandidate("note-second", "beta gamma", "beta gamma deploy status", 0.8, nil),
		}
		fits, _ := teamnote.PlanRecall(candidates[:1], request, policy)
		s.Require().Len(fits, 1)
		limited := request
		limited.TokenBudget = fits[0].Tokens
		planned, trace := teamnote.PlanRecall(candidates, limited, policy)
		s.Require().Len(planned, 1)
		s.Equal("note-first", planned[0].Note.ID)
		s.Require().Len(trace.Rejections, 1)
		s.Equal(teamnote.RejectTokenBudget, trace.Rejections[0].Reason)
		s.Equal("note-second", trace.Rejections[0].NoteID)
		s.Positive(trace.Rejections[0].Tokens)
	})

	s.Run("max items rejection marks remaining candidates", func() {
		candidates := []teamnote.RecallCandidate{
			recallCandidate("note-first", "alpha beta", "alpha beta release notes", 0.9, nil),
			recallCandidate("note-second", "beta gamma", "beta gamma deploy status", 0.8, nil),
			recallCandidate("note-third", "alpha gamma", "alpha gamma audit trail", 0.7, nil),
		}
		limited := request
		limited.MaxItems = 1
		planned, trace := teamnote.PlanRecall(candidates, limited, policy)
		s.Require().Len(planned, 1)
		s.Require().Len(trace.Rejections, 2)
		for _, rejection := range trace.Rejections {
			s.Equal(teamnote.RejectMaxItems, rejection.Reason)
		}
		s.Equal("note-second", trace.Rejections[0].NoteID)
		s.Equal("note-third", trace.Rejections[1].NoteID)
	})

	s.Run("empty query keeps every candidate", func() {
		candidates := []teamnote.RecallCandidate{
			recallCandidate("note-first", "alpha beta", "alpha beta release notes", 0, nil),
			recallCandidate("note-second", "delta epsilon", "delta epsilon zeta", 0, nil),
		}
		blank := request
		blank.Query = ""
		planned, trace := teamnote.PlanRecall(candidates, blank, policy)
		s.Len(planned, 2)
		s.Equal(2, trace.FusionKept)
		s.Empty(trace.Rejections)
	})

	s.Run("duplicate suppression keeps first distinct fact", func() {
		candidates := []teamnote.RecallCandidate{
			recallCandidate("note-first", "alpha beta", "alpha beta release notes freeze window", 0.9, nil),
			recallCandidate("note-duplicate", "gamma delta", "alpha beta release notes freeze window today", 0.8, nil),
			recallCandidate("note-distinct", "alpha beta", "alpha beta blocked on approval", 0.7, nil),
		}
		dedup := teamnote.RecallPolicy{SemanticThreshold: 0.65, CandidateLimit: 10, SuppressDuplicates: true}
		planned, trace := teamnote.PlanRecall(candidates, request, dedup)
		s.Require().Len(planned, 2)
		plannedIDs := []string{planned[0].Note.ID, planned[1].Note.ID}
		s.ElementsMatch([]string{"note-first", "note-distinct"}, plannedIDs)
		s.Require().Len(trace.Rejections, 1)
		s.Equal(teamnote.RecallRejection{NoteID: "note-duplicate", Reason: teamnote.RejectDuplicate}, trace.Rejections[0])

		relaxed := teamnote.RecallPolicy{SemanticThreshold: 0.65, CandidateLimit: 10}
		relaxedPlanned, _ := teamnote.PlanRecall(candidates, request, relaxed)
		s.Len(relaxedPlanned, 3)
	})

	s.Run("degrade related fits note within budget", func() {
		primary := recallCandidate("note-primary", "alpha beta", "alpha beta release notes", 0.9, nil)
		primary.RelatedSubjects = []string{"gamma related"}
		linked := recallCandidate("note-linked", "gamma related", "gamma related supporting detail with extra words", 0.4, nil)
		candidates := []teamnote.RecallCandidate{primary, linked}

		full, _ := teamnote.PlanRecall(candidates, request, teamnote.RecallPolicy{CandidateLimit: 10})
		s.Require().Len(full, 1)
		s.Contains(full[0].Text, "[related:")

		limited := request
		limited.TokenBudget = full[0].Tokens - 1
		degraded, trace := teamnote.PlanRecall(candidates, limited, teamnote.RecallPolicy{CandidateLimit: 10, DegradeRelated: true})
		s.Require().NotEmpty(degraded)
		s.Equal("note-primary", degraded[0].Note.ID)
		s.NotContains(degraded[0].Text, "[related:")
		for _, rejection := range trace.Rejections {
			s.NotEqual(teamnote.RejectTokenBudget, rejection.Reason)
		}

		strict, strictTrace := teamnote.PlanRecall(candidates, limited, teamnote.RecallPolicy{CandidateLimit: 10})
		s.Empty(strict)
		var strictBudgetReject *teamnote.RecallRejection
		for index, rejection := range strictTrace.Rejections {
			if rejection.Reason == teamnote.RejectTokenBudget {
				strictBudgetReject = &strictTrace.Rejections[index]
			}
		}
		s.Require().NotNil(strictBudgetReject)
		s.Equal("note-primary", strictBudgetReject.NoteID)
	})
}

func recallCandidate(id, subject, body string, lexical float64, semantic *float64) teamnote.RecallCandidate {
	occurred := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	return teamnote.RecallCandidate{
		Note: teamnote.Note{
			ID: id, Kind: teamnote.KindStatus, Subject: subject, Body: body,
			Origin:           teamnote.Actor{UserID: "owner", AgentID: "producer", SessionID: "producer-session"},
			Revision:         1,
			SourceOccurredAt: occurred, UpdatedAt: occurred,
		},
		LexicalScore: lexical, SemanticScore: semantic,
	}
}
