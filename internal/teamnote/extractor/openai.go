package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

var ErrInvalidModelResponse = errors.New("invalid extractor model response")

const maxCandidatesPerSlice = 10

type OpenAIConfig struct {
	BaseURL       string
	APIKey        string
	Model         string
	PromptVersion string
	Client        *http.Client
}

type OpenAI struct {
	config OpenAIConfig
}

func NewOpenAI(config OpenAIConfig) (*OpenAI, error) {
	if strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("create OpenAI extractor: base URL and model are required")
	}
	baseURL, err := url.Parse(config.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("create OpenAI extractor base URL: %w", err)
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("create OpenAI extractor: valid base URL is required")
	}
	if config.Client == nil {
		config.Client = http.DefaultClient
	}
	return &OpenAI{config: config}, nil
}

func (e *OpenAI) Extract(ctx context.Context, slice sessionlake.Slice) (Result, error) {
	prompt, err := buildPrompt(slice, e.config.PromptVersion)
	if err != nil {
		return Result{}, err
	}
	body, err := json.Marshal(chatRequest{
		Model: e.config.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
		Temperature:    0,
		ResponseFormat: responseFormat{Type: "json_object"},
	})
	if err != nil {
		return Result{}, fmt.Errorf("encode extractor request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(e.config.BaseURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("create extractor request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if e.config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+e.config.APIKey)
	}

	response, err := e.config.Client.Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("call extractor model: %w", err)
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	closeErr := response.Body.Close()
	if err != nil {
		return Result{}, fmt.Errorf("read extractor response: %w", err)
	}
	if closeErr != nil {
		return Result{}, fmt.Errorf("close extractor response: %w", closeErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Result{}, fmt.Errorf("extractor response status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	result, err := decodeResponse(responseBody)
	if err != nil {
		return Result{}, err
	}
	if err := normalizeCandidates(&result, slice); err != nil {
		return Result{}, err
	}
	result.Model = e.config.Model
	result.PromptVersion = e.config.PromptVersion
	return result, nil
}

func buildPrompt(slice sessionlake.Slice, promptVersion string) (string, error) {
	input := struct {
		PromptVersion   string                  `json:"prompt_version"`
		Actor           teamnote.Actor          `json:"actor"`
		FromSequence    int64                   `json:"from_sequence"`
		ToSequence      int64                   `json:"to_sequence"`
		NewEventIDs     []string                `json:"new_event_ids"`
		OverlapEventIDs []string                `json:"overlap_event_ids,omitempty"`
		Events          []teamnote.SessionEvent `json:"events"`
	}{promptVersion, slice.Actor, slice.FromSequence, slice.ToSequence, slice.NewEventIDs, slice.OverlapEventIDs, slice.Events}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode extractor prompt: %w", err)
	}
	return string(encoded), nil
}

func decodeResponse(body []byte) (Result, error) {
	var response chatResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return Result{}, fmt.Errorf("decode extractor response: %w", errors.Join(ErrInvalidModelResponse, err))
	}
	if len(response.Choices) == 0 {
		return Result{}, fmt.Errorf("extractor response has no choices: %w", ErrInvalidModelResponse)
	}
	content := trimCodeFence(response.Choices[0].Message.Content)
	var output candidateOutput
	if err := json.Unmarshal([]byte(content), &output); err != nil {
		return Result{}, fmt.Errorf("decode extractor candidates: %w", errors.Join(ErrInvalidModelResponse, err))
	}
	return Result{
		Candidates: output.Candidates,
		Usage:      Usage{InputTokens: response.Usage.PromptTokens, OutputTokens: response.Usage.CompletionTokens},
	}, nil
}

func normalizeCandidates(result *Result, slice sessionlake.Slice) error {
	if len(result.Candidates) > maxCandidatesPerSlice {
		return fmt.Errorf("extractor returned %d candidates, maximum is %d: %w", len(result.Candidates), maxCandidatesPerSlice, ErrInvalidModelResponse)
	}
	checksum := slice.InputChecksum
	if len(checksum) > 16 {
		checksum = checksum[:16]
	}
	if checksum == "" {
		return fmt.Errorf("normalize extractor candidates: slice checksum is required")
	}
	allEvents := stringSet(eventIDs(slice.Events))
	newEvents := stringSet(slice.NewEventIDs)
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		if strings.TrimSpace(candidate.Subject) == "" || len(candidate.EvidenceEventIDs) == 0 {
			return fmt.Errorf("extractor candidate %d is missing subject or evidence: %w", index, ErrInvalidModelResponse)
		}
		if candidate.Action != teamnote.ActionResolve && strings.TrimSpace(candidate.Body) == "" {
			return fmt.Errorf("extractor candidate %d is missing body: %w", index, ErrInvalidModelResponse)
		}
		if err := validateCandidateEvidence(index, candidate.EvidenceEventIDs, allEvents, newEvents); err != nil {
			return err
		}
		taskRef, threadRef, err := evidenceScope(candidate.EvidenceEventIDs, slice.Events)
		if err != nil {
			return fmt.Errorf("extractor candidate %d scope: %w", index, err)
		}
		if (candidate.TaskRef != "" && candidate.TaskRef != taskRef) ||
			(candidate.ThreadRef != "" && candidate.ThreadRef != threadRef) {
			return fmt.Errorf("extractor candidate %d scope differs from evidence: %w", index, ErrInvalidModelResponse)
		}
		candidate.ID = "extract-" + checksum + "-" + strconv.Itoa(index+1)
		candidate.Origin = slice.Actor
		candidate.TaskRef = taskRef
		candidate.ThreadRef = threadRef
		// Audience is an authorization boundary owned by the server, not a
		// classification decision delegated to the extraction model.
		candidate.AudienceAgentIDs = nil
		candidate.SourceOccurredAt = latestEvidenceTime(candidate.EvidenceEventIDs, slice.Events)
	}
	return nil
}

