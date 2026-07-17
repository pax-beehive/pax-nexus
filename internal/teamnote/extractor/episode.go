package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

var ErrEpisodeConflict = errors.New("extraction episode conflict")

type ContextMode string

const (
	ContextModeSlice   ContextMode = "slice"
	ContextModeRolling ContextMode = "rolling"
)

const (
	// ExtractionVersionV1 is the original candidate-only response protocol.
	ExtractionVersionV1 = "v1"
	// ExtractionVersionV2 separates claims and state decisions inside one
	// primary model call and maps them onto candidates deterministically.
	ExtractionVersionV2 = "v2"
)

type EpisodeKey struct {
	ScopeID   string
	TaskRef   string
	ThreadRef string
}

type EpisodeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type KnowledgeItem struct {
	MemoryID         string   `json:"memory_id"`
	Kind             string   `json:"kind"`
	Subject          string   `json:"subject"`
	Body             string   `json:"body"`
	EvidenceEventIDs []string `json:"evidence_event_ids"`
}

type Checkpoint struct {
	ActiveKnowledge   []KnowledgeItem     `json:"active_knowledge"`
	ResolvedKnowledge []KnowledgeItem     `json:"resolved_knowledge"`
	OpenQuestions     []KnowledgeItem     `json:"open_questions"`
	EvidenceIndex     map[string][]string `json:"evidence_index"`
	SourceCursors     map[string]int64    `json:"source_cursors"`
	Summary           string              `json:"summary,omitempty"`
	SummaryCount      int                 `json:"summary_count,omitempty"`
	SummaryAttempts   int                 `json:"summary_attempts,omitempty"`
	SummaryFailures   int                 `json:"summary_failures,omitempty"`
	SummaryLastError  string              `json:"summary_last_error,omitempty"`
}

type Episode struct {
	Key             EpisodeKey
	Version         int64
	ProtocolVersion string
	Checkpoint      Checkpoint
	Messages        []EpisodeMessage
	EstimatedTokens int
	EventCount      int
	CompactionCount int
	Model           string
	PromptVersion   string
	Runs            map[string]EpisodeRun
}

type EpisodeRun struct {
	Response string `json:"response"`
	Ordinal  int    `json:"ordinal"`
}

type EpisodeStore interface {
	LoadEpisode(context.Context, EpisodeKey) (Episode, bool, error)
	SaveEpisode(context.Context, Episode, int64) error
}

// MemoryEpisodeStore is an in-memory EpisodeStore for evaluations and tests
// that replay extraction without a durable platform store.
type MemoryEpisodeStore struct {
	mu       sync.Mutex
	episodes map[EpisodeKey]Episode
}

func NewMemoryEpisodeStore() *MemoryEpisodeStore {
	return &MemoryEpisodeStore{episodes: make(map[EpisodeKey]Episode)}
}

func (s *MemoryEpisodeStore) LoadEpisode(_ context.Context, key EpisodeKey) (Episode, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	episode, ok := s.episodes[key]
	if !ok {
		return Episode{Key: key}, false, nil
	}
	return episode, true, nil
}

func (s *MemoryEpisodeStore) SaveEpisode(_ context.Context, episode Episode, expectedVersion int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.episodes[episode.Key]
	if (!ok && expectedVersion != 0) || (ok && current.Version != expectedVersion) {
		return ErrEpisodeConflict
	}
	episode.Version = expectedVersion + 1
	encoded, err := json.Marshal(episode)
	if err != nil {
		return fmt.Errorf("encode memory episode: %w", err)
	}
	var copied Episode
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return fmt.Errorf("decode memory episode: %w", err)
	}
	s.episodes[episode.Key] = copied
	return nil
}

func checkpointJSON(checkpoint Checkpoint) (string, error) {
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
