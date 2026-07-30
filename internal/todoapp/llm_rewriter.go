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

	var lastErr error
	for attempt := 0; attempt < rewriterAttempts; attempt++ {
		response, err := r.client.Complete(ctx, llm.ChatRequest{
			Model: r.model,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: todoRewriterPrompt},
				{Role: "user", Content: string(userMessage)},
			},
		})
		if err != nil {
			lastErr = err
			continue
		}

		var decoded rewriteResponse
		if err := json.Unmarshal(
			[]byte(trimJSONFence(response.Message.Content)),
			&decoded,
		); err != nil {
			lastErr = err
			continue
		}

		// Reject blank title as a failed attempt
		if strings.TrimSpace(decoded.Title) == "" {
			lastErr = errors.New("title is blank")
			continue
		}

		return decoded.Title, decoded.Body, nil
	}

	r.logger.Warn("todo rewrite degraded", "note_id", item.NoteID, "error", lastErr)
	return item.Subject, item.Body, nil
}

// trimJSONFence strips Markdown JSON fences from a response string.
// Copied from internal/pagewiki/llm_session_editor.go:206-216.
func trimJSONFence(value string) string {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) >= 3 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		return strings.Join(lines[1:len(lines)-1], "\n")
	}
	return trimmed
}

var _ Rewriter = (*LLMRewriter)(nil)
