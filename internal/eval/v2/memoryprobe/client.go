// Package memoryprobe provides the Eval v2 provider preflight and transcript ingestion path.
package memoryprobe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

const (
	ProviderTeamNote     = "team_note"
	ProviderMem0         = "mem0"
	ProviderMem0Messages = "mem0_messages"
	defaultAttempts      = 120
	preflightNeedle      = "durable verification code remains active"
)

type Config struct {
	TeamNoteURL    string
	TeamNoteAPIKey string
	Mem0URL        string
	UserID         string
	AgentID        string
	RunID          string
	HTTPClient     *http.Client
	PollInterval   time.Duration
}

type Client struct {
	teamNoteURL    string
	teamNoteAPIKey string
	mem0URL        string
	userID         string
	agentID        string
	runID          string
	httpClient     *http.Client
	pollInterval   time.Duration
}

type IngestResult struct {
	Provider       string `json:"provider"`
	Accepted       int    `json:"accepted"`
	Duplicate      int    `json:"duplicate"`
	Created        int    `json:"created"`
	Updated        int    `json:"updated"`
	Deleted        int    `json:"deleted"`
	NoOpKnown      bool   `json:"noop_known"`
	NoOp           bool   `json:"noop"`
	SourceEvents   int    `json:"source_events,omitempty"`
	SourceActors   int    `json:"source_actors,omitempty"`
	SourceSessions int    `json:"source_sessions,omitempty"`
}

func New(config Config) (*Client, error) {
	teamNoteURL, err := validateURL("Team Note", config.TeamNoteURL)
	if err != nil {
		return nil, err
	}
	mem0URL, err := validateURL("Mem0", config.Mem0URL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.TeamNoteAPIKey) == "" || strings.TrimSpace(config.UserID) == "" || strings.TrimSpace(config.AgentID) == "" || strings.TrimSpace(config.RunID) == "" {
		return nil, fmt.Errorf("create eval memory probe: API key, user ID, agent ID, and run ID are required")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}
	pollInterval := config.PollInterval
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	return &Client{
		teamNoteURL: teamNoteURL, teamNoteAPIKey: config.TeamNoteAPIKey, mem0URL: mem0URL,
		userID: config.UserID, agentID: config.AgentID, runID: config.RunID,
		httpClient: httpClient, pollInterval: pollInterval,
	}, nil
}

func (c *Client) Ingest(ctx context.Context, provider, text string) (IngestResult, error) {
	if strings.TrimSpace(text) == "" {
		return IngestResult{}, fmt.Errorf("ingest eval transcript: text is required")
	}
	switch provider {
	case ProviderTeamNote:
		return c.ingestTeamNote(ctx, text)
	case ProviderMem0:
		added, err := c.addMem0(ctx, text)
		return added.IngestResult, err
	default:
		return IngestResult{}, fmt.Errorf("ingest eval transcript: unsupported provider %q", provider)
	}
}

// IngestBatches feeds the same native source events to each memory provider.
// Team Note retains the source sessions and actors, while Mem0 receives a
// deterministic transcript because its API accepts conversational messages.
func (c *Client) IngestBatches(ctx context.Context, provider string, batches []session.SessionBatch) (IngestResult, error) {
	counts, err := validateBatches(batches)
	if err != nil {
		return IngestResult{}, fmt.Errorf("ingest native eval memory: %w", err)
	}
	var result IngestResult
	switch provider {
	case ProviderTeamNote:
		result, err = c.ingestTeamNoteBatches(ctx, batches)
	case ProviderMem0:
		var added mem0AddResult
		added, err = c.addMem0(ctx, renderBatches(batches))
		result = added.IngestResult
	case ProviderMem0Messages:
		result, err = c.ingestMem0Messages(ctx, batches)
	default:
		return IngestResult{}, fmt.Errorf("ingest eval session batches: unsupported provider %q", provider)
	}
	if err != nil {
		return IngestResult{}, fmt.Errorf("ingest native eval memory with %s: %w", provider, err)
	}
	result.SourceEvents = counts.events
	result.SourceActors = counts.actors
	result.SourceSessions = counts.sessions
	return result, nil
}

