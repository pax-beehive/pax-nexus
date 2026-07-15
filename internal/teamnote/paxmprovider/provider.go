package paxmprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

const (
	methodHealth       = "paxm.health"
	methodSearch       = "paxm.search"
	methodPut          = "paxm.put"
	methodPutBatch     = "paxm.putBatch"
	methodCapabilities = "paxm.capabilities"
)

type Config struct {
	BaseURL        string
	APIKey         string
	UserID         string
	AgentID        string
	TokenBudget    int
	RequestTimeout time.Duration
	Client         *http.Client
	Logger         *slog.Logger
}

type Provider struct {
	baseURL     *url.URL
	apiKey      string
	userID      string
	agentID     string
	tokenBudget int
	client      *http.Client
	logger      *slog.Logger
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      string    `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type memoryItem struct {
	ID        string            `json:"id"`
	Text      string            `json:"text"`
	Source    string            `json:"source"`
	Metadata  map[string]string `json:"metadata"`
	CreatedAt time.Time         `json:"created_at"`
	Origin    actor             `json:"origin"`
}

type searchQuery struct {
	Text     string            `json:"text"`
	Limit    int               `json:"limit"`
	Metadata map[string]string `json:"metadata"`
}

type putBatchParams struct {
	Items []memoryItem `json:"items"`
}

type actor struct {
	UserID    string `json:"user_id"`
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
}

type sessionEventPayload struct {
	ID         string            `json:"id"`
	Actor      actor             `json:"actor"`
	Sequence   int64             `json:"sequence"`
	Type       string            `json:"type"`
	Content    string            `json:"content"`
	TaskRef    string            `json:"task_ref,omitempty"`
	ThreadRef  string            `json:"thread_ref,omitempty"`
	Visibility string            `json:"visibility"`
	OccurredAt string            `json:"occurred_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type sessionBatchPayload struct {
	Events   []sessionEventPayload `json:"events"`
	Complete bool                  `json:"complete"`
}

type preparedMemoryItem struct {
	event sessionEventPayload
	ref   map[string]string
}

func New(config Config) (*Provider, error) {
	baseURL, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil {
		return nil, fmt.Errorf("create paxm provider base URL: %w", err)
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("create paxm provider: valid base URL is required")
	}
	if strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.UserID) == "" || strings.TrimSpace(config.AgentID) == "" {
		return nil, fmt.Errorf("create paxm provider: API key, user ID, and agent ID are required")
	}
	if config.RequestTimeout < 0 {
		return nil, fmt.Errorf("create paxm provider: request timeout cannot be negative")
	}
	client := config.Client
	if client == nil {
		requestTimeout := config.RequestTimeout
		if requestTimeout == 0 {
			requestTimeout = 60 * time.Second
		}
		client = &http.Client{Timeout: requestTimeout}
	}
	tokenBudget := config.TokenBudget
	if tokenBudget <= 0 {
		tokenBudget = 500
	}
	if config.Logger == nil {
		config.Logger = observability.DiscardLogger()
	}
	return &Provider{
		baseURL: baseURL, apiKey: config.APIKey, userID: config.UserID,
		agentID: config.AgentID, tokenBudget: tokenBudget, client: client, logger: config.Logger,
	}, nil
}

func (p *Provider) ServeOne(ctx context.Context, input io.Reader, output io.Writer) error {
	var request rpcRequest
	if err := json.NewDecoder(input).Decode(&request); err != nil {
		return fmt.Errorf("decode JSON-RPC request: %w", err)
	}
	response := rpcResponse{JSONRPC: "2.0", ID: request.ID}
	result, err := p.dispatch(ctx, request.Method, request.Params)
	if err != nil {
		p.logger.ErrorContext(ctx, "paxm request failed", "method", request.Method, "error", err)
		response.Error = &rpcError{Code: -32000, Message: "request failed"}
	} else {
		response.Result = result
	}
	if err := json.NewEncoder(output).Encode(response); err != nil {
		return fmt.Errorf("encode JSON-RPC response: %w", err)
	}
	return nil
}

