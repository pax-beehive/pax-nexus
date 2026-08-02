package agentsessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Observation struct {
	Channel   string `json:"channel"`
	MsgNode   string `json:"msg_node"`
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
}

type Session struct {
	SessionID    string        `json:"session_id"`
	Agent        Persona       `json:"agent"`
	WindowStart  string        `json:"window_start"`
	WindowEnd    string        `json:"window_end"`
	Observations []Observation `json:"observations"`
	Trajectory   []Action      `json:"trajectory"`
}

func BuildSession(w Window, persona Persona, actions []Action) Session {
	s := Session{SessionID: w.SessionID(), Agent: persona, Trajectory: actions}
	for i, m := range w.Msgs {
		if i == 0 {
			s.WindowStart = m.At.Format(timestampLayout)
		}
		s.WindowEnd = m.At.Format(timestampLayout)
		s.Observations = append(s.Observations, Observation{Channel: m.Channel,
			MsgNode: m.NodeID, Author: m.Author,
			Timestamp: m.At.Format(timestampLayout)})
	}
	return s
}

func AttachEvidenceSessions(questions []EnhancedQuestion, sessions []Session) []EnhancedQuestion {
	// user → msg_node → session ids(保持 session 序)
	seen := map[string]map[string][]string{}
	for _, s := range sessions {
		user := s.Agent.UserID
		if seen[user] == nil {
			seen[user] = map[string][]string{}
		}
		for _, o := range s.Observations {
			seen[user][o.MsgNode] = append(seen[user][o.MsgNode], s.SessionID)
		}
	}
	out := make([]EnhancedQuestion, len(questions))
	for i, q := range questions {
		q.EvidenceSessionIDs = nil
		unique := map[string]bool{}
		for _, node := range q.EvidenceMsgIDs {
			for _, id := range seen[q.AskingUserID][node] {
				if !unique[id] {
					unique[id] = true
					q.EvidenceSessionIDs = append(q.EvidenceSessionIDs, id)
				}
			}
		}
		out[i] = q
	}
	return out
}

func WriteJSONL[T any](path string, rows []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	encoder := json.NewEncoder(w)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return fmt.Errorf("encode %s: %w", path, err)
		}
	}
	return w.Flush()
}

type MessageRow struct {
	MsgNode   string `json:"msg_node"`
	Channel   string `json:"channel"`
	Author    string `json:"author"`
	Role      string `json:"role"`
	Timestamp string `json:"timestamp"`
	ReplyTo   string `json:"reply_to,omitempty"`
	IsNoise   bool   `json:"is_noise"`
	Content   string `json:"content"`
}

func MessageRows(msgs []Msg) []MessageRow {
	rows := make([]MessageRow, 0, len(msgs))
	for _, m := range msgs {
		rows = append(rows, MessageRow{MsgNode: m.NodeID, Channel: m.Channel,
			Author: m.Author, Role: m.Role,
			Timestamp: m.At.Format(timestampLayout), ReplyTo: m.ReplyTo,
			IsNoise: m.IsNoise, Content: m.Content})
	}
	return rows
}
