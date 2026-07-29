package pagewiki_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/stretchr/testify/suite"
)

type llmSessionEditorSuite struct {
	suite.Suite
}

func TestLLMSessionEditorSuite(t *testing.T) {
	suite.Run(t, new(llmSessionEditorSuite))
}

func (s *llmSessionEditorSuite) TestWritesEnglishPagesWithDeterministicEvidenceAndXanaduLinks() {
	client := &wikiChatClient{responses: []string{
		`{"title":"Wiki Data Architecture","summary":"How immutable revisions preserve the Wiki model.","sections":[{"key":"design","heading":"Design","markdown":"The Wiki stores immutable revisions as its durable publication boundary."}]}`,
		`{"title":"Evidence Grounding","summary":"How claims remain traceable to immutable source material.","sections":[{"key":"grounding","heading":"Grounding","markdown":"Published knowledge remains auditable through exact source anchors."}]}`,
	}}
	editor, err := pagewiki.NewLLMSessionEditor(pagewiki.LLMEditorConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)
	repository := memory.NewRepository()
	service := pagewiki.NewService(repository, pagewiki.SessionDocumentPlanner{}, editor)
	raw := "## Wiki 数据模型\n页面使用 immutable revision。\n第二行仍属于同一条精确证据。\n\n## Evidence grounding\nCitation 保存精确的 source anchor。"
	request := pagewiki.InjectSessionRequest{
		SourceID:       "session:agent-1:llm-editor",
		IdempotencyKey: "llm-editor",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{{
			ID: "event-wiki", StartByte: 0, EndByte: len(raw),
		}},
	}

	result, err := service.InjectSession(context.Background(), request)

	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, result.Run.Status)
	s.Require().Len(client.requests, 2)
	for _, modelRequest := range client.requests {
		s.Equal("test-model", modelRequest.Model)
		s.Contains(modelRequest.Messages[0].Content, "in English")
	}
	dataModel, err := repository.PageBySlug(context.Background(), "knowledge-wiki-data-model")
	s.Require().NoError(err)
	dataRevision, err := repository.PageRevision(context.Background(), dataModel.CurrentRevisionID)
	s.Require().NoError(err)
	s.Equal("Wiki Data Architecture", dataRevision.Title)
	s.Contains(dataRevision.Markdown, "The Wiki stores immutable revisions")
	s.Contains(dataRevision.Markdown, "页面使用 immutable revision。")
	s.Contains(dataRevision.Markdown, "第二行仍属于同一条精确证据。")
	s.Require().Len(dataRevision.Citations, 1)

	grounding, err := repository.PageBySlug(context.Background(), "knowledge-evidence-grounding")
	s.Require().NoError(err)
	groundingLinks, err := repository.PageLinks(context.Background(), grounding.ID)
	s.Require().NoError(err)
	s.Require().Len(groundingLinks.Outgoing, 1)
	s.Equal(dataModel.ID, groundingLinks.Outgoing[0].TargetPage.ID)
	dataLinks, err := repository.PageLinks(context.Background(), dataModel.ID)
	s.Require().NoError(err)
	s.Require().Len(dataLinks.Incoming, 1)
}

func (s *llmSessionEditorSuite) TestRejectsInvalidConfigurationAndMalformedResponse() {
	_, err := pagewiki.NewLLMSessionEditor(pagewiki.LLMEditorConfig{})
	s.Require().ErrorContains(err, "client is required")
	_, err = pagewiki.NewLLMSessionEditor(pagewiki.LLMEditorConfig{
		Client: &wikiChatClient{}, Model: "",
	})
	s.Require().ErrorContains(err, "model is required")

	client := &wikiChatClient{responses: []string{"not-json"}}
	editor, err := pagewiki.NewLLMSessionEditor(pagewiki.LLMEditorConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)
	_, err = editor.Edit(context.Background(), pagewiki.EditInput{
		SourceRevision: pagewiki.SourceRevision{
			Raw: []byte("Evidence grounding: exact anchors."),
			Events: []pagewiki.SourceEvent{{
				ID: "event-1", StartByte: 0, EndByte: len("Evidence grounding: exact anchors."),
			}},
		},
		Brief: pagewiki.PageBrief{Key: "knowledge-evidence-grounding"},
	})
	s.Require().ErrorContains(err, "decode Page Wiki LLM response")
}

