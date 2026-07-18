package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/pax-beehive/pax-nexus/internal/eval/extractionshadow"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractor"
)

type scopedSliceRecord struct {
	ScopeID string `json:"scope_id"`
	extractionshadow.SliceRecord
}

type runJournal struct {
	mu            sync.Mutex
	sliceFile     *os.File
	providerFile  *os.File
	slicesByScope map[string][]extractionshadow.SliceRecord
	providerCalls []extractor.ProviderCall
	providerErr   error
}

func openRunJournal(outputDir string, resume bool) (*runJournal, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create extraction eval run directory: %w", err)
	}
	journal := &runJournal{slicesByScope: make(map[string][]extractionshadow.SliceRecord)}
	slicePath := filepath.Join(outputDir, "slices.jsonl")
	providerPath := filepath.Join(outputDir, "provider-calls.jsonl")
	if resume {
		if err := loadJSONL(slicePath, func(record scopedSliceRecord) error {
			journal.slicesByScope[record.ScopeID] = append(journal.slicesByScope[record.ScopeID], record.SliceRecord)
			return nil
		}); err != nil {
			return nil, err
		}
		if err := loadJSONL(providerPath, func(record extractor.ProviderCall) error {
			journal.providerCalls = append(journal.providerCalls, record)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	var err error
	journal.sliceFile, err = os.OpenFile(slicePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open extraction slice journal: %w", err)
	}
	journal.providerFile, err = os.OpenFile(providerPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		closeErr := journal.sliceFile.Close()
		return nil, errors.Join(fmt.Errorf("open provider-call journal: %w", err), closeErr)
	}
	return journal, nil
}

func (j *runJournal) appendSlice(scopeID string, record extractionshadow.SliceRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := encodeAndSync(j.sliceFile, scopedSliceRecord{ScopeID: scopeID, SliceRecord: record}); err != nil {
		return fmt.Errorf("append extraction slice journal: %w", err)
	}
	j.slicesByScope[scopeID] = append(j.slicesByScope[scopeID], record)
	return nil
}

func (j *runJournal) observeProviderCall(record extractor.ProviderCall) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.providerErr != nil {
		return
	}
	if err := encodeAndSync(j.providerFile, record); err != nil {
		j.providerErr = fmt.Errorf("append provider-call journal: %w", err)
		return
	}
	j.providerCalls = append(j.providerCalls, record)
}

func (j *runJournal) initialSlices(scopeID string) []extractionshadow.SliceRecord {
	j.mu.Lock()
	defer j.mu.Unlock()
	result := make([]extractionshadow.SliceRecord, 0, len(j.slicesByScope[scopeID]))
	for _, record := range j.slicesByScope[scopeID] {
		if record.Error == "" {
			result = append(result, record)
		}
	}
	return result
}

func (j *runJournal) callsForScope(scopeID string) []extractor.ProviderCall {
	j.mu.Lock()
	defer j.mu.Unlock()
	result := make([]extractor.ProviderCall, 0)
	for _, call := range j.providerCalls {
		if call.ScopeID == scopeID {
			result = append(result, call)
		}
	}
	return result
}

func (j *runJournal) close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return errors.Join(j.providerErr, j.sliceFile.Close(), j.providerFile.Close())
}

func encodeAndSync[T any](file *os.File, value T) error {
	if err := json.NewEncoder(file).Encode(value); err != nil {
		return err
	}
	return file.Sync()
}

func loadJSONL[T any](path string, consume func(T) error) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("resume extraction eval: journal %q does not exist", path)
	}
	if err != nil {
		return fmt.Errorf("open extraction eval journal %q: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var value T
		if err := decoder.Decode(&value); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return fmt.Errorf("decode extraction eval journal %q: %w", path, err)
		}
		if err := consume(value); err != nil {
			return err
		}
	}
}
