package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
)

type paxlRecord struct {
	SchemaVersion string `json:"schemaVersion"`
	Type          string `json:"type"`
	Role          string `json:"role"`
	ContentText   string `json:"contentText"`
	SessionID     string `json:"sessionId"`
	NativeID      string `json:"nativeId"`
	ProjectID     string `json:"projectId"`
	Title         string `json:"title"`
	Seq           int64  `json:"seq"`
}

type paxlMessage struct {
	Seq     int64
	Role    string
	Content string
}

type paxlSession struct {
	ID        string
	NativeID  string
	ProjectID string
	Title     string
	Messages  []paxlMessage
}

func loadPaxlSession(path string) (paxlSession, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return paxlSession{}, fmt.Errorf("read paxl session %q: %w", path, err)
	}
	session, err := parsePaxlSession(bytes.NewReader(content))
	if err != nil {
		return paxlSession{}, fmt.Errorf("parse paxl session %q: %w", path, err)
	}
	return session, nil
}

func parsePaxlSession(reader io.Reader) (paxlSession, error) {
	var session paxlSession
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var record paxlRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return paxlSession{}, fmt.Errorf("decode JSONL record: %w", err)
		}
		if record.SchemaVersion == "paxl.session.snapshot.v1" {
			session.ID = record.SessionID
			session.NativeID = record.NativeID
			session.ProjectID = record.ProjectID
			session.Title = record.Title
			continue
		}
		if !isKnowledgeMessage(record) {
			continue
		}
		if session.ID == "" {
			session.ID = record.SessionID
		}
		session.Messages = append(session.Messages, paxlMessage{
			Seq:     record.Seq,
			Role:    record.Role,
			Content: strings.TrimSpace(record.ContentText),
		})
	}
	if err := scanner.Err(); err != nil {
		return paxlSession{}, fmt.Errorf("scan JSONL records: %w", err)
	}
	if session.ID == "" || session.NativeID == "" || session.Title == "" {
		return paxlSession{}, fmt.Errorf("snapshot metadata is incomplete")
	}
	if len(session.Messages) == 0 {
		return paxlSession{}, fmt.Errorf("session contains no knowledge messages")
	}
	sort.Slice(session.Messages, func(left, right int) bool {
		return session.Messages[left].Seq < session.Messages[right].Seq
	})
	return session, nil
}

func isKnowledgeMessage(record paxlRecord) bool {
	if record.Type != "message" ||
		(record.Role != "user" && record.Role != "assistant") ||
		strings.TrimSpace(record.ContentText) == "" {
		return false
	}
	return !strings.Contains(record.ContentText, "<recommended_plugins>") &&
		!strings.Contains(record.ContentText, "<environment_context>")
}

func (session paxlSession) injectionRequest(idempotencyKey string) pagewiki.InjectSessionRequest {
	var raw strings.Builder
	fmt.Fprintf(
		&raw,
		"# %s\n\nSession: %s\nProject: %s",
		session.Title,
		session.ID,
		session.ProjectID,
	)
	events := make([]pagewiki.SourceEventInput, 0, len(session.Messages))
	for _, message := range session.Messages {
		fmt.Fprintf(&raw, "\n\n## %s message %d\n\n", message.Role, message.Seq)
		start := raw.Len()
		raw.WriteString(message.Content)
		events = append(events, pagewiki.SourceEventInput{
			ID:        messageEventID(message.Seq),
			StartByte: start,
			EndByte:   raw.Len(),
		})
	}
	return pagewiki.InjectSessionRequest{
		SourceID:       session.ID,
		IdempotencyKey: idempotencyKey,
		Raw:            []byte(raw.String()),
		Events:         events,
	}
}

func messageEventID(sequence int64) string {
	return "message-" + strconv.FormatInt(sequence, 10)
}
