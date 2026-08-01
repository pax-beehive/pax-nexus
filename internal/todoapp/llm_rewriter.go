package todoapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

const (
	rewriterAttempts   = 2
	todoRewriterPrompt = `You rewrite one team-memory action item into a short actionable todo. Respond with a single JSON object {"title": string, "body": string} and nothing else — no Markdown fence. Title imperative, max 80 chars. Body: 1-3 sentences, keep concrete identifiers (commands, file names) verbatim.`
)

type LLMRewriterConfig struct {
	Client llm.ChatClient
	Model  string
	Logger *slog.Logger
}

type LLMRewriter struct {
	client llm.ChatClient
	model  string
	logger *slog.Logger
}

func NewLLMRewriter(config LLMRewriterConfig) (*LLMRewriter, error) {
	if config.Client == nil {
		return nil, errors.New("create todo LLM rewriter: client is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("create todo LLM rewriter: model is required")
	}
	logger := config.Logger
	if logger == nil {
		logger = observability.DiscardLogger()
	}
	return &LLMRewriter{
		client: config.Client, model: strings.TrimSpace(config.Model), logger: logger,
	}, nil
}

type rewriteResponse struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

func (r *LLMRewriter) Rewrite(ctx context.Context, item ActionItem) (string, string, error) {
	payload := map[string]string{
		"kind":    item.Kind,
		"subject": item.Subject,
		"body":    item.Body,
	}
	userMessage, err := json.Marshal(payload)
	if err != nil {
		r.logger.Warn("todo rewrite encode request", "note_id", item.NoteID, "error", err)
		return item.Subject, item.Body, nil
	}

	decoded, err := llm.CompleteJSONAs(ctx, r.client, llm.ChatRequest{
		Model: r.model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: todoRewriterPrompt},
			{Role: "user", Content: string(userMessage)},
		},
	}, rewriterAttempts, func(decoded rewriteResponse) (rewriteResponse, error) {
		if strings.TrimSpace(decoded.Title) == "" {
			return rewriteResponse{}, errors.New("title is blank")
		}
		return decoded, nil
	})
	if err != nil {
		r.logger.Warn("todo rewrite degraded", "note_id", item.NoteID, "error", err)
		return item.Subject, item.Body, nil
	}
	return decoded.Title, decoded.Body, nil
}

var _ Rewriter = (*LLMRewriter)(nil)
