package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type summaryFlight struct {
	done       chan struct{}
	result     summaryResult
	err        error
	persistErr error
}

type summaryResult struct {
	summary          string
	usage            Usage
	baseMessageCount int
	baseDigest       [32]byte
}

func (e *OpenAI) consumeReadySummary(key EpisodeKey) error {
	flight := e.summaryFlight(key)
	if flight == nil {
		return nil
	}
	select {
	case <-flight.done:
	default:
		return nil
	}
	e.deleteSummaryFlight(key, flight)
	if errors.Is(flight.persistErr, context.DeadlineExceeded) || errors.Is(flight.persistErr, context.Canceled) {
		// An interrupted background persist is a lost optimization, not
		// corrupted state: the episode is unchanged and a later slice simply
		// re-triggers the summary. Surfacing it here would fail the next
		// unrelated foreground extraction; the error still reaches
		// WaitForBackground through the flight's lifecycle completion.
		return nil
	}
	return flight.persistErr
}

func (e *OpenAI) shouldStartSummary(episode Episode) bool {
	if !e.config.SummaryEnabled || len(episode.Messages) == 0 {
		return false
	}
	return messageTokens(episode.Messages) >= e.config.SummaryTailTokens+e.config.SummaryTriggerTokens
}

func (e *OpenAI) startSummary(ctx context.Context, key EpisodeKey, episode Episode) *summaryFlight {
	e.summariesMu.Lock()
	if existing := e.summaries[key]; existing != nil {
		e.summariesMu.Unlock()
		return existing
	}
	flight := &summaryFlight{done: make(chan struct{})}
	e.summaries[key] = flight
	owned, finishBackground := e.lifecycle.beginBackground(ctx)
	e.summariesMu.Unlock()

	go func() {
		background, cancel := context.WithTimeout(owned, backgroundProviderTimeout(e.config.ExecutionPolicy))
		flight.result, flight.err = e.computeSummary(background, episode)
		cancel()
		// Persist runs on the pre-timeout owned context: its own budget starts
		// only after the episode lease is acquired, so a slow foreground
		// extraction holding the lease cannot burn the persist budget.
		flight.persistErr = e.persistSummaryOutcome(owned, key, flight.result, flight.err)
		close(flight.done)
		finishBackground(errors.Join(flight.err, flight.persistErr))
	}()
	return flight
}

func (e *OpenAI) persistSummaryOutcome(
	ctx context.Context,
	key EpisodeKey,
	result summaryResult,
	resultErr error,
) error {
	releaseEpisode := e.acquireEpisode(key)
	defer releaseEpisode()
	// The persist timeout budget starts after the lease is acquired. Waiting
	// behind a foreground extraction preserves the documented happens-before
	// ordering without consuming the store-call budget.
	ctx, cancel := context.WithTimeout(ctx, backgroundProviderTimeout(e.config.ExecutionPolicy))
	defer cancel()
	episode, found, err := e.config.EpisodeStore.LoadEpisode(ctx, key)
	if err != nil {
		return fmt.Errorf("load rolling extraction episode for summary: %w", err)
	}
	if !found {
		return fmt.Errorf("persist periodic extraction summary: episode is missing")
	}
	expectedVersion := episode.Version
	if resultErr != nil {
		recordSummaryFailure(&episode.Checkpoint, resultErr)
	} else if err := applyPeriodicSummary(e.systemPrompt(), &episode, result, e.config.SummaryTailTokens); err != nil {
		recordSummaryFailure(&episode.Checkpoint, err)
	}
	if err := e.config.EpisodeStore.SaveEpisode(ctx, episode, expectedVersion); err != nil {
		return fmt.Errorf("persist periodic extraction summary: %w", err)
	}
	return nil
}

func (e *OpenAI) summaryFlight(key EpisodeKey) *summaryFlight {
	e.summariesMu.Lock()
	defer e.summariesMu.Unlock()
	return e.summaries[key]
}

func (e *OpenAI) deleteSummaryFlight(key EpisodeKey, flight *summaryFlight) {
	e.summariesMu.Lock()
	if e.summaries[key] == flight {
		delete(e.summaries, key)
	}
	e.summariesMu.Unlock()
}