func (c *Client) ingestMem0Messages(ctx context.Context, batches []session.SessionBatch) (IngestResult, error) {
	events := make([]session.SessionEvent, 0)
	for _, batch := range batches {
		events = append(events, batch.Events...)
	}
	slices.SortStableFunc(events, func(left, right session.SessionEvent) int {
		if comparison := left.OccurredAt.Compare(right.OccurredAt); comparison != 0 {
			return comparison
		}
		return strings.Compare(left.ID, right.ID)
	})
	result := IngestResult{Provider: ProviderMem0Messages, NoOpKnown: true, NoOp: true}
	for index, event := range events {
		added, err := c.addMem0Event(ctx, event)
		if err != nil {
			return IngestResult{}, fmt.Errorf("ingest Mem0 original message %d %q: %w", index, event.ID, err)
		}
		result.Accepted += added.Accepted
		result.Created += added.Created
		result.Updated += added.Updated
		result.Deleted += added.Deleted
		result.NoOp = result.NoOp && added.NoOp
	}
	return result, nil
}

type batchCounts struct {
	events   int
	actors   int
	sessions int
}

func validateBatches(batches []session.SessionBatch) (batchCounts, error) {
	if len(batches) == 0 {
		return batchCounts{}, fmt.Errorf("ingest eval session batches: at least one batch is required")
	}
	actorIDs := make(map[string]struct{})
	sessionIDs := make(map[string]struct{})
	eventCount := 0
	for batchIndex, batch := range batches {
		if !batch.Complete || len(batch.Events) == 0 {
			return batchCounts{}, fmt.Errorf("ingest eval session batches: batch %d must be complete and contain events", batchIndex)
		}
		first := batch.Events[0].Actor
		previousSequence := int64(0)
		for eventIndex, event := range batch.Events {
			if strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.Content) == "" ||
				strings.TrimSpace(event.Actor.UserID) == "" || strings.TrimSpace(event.Actor.AgentID) == "" || strings.TrimSpace(event.Actor.SessionID) == "" {
				return batchCounts{}, fmt.Errorf("ingest eval session batches: batch %d event %d has empty required fields", batchIndex, eventIndex)
			}
			if event.Actor != first {
				return batchCounts{}, fmt.Errorf("ingest eval session batches: batch %d mixes actor sessions", batchIndex)
			}
			if event.Sequence <= previousSequence {
				return batchCounts{}, fmt.Errorf("ingest eval session batches: batch %d sequences must increase", batchIndex)
			}
			previousSequence = event.Sequence
			eventCount++
		}
		actorIDs[first.AgentID] = struct{}{}
		sessionIDs[first.SessionID] = struct{}{}
	}
	return batchCounts{events: eventCount, actors: len(actorIDs), sessions: len(sessionIDs)}, nil
}

func (c *Client) ingestTeamNoteBatches(ctx context.Context, batches []session.SessionBatch) (IngestResult, error) {
	result := IngestResult{Provider: ProviderTeamNote}
	for index, batch := range batches {
		body, err := c.do(ctx, http.MethodPost, c.teamNoteURL+"/v1/session-batches", c.teamNoteAPIKey, batch)
		if err != nil {
			return IngestResult{}, fmt.Errorf("ingest Team Note session batch %d: %w", index, err)
		}
		var receipt teamNoteReceipt
		if err := json.Unmarshal(body, &receipt); err != nil {
			return IngestResult{}, fmt.Errorf("decode Team Note session batch %d response: %w", index, err)
		}
		result.Accepted += receipt.Accepted
		result.Duplicate += receipt.Duplicate
	}
	return result, nil
}

