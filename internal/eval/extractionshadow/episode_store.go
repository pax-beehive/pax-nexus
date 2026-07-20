package extractionshadow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
)

const episodeStoreSchemaVersion = "pax-extraction-eval-episodes-v1"

// FileEpisodeStore persists rolling model responses after every successful
// slice. Reopening it lets the normal extractor replay saved checksums while
// the in-memory runtime rebuilds notes, then continue at the first unpaid
// slice with the original rolling prefix intact.
type FileEpisodeStore struct {
	path     string
	mu       sync.Mutex
	episodes map[extractor.EpisodeKey]extractor.Episode
}

type episodeStoreFile struct {
	SchemaVersion string              `json:"schema_version"`
	Episodes      []extractor.Episode `json:"episodes"`
}

func OpenFileEpisodeStore(path string, resume bool) (*FileEpisodeStore, error) {
	store := &FileEpisodeStore{path: path, episodes: make(map[extractor.EpisodeKey]extractor.Episode)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if resume {
			return nil, fmt.Errorf("open extraction episode store for resume: %q does not exist", path)
		}
		if err := store.writeLocked(); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open extraction episode store: %w", err)
	}
	if !resume {
		return nil, fmt.Errorf("open extraction episode store: %q already exists", path)
	}
	var saved episodeStoreFile
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("decode extraction episode store: %w", err)
	}
	if saved.SchemaVersion != episodeStoreSchemaVersion {
		return nil, fmt.Errorf("decode extraction episode store: unsupported schema %q", saved.SchemaVersion)
	}
	for _, episode := range saved.Episodes {
		if _, duplicate := store.episodes[episode.Key]; duplicate {
			return nil, fmt.Errorf("decode extraction episode store: duplicate episode key")
		}
		store.episodes[episode.Key] = episode
	}
	return store, nil
}

func (s *FileEpisodeStore) LoadEpisode(
	_ context.Context,
	key extractor.EpisodeKey,
) (extractor.Episode, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	episode, ok := s.episodes[key]
	if !ok {
		return extractor.Episode{Key: key}, false, nil
	}
	copied, err := copyEpisode(episode)
	if err != nil {
		return extractor.Episode{}, false, err
	}
	return copied, true, nil
}

func (s *FileEpisodeStore) SaveEpisode(
	_ context.Context,
	episode extractor.Episode,
	expectedVersion int64,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.episodes[episode.Key]
	if (!ok && expectedVersion != 0) || (ok && current.Version != expectedVersion) {
		return extractor.ErrEpisodeConflict
	}
	episode.Version = expectedVersion + 1
	copied, err := copyEpisode(episode)
	if err != nil {
		return err
	}
	s.episodes[episode.Key] = copied
	if err := s.writeLocked(); err != nil {
		if ok {
			s.episodes[episode.Key] = current
		} else {
			delete(s.episodes, episode.Key)
		}
		return err
	}
	return nil
}

func (s *FileEpisodeStore) writeLocked() error {
	saved := episodeStoreFile{SchemaVersion: episodeStoreSchemaVersion}
	for _, episode := range s.episodes {
		saved.Episodes = append(saved.Episodes, episode)
	}
	sort.Slice(saved.Episodes, func(left, right int) bool {
		leftKey, rightKey := saved.Episodes[left].Key, saved.Episodes[right].Key
		if leftKey.ScopeID != rightKey.ScopeID {
			return leftKey.ScopeID < rightKey.ScopeID
		}
		if leftKey.TaskRef != rightKey.TaskRef {
			return leftKey.TaskRef < rightKey.TaskRef
		}
		return leftKey.ThreadRef < rightKey.ThreadRef
	})
	encoded, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return fmt.Errorf("encode extraction episode store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create extraction episode store directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".episodes-*.json")
	if err != nil {
		return fmt.Errorf("create extraction episode store temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	writeErr := func() error {
		if err := temporary.Chmod(0o600); err != nil {
			return err
		}
		if _, err := temporary.Write(encoded); err != nil {
			return err
		}
		return temporary.Sync()
	}()
	closeErr := temporary.Close()
	if writeErr == nil && closeErr == nil {
		if err := os.Rename(temporaryPath, s.path); err != nil {
			return fmt.Errorf("replace extraction episode store: %w", err)
		}
		return nil
	}
	removeErr := os.Remove(temporaryPath)
	return fmt.Errorf("write extraction episode store: %w", errors.Join(writeErr, closeErr, removeErr))
}

func copyEpisode(episode extractor.Episode) (extractor.Episode, error) {
	encoded, err := json.Marshal(episode)
	if err != nil {
		return extractor.Episode{}, fmt.Errorf("copy extraction episode: %w", err)
	}
	var copied extractor.Episode
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return extractor.Episode{}, fmt.Errorf("copy extraction episode: %w", err)
	}
	return copied, nil
}
