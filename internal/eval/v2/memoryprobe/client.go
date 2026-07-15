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
	"strings"
	"time"
)

const (
	ProviderTeamNote = "team_note"
	ProviderMem0     = "mem0"
	defaultAttempts  = 120
	preflightNeedle  = "durable verification code remains active"
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

func (c *Client) Ingest(ctx context.Context, provider, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("ingest eval transcript: text is required")
	}
	switch provider {
	case ProviderTeamNote:
		return c.ingestTeamNote(ctx, text)
	case ProviderMem0:
		_, err := c.addMem0(ctx, text)
		return err
	default:
		return fmt.Errorf("ingest eval transcript: unsupported provider %q", provider)
	}
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
	refs, err := probe.addMem0(ctx, probeText)
	if err != nil {
		return fmt.Errorf("preflight Mem0 add: %w", err)
	}
	if len(refs) == 0 {
		return fmt.Errorf("preflight Mem0 add: add returned no memory IDs")
	}
	if err := probe.pollNonEmpty(ctx, preflightNeedle, "results", probe.searchMem0); err != nil {
		return fmt.Errorf("preflight Mem0 recall: %w", err)
	}
	for _, ref := range refs {
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

func (c *Client) ingestTeamNote(ctx context.Context, text string) error {
	_, err := c.observeTeamNote(ctx, text)
	return err
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

func (c *Client) addMem0(ctx context.Context, text string) ([]string, error) {
	payload := map[string]any{
		"messages": []map[string]string{{"role": "user", "content": text}},
		"user_id":  c.userID, "agent_id": c.agentID, "run_id": c.runID,
		"metadata": map[string]string{"eval_run_id": c.runID, "eval_event_id": stableID(c.runID, c.agentID, text)},
	}
	body, err := c.do(ctx, http.MethodPost, c.mem0URL+"/memories", "", payload)
	if err != nil {
		return nil, err
	}
	refs, err := memoryIDs(body)
	if err != nil {
		return nil, err
	}
	return refs, nil
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

func memoryIDs(body []byte) ([]string, error) {
	var response struct {
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode Mem0 add response: %w", err)
	}
	rawResults := bytes.TrimSpace(response.Results)
	if len(rawResults) == 0 || rawResults[0] != '[' {
		return nil, fmt.Errorf("decode Mem0 add response: results must be an array")
	}
	var items []struct {
		ID    string `json:"id"`
		Event string `json:"event"`
	}
	if err := json.Unmarshal(rawResults, &items); err != nil {
		return nil, fmt.Errorf("decode Mem0 add response results: %w", err)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" && (item.Event == "" || strings.EqualFold(item.Event, "add")) {
			result = append(result, item.ID)
		}
	}
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