func renderBatches(batches []session.SessionBatch) string {
	events := make([]session.SessionEvent, 0)
	for _, batch := range batches {
		events = append(events, batch.Events...)
	}
	slices.SortStableFunc(events, func(left, right session.SessionEvent) int {
		if comparison := left.OccurredAt.Compare(right.OccurredAt); comparison != 0 {
			return comparison
		}
		return strings.Compare(left.ID, right.ID)
	})
	parts := []string{"# GroupMemBench native conversation events\n\n"}
	for _, event := range events {
		parts = append(parts, fmt.Sprintf("## %s\n\n", event.ID))
		parts = append(parts, fmt.Sprintf("- User: %s\n- Agent: %s\n", event.Actor.UserID, event.Actor.AgentID))
		if role := event.Metadata["role"]; role != "" {
			parts = append(parts, fmt.Sprintf("- Role: %s\n", role))
		}
		for _, field := range []struct {
			label string
			key   string
		}{
			{label: "Channel", key: "channel"},
			{label: "Phase", key: "phase"},
			{label: "Topic", key: "topic"},
			{label: "Decision point", key: "decision_point"},
			{label: "Noise", key: "noise"},
			{label: "Decision change metadata", key: "decision_change_metadata"},
		} {
			if value := event.Metadata[field.key]; value != "" {
				parts = append(parts, fmt.Sprintf("- %s: %s\n", field.label, value))
			}
		}
		parts = append(parts, fmt.Sprintf("- Timestamp: %s\n", event.OccurredAt.UTC().Format(time.RFC3339Nano)))
		if replyTo := event.Metadata["reply_to"]; replyTo != "" {
			parts = append(parts, fmt.Sprintf("- Reply to: %s\n", replyTo))
		}
		parts = append(parts, fmt.Sprintf("\n%s\n\n", event.Content))
	}
	return strings.Join(parts, "")
}

func (c *Client) Preflight(ctx context.Context, marker string) error {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return fmt.Errorf("preflight eval memory: marker is required")
	}
	probe := c.newPreflightProbe(marker)
	probeText := fmt.Sprintf("The evaluation owner confirmed the durable verification code %s remains active and must be retained for this run.", marker)
	if _, err := c.do(ctx, http.MethodGet, c.teamNoteURL+"/healthz", "", nil); err != nil {
		return fmt.Errorf("preflight Team Note health: %w", err)
	}
	receipt, err := probe.observeTeamNote(ctx, probeText)
	if err != nil {
		return fmt.Errorf("preflight Team Note add: %w", err)
	}
	if receipt.Accepted != 1 || receipt.Duplicate != 0 {
		return fmt.Errorf("preflight Team Note add: expected one accepted event, got accepted=%d duplicate=%d", receipt.Accepted, receipt.Duplicate)
	}
	if err := probe.pollTeamNoteOrigin(ctx, preflightNeedle, probe.runID); err != nil {
		return fmt.Errorf("preflight Team Note recall: %w", err)
	}
	if _, err := c.do(ctx, http.MethodGet, c.mem0URL+"/openapi.json", "", nil); err != nil {
		return fmt.Errorf("preflight Mem0 health: %w", err)
	}
	added, err := probe.addMem0(ctx, probeText)
	if err != nil {
		return fmt.Errorf("preflight Mem0 add: %w", err)
	}
	if len(added.refs) == 0 {
		return fmt.Errorf("preflight Mem0 add: add returned no memory IDs")
	}
	if err := probe.pollNonEmpty(ctx, preflightNeedle, "results", probe.searchMem0); err != nil {
		return fmt.Errorf("preflight Mem0 recall: %w", err)
	}
	for _, ref := range added.refs {
		if _, err := probe.do(ctx, http.MethodDelete, c.mem0URL+"/memories/"+url.PathEscape(ref), "", nil); err != nil {
			return fmt.Errorf("preflight Mem0 delete %q: %w", ref, err)
		}
	}
	body, err := probe.searchMem0(ctx, preflightNeedle)
	if err != nil {
		return fmt.Errorf("preflight Mem0 cleanup search: %w", err)
	}
	found, err := responseHasItems(body, "results")
	if err != nil {
		return fmt.Errorf("preflight Mem0 cleanup: %w", err)
	}
	if found {
		return fmt.Errorf("preflight Mem0 cleanup: deleted probe is still searchable")
	}
	return nil
}

func (c *Client) newPreflightProbe(marker string) *Client {
	invocationID := stableID(marker, time.Now().UTC().Format(time.RFC3339Nano))
	probe := *c
	probe.agentID = c.agentID + "-" + invocationID
	probe.runID = c.runID + "-" + invocationID
	return &probe
}

