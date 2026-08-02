package agentsessions

import (
	"fmt"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

func smsg(node, author, content string, opts func(*groupmembench.Message)) Msg {
	base := groupmembench.Message{NodeID: node, Channel: "A", Author: author,
		Timestamp: "2025-07-19T01:00:00", Content: content}
	if opts != nil {
		opts(&base)
	}
	out, _, err := Normalize([]groupmembench.Message{base})
	if err != nil {
		panic(err)
	}
	return out[0]
}

func TestSkeletonPriorities(t *testing.T) {
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1, Msgs: []Msg{
		smsg("Msg_anchor", "User_2", "the fee cap is 3%", nil),
		smsg("Msg_rev", "User_3", "reversing the earlier call",
			func(m *groupmembench.Message) {
				m.DecisionChangeMetadata = map[string]any{"reversal_of": "Msg_0"}
			}),
		smsg("Msg_mention", "User_4", "User_1 please confirm scope", nil),
		smsg("Msg_todo", "User_5", "assessment due 2025-07-16", nil),
		smsg("Msg_plain", "User_6", "nothing interesting", nil),
	}}
	specs := BuildSkeleton(w, map[string]string{},
		map[string]bool{"Msg_anchor": true})
	want := map[string]string{
		"Msg_anchor":  "memory_write",
		"Msg_rev":     "memory_write",
		"Msg_mention": "draft_reply",
		"Msg_todo":    "todo",
	}
	got := map[string]string{}
	freeform := 0
	for _, s := range specs {
		if s.Freeform {
			freeform++
			continue
		}
		got[s.SourceMsg] = s.Type
	}
	for node, typ := range want {
		if got[node] != typ {
			t.Fatalf("%s: got %q want %q", node, got[node], typ)
		}
	}
	if _, ok := got["Msg_plain"]; ok {
		t.Fatal("plain message should not trigger")
	}
	if freeform != 1 {
		t.Fatalf("want 1 freeform slot, got %d", freeform)
	}
}

func TestSkeletonNoiseAndCap(t *testing.T) {
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1}
	w.Msgs = append(w.Msgs, smsg("Msg_noise", "User_2", "due 2025-07-16",
		func(m *groupmembench.Message) { m.IsNoise = true }))
	for i := 0; i < 10; i++ {
		node := fmt.Sprintf("Msg_a%d", i)
		w.Msgs = append(w.Msgs, smsg(node, "User_2", "evidence", nil))
	}
	anchors := map[string]bool{}
	for i := 0; i < 10; i++ {
		anchors[fmt.Sprintf("Msg_a%d", i)] = true
	}
	specs := BuildSkeleton(w, map[string]string{}, anchors)
	if len(specs) > maxActionsPerSession {
		t.Fatalf("cap exceeded: %d", len(specs))
	}
	for _, s := range specs {
		if s.SourceMsg == "Msg_noise" {
			t.Fatal("noise message triggered an action")
		}
	}
}

func TestSkeletonMentionPrefixNoFalsePositive(t *testing.T) {
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1, Msgs: []Msg{
		smsg("Msg_other", "User_2", "User_13 please confirm scope", nil),
	}}
	for _, s := range BuildSkeleton(w, map[string]string{}, nil) {
		if s.SourceMsg == "Msg_other" {
			t.Fatalf("User_13 mention must not trigger for User_1: %+v", s)
		}
	}
	w13 := Window{User: "User_13", Date: "2025-07-19", Part: 1, Msgs: []Msg{
		smsg("Msg_other", "User_2", "User_13 please confirm scope", nil),
	}}
	specs := BuildSkeleton(w13, map[string]string{}, nil)
	if len(specs) == 0 || specs[0].Type != "draft_reply" || specs[0].SourceMsg != "Msg_other" {
		t.Fatalf("User_13 mention must trigger draft_reply for User_13: %+v", specs)
	}
}

func TestSkeletonOneActionPerMessage(t *testing.T) {
	// 同一消息同时命中 anchor、@提及和 deadline:只产生一个动作,取最高优先级。
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1, Msgs: []Msg{
		smsg("Msg_multi", "User_2", "User_1 the assessment is due 2025-07-16", nil),
	}}
	specs := BuildSkeleton(w, map[string]string{}, map[string]bool{"Msg_multi": true})
	var forMsg []ActionSpec
	for _, s := range specs {
		if s.SourceMsg == "Msg_multi" {
			forMsg = append(forMsg, s)
		}
	}
	if len(forMsg) != 1 || forMsg[0].Type != "memory_write" {
		t.Fatalf("want exactly one memory_write for Msg_multi, got %+v", forMsg)
	}
}

func TestSkeletonReplyToUserTriggersDraftReply(t *testing.T) {
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1, Msgs: []Msg{
		smsg("Msg_c", "User_2", "responding to your point",
			func(m *groupmembench.Message) { m.ReplyTo = "Msg_mine" }),
	}}
	specs := BuildSkeleton(w, map[string]string{"Msg_mine": "User_1"}, nil)
	if len(specs) == 0 || specs[0].Type != "draft_reply" || specs[0].SourceMsg != "Msg_c" {
		t.Fatalf("want draft_reply for Msg_c, got %+v", specs)
	}
}

func TestSkeletonAnchorBypassesSelfAndNoise(t *testing.T) {
	// Anchors must produce memory_write even if authored by window user or marked as noise
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1, Msgs: []Msg{
		// Message A: authored by User_1 (window user), but is an anchor
		smsg("Msg_A", "User_1", "critical evidence from self", nil),
		// Message B: marked as noise, but is an anchor
		smsg("Msg_B", "User_2", "noisy evidence due 2025-07-16",
			func(m *groupmembench.Message) { m.IsNoise = true }),
		// Control message C: marked as noise, NOT an anchor, with deadline
		smsg("Msg_C", "User_3", "assessment due 2025-07-16",
			func(m *groupmembench.Message) { m.IsNoise = true }),
	}}
	specs := BuildSkeleton(w, map[string]string{}, map[string]bool{
		"Msg_A": true,
		"Msg_B": true,
	})

	got := map[string]string{}
	for _, s := range specs {
		if !s.Freeform {
			got[s.SourceMsg] = s.Type
		}
	}

	// A must produce memory_write (self-authored anchor)
	if got["Msg_A"] != "memory_write" {
		t.Fatalf("Msg_A: got %q, want memory_write", got["Msg_A"])
	}
	// B must produce memory_write (noise anchor)
	if got["Msg_B"] != "memory_write" {
		t.Fatalf("Msg_B: got %q, want memory_write", got["Msg_B"])
	}
	// C must not produce any action (noise non-anchor)
	if _, ok := got["Msg_C"]; ok {
		t.Fatalf("Msg_C: should not trigger (noise non-anchor), got %q", got["Msg_C"])
	}
}