func (p *Provider) dispatch(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case methodHealth:
		return map[string]string{"status": "ok"}, p.health(ctx)
	case methodCapabilities:
		return map[string]bool{"put_batch": true, "delete": false, "attribution": false}, nil
	case methodPut:
		var item memoryItem
		if err := decodeParams(params, &item); err != nil {
			return nil, err
		}
		ref, err := p.put(ctx, item)
		return map[string]any{"ref": ref}, err
	case methodPutBatch:
		var batch putBatchParams
		if err := decodeParams(params, &batch); err != nil {
			return nil, err
		}
		refs, err := p.putBatch(ctx, batch.Items)
		return map[string]any{"refs": refs}, err
	case methodSearch:
		var query searchQuery
		if err := decodeParams(params, &query); err != nil {
			return nil, err
		}
		hits, err := p.search(ctx, query)
		return map[string]any{"hits": hits}, err
	default:
		return nil, fmt.Errorf("unsupported method %q", method)
	}
}

func (p *Provider) health(ctx context.Context) error {
	request, err := p.newRequest(ctx, http.MethodGet, "/healthz", nil)
	if err != nil {
		return err
	}
	return p.do(request, nil)
}

func (p *Provider) putBatch(ctx context.Context, items []memoryItem) ([]map[string]string, error) {
	prepared := make([]preparedMemoryItem, len(items))
	for index, item := range items {
		value, err := p.prepareMemoryItem(item)
		if err != nil {
			return nil, err
		}
		prepared[index] = value
	}
	refs := make([]map[string]string, len(prepared))
	batches := make([]sessionBatchPayload, 0)
	batchByActor := make(map[actor]int)
	for index, item := range prepared {
		batchIndex, ok := batchByActor[item.event.Actor]
		if !ok {
			batchIndex = len(batches)
			batchByActor[item.event.Actor] = batchIndex
			batches = append(batches, sessionBatchPayload{Complete: true})
		}
		batches[batchIndex].Events = append(batches[batchIndex].Events, item.event)
		refs[index] = item.ref
	}
	for _, batch := range batches {
		if err := p.sendSessionBatch(ctx, batch); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

func (p *Provider) put(ctx context.Context, item memoryItem) (map[string]string, error) {
	prepared, err := p.prepareMemoryItem(item)
	if err != nil {
		return nil, err
	}
	if err := p.sendSessionBatch(ctx, sessionBatchPayload{Events: []sessionEventPayload{prepared.event}, Complete: true}); err != nil {
		return nil, err
	}
	return prepared.ref, nil
}

func (p *Provider) prepareMemoryItem(item memoryItem) (preparedMemoryItem, error) {
	if strings.TrimSpace(item.Text) == "" {
		return preparedMemoryItem{}, errors.New("put memory item text is required")
	}
	itemActor := p.itemActor(item)
	if itemActor.SessionID == "" {
		return preparedMemoryItem{}, errors.New("put memory item session ID is required")
	}
	when := item.CreatedAt
	if when.IsZero() {
		return preparedMemoryItem{}, errors.New("put memory item created_at is required")
	}
	eventID := strings.TrimSpace(item.ID)
	if eventID == "" {
		eventID = stableID(p.agentID, itemActor.SessionID, item.Text, when.Format(time.RFC3339Nano))
	}
	sequence, err := eventSequence(item.Metadata, when)
	if err != nil {
		return preparedMemoryItem{}, err
	}
	event := sessionEventPayload{
		ID: eventID, Actor: itemActor, Sequence: sequence, Type: "assistant", Content: item.Text,
		TaskRef: item.Metadata["task_ref"], ThreadRef: item.Metadata["thread_ref"],
		Visibility: "team_note_eligible", OccurredAt: when.UTC().Format(time.RFC3339Nano), Metadata: item.Metadata,
	}
	return preparedMemoryItem{event: event, ref: map[string]string{"provider": "team-memory", "id": eventID}}, nil
}

func (p *Provider) sendSessionBatch(ctx context.Context, payload sessionBatchPayload) error {
	request, err := p.newRequest(ctx, http.MethodPost, "/v1/session-batches", payload)
	if err != nil {
		return err
	}
	if err := p.do(request, nil); err != nil {
		return err
	}
	return nil
}

func (p *Provider) itemActor(item memoryItem) actor {
	sessionID := strings.TrimSpace(item.Origin.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(item.Metadata["session_id"])
	}
	resolved := p.actor(item.Metadata, sessionID)
	if strings.TrimSpace(item.Origin.UserID) != "" {
		resolved.UserID = strings.TrimSpace(item.Origin.UserID)
	}
	if strings.TrimSpace(item.Origin.AgentID) != "" {
		resolved.AgentID = strings.TrimSpace(item.Origin.AgentID)
	}
	return resolved
}

func (p *Provider) search(ctx context.Context, query searchQuery) ([]map[string]any, error) {
	sessionID := strings.TrimSpace(query.Metadata["session_id"])
	if sessionID == "" {
		return nil, errors.New("search metadata.session_id is required")
	}
	budget := p.tokenBudget
	if query.Limit > 0 && query.Limit*100 < budget {
		budget = query.Limit * 100
	}
	payload := map[string]any{
		"actor":    p.actor(query.Metadata, sessionID),
		"task_ref": query.Metadata["task_ref"], "thread_ref": query.Metadata["thread_ref"],
		"token_budget": budget, "query": query.Text, "max_items": query.Limit,
	}
	request, err := p.newRequest(ctx, http.MethodPost, "/v1/notes/recall", payload)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Revision string                `json:"revision"`
		Items    []string              `json:"items"`
		Details  []recalledNotePayload `json:"details"`
	}
	if err := p.do(request, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Details) > 0 {
		return attributedHits(envelope.Details), nil
	}
	return legacyHits(envelope.Revision, envelope.Items), nil
}

type recalledNotePayload struct {
	NoteID    string  `json:"note_id"`
	Revision  int     `json:"revision"`
	Text      string  `json:"text"`
	Origin    actor   `json:"origin"`
	Relevance float64 `json:"relevance"`
	Certainty string  `json:"certainty"`
}

func attributedHits(notes []recalledNotePayload) []map[string]any {
	hits := make([]map[string]any, 0, len(notes))
	for _, note := range notes {
		revision := fmt.Sprintf("%s:%d", note.NoteID, note.Revision)
		hits = append(hits, map[string]any{
			"provider": "team-memory", "id": revision, "text": note.Text,
			"relevance": note.Relevance, "score": note.Relevance, "source": "team-note", "origin": note.Origin,
			"metadata": map[string]string{"revision": revision, "certainty": note.Certainty},
		})
	}
	return hits
}

func legacyHits(revision string, items []string) []map[string]any {
	hits := make([]map[string]any, 0, len(items))
	for index, item := range items {
		hits = append(hits, map[string]any{
			"provider": "team-memory", "id": fmt.Sprintf("%s:%d", revision, index),
			"text": item, "relevance": 1.0, "score": 1.0, "source": "team-note",
			"metadata": map[string]string{"revision": revision},
		})
	}
	return hits
}

func (p *Provider) actor(metadata map[string]string, sessionID string) actor {
	userID := strings.TrimSpace(metadata["user_id"])
	if userID == "" {
		userID = p.userID
	}
	agentID := strings.TrimSpace(metadata["agent_id"])
	if agentID == "" {
		agentID = p.agentID
	}
	return actor{UserID: userID, AgentID: agentID, SessionID: sessionID}
}

func (p *Provider) newRequest(ctx context.Context, method, path string, payload any) (*http.Request, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode Team Memory request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	target := p.baseURL.ResolveReference(&url.URL{Path: path})
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create Team Memory request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	request.Header.Set("Content-Type", "application/json")
	return request, nil
}

func (p *Provider) do(request *http.Request, target any) (returnedErr error) {
	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("call Team Memory: %w", err)
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("close Team Memory response: %w", closeErr))
		}
	}()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		if readErr != nil {
			return fmt.Errorf("read Team Memory error: %w", readErr)
		}
		return fmt.Errorf("team memory returned %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	if target != nil {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			return fmt.Errorf("decode Team Memory response: %w", err)
		}
	}
	return nil
}

func decodeParams(params json.RawMessage, target any) error {
	if len(params) == 0 {
		return errors.New("JSON-RPC params are required")
	}
	if err := json.Unmarshal(params, target); err != nil {
		return fmt.Errorf("decode JSON-RPC params: %w", err)
	}
	return nil
}

func eventSequence(metadata map[string]string, occurredAt time.Time) (int64, error) {
	if value := strings.TrimSpace(metadata["sequence"]); value != "" {
		sequence, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("put memory item metadata.sequence: %w", err)
		}
		if sequence <= 0 {
			return 0, fmt.Errorf("put memory item metadata.sequence must be a positive integer")
		}
		return sequence, nil
	}
	return occurredAt.UTC().UnixNano(), nil
}

func stableID(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "paxm-" + hex.EncodeToString(digest[:16])
}
