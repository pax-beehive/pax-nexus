package agentsessions

import (
	"fmt"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

func tmsg(node, channel, author, ts string) Msg {
	m, _, err := Normalize([]groupmembench.Message{{NodeID: node, Channel: channel,
		Author: author, Timestamp: ts, Content: "x"}})
	if err != nil {
		panic(err)
	}
	return m[0]
}

func TestUserChannelsOnlyWhereAuthored(t *testing.T) {
	msgs := []Msg{
		tmsg("Msg_1", "A", "User_1", "2025-07-19T01:00:00"),
		tmsg("Msg_2", "B", "User_2", "2025-07-19T02:00:00"),
	}
	got := UserChannels(msgs)
	if len(got["User_1"]) != 1 || got["User_1"][0] != "A" {
		t.Fatalf("User_1 channels = %v, want [A]", got["User_1"])
	}
}

func TestWindowsSplitByDayAndCap(t *testing.T) {
	var visible []Msg
	for i := 0; i < 3; i++ { // 同一天 3 条,cap=2 → s1(2条)+s2(1条)
		visible = append(visible, tmsg(fmt.Sprintf("Msg_%d", i), "A", "User_1",
			fmt.Sprintf("2025-07-19T0%d:00:00", i)))
	}
	visible = append(visible, tmsg("Msg_9", "A", "User_1", "2025-07-20T01:00:00"))
	wins := Windows("User_1", visible, 2)
	if len(wins) != 3 {
		t.Fatalf("want 3 windows, got %d", len(wins))
	}
	if wins[0].SessionID() != "User_1/2025-07-19/s1" ||
		wins[1].SessionID() != "User_1/2025-07-19/s2" ||
		wins[2].SessionID() != "User_1/2025-07-20/s1" {
		t.Fatalf("bad ids: %s %s %s",
			wins[0].SessionID(), wins[1].SessionID(), wins[2].SessionID())
	}
	if len(wins[0].Msgs) != 2 || len(wins[1].Msgs) != 1 {
		t.Fatal("cap not applied")
	}
}