func evidenceScope(evidenceIDs []string, events []teamnote.SessionEvent) (string, string, error) {
	evidence := stringSet(evidenceIDs)
	var taskRef, threadRef string
	found := false
	for _, event := range events {
		if _, ok := evidence[event.ID]; !ok {
			continue
		}
		if !found {
			taskRef, threadRef, found = event.TaskRef, event.ThreadRef, true
			continue
		}
		if event.TaskRef != taskRef || event.ThreadRef != threadRef {
			return "", "", ErrInvalidModelResponse
		}
	}
	return taskRef, threadRef, nil
}

func latestEvidenceTime(evidenceIDs []string, events []teamnote.SessionEvent) time.Time {
	evidence := stringSet(evidenceIDs)
	var latest time.Time
	for _, event := range events {
		if _, ok := evidence[event.ID]; ok && event.OccurredAt.After(latest) {
			latest = event.OccurredAt.UTC()
		}
	}
	return latest
}

func validateCandidateEvidence(index int, evidence []string, allEvents, newEvents map[string]struct{}) error {
	groundedInNewEvent := false
	for _, eventID := range evidence {
		if _, ok := allEvents[eventID]; !ok {
			return fmt.Errorf("extractor candidate %d cites unknown event %q: %w", index, eventID, ErrInvalidModelResponse)
		}
		if _, ok := newEvents[eventID]; ok {
			groundedInNewEvent = true
		}
	}
	if !groundedInNewEvent {
		return fmt.Errorf("extractor candidate %d cites only overlap events: %w", index, ErrInvalidModelResponse)
	}
	return nil
}

func eventIDs(events []teamnote.SessionEvent) []string {
	ids := make([]string, len(events))
	for index, event := range events {
		ids[index] = event.ID
	}
	return ids
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func trimCodeFence(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[len(lines)-1]) != "```" {
		return trimmed
	}
	return strings.Join(lines[1:len(lines)-1], "\n")
}

const systemPrompt = `You extract short-lived collaboration notes from session events.
Return exactly one JSON object in this shape:
{"candidates":[{"action":"create","kind":"status","subject":"stable short subject","body":"concise factual note","task_ref":"","thread_ref":"","valid_at":null,"invalid_at":null,"related_subjects":[],"audience_agent_ids":[],"evidence_event_ids":["event-id"]}]}
Allowed actions are create, update, resolve. Allowed kinds are status, blocker,
handoff, artifact_reference. Use exactly the field names shown above. Never use
description, citations, evidence, or other aliases. Preserve exact identifiers,
codes, paths, decisions, blockers, and handoff targets in body. Every candidate
must cite one or more input event IDs in evidence_event_ids. Do not infer identity,
authorization, approval, membership, or facts not stated in the events. Return an
empty candidates array when no grounded collaboration note is present. Return at
most 10 candidates. Every candidate must cite at least one ID from new_event_ids;
overlap_event_ids may only provide context and cannot be the sole evidence.
Always return audience_agent_ids as an empty array; the server owns audience and
authorization. If more than 10 grounded facts are available, prioritize explicit
decisions, changes, owners, exact values, deadlines, dependencies, and blockers
over routine progress updates or conversational acknowledgements.
valid_at and invalid_at are optional RFC3339 timestamps describing when the fact
became true and stopped being true in the source domain. Do not use ingestion time
as valid_at. Use the same stable subject and action update when a later event
changes an existing fact. Preserve dates stated in the source in body even when a
validity timestamp is present. Never collapse multiple dated decisions or actions
into one broad handoff note. Emit a separate candidate for each explicit deadline,
review date, expected completion date, or schedule change. The subject identifies
the stable obligation without embedding the date; the body preserves the owner,
action, and exact date. invalid_at is only for a fact explicitly stated to have
stopped being true. related_subjects contains exact subject strings of other
candidates in the same response that this fact depends on or qualifies. When an
action happens after another action, emit both candidates and link the dependent
action to the prerequisite subject. Keep the prerequisite deadline in its body.`

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	Temperature    float64        `json:"temperature"`
	ResponseFormat responseFormat `json:"response_format"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type candidateOutput struct {
	Candidates []teamnote.Candidate `json:"candidates"`
}
