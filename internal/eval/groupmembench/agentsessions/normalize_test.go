package agentsessions

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

func msg(node, ts, replyTo string) groupmembench.Message {
	return groupmembench.Message{NodeID: node, Timestamp: ts, ReplyTo: replyTo,
		Author: "User_1", Channel: "C", Content: "x"}
}

func TestNormalizeSortsByTimestampThenNode(t *testing.T) {
	out, _, err := Normalize([]groupmembench.Message{
		msg("Msg_2", "2025-07-19T00:20:00", ""),
		msg("Msg_1", "2025-07-19T00:10:00", ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].NodeID != "Msg_1" || out[1].NodeID != "Msg_2" {
		t.Fatalf("wrong order: %s, %s", out[0].NodeID, out[1].NodeID)
	}
}

func TestNormalizeClampsReplyBeforeParent(t *testing.T) {
	out, violations, err := Normalize([]groupmembench.Message{
		msg("Msg_p", "2025-07-19T01:00:00", ""),
		msg("Msg_c", "2025-07-19T00:30:00", "Msg_p"), // reply 早于父消息
	})
	if err != nil {
		t.Fatal(err)
	}
	byNode := map[string]Msg{}
	for _, m := range out {
		byNode[m.NodeID] = m
	}
	if !byNode["Msg_c"].At.Equal(byNode["Msg_p"].At) {
		t.Fatalf("child not clamped: %v vs %v", byNode["Msg_c"].At, byNode["Msg_p"].At)
	}
	if len(violations) != 1 {
		t.Fatalf("want 1 violation, got %d", len(violations))
	}
}

func TestNormalizeRejectsBadTimestamp(t *testing.T) {
	_, _, err := Normalize([]groupmembench.Message{msg("Msg_1", "not-a-time", "")})
	if err == nil {
		t.Fatal("want error for bad timestamp")
	}
}