func (s *llmSessionEditorSuite) TestUsesBriefEvidenceInsteadOfHeadingChunks() {
	client := &wikiChatClient{responses: []string{
		`{"title":"Release Policy","summary":"How the team ships releases.","sections":[{"key":"policy","heading":"Policy","markdown":"Releases ship weekly after the validation gate passes."}]}`,
	}}
	editor, err := pagewiki.NewLLMSessionEditor(pagewiki.LLMEditorConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)
	noiseEvent := "noise before ## Fake Heading\n"
	decisionEvent := "real decision: releases ship weekly."
	raw := noiseEvent + decisionEvent
	draft, err := editor.Edit(context.Background(), pagewiki.EditInput{
		SourceRevision: pagewiki.SourceRevision{
			ID:  "source-revision-1",
			Raw: []byte(raw),
			Events: []pagewiki.SourceEvent{
				{ID: "event-1", StartByte: len(noiseEvent), EndByte: len(raw)},
				{ID: "event-2", StartByte: 0, EndByte: len(noiseEvent)},
			},
		},
		Brief: pagewiki.PageBrief{
			Key: "release-policy", Action: pagewiki.PageActionCreate,
			ProposedSlug: "release-policy", ProposedTitle: "Release Policy",
			ReaderGoal:       "Understand the release cadence.",
			TopicPath:        []string{"Engineering", "Runtime"},
			EvidenceEventIDs: []string{"event-1"},
			Evidence: []pagewiki.EvidenceQuoteDraft{{
				EventID: "event-1", ExactText: "real decision: releases ship weekly.",
			}},
		},
	})
	s.Require().NoError(err)
	s.Equal("release-policy", draft.Slug)
	s.Require().Len(draft.Citations, 1)
	s.Equal("real decision: releases ship weekly.", draft.Citations[0].ExactText)
	s.Equal("event-1", draft.Citations[0].Evidence[0].EventID)
	s.Require().Len(client.requests, 1)
	payload := client.requests[0].Messages[1].Content
	s.Contains(payload, "Release Policy")
	s.Contains(payload, "real decision: releases ship weekly.")
	s.Contains(payload, "evidence_context")
	s.NotContains(payload, "Fake Heading")
}

func (s *llmSessionEditorSuite) TestSendsFullEvidenceContextToTheModel() {
	client := &wikiChatClient{responses: []string{
		`{"title":"Release Policy","summary":"How the team ships.","sections":[{"key":"policy","heading":"Policy","markdown":"Ships weekly."}]}`,
	}}
	editor, err := pagewiki.NewLLMSessionEditor(pagewiki.LLMEditorConfig{
		Client: client, Model: "test-model",
	})
	s.Require().NoError(err)
	first := "background before the quote. the decision: ship weekly. aftermath after the quote."
	oversized := strings.Repeat("x", 9000) + "TAIL-MARKER"
	raw := first + oversized
	_, err = editor.Edit(context.Background(), pagewiki.EditInput{
		SourceRevision: pagewiki.SourceRevision{
			ID:  "source-revision-1",
			Raw: []byte(raw),
			Events: []pagewiki.SourceEvent{
				{ID: "event-1", StartByte: 0, EndByte: len(first)},
				{ID: "event-2", StartByte: len(first), EndByte: len(raw)},
			},
		},
		Brief: pagewiki.PageBrief{
			Key: "release-policy", Action: pagewiki.PageActionCreate,
			ProposedSlug: "release-policy", ProposedTitle: "Release Policy",
			TopicPath:        []string{"Engineering"},
			EvidenceEventIDs: []string{"event-1", "event-2"},
			Evidence: []pagewiki.EvidenceQuoteDraft{{
				EventID: "event-1", ExactText: "the decision: ship weekly.",
			}},
		},
	})
	s.Require().NoError(err)
	s.Require().Len(client.requests, 1)
	payload := client.requests[0].Messages[1].Content
	s.Contains(payload, "evidence_context")
	s.Contains(payload, "background before the quote")
	s.Contains(payload, "aftermath after the quote")
	s.NotContains(payload, "TAIL-MARKER")
}

type wikiChatClient struct {
	requests  []llm.ChatRequest
	responses []string
	err       error
}

func (c *wikiChatClient) Complete(
	_ context.Context,
	request llm.ChatRequest,
) (llm.ChatResponse, error) {
	c.requests = append(c.requests, request)
	if c.err != nil {
		return llm.ChatResponse{}, c.err
	}
	response := c.responses[0]
	c.responses = c.responses[1:]
	return llm.ChatResponse{
		Message: llm.ChatMessage{Role: "assistant", Content: response},
	}, nil
}