func (c *Client) ingestTeamNote(ctx context.Context, text string) (IngestResult, error) {
	receipt, err := c.observeTeamNote(ctx, text)
	if err != nil {
		return IngestResult{}, err
	}
	return IngestResult{
		Provider: ProviderTeamNote, Accepted: receipt.Accepted, Duplicate: receipt.Duplicate,
	}, nil
}

type teamNoteReceipt struct {
	Accepted  int `json:"accepted"`
	Duplicate int `json:"duplicate"`
}

func (c *Client) observeTeamNote(ctx context.Context, text string) (teamNoteReceipt, error) {
	now := time.Now().UTC()
	payload := map[string]any{
		"events": []map[string]any{{
			"id":       stableID(c.runID, c.agentID, text),
			"actor":    map[string]string{"user_id": c.userID, "agent_id": c.agentID, "session_id": c.runID},
			"sequence": now.UnixNano(), "type": "assistant", "content": text,
			"visibility": "team_note_eligible", "occurred_at": now.Format(time.RFC3339Nano),
			"metadata": map[string]string{"eval_run_id": c.runID},
		}},
		"complete": true,
	}
	body, err := c.do(ctx, http.MethodPost, c.teamNoteURL+"/v1/session-batches", c.teamNoteAPIKey, payload)
	if err != nil {
		return teamNoteReceipt{}, err
	}
	var receipt teamNoteReceipt
	if err := json.Unmarshal(body, &receipt); err != nil {
		return teamNoteReceipt{}, fmt.Errorf("decode Team Note add response: %w", err)
	}
	return receipt, nil
}

func (c *Client) recallTeamNote(ctx context.Context, marker string) ([]byte, error) {
	payload := map[string]any{
		"actor":        map[string]string{"user_id": c.userID, "agent_id": c.agentID, "session_id": c.runID},
		"token_budget": 500, "query": marker,
	}
	return c.do(ctx, http.MethodPost, c.teamNoteURL+"/v1/notes/recall", c.teamNoteAPIKey, payload)
}

type mem0AddResult struct {
	IngestResult
	refs []string
}

func (c *Client) addMem0(ctx context.Context, text string) (mem0AddResult, error) {
	metadata := map[string]string{"eval_run_id": c.runID, "eval_event_id": stableID(c.runID, c.agentID, text)}
	return c.addMem0WithMetadata(ctx, text, metadata)
}

func (c *Client) addMem0Event(ctx context.Context, event session.SessionEvent) (mem0AddResult, error) {
	text := fmt.Sprintf(
		"Message %s from %s (%s) at %s:\n%s",
		event.ID, event.Actor.UserID, event.Metadata["role"], event.OccurredAt.UTC().Format(time.RFC3339Nano), event.Content,
	)
	metadata := map[string]string{
		"eval_run_id": c.runID, "eval_event_id": event.ID,
		"source_user_id": event.Actor.UserID, "source_agent_id": event.Actor.AgentID,
		"source_session_id": event.Actor.SessionID, "source_occurred_at": event.OccurredAt.UTC().Format(time.RFC3339Nano),
	}
	for _, key := range []string{"role", "channel", "phase", "topic", "reply_to", "decision_point"} {
		if value := event.Metadata[key]; value != "" {
			metadata[key] = value
		}
	}
	return c.addMem0WithMetadata(ctx, text, metadata)
}

func (c *Client) addMem0WithMetadata(ctx context.Context, text string, metadata map[string]string) (mem0AddResult, error) {
	payload := map[string]any{
		"messages": []map[string]string{{"role": "user", "content": text}},
		"user_id":  c.userID, "agent_id": c.agentID, "run_id": c.runID,
		"metadata": metadata,
	}
	body, err := c.do(ctx, http.MethodPost, c.mem0URL+"/memories", "", payload)
	if err != nil {
		return mem0AddResult{}, err
	}
	result, err := mem0AddResultFromResponse(body)
	if err != nil {
		return mem0AddResult{}, err
	}
	return result, nil
}

