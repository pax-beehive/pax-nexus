// Command eval-v2-zep ingests and retrieves isolated Zep Eval v2 evidence.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

const defaultBaseURL = "https://api.getzep.com/api/v2"

type output struct {
	Provider     string   `json:"provider,omitempty"`
	Accepted     int      `json:"accepted,omitempty"`
	SourceEvents int      `json:"source_events,omitempty"`
	Context      string   `json:"context,omitempty"`
	Episodes     int      `json:"episodes,omitempty"`
	Processed    int      `json:"processed,omitempty"`
	EpisodeIDs   []string `json:"episode_ids,omitempty"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, http.DefaultClient); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "eval v2 Zep failed: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, client *http.Client) error {
	flags := flag.NewFlagSet("eval-v2-zep", flag.ContinueOnError)
	action := flags.String("action", "", "ingest, search, ready, or preflight")
	input := flags.String("session-batches-file", "", "native session batches JSON")
	userID := flags.String("user-id", "", "isolated Zep user graph identifier")
	query := flags.String("query", "", "retrieval query")
	budget := flags.Int("max-characters", 2000, "native Zep context character budget")
	baseURL := flags.String("base-url", envOr("ZEP_BASE_URL", defaultBaseURL), "Zep API base URL")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse Zep flags: %w", err)
	}
	apiKey := strings.TrimSpace(os.Getenv("ZEP_API_KEY"))
	if apiKey == "" {
		return fmt.Errorf("ZEP_API_KEY is required")
	}
	if strings.TrimSpace(*userID) == "" {
		return fmt.Errorf("user-id is required")
	}
	zep := zepClient{baseURL: strings.TrimRight(*baseURL, "/"), apiKey: apiKey, http: client}
	var result output
	var err error
	switch *action {
	case "ingest":
		result, err = ingest(zep, *userID, *input)
	case "search":
		result, err = search(zep, *userID, *query, *budget)
	case "ready":
		result, err = readiness(zep, *userID)
	case "preflight":
		result, err = preflight(zep, *userID)
	default:
		err = fmt.Errorf("action must be ingest, search, ready, or preflight")
	}
	if err != nil {
		return err
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return fmt.Errorf("encode Zep result: %w", err)
	}
	return nil
}

func preflight(z zepClient, userID string) (output, error) {
	if strings.TrimSpace(userID) == "" {
		return output{}, fmt.Errorf("user-id is required")
	}
	if err := z.call(http.MethodPost, "/users", map[string]string{"user_id": userID}, nil, http.StatusCreated, http.StatusConflict); err != nil {
		return output{}, err
	}
	return output{Provider: "zep"}, nil
}

type zepClient struct {
	baseURL, apiKey string
	http            *http.Client
}

func (z zepClient) call(method, path string, request, response any, accepted ...int) (callErr error) {
	var body io.Reader
	if request != nil {
		encoded, err := json.Marshal(request)
		if err != nil {
			return fmt.Errorf("encode Zep request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, z.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create Zep request: %w", err)
	}
	req.Header.Set("Authorization", "Api-Key "+z.apiKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := z.http.Do(req)
	if err != nil {
		return fmt.Errorf("call Zep %s: %w", path, err)
	}
	defer func() {
		if closeErr := res.Body.Close(); closeErr != nil && callErr == nil {
			callErr = fmt.Errorf("close Zep response: %w", closeErr)
		}
	}()
	for _, status := range accepted {
		if res.StatusCode == status {
			if response != nil {
				if err := json.NewDecoder(res.Body).Decode(response); err != nil && err != io.EOF {
					return fmt.Errorf("decode Zep response: %w", err)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("call Zep %s: unexpected status %d", path, res.StatusCode)
}

func ingest(z zepClient, userID, path string) (output, error) {
	if strings.TrimSpace(path) == "" {
		return output{}, fmt.Errorf("session-batches-file is required")
	}
	if err := z.call(http.MethodPost, "/users", map[string]string{"user_id": userID}, nil, http.StatusCreated, http.StatusConflict); err != nil {
		return output{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return output{}, fmt.Errorf("read session batches: %w", err)
	}
	var batches []session.SessionBatch
	if err := json.Unmarshal(data, &batches); err != nil {
		return output{}, fmt.Errorf("decode session batches: %w", err)
	}
	bySession := make(map[string][]session.SessionEvent)
	result := output{Provider: "zep", EpisodeIDs: []string{}}
	for batchIndex, batch := range batches {
		if !batch.Complete {
			return output{}, fmt.Errorf("incomplete session batch")
		}
		for _, event := range batch.Events {
			result.SourceEvents++
			if event.Visibility == "team_note_eligible" {
				sessionID := strings.TrimSpace(event.Actor.SessionID)
				if sessionID == "" {
					sessionID = "batch-" + strconv.Itoa(batchIndex)
				}
				bySession[sessionID] = append(bySession[sessionID], event)
			}
		}
	}
	sessionIDs := make([]string, 0, len(bySession))
	for sessionID := range bySession {
		sessionIDs = append(sessionIDs, sessionID)
	}
	sort.Strings(sessionIDs)
	for _, sessionID := range sessionIDs {
		request := sessionEpisodeRequest(userID, bySession[sessionID])
		var episode struct {
			UUID string `json:"uuid"`
		}
		if err := z.call(http.MethodPost, "/graph", request, &episode, http.StatusAccepted); err != nil {
			return output{}, err
		}
		result.Accepted++
		result.EpisodeIDs = append(result.EpisodeIDs, episode.UUID)
	}
	return result, nil
}

func sessionEpisodeRequest(userID string, events []session.SessionEvent) map[string]any {
	sorted := append([]session.SessionEvent(nil), events...)
	sort.Slice(sorted, func(left, right int) bool {
		return sorted[left].OccurredAt.Before(sorted[right].OccurredAt)
	})
	actors := make(map[string]struct{})
	sessions := make(map[string]struct{})
	eventIDs := make([]string, 0, len(sorted))
	var content strings.Builder
	for _, event := range sorted {
		actors[event.Actor.UserID+"/"+event.Actor.AgentID] = struct{}{}
		sessions[event.Actor.SessionID] = struct{}{}
		eventIDs = append(eventIDs, event.ID)
		_, _ = fmt.Fprintf(&content, "[%s] user=%s agent=%s session=%s event=%s\n%s\n\n", event.OccurredAt.UTC().Format(time.RFC3339Nano), event.Actor.UserID, event.Actor.AgentID, event.Actor.SessionID, event.ID, event.Content)
	}
	return map[string]any{
		"user_id": userID, "type": "text", "data": content.String(),
		"created_at": sorted[len(sorted)-1].OccurredAt.UTC().Format(time.RFC3339Nano),
		"metadata": map[string]string{
			"source_event_count": strconv.Itoa(len(sorted)),
			"source_event_ids":   strings.Join(eventIDs, ","),
			"actors":             joinedKeys(actors),
			"session_ids":        joinedKeys(sessions),
			"visibility":         "team_note_eligible",
		},
	}
}

func joinedKeys(values map[string]struct{}) string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func search(z zepClient, userID, query string, maxCharacters int) (output, error) {
	if strings.TrimSpace(query) == "" {
		return output{}, fmt.Errorf("query is required")
	}
	var response struct {
		Context  string `json:"context"`
		Episodes []struct {
			UUID      string `json:"uuid"`
			Processed bool   `json:"processed"`
		} `json:"episodes"`
	}
	request := map[string]any{"user_id": userID, "query": query, "scope": "auto", "return_raw_results": true, "max_characters": max(1, maxCharacters)}
	if err := z.call(http.MethodPost, "/graph/search", request, &response, http.StatusOK); err != nil {
		return output{}, err
	}
	result := output{Provider: "zep", Context: response.Context, Episodes: len(response.Episodes), EpisodeIDs: []string{}}
	for _, episode := range response.Episodes {
		result.EpisodeIDs = append(result.EpisodeIDs, episode.UUID)
		if episode.Processed {
			result.Processed++
		}
	}
	return result, nil
}

func readiness(z zepClient, userID string) (output, error) {
	var response struct {
		Episodes []struct {
			UUID      string `json:"uuid"`
			Processed bool   `json:"processed"`
		} `json:"episodes"`
	}
	if err := z.call(http.MethodGet, "/graph/episodes/user/"+userID+"?lastn=100", nil, &response, http.StatusOK); err != nil {
		return output{}, err
	}
	result := output{Provider: "zep", Episodes: len(response.Episodes), EpisodeIDs: []string{}}
	for _, episode := range response.Episodes {
		result.EpisodeIDs = append(result.EpisodeIDs, episode.UUID)
		if episode.Processed {
			result.Processed++
		}
	}
	return result, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
