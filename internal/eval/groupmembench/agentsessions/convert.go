package agentsessions

import (
	"fmt"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

const converterAgentID = "groupmembench-agent"

// ToSessionBatch converts a per-user GroupMemBench Session into the
// team-memory ingest wire format: one "observation" event per channel
// message the persona witnessed (content carries channel/msg_node/author
// plus the original text via msgText), followed by one event per
// trajectory action (event type mirrors the action type). Sequence
// numbers increase monotonically across observations then actions;
// observation events use the message timestamp, action events use the
// session window end.
func ToSessionBatch(s Session, msgText map[string]string) (session.SessionBatch, error) {
	actor := session.Actor{UserID: s.Agent.UserID, AgentID: converterAgentID,
		SessionID: s.SessionID}
	windowEnd, err := time.ParseInLocation(timestampLayout, s.WindowEnd, time.UTC)
	if err != nil {
		return session.SessionBatch{}, fmt.Errorf("parse window end: %w", err)
	}
	var events []session.SessionEvent
	sequence := int64(0)
	for _, o := range s.Observations {
		text, ok := msgText[o.MsgNode]
		if !ok {
			return session.SessionBatch{}, fmt.Errorf(
				"missing text for %s in %s", o.MsgNode, s.SessionID)
		}
		at, err := time.ParseInLocation(timestampLayout, o.Timestamp, time.UTC)
		if err != nil {
			return session.SessionBatch{}, fmt.Errorf(
				"parse observation timestamp %s: %w", o.MsgNode, err)
		}
		sequence++
		events = append(events, session.SessionEvent{
			ID:       fmt.Sprintf("%s/obs/%s", s.SessionID, o.MsgNode),
			Actor:    actor,
			Sequence: sequence,
			Type:     "observation",
			Content: fmt.Sprintf("[#%s msg:%s author:%s] %s",
				o.Channel, o.MsgNode, o.Author, text),
			Visibility: "team_note_eligible",
			OccurredAt: at,
			CapturedAt: at,
		})
	}
	for i, action := range s.Trajectory {
		sequence++
		events = append(events, session.SessionEvent{
			ID:         fmt.Sprintf("%s/act/%d", s.SessionID, i),
			Actor:      actor,
			Sequence:   sequence,
			Type:       action.Type,
			Content:    action.Content,
			Visibility: "team_note_eligible",
			OccurredAt: windowEnd,
			CapturedAt: windowEnd,
			Metadata:   actionMetadata(action),
		})
	}
	return session.SessionBatch{Events: events, Complete: true}, nil
}

func actionMetadata(action Action) map[string]string {
	metadata := map[string]string{}
	if len(action.SourceMsgs) > 0 {
		metadata["source_msgs"] = action.SourceMsgs[0]
		for _, node := range action.SourceMsgs[1:] {
			metadata["source_msgs"] += "," + node
		}
	}
	if action.Freeform {
		metadata["freeform"] = "true"
	}
	if action.Fallback {
		metadata["fallback"] = "true"
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}
