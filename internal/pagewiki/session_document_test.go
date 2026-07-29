package pagewiki_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type sessionDocumentSuite struct {
	suite.Suite
}

func TestSessionDocumentSuite(t *testing.T) {
	suite.Run(t, new(sessionDocumentSuite))
}

func (s *sessionDocumentSuite) TestCreatesKnowledgePagesUnderSemanticTopics() {
	repository := memory.NewRepository()
	service := pagewiki.NewService(
		repository,
		pagewiki.SessionDocumentPlanner{},
		pagewiki.SessionDocumentEditor{},
	)
	request := knowledgeRequest("first")

	created, err := service.InjectSession(context.Background(), request)

	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, created.Run.Status)
	s.Require().Len(created.Run.Targets, 2)
	navigation, err := repository.Navigation(context.Background())
	s.Require().NoError(err)
	s.Require().Len(navigation.Roots, 1)
	s.Equal("Engineering", navigation.Roots[0].Title)
	s.Require().Len(navigation.Roots[0].Children, 2)
	s.Equal([]string{"Retrieval", "Wiki Architecture"}, []string{
		navigation.Roots[0].Children[0].Title,
		navigation.Roots[0].Children[1].Title,
	})
	for _, child := range navigation.Roots[0].Children {
		for _, page := range child.Pages {
			s.NotContains(page.Title, "runtime-demo")
			s.NotContains(page.Slug, "session-")
		}
	}
}

func (s *sessionDocumentSuite) TestUpdatesStableKnowledgePageWithNewEvidence() {
	repository := memory.NewRepository()
	service := pagewiki.NewService(
		repository,
		pagewiki.SessionDocumentPlanner{},
		pagewiki.SessionDocumentEditor{},
	)
	first, err := service.InjectSession(context.Background(), knowledgeRequest("first"))
	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, first.Run.Status)

	second := knowledgeRequest("second")
	second.Raw = []byte(strings.ReplaceAll(
		string(second.Raw),
		"保留完整 source 原文和精确引用区间",
		"保留完整 source 原文、精确引用区间和 immutable revision",
	))
	second.Events[0].EndByte = strings.Index(string(second.Raw), "\n\n")
	second.Events[1].StartByte = second.Events[0].EndByte + 2
	second.Events[1].EndByte = len(second.Raw)
	updated, err := service.InjectSession(context.Background(), second)

	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, updated.Run.Status)
	catalog, err := repository.PageCatalog(context.Background())
	s.Require().NoError(err)
	s.Len(catalog, 2)
	for _, entry := range catalog {
		history, historyErr := repository.PageRevisionHistory(context.Background(), entry.ID)
		s.Require().NoError(historyErr)
		s.Len(history, 2)
	}
}

func (s *sessionDocumentSuite) TestMergesMultipleFactsFromOneEventWithoutDuplicatingBriefEvidence() {
	repository := memory.NewRepository()
	service := pagewiki.NewService(
		repository,
		pagewiki.SessionDocumentPlanner{},
		pagewiki.SessionDocumentEditor{},
	)
	raw := "## Wiki 数据模型\n页面使用 immutable revision。\n\n## Wiki 数据模型\nRevisioned tree 保存来源坐标。"
	request := pagewiki.InjectSessionRequest{
		SourceID:       "session:agent-1:wiki-data-model",
		IdempotencyKey: "same-event",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{{
			ID: "event-wiki", StartByte: 0, EndByte: len(raw),
		}},
	}

	result, err := service.InjectSession(context.Background(), request)

	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Len(result.Run.Targets, 1)
	s.Equal(pagewiki.TargetStatusSucceeded, result.Run.Targets[0].Status)
}

func (s *sessionDocumentSuite) TestCreatesExactTextLinksAndDerivedBacklinksForRelatedKnowledge() {
	repository := memory.NewRepository()
	service := pagewiki.NewService(
		repository,
		pagewiki.SessionDocumentPlanner{},
		pagewiki.SessionDocumentEditor{},
	)
	raw := "## Wiki 数据模型\n页面使用 immutable revision。\n\n## Evidence grounding\nCitation 保存精确的 source anchor。"
	request := pagewiki.InjectSessionRequest{
		SourceID:       "session:agent-1:xanadu-links",
		IdempotencyKey: "xanadu-links",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{{
			ID: "event-wiki", StartByte: 0, EndByte: len(raw),
		}},
	}

	result, err := service.InjectSession(context.Background(), request)

	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	dataModel, err := repository.PageBySlug(context.Background(), "knowledge-wiki-data-model")
	s.Require().NoError(err)
	grounding, err := repository.PageBySlug(context.Background(), "knowledge-evidence-grounding")
	s.Require().NoError(err)
	dataModelLinks, err := repository.PageLinks(context.Background(), dataModel.ID)
	s.Require().NoError(err)
	groundingLinks, err := repository.PageLinks(context.Background(), grounding.ID)
	s.Require().NoError(err)
	s.Require().Len(dataModelLinks.Incoming, 1)
	s.Equal(grounding.ID, dataModelLinks.Incoming[0].SourcePage.ID)
	s.Require().Len(groundingLinks.Outgoing, 1)
	s.Equal(dataModel.ID, groundingLinks.Outgoing[0].TargetPage.ID)
	s.Equal("Wiki Data Model", groundingLinks.Outgoing[0].Link.ExactText)

	request.IdempotencyKey = "xanadu-links-update"
	updated, err := service.InjectSession(context.Background(), request)
	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, updated.Run.Status)
	updatedLinks, err := repository.PageLinks(context.Background(), grounding.ID)
	s.Require().NoError(err)
	s.Require().Len(updatedLinks.Outgoing, 1)
}

func (s *sessionDocumentSuite) TestSessionDocumentPlannerCarriesEvidenceQuotes() {
	raw := "## Wiki 数据模型\n页面使用 immutable revision。"
	briefs, err := pagewiki.SessionDocumentPlanner{}.Plan(
		context.Background(),
		pagewiki.PlanInput{SourceRevision: pagewiki.SourceRevision{
			ID:  "source-revision-1",
			Raw: []byte(raw),
			Events: []pagewiki.SourceEvent{{
				ID: "event-1", StartByte: 0, EndByte: len(raw),
			}},
		}},
	)
	s.Require().NoError(err)
	s.Require().Len(briefs, 1)
	s.Require().Len(briefs[0].Evidence, 1)
	s.Equal("event-1", briefs[0].Evidence[0].EventID)
	s.Equal("页面使用 immutable revision。", briefs[0].Evidence[0].ExactText)
}

func knowledgeRequest(key string) pagewiki.InjectSessionRequest {
	first := "[event:event-storage sequence:1 type:message] 存储：LLM Wiki 必须保留完整 source 原文和精确引用区间。"
	second := "[event:event-search sequence:2 type:message] 检索：Agent 默认只展开一到三跳关系。"
	raw := first + "\n\n" + second
	return pagewiki.InjectSessionRequest{
		SourceID: "session:agent-1:runtime-demo", IdempotencyKey: key,
		Raw: []byte(raw),
		Events: []pagewiki.SourceEventInput{
			{ID: "event-storage", StartByte: 0, EndByte: len(first)},
			{ID: "event-search", StartByte: len(first) + 2, EndByte: len(raw)},
		},
	}
}
