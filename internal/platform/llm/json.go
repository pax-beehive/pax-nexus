package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidJSON marks CompleteJSON failures where the model replied but the
// reply did not decode as JSON. errors.Is lets callers keep domain-specific
// wording for that case while transport and accept errors pass through as-is.
var ErrInvalidJSON = errors.New("LLM response is not valid JSON")

// CompleteJSON calls client.Complete up to attempts times, strips an
// optional Markdown fence, and decodes the reply into a fresh T per attempt.
// It retries transport errors and invalid JSON alike and returns the last
// error once attempts are exhausted; decode failures satisfy
// errors.Is(err, ErrInvalidJSON) and carry the provider's finish_reason so
// truncation stays distinguishable from other malformed replies.
func CompleteJSON[T any](
	ctx context.Context,
	client ChatClient,
	request ChatRequest,
	attempts int,
) (T, error) {
	return CompleteJSONAs(ctx, client, request, attempts,
		func(decoded T) (T, error) { return decoded, nil })
}

// CompleteJSONAs is CompleteJSON followed by accept, which validates the
// decoded value and maps it to its final shape. An accept error rejects the
// attempt and triggers the same retry as invalid JSON, but surfaces
// unwrapped so callers can match their own sentinel errors.
func CompleteJSONAs[T, R any](
	ctx context.Context,
	client ChatClient,
	request ChatRequest,
	attempts int,
	accept func(T) (R, error),
) (R, error) {
	var lastErr error
	for range attempts {
		response, err := client.Complete(ctx, request)
		if err != nil {
			lastErr = err
			continue
		}
		var decoded T
		if err := json.Unmarshal(
			[]byte(trimJSONFence(response.Message.Content)), &decoded,
		); err != nil {
			lastErr = fmt.Errorf(
				"%w (finish_reason=%q): %w", ErrInvalidJSON, response.FinishReason, err,
			)
			continue
		}
		accepted, err := accept(decoded)
		if err != nil {
			lastErr = err
			continue
		}
		return accepted, nil
	}
	var zero R
	return zero, lastErr
}

// trimJSONFence strips a Markdown code fence when the model ignores the
// "JSON only" instruction and wraps its reply anyway.
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
