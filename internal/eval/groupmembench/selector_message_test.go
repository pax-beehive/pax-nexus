package groupmembench

import (
	"encoding/json"
	"testing"
)

func TestMessageDecodesPersonaFields(t *testing.T) {
	raw := `{"msg_node":"Msg_1","author":"User_1","role":"Business Analyst",
"content":"x","timestamp":"2025-07-19T00:14:03","tone":"anxious",
"style":"anecdotal","expertise":"expert","message_type":"post"}`
	var m Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Tone != "anxious" || m.Style != "anecdotal" ||
		m.Expertise != "expert" || m.MessageType != "post" {
		t.Fatalf("persona fields not decoded: %+v", m)
	}
}