func (e *OpenAI) computeSummary(ctx context.Context, episode Episode) (summaryResult, error) {
	messages, err := episodeMessages(episode, e.systemPrompt())
	if err != nil {
		return summaryResult{}, fmt.Errorf("build periodic summary context: %w", err)
	}
	messages = append(messages, chatMessage{Role: "user", Content: periodicSummaryPrompt})
	var summary string
	var usage Usage
	_, err = e.executeProvider(ctx, messages, 0, ProviderCallSummary, func(body []byte) error {
		decodedSummary, decodedUsage, decodeErr := decodeSummary(body)
		if decodeErr != nil {
			return decodeErr
		}
		summary, usage = decodedSummary, decodedUsage
		return nil
	})
	if err != nil {
		return summaryResult{}, fmt.Errorf("summarize extraction episode: %w", err)
	}
	return summaryResult{
		summary: summary, usage: usage,
		baseMessageCount: len(episode.Messages), baseDigest: digestMessages(episode.Messages),
	}, nil
}

func decodeSummary(body []byte) (string, Usage, error) {
	content, usage, err := decodeChatContent(body)
	if err != nil {
		return "", Usage{}, fmt.Errorf("decode periodic summary response: %w", err)
	}
	var output struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(content), &output); err != nil {
		return "", Usage{}, fmt.Errorf("decode periodic summary: %w", errors.Join(ErrInvalidModelResponse, err))
	}
	if strings.TrimSpace(output.Summary) == "" {
		return "", Usage{}, fmt.Errorf("decode periodic summary: summary is empty: %w", ErrInvalidModelResponse)
	}
	return strings.TrimSpace(output.Summary), usage, nil
}

func applyPeriodicSummary(system string, episode *Episode, result summaryResult, tailTokenBudget int) error {
	if result.baseMessageCount > len(episode.Messages) ||
		digestMessages(episode.Messages[:result.baseMessageCount]) != result.baseDigest {
		return fmt.Errorf("apply periodic extraction summary: snapshot is stale: %w", ErrEpisodeConflict)
	}
	baseTail := recentMessageTail(episode.Messages[:result.baseMessageCount], tailTokenBudget)
	appended := append([]EpisodeMessage(nil), episode.Messages[result.baseMessageCount:]...)
	messages := append(baseTail, appended...)
	checkpoint := Checkpoint{
		Summary: result.summary, SourceCursors: episode.Checkpoint.SourceCursors,
		SummaryCount:    episode.Checkpoint.SummaryCount + 1,
		SummaryAttempts: episode.Checkpoint.SummaryAttempts + 1,
		SummaryFailures: episode.Checkpoint.SummaryFailures,
	}
	episode.Checkpoint = checkpoint
	episode.Messages = messages
	episode.EstimatedTokens = estimateEpisodeTokens(system, checkpoint, messages)
	return nil
}

func recordSummaryFailure(checkpoint *Checkpoint, err error) {
	checkpoint.SummaryAttempts++
	checkpoint.SummaryFailures++
	message := err.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	checkpoint.SummaryLastError = message
}

func recentMessageTail(messages []EpisodeMessage, tokenBudget int) []EpisodeMessage {
	start, tokens := len(messages), 0
	for start > 0 {
		candidate := start - 1
		if messages[candidate].Role == "assistant" && candidate > 0 && messages[candidate-1].Role == "user" {
			candidate--
		}
		pairTokens := messageTokens(messages[candidate:start])
		if tokens > 0 && tokens+pairTokens > tokenBudget {
			break
		}
		tokens += pairTokens
		start = candidate
	}
	return append([]EpisodeMessage(nil), messages[start:]...)
}

func messageTokens(messages []EpisodeMessage) int {
	total := 0
	for _, message := range messages {
		total += estimateTokens(message.Content)
	}
	return total
}

const periodicSummaryPrompt = `Update the rolling continuity summary for this extraction episode.

The summary is derived context, never factual evidence. Preserve the current
state of decisions, obligations, owners, exact values, dates, blockers,
handoffs, artifacts, superseded facts, unresolved questions, and stable memory
identities. Include relevant event IDs next to the facts they support so a
future extractor can understand provenance, but do not instruct it to emit
those historical IDs as evidence for a new candidate. Prefer current state over
chronology and state explicitly when something was superseded or resolved.

Return exactly one JSON object with one string field:
{"summary":"concise continuity summary"}

Keep the summary below 4,096 tokens.`
