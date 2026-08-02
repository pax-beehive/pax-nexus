package agentsessions

import (
	"regexp"
	"sort"
)

const maxActionsPerSession = 8

type ActionSpec struct {
	Type      string
	SourceMsg string
	Freeform  bool
	priority  int
}

var (
	deadlinePattern = regexp.MustCompile(
		`(?i)\b(deadline|due)\b|\bby \d{4}-\d{2}-\d{2}\b|\b\d{4}-\d{2}-\d{2}\b`)
	userToken = regexp.MustCompile(`\bUser_\d+\b`)
)

func BuildSkeleton(w Window, parentAuthor map[string]string, anchors map[string]bool) []ActionSpec {
	var specs []ActionSpec
	for _, m := range w.Msgs {
		if anchors[m.NodeID] {
			specs = append(specs, ActionSpec{Type: "memory_write",
				SourceMsg: m.NodeID, priority: 1})
			continue
		}
		if m.Author == w.User || m.IsNoise {
			continue
		}
		switch {
		case len(m.DecisionChangeMetadata) > 0:
			specs = append(specs, ActionSpec{Type: "memory_write",
				SourceMsg: m.NodeID, priority: 2})
		case mentionsUser(m.Content, w.User) ||
			(m.ReplyTo != "" && parentAuthor[m.ReplyTo] == w.User):
			specs = append(specs, ActionSpec{Type: "draft_reply",
				SourceMsg: m.NodeID, priority: 3})
		case deadlinePattern.MatchString(m.Content):
			specs = append(specs, ActionSpec{Type: "todo",
				SourceMsg: m.NodeID, priority: 4})
		}
	}
	sort.SliceStable(specs, func(i, j int) bool {
		return specs[i].priority < specs[j].priority
	})
	// 留 1 个 freeform 槽位(≈ cap 的 15%),只要有 observation 就给。
	limit := maxActionsPerSession - 1
	if len(specs) > limit {
		specs = specs[:limit]
	}
	if len(w.Msgs) > 0 {
		specs = append(specs, ActionSpec{Type: "memory_write", Freeform: true,
			priority: 5})
	}
	return specs
}

func mentionsUser(content, user string) bool {
	for _, token := range userToken.FindAllString(content, -1) {
		if token == user {
			return true
		}
	}
	return false
}
