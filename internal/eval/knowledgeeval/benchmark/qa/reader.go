package qa

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

type Reader interface {
	ID() string
	Answer(context.Context, string, []knowledgeeval.GetResponse) (string, error)
}

type ContextReader struct{}

func (ContextReader) ID() string {
	return "context-reader:v1"
}

func (ContextReader) Answer(
	_ context.Context,
	_ string,
	documents []knowledgeeval.GetResponse,
) (string, error) {
	passages := make([]string, 0, len(documents))
	for _, document := range documents {
		passages = append(passages, document.Text)
	}
	return strings.Join(passages, "\n"), nil
}

type ChatReader struct {
	client llm.ChatClient
	model  string
}

func NewChatReader(client llm.ChatClient, model string) (*ChatReader, error) {
	if client == nil {
		return nil, errors.New("QA chat reader client is required")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("QA chat reader model is required")
	}
	return &ChatReader{client: client, model: strings.TrimSpace(model)}, nil
}

func (r *ChatReader) ID() string {
	return "chat-reader:v1:" + r.model
}

func (r *ChatReader) Answer(
	ctx context.Context,
	question string,
	documents []knowledgeeval.GetResponse,
) (string, error) {
	var contextText strings.Builder
	for _, document := range documents {
		if contextText.Len() > 0 {
			contextText.WriteString("\n\n")
		}
		contextText.WriteString("Document: ")
		contextText.WriteString(document.Ref)
		contextText.WriteString("\n")
		contextText.WriteString(document.Text)
	}
	response, err := r.client.Complete(ctx, llm.ChatRequest{
		Model: r.model,
		Messages: []llm.ChatMessage{
			{
				Role: "system",
				Content: "Answer only from the supplied Wiki context. " +
					"Return a concise answer without explanation. If the context " +
					"does not contain the answer, return unknown.",
			},
			{
				Role: "user",
				Content: fmt.Sprintf(
					"Question:\n%s\n\nWiki context:\n%s",
					strings.TrimSpace(question),
					contextText.String(),
				),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("complete QA reader request: %w", err)
	}
	return strings.TrimSpace(response.Message.Content), nil
}