func (c *Client) searchMem0(ctx context.Context, marker string) ([]byte, error) {
	payload := map[string]any{
		"query": marker, "user_id": c.userID, "agent_id": c.agentID, "run_id": c.runID,
	}
	return c.do(ctx, http.MethodPost, c.mem0URL+"/search", "", payload)
}

func (c *Client) pollNonEmpty(
	ctx context.Context,
	query string,
	field string,
	request func(context.Context, string) ([]byte, error),
) error {
	for attempt := range defaultAttempts {
		body, err := request(ctx, query)
		if err == nil {
			var found bool
			found, err = responseHasItems(body, field)
			if err == nil && found {
				return nil
			}
		}
		if attempt == defaultAttempts-1 {
			if err != nil {
				return err
			}
			break
		}
		timer := time.NewTimer(c.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("no recalled %s after %d attempts", field, defaultAttempts)
}

func (c *Client) pollTeamNoteOrigin(ctx context.Context, query, originSessionID string) error {
	for attempt := range defaultAttempts {
		body, err := c.recallTeamNote(ctx, query)
		if err == nil {
			var found bool
			found, err = responseHasOrigin(body, originSessionID)
			if err == nil && found {
				return nil
			}
		}
		if attempt == defaultAttempts-1 {
			if err != nil {
				return err
			}
			break
		}
		timer := time.NewTimer(c.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("team note origin %q was not recalled after %d attempts", originSessionID, defaultAttempts)
}

func responseHasOrigin(body []byte, originSessionID string) (bool, error) {
	var response struct {
		Details []struct {
			Origin struct {
				SessionID string `json:"session_id"`
			} `json:"origin"`
		} `json:"details"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return false, fmt.Errorf("decode Team Note recall response: %w", err)
	}
	for _, detail := range response.Details {
		if detail.Origin.SessionID == originSessionID {
			return true, nil
		}
	}
	return false, nil
}

func responseHasItems(body []byte, field string) (bool, error) {
	var response map[string]json.RawMessage
	if err := json.Unmarshal(body, &response); err != nil {
		return false, fmt.Errorf("decode response: %w", err)
	}
	rawItems, ok := response[field]
	if !ok {
		return false, fmt.Errorf("decode response: field %q is missing", field)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(rawItems, &items); err != nil {
		return false, fmt.Errorf("decode response field %q: %w", field, err)
	}
	return len(items) > 0, nil
}

func (c *Client) do(ctx context.Context, method, endpoint, apiKey string, payload any) (body []byte, returnedErr error) {
	var input io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		input = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, input)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("close response: %w", closeErr))
		}
	}()
	body, err = io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("request returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func mem0AddResultFromResponse(body []byte) (mem0AddResult, error) {
	var response struct {
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return mem0AddResult{}, fmt.Errorf("decode Mem0 add response: %w", err)
	}
	rawResults := bytes.TrimSpace(response.Results)
	if len(rawResults) == 0 || rawResults[0] != '[' {
		return mem0AddResult{}, fmt.Errorf("decode Mem0 add response: results must be an array")
	}
	var items []struct {
		ID    string `json:"id"`
		Event string `json:"event"`
	}
	if err := json.Unmarshal(rawResults, &items); err != nil {
		return mem0AddResult{}, fmt.Errorf("decode Mem0 add response results: %w", err)
	}
	result := mem0AddResult{IngestResult: IngestResult{Provider: ProviderMem0, Accepted: 1, NoOpKnown: true}, refs: make([]string, 0, len(items))}
	for _, item := range items {
		switch strings.ToUpper(strings.TrimSpace(item.Event)) {
		case "", "ADD":
			result.Created++
			if strings.TrimSpace(item.ID) != "" {
				result.refs = append(result.refs, item.ID)
			}
		case "UPDATE":
			result.Updated++
		case "DELETE":
			result.Deleted++
		case "NONE":
		default:
			return mem0AddResult{}, fmt.Errorf("decode Mem0 add response: unsupported event %q", item.Event)
		}
	}
	result.NoOp = result.Created+result.Updated+result.Deleted == 0
	return result, nil
}

func validateURL(name, value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("create eval memory probe: valid %s URL is required", name)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func stableID(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "eval-" + hex.EncodeToString(digest[:16])
}
