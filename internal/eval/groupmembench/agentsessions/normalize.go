// Package agentsessions 把 GroupMemBench 群聊重组为 per-user agent 工作
// session:规则骨架决定动作,LLM 只负责动作的自然语言内容。
package agentsessions

import (
	"fmt"
	"sort"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

const timestampLayout = "2006-01-02T15:04:05"

type Msg struct {
	groupmembench.Message
	At time.Time
}

func Normalize(messages []groupmembench.Message) ([]Msg, []string, error) {
	msgs := make([]Msg, 0, len(messages))
	for _, m := range messages {
		at, err := time.ParseInLocation(timestampLayout, m.Timestamp, time.UTC)
		if err != nil {
			return nil, nil, fmt.Errorf("parse timestamp of %s: %w", m.NodeID, err)
		}
		msgs = append(msgs, Msg{Message: m, At: at})
	}
	byNode := make(map[string]int, len(msgs))
	for i, m := range msgs {
		byNode[m.NodeID] = i
	}
	var violations []string
	for i, m := range msgs {
		if m.ReplyTo == "" {
			continue
		}
		parent, ok := byNode[m.ReplyTo]
		if !ok {
			violations = append(violations,
				fmt.Sprintf("%s: reply_to %s not found", m.NodeID, m.ReplyTo))
			continue
		}
		if m.At.Before(msgs[parent].At) {
			violations = append(violations, fmt.Sprintf(
				"%s: reply at %s before parent %s at %s, clamped",
				m.NodeID, m.At, m.ReplyTo, msgs[parent].At))
			msgs[i].At = msgs[parent].At
		}
	}
	sort.Slice(msgs, func(i, j int) bool {
		if !msgs[i].At.Equal(msgs[j].At) {
			return msgs[i].At.Before(msgs[j].At)
		}
		return msgs[i].NodeID < msgs[j].NodeID
	})
	return msgs, violations, nil
}
