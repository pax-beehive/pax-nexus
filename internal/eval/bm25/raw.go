// Package bm25 provides deterministic lexical baselines for memory evaluation.
package bm25

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

const (
	defaultCandidateLimit = 8
	defaultTokenBudget    = 500
	defaultChunkEvents    = 4
	bm25K1                = 1.2
	bm25B                 = 0.75
)

type RawQuery struct {
	Text           string
	CandidateLimit int
	TokenBudget    int
	ChunkEvents    int
	TemporalCutoff time.Time
}

type RawResult struct {
	Context          string           `json:"context"`
	SourceEvents     int              `json:"source_events"`
	EligibleEvents   int              `json:"eligible_events"`
	Candidates       int              `json:"candidates"`
	Selected         int              `json:"selected"`
	BudgetDrops      int              `json:"budget_drops"`
	SelectedEventIDs []string         `json:"selected_event_ids"`
	CandidateTrace   []CandidateTrace `json:"candidate_trace"`
}

type CandidateTrace struct {
	Rank      int      `json:"rank"`
	Score     float64  `json:"score"`
	EventIDs  []string `json:"event_ids"`
	Selected  bool     `json:"selected"`
	Rejection string   `json:"rejection,omitempty"`
}

type rawChunk struct {
	events []session.SessionEvent
	text   string
	tokens []string
	score  float64
}

// RecallRaw applies visibility and temporal filters before deterministic BM25
// ranking and shared-budget packing of raw session evidence.
func RecallRaw(batches []session.SessionBatch, query RawQuery) (RawResult, error) {
	if strings.TrimSpace(query.Text) == "" {
		return RawResult{}, fmt.Errorf("recall raw BM25: query is required")
	}
	config := normalizeQuery(query)
	events, sourceEvents, err := eligibleEvents(batches, config.TemporalCutoff)
	if err != nil {
		return RawResult{}, err
	}
	chunks := makeChunks(events, config.ChunkEvents)
	rankChunks(chunks, tokens(config.Text))
	return packChunks(chunks, sourceEvents, len(events), config.CandidateLimit, config.TokenBudget), nil
}

func normalizeQuery(query RawQuery) RawQuery {
	if query.CandidateLimit <= 0 {
		query.CandidateLimit = defaultCandidateLimit
	}
	if query.TokenBudget <= 0 {
		query.TokenBudget = defaultTokenBudget
	}
	if query.ChunkEvents <= 0 {
		query.ChunkEvents = defaultChunkEvents
	}
	return query
}

func eligibleEvents(batches []session.SessionBatch, cutoff time.Time) ([]session.SessionEvent, int, error) {
	events := make([]session.SessionEvent, 0)
	sourceEvents := 0
	for batchIndex, batch := range batches {
		if !batch.Complete {
			return nil, 0, fmt.Errorf("recall raw BM25: batch %d is incomplete", batchIndex)
		}
		for eventIndex, event := range batch.Events {
			sourceEvents++
			if strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.Content) == "" {
				return nil, 0, fmt.Errorf("recall raw BM25: batch %d event %d has empty required fields", batchIndex, eventIndex)
			}
			if event.Visibility != "team_note_eligible" {
				continue
			}
			if !cutoff.IsZero() && event.OccurredAt.After(cutoff) {
				continue
			}
			events = append(events, event)
		}
	}
	slices.SortStableFunc(events, compareEvents)
	return events, sourceEvents, nil
}

func makeChunks(events []session.SessionEvent, size int) []rawChunk {
	chunks := make([]rawChunk, 0, (len(events)+size-1)/size)
	for start := 0; start < len(events); start += size {
		end := min(start+size, len(events))
		chunkEvents := slices.Clone(events[start:end])
		chunks = append(chunks, rawChunk{events: chunkEvents, text: renderChunk(chunkEvents), tokens: tokens(eventText(chunkEvents))})
	}
	return chunks
}

func eventText(events []session.SessionEvent) string {
	parts := make([]string, 0, len(events))
	for _, event := range events {
		parts = append(parts, event.Content)
	}
	return strings.Join(parts, "\n")
}

func renderChunk(events []session.SessionEvent) string {
	parts := make([]string, 0, len(events))
	for _, event := range events {
		parts = append(parts, fmt.Sprintf("[%s] %s / %s / %s\n%s", event.OccurredAt.UTC().Format(time.RFC3339), event.Actor.UserID, event.Actor.AgentID, event.Actor.SessionID, event.Content))
	}
	return strings.Join(parts, "\n\n")
}

func rankChunks(chunks []rawChunk, queryTerms []string) {
	if len(chunks) == 0 || len(queryTerms) == 0 {
		return
	}
	documents := make([]Document, 0, len(chunks))
	for index, chunk := range chunks {
		documents = append(documents, Document{ID: fmt.Sprintf("%08d", index), Text: chunk.text})
	}
	ranked, err := RankDocuments(documents, strings.Join(queryTerms, " "))
	if err != nil {
		return
	}
	ordered := make([]rawChunk, 0, len(chunks))
	for _, scored := range ranked {
		index := 0
		if _, err := fmt.Sscanf(scored.ID, "%d", &index); err != nil {
			continue
		}
		chunks[index].score = scored.Score
		ordered = append(ordered, chunks[index])
	}
	copy(chunks, ordered)
}

func packChunks(chunks []rawChunk, sourceEvents, eligibleEvents, candidateLimit, tokenBudget int) RawResult {
	limit := min(candidateLimit, len(chunks))
	result := RawResult{
		SourceEvents: sourceEvents, EligibleEvents: eligibleEvents, Candidates: limit,
		SelectedEventIDs: make([]string, 0), CandidateTrace: make([]CandidateTrace, 0, len(chunks)),
	}
	parts := make([]string, 0, limit)
	usedTokens := 0
	for index, chunk := range chunks {
		trace := CandidateTrace{Rank: index + 1, Score: chunk.score, EventIDs: chunkEventIDs(chunk.events)}
		if index >= limit {
			trace.Rejection = "candidate_limit"
			result.CandidateTrace = append(result.CandidateTrace, trace)
			continue
		}
		chunkTokens := len(tokens(chunk.text))
		if usedTokens+chunkTokens > tokenBudget {
			result.BudgetDrops++
			trace.Rejection = "token_budget"
			result.CandidateTrace = append(result.CandidateTrace, trace)
			continue
		}
		usedTokens += chunkTokens
		parts = append(parts, chunk.text)
		result.Selected++
		trace.Selected = true
		result.CandidateTrace = append(result.CandidateTrace, trace)
		for _, event := range chunk.events {
			result.SelectedEventIDs = append(result.SelectedEventIDs, event.ID)
		}
	}
	result.Context = strings.Join(parts, "\n\n")
	return result
}

func chunkEventIDs(events []session.SessionEvent) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	return ids
}

func compareEvents(left, right session.SessionEvent) int {
	if comparison := left.OccurredAt.Compare(right.OccurredAt); comparison != 0 {
		return comparison
	}
	return strings.Compare(left.ID, right.ID)
}

func tokens(text string) []string {
	result := make([]string, 0)
	var builder strings.Builder
	flush := func() {
		if builder.Len() > 0 {
			result = append(result, strings.ToLower(builder.String()))
			builder.Reset()
		}
	}
	for _, character := range text {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			builder.WriteRune(character)
			continue
		}
		flush()
	}
	flush()
	return result
}
