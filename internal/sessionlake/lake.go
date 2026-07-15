package sessionlake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

type Repository interface {
	AppendSession(context.Context, string, session.SessionBatch) (session.IngestReceipt, error)
	SessionEvents(context.Context, string, session.Actor, int64, int) ([]session.SessionEvent, error)
	SessionEventsBefore(context.Context, string, session.Actor, int64, int) ([]session.SessionEvent, error)
	SessionLatestSequence(context.Context, string, session.Actor) (int64, error)
	ExtractionCursor(context.Context, string, session.Actor) (int64, error)
	AdvanceExtractionCursor(context.Context, string, session.Actor, int64) error
}

func (l *Lake) IsCurrent(ctx context.Context, actor session.Actor, sequence int64) (bool, error) {
	scopeID, err := session.ScopeFromContext(ctx)
	if err != nil {
		return false, fmt.Errorf("check session head: %w", err)
	}
	latest, err := l.repository.SessionLatestSequence(ctx, scopeID, actor)
	if err != nil {
		return false, fmt.Errorf("read session head: %w", err)
	}
	return latest == sequence, nil
}

type Slice struct {
	Actor           session.Actor
	Events          []session.SessionEvent
	NewEventIDs     []string
	OverlapEventIDs []string
	FromSequence    int64
	ToSequence      int64
	InputChecksum   string
}

type SlicePolicy struct {
	EventLimit      int
	TokenLimit      int
	Overlap         int
	ThroughSequence int64
}

type Lake struct {
	repository Repository
}

func New(repository Repository) *Lake {
	return &Lake{repository: repository}
}

func (l *Lake) Observe(ctx context.Context, batch session.SessionBatch) (session.IngestReceipt, error) {
	scopeID, err := session.ScopeFromContext(ctx)
	if err != nil {
		return session.IngestReceipt{}, fmt.Errorf("observe session: %w", err)
	}
	receipt, err := l.repository.AppendSession(ctx, scopeID, batch)
	if err != nil {
		return session.IngestReceipt{}, fmt.Errorf("observe session: %w", err)
	}
	return receipt, nil
}

func (l *Lake) NextSlice(ctx context.Context, actor session.Actor, policy SlicePolicy) (Slice, error) {
	scopeID, err := session.ScopeFromContext(ctx)
	if err != nil {
		return Slice{}, fmt.Errorf("plan session slice: %w", err)
	}
	if policy.EventLimit <= 0 || policy.TokenLimit <= 0 || policy.Overlap < 0 {
		return Slice{}, fmt.Errorf("plan session slice: event limit, token limit, and overlap are invalid")
	}
	cursor, err := l.repository.ExtractionCursor(ctx, scopeID, actor)
	if err != nil {
		return Slice{}, fmt.Errorf("read extraction cursor: %w", err)
	}
	newEvents, err := l.repository.SessionEvents(ctx, scopeID, actor, cursor, policy.EventLimit)
	if err != nil {
		return Slice{}, fmt.Errorf("read session slice: %w", err)
	}
	newEvents = eventsThrough(newEvents, policy.ThroughSequence)
	slice := Slice{Actor: actor, FromSequence: cursor + 1}
	if len(newEvents) == 0 {
		return slice, nil
	}
	newEvents = boundedEvents(newEvents, policy.TokenLimit)
	overlap, err := l.overlapEvents(ctx, scopeID, actor, cursor, policy, eventTokens(newEvents))
	if err != nil {
		return Slice{}, err
	}
	slice.Events = append(overlap, newEvents...)
	slice.NewEventIDs = eventIDs(newEvents)
	slice.OverlapEventIDs = eventIDs(overlap)
	slice.FromSequence = newEvents[0].Sequence
	slice.ToSequence = newEvents[len(newEvents)-1].Sequence
	slice.InputChecksum = checksum(newEvents)
	return slice, nil
}

func eventsThrough(events []session.SessionEvent, sequence int64) []session.SessionEvent {
	if sequence <= 0 {
		return events
	}
	for index, event := range events {
		if event.Sequence > sequence {
			return events[:index]
		}
	}
	return events
}

func (l *Lake) overlapEvents(ctx context.Context, scopeID string, actor session.Actor, cursor int64, policy SlicePolicy, usedTokens int) ([]session.SessionEvent, error) {
	if cursor == 0 || policy.Overlap == 0 || usedTokens >= policy.TokenLimit {
		return nil, nil
	}
	events, err := l.repository.SessionEventsBefore(ctx, scopeID, actor, cursor, policy.Overlap)
	if err != nil {
		return nil, fmt.Errorf("read session overlap: %w", err)
	}
	remaining := policy.TokenLimit - usedTokens
	start := len(events)
	for start > 0 {
		tokens := estimateEventTokens(events[start-1])
		if tokens > remaining {
			break
		}
		remaining -= tokens
		start--
	}
	return events[start:], nil
}

func (l *Lake) CommitSlice(ctx context.Context, slice Slice) error {
	if len(slice.Events) == 0 {
		return nil
	}
	scopeID, err := session.ScopeFromContext(ctx)
	if err != nil {
		return fmt.Errorf("commit session slice: %w", err)
	}
	if err := l.repository.AdvanceExtractionCursor(ctx, scopeID, slice.Actor, slice.ToSequence); err != nil {
		return fmt.Errorf("advance extraction cursor: %w", err)
	}
	return nil
}

func checksum(events []session.SessionEvent) string {
	var input strings.Builder
	for _, event := range events {
		input.WriteString(event.ID)
		input.WriteByte(0)
		input.WriteString(strconv.FormatInt(event.Sequence, 10))
		input.WriteByte(0)
		input.WriteString(event.Content)
		input.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(input.String()))
	return hex.EncodeToString(sum[:])
}

func boundedEvents(events []session.SessionEvent, tokenLimit int) []session.SessionEvent {
	used := 0
	for index, event := range events {
		tokens := estimateEventTokens(event)
		if index > 0 && used+tokens > tokenLimit {
			return events[:index]
		}
		used += tokens
	}
	return events
}

func eventTokens(events []session.SessionEvent) int {
	total := 0
	for _, event := range events {
		total += estimateEventTokens(event)
	}
	return total
}

func estimateEventTokens(event session.SessionEvent) int {
	characters := len(event.Content) + len(event.Type) + len(event.TaskRef) + len(event.ThreadRef)
	characters += len(event.Actor.UserID) + len(event.Actor.AgentID) + len(event.Actor.SessionID)
	return 32 + (characters+3)/4
}

func eventIDs(events []session.SessionEvent) []string {
	ids := make([]string, len(events))
	for index, event := range events {
		ids[index] = event.ID
	}
	return ids
}
