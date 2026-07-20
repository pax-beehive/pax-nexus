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
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

var ErrInvalidModelResponse = errors.New("invalid extractor model response")

const maxCandidatesPerSlice = 10

type OpenAIConfig struct {
	BaseURL              string
	APIKey               string
	Model                string
	PromptVersion        string
	Client               *http.Client
	ContextMode          ContextMode
	ExtractionVersion    string
	V2Variant            string
	EpisodeStore         EpisodeStore
	CompactionEnabled    bool
	CompactStartTokens   int
	CompactTokens        int
	SummaryEnabled       bool
	SummaryTriggerTokens int
	SummaryTailTokens    int
	ProviderCallObserver ProviderCallObserver
	ExecutionPolicy      ExecutionPolicy
}

type OpenAI struct {
	config            OpenAIConfig
	candidateStrategy candidateStrategy
	lifecycle         lifecycleCoordinator
	locksMu           sync.Mutex
	locks             map[EpisodeKey]*episodeLease
	flightsMu         sync.Mutex
	flights           map[EpisodeKey]*compactionFlight
	summariesMu       sync.Mutex
	summaries         map[EpisodeKey]*summaryFlight
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
	if err := normalizeOpenAIConfig(&config); err != nil {
		return nil, err
	}
	strategy, err := resolveCandidateStrategy(config.V2Variant)
	if err != nil {
		return nil, fmt.Errorf("create OpenAI extractor: %w", err)
	}
	return &OpenAI{
		config: config, candidateStrategy: strategy, lifecycle: newLifecycleCoordinator(),
		locks:     make(map[EpisodeKey]*episodeLease),
		flights:   make(map[EpisodeKey]*compactionFlight),
		summaries: make(map[EpisodeKey]*summaryFlight),
	}, nil
}

func normalizeOpenAIConfig(config *OpenAIConfig) error {
	if config.Client == nil {
		config.Client = http.DefaultClient
	}
	if config.ContextMode == "" {
		config.ContextMode = ContextModeSlice
	}
	if config.ContextMode != ContextModeSlice && config.ContextMode != ContextModeRolling {
		return fmt.Errorf("create OpenAI extractor: unsupported context mode %q", config.ContextMode)
	}
	if config.ContextMode == ContextModeRolling && config.EpisodeStore == nil {
		return fmt.Errorf("create OpenAI extractor: episode store is required in rolling mode")
	}
	if err := normalizeExtractionVersion(config); err != nil {
		return err
	}
	if config.CompactTokens == 0 {
		config.CompactTokens = 16 * 1024
	}
	if config.CompactStartTokens == 0 {
		config.CompactStartTokens = 12 * 1024
	}
	if config.CompactStartTokens < 1 || config.CompactTokens < config.CompactStartTokens {
		return fmt.Errorf("create OpenAI extractor: compact thresholds must be positive and ordered")
	}
	if config.SummaryTriggerTokens == 0 {
		config.SummaryTriggerTokens = 8 * 1024
	}
	if config.SummaryTailTokens == 0 {
		config.SummaryTailTokens = 16 * 1024
	}
	if config.SummaryTriggerTokens < 1 || config.SummaryTailTokens < 1 {
		return fmt.Errorf("create OpenAI extractor: summary token thresholds must be positive")
	}
	if config.CompactionEnabled && config.SummaryEnabled {
		return fmt.Errorf("create OpenAI extractor: compaction and periodic summary cannot both be enabled")
	}
	if err := normalizeExecutionPolicy(&config.ExecutionPolicy); err != nil {
		return fmt.Errorf("create OpenAI extractor: %w", err)
	}
	return nil
}

func normalizeExtractionVersion(config *OpenAIConfig) error {
	if config.ExtractionVersion == "" {
		config.ExtractionVersion = ExtractionVersionV1
	}
	if config.ExtractionVersion != ExtractionVersionV1 && config.ExtractionVersion != ExtractionVersionV2 {
		return fmt.Errorf("create OpenAI extractor: unsupported extraction version %q", config.ExtractionVersion)
	}
	if config.ExtractionVersion == ExtractionVersionV2 && config.ContextMode != ContextModeRolling {
		return fmt.Errorf("create OpenAI extractor: extraction v2 requires rolling context mode")
	}
	strategy, err := resolveCandidateStrategy(config.V2Variant)
	if err != nil {
		return fmt.Errorf("create OpenAI extractor: %w", err)
	}
	config.V2Variant = strategy.name
	return nil
}

func (e *OpenAI) Extract(ctx context.Context, slice sessionlake.Slice) (Result, error) {
	if err := e.lifecycle.beginForeground(); err != nil {
		return Result{}, fmt.Errorf("extract with OpenAI: %w", err)
	}
	defer e.lifecycle.finishForeground()
	if e.config.ContextMode == ContextModeRolling {
		if e.config.ExtractionVersion == ExtractionVersionV2 {
			return e.extractRollingV2(ctx, slice)
		}
		return e.extractRolling(ctx, slice)
	}
	return e.extractSlice(ctx, slice)
}

// WaitForBackground waits until active extractions and their compaction or
// periodic-summary calls are drained. Including active extractions prevents a
// wait from missing background work that is started concurrently. Evaluation
// harnesses use it before closing their call journal so usage is not lost.
func (e *OpenAI) WaitForBackground(ctx context.Context) error {
	return e.lifecycle.wait(ctx, false)
}

// Close prevents new extractions and waits for in-progress extractions and
// their background work to finish. A canceled close still leaves the extractor
// closed; callers may invoke Close again with a fresh context to finish waiting.
func (e *OpenAI) Close(ctx context.Context) error {
	return e.lifecycle.wait(ctx, true)
}

// LifecycleStatus reports current coordinator activity. ActiveEpisodes counts
// only Episode locks that are held or have waiters; idle keys are reclaimed.
func (e *OpenAI) LifecycleStatus() LifecycleStatus {
	status := e.lifecycle.status()
	e.locksMu.Lock()
	status.ActiveEpisodes = len(e.locks)
	e.locksMu.Unlock()
	return status
}

// systemPrompt returns the stable extraction system prompt for the configured
// protocol, shared by extraction, compaction, and summary calls.
func (e *OpenAI) systemPrompt() string {
	if e.config.ExtractionVersion == ExtractionVersionV2 {
		return e.candidateStrategy.protocol.systemPrompt
	}
	return rollingSystemPrompt
}

func (e *OpenAI) extractSlice(ctx context.Context, slice sessionlake.Slice) (Result, error) {
	prompt, err := buildPrompt(slice, e.config.PromptVersion)
	if err != nil {
		return Result{}, err
	}
	result, _, err := e.complete(ctx, []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return Result{}, err
	}
	if err := normalizeCandidates(&result, slice, e.candidateStrategy.candidateLimit); err != nil {
		return Result{}, err
	}
	result.Model = e.config.Model
	result.PromptVersion = e.config.PromptVersion
	result.ExtractionVersion = e.config.ExtractionVersion
	return result, nil
}

func (e *OpenAI) complete(ctx context.Context, messages []chatMessage) (Result, string, error) {
	return e.completeWith(ctx, messages, decodeResponse)
}

// completeWith decodes one chat response with the protocol-specific decoder
// so v1 and v2 share the transport path.
func (e *OpenAI) completeWith(ctx context.Context, messages []chatMessage, decode func([]byte) (Result, string, error)) (Result, string, error) {
	var result Result
	var content string
	_, err := e.executeProvider(ctx, messages, 0, ProviderCallPrimary, func(responseBody []byte) error {
		decoded, raw, decodeErr := decode(responseBody)
		if decodeErr != nil {
			return decodeErr
		}
		result, content = decoded, raw
		return nil
	})
	return result, content, err
}

func (e *OpenAI) providerRequest(
	ctx context.Context,
	messages []chatMessage,
	maxTokens int,
) ([]byte, int, error) {
	body, err := json.Marshal(chatRequest{
		Model: e.config.Model, Messages: messages, Temperature: 0,
		ResponseFormat: responseFormat{Type: "json_object"},
		MaxTokens:      maxTokens,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("encode extractor request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(e.config.BaseURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("create extractor request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if e.config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+e.config.APIKey)
	}
	response, err := e.config.Client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("call extractor model: %w", err)
	}
	limit := e.config.ExecutionPolicy.MaxResponseBytes
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	closeErr := response.Body.Close()
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("read extractor response: %w", err)
	}
	if closeErr != nil {
		return nil, response.StatusCode, fmt.Errorf("close extractor response: %w", closeErr)
	}
	if int64(len(responseBody)) > limit {
		return nil, response.StatusCode, fmt.Errorf("extractor response exceeds %d bytes: %w", limit, ErrProviderResponseTooLarge)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, response.StatusCode, &providerStatusError{status: response.StatusCode, body: strings.TrimSpace(string(responseBody))}
	}
	return responseBody, response.StatusCode, nil
}

func providerUsage(body []byte) Usage {
	var response chatResponse
	if len(body) == 0 || json.Unmarshal(body, &response) != nil {
		return Usage{}
	}
	return Usage{
		InputTokens: response.Usage.PromptTokens, OutputTokens: response.Usage.CompletionTokens,
		PromptCacheHitTokens:  response.Usage.PromptCacheHitTokens,
		PromptCacheMissTokens: response.Usage.PromptCacheMissTokens,
	}
}

func buildPrompt(slice sessionlake.Slice, promptVersion string) (string, error) {
	input := struct {
		PromptVersion   string                  `json:"prompt_version"`
		InputChecksum   string                  `json:"input_checksum"`
		Actor           teamnote.Actor          `json:"actor"`
		FromSequence    int64                   `json:"from_sequence"`
		ToSequence      int64                   `json:"to_sequence"`
		NewEventIDs     []string                `json:"new_event_ids"`
		OverlapEventIDs []string                `json:"overlap_event_ids,omitempty"`
		Events          []teamnote.SessionEvent `json:"events"`
	}{promptVersion, slice.InputChecksum, slice.Actor, slice.FromSequence, slice.ToSequence, slice.NewEventIDs, slice.OverlapEventIDs, slice.Events}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode extractor prompt: %w", err)
	}
	return string(encoded), nil
}

func decodeResponse(body []byte) (Result, string, error) {
	var response chatResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return Result{}, "", fmt.Errorf("decode extractor response: %w", errors.Join(ErrInvalidModelResponse, err))
	}
	if len(response.Choices) == 0 {
		return Result{}, "", fmt.Errorf("extractor response has no choices: %w", ErrInvalidModelResponse)
	}
	content := trimCodeFence(response.Choices[0].Message.Content)
	var output candidateOutput
	if err := json.Unmarshal([]byte(content), &output); err != nil {
		return Result{}, "", fmt.Errorf("decode extractor candidates: %w", errors.Join(ErrInvalidModelResponse, err))
	}
	return Result{
		Candidates: output.Candidates,
		Usage: Usage{
			InputTokens: response.Usage.PromptTokens, OutputTokens: response.Usage.CompletionTokens,
			PromptCacheHitTokens:  response.Usage.PromptCacheHitTokens,
			PromptCacheMissTokens: response.Usage.PromptCacheMissTokens,
		},
	}, content, nil
}

func normalizeCandidates(result *Result, slice sessionlake.Slice, candidateLimit int) error {
	checksum := slice.InputChecksum
	if len(checksum) > 16 {
		checksum = checksum[:16]
	}
	if checksum == "" {
		return fmt.Errorf("normalize extractor candidates: slice checksum is required")
	}
	allEvents := stringSet(eventIDs(slice.Events))
	newEvents := stringSet(slice.NewEventIDs)
	kept := make([]teamnote.Candidate, 0, len(result.Candidates))
	for index := range result.Candidates {
		candidate := result.Candidates[index]
		candidate.ID = "extract-" + checksum + "-" + strconv.Itoa(index+1)
		candidate.Origin = slice.Actor
		candidate.IdentityRef = strings.TrimSpace(candidate.IdentityRef)
		// Audience is an authorization boundary owned by the server, not a
		// classification decision delegated to the extraction model.
		candidate.AudienceAgentIDs = nil
		if reason := candidateRejectionReason(index, candidate, allEvents, newEvents); reason != "" {
			result.Rejections = append(result.Rejections, teamnote.CandidateRejection{Candidate: candidate, Reason: reason})
			continue
		}
		if evidenceIsOnlyNonCommittalProposal(candidate.EvidenceEventIDs, slice.Events) {
			result.Rejections = append(result.Rejections, teamnote.CandidateRejection{
				Candidate: candidate, Reason: "extractor candidate is grounded only in a non-committal proposal or request",
			})
			continue
		}
		taskRef, threadRef, err := evidenceScope(candidate.EvidenceEventIDs, slice.Events)
		if err != nil {
			result.Rejections = append(result.Rejections, teamnote.CandidateRejection{
				Candidate: candidate, Reason: fmt.Sprintf("extractor candidate %d scope spans multiple tasks or threads", index),
			})
			continue
		}
		if (candidate.TaskRef != "" && candidate.TaskRef != taskRef) ||
			(candidate.ThreadRef != "" && candidate.ThreadRef != threadRef) {
			result.Rejections = append(result.Rejections, teamnote.CandidateRejection{
				Candidate: candidate, Reason: fmt.Sprintf("extractor candidate %d scope differs from evidence", index),
			})
			continue
		}
		candidate.TaskRef = taskRef
		candidate.ThreadRef = threadRef
		candidate.SourceOccurredAt = latestEvidenceTime(candidate.EvidenceEventIDs, slice.Events)
		kept = append(kept, candidate)
	}
	if candidateLimit == 0 {
		candidateLimit = maxCandidatesPerSlice
	}
	if candidateLimit > 0 && len(kept) > candidateLimit {
		for _, overflow := range kept[candidateLimit:] {
			result.Rejections = append(result.Rejections, teamnote.CandidateRejection{
				Candidate: overflow, Reason: "extractor candidate overflow beyond the per-slice maximum",
			})
		}
		kept = kept[:candidateLimit]
	}
	result.Candidates = kept
	return nil
}

func evidenceIsOnlyNonCommittalProposal(evidenceIDs []string, events []teamnote.SessionEvent) bool {
	eventsByID := make(map[string]teamnote.SessionEvent, len(events))
	for _, event := range events {
		eventsByID[event.ID] = event
	}
	for _, eventID := range evidenceIDs {
		event, exists := eventsByID[eventID]
		if !exists || !isNonCommittalProposal(event.Content) {
			return false
		}
	}
	return len(evidenceIDs) > 0
}

func isNonCommittalProposal(content string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(content), " "))
	for _, marker := range []string{
		"approved", "agreed", "assigned", "designated", "committed", "confirmed", "decided", "accepted",
	} {
		if strings.Contains(normalized, marker) {
			return false
		}
	}
	for _, prefix := range []string{
		"i propose ", "we propose ", "i suggest ", "we suggest ", "i recommend ", "we recommend ",
		"my proposal ", "our proposal ", "please assign ", "can you assign ", "would you assign ",
	} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func evidenceIsOnlyNonCommittalSourceLanguage(
	evidenceIDs []string,
	events []teamnote.SessionEvent,
	candidate *DecisionCandidate,
) bool {
	eventsByID := make(map[string]teamnote.SessionEvent, len(events))
	for _, event := range events {
		eventsByID[event.ID] = event
	}
	for _, eventID := range evidenceIDs {
		event, exists := eventsByID[eventID]
		if !exists || !isNonCommittalSourceLanguage(event.Content, candidate) {
			return false
		}
	}
	return len(evidenceIDs) > 0
}

func isNonCommittalSourceLanguage(content string, candidate *DecisionCandidate) bool {
	if isNonCommittalProposal(content) {
		return true
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(content), " "))
	padded := " " + normalized + " "
	for _, marker := range []string{
		" ask ", " asks ", " asking ", " should ", " please ",
		" i'd have ", " i would have ", " let's ",
	} {
		index := strings.Index(padded, marker)
		if index >= 0 {
			return !committedClauseSupportsCandidate(padded[:index], candidate)
		}
	}
	if index := strings.Index(padded, " while "); strings.Contains(padded, " can ") && index >= 0 {
		return !committedClauseSupportsCandidate(padded[:index], candidate)
	}
	return false
}

func committedClauseSupportsCandidate(content string, candidate *DecisionCandidate) bool {
	if candidate == nil {
		return false
	}
	candidateTokens := significantTokens(candidate.Subject + " " + candidate.Body)
	clauses := strings.FieldsFunc(content, func(character rune) bool {
		return character == '.' || character == ';' || character == '!' || character == '?' || character == '\n'
	})
	for _, clause := range clauses {
		paddedClause := " " + strings.TrimSpace(clause) + " "
		if !containsCommittedPredicate(paddedClause) {
			continue
		}
		clauseTokens := significantTokens(paddedClause)
		matches := 0
		for token := range candidateTokens {
			if _, exists := clauseTokens[token]; exists {
				matches++
			}
		}
		if matches >= 2 {
			return true
		}
	}
	return false
}

func containsCommittedPredicate(content string) bool {
	for _, marker := range []string{
		" is ", " are ", " owns ", " has ", " have ", " completed ", " finished ",
		" approved ", " assigned ", " designated ", " decided ", " confirmed ",
	} {
		if strings.Contains(content, marker) {
			return true
		}
	}
	return false
}

func significantTokens(content string) map[string]struct{} {
	stopWords := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "are": {}, "for": {}, "has": {}, "have": {}, "is": {},
		"of": {}, "should": {}, "the": {}, "to": {}, "today": {}, "was": {}, "were": {},
	}
	tokens := make(map[string]struct{})
	for _, field := range strings.FieldsFunc(strings.ToLower(content), func(character rune) bool {
		return character < 'a' || character > 'z'
	}) {
		if len(field) < 3 {
			continue
		}
		if _, stop := stopWords[field]; !stop {
			tokens[field] = struct{}{}
		}
	}
	return tokens
}

// candidateRejectionReason returns the deterministic reason one candidate can
// never pass admission, or "" when it is admissible at this stage. A saved
// extraction response is replayed verbatim, so a malformed candidate would
// otherwise poison every retry of the same slice.
func candidateRejectionReason(index int, candidate teamnote.Candidate, allEvents, newEvents map[string]struct{}) string {
	if strings.TrimSpace(candidate.Subject) == "" {
		return fmt.Sprintf("extractor candidate %d is missing subject", index)
	}
	if len(candidate.EvidenceEventIDs) == 0 {
		return fmt.Sprintf("extractor candidate %d is missing evidence", index)
	}
	if candidate.Action != teamnote.ActionResolve && strings.TrimSpace(candidate.Body) == "" {
		return fmt.Sprintf("extractor candidate %d is missing body", index)
	}
	if err := validateCandidateEvidence(index, candidate.EvidenceEventIDs, allEvents, newEvents); err != nil {
		return err.Error()
	}
	return ""
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
{"candidates":[{"action":"create","kind":"status","subject":"stable short subject","identity_ref":"","body":"concise factual note","task_ref":"","thread_ref":"","valid_at":null,"invalid_at":null,"related_subjects":[],"audience_agent_ids":[],"evidence_event_ids":["event-id"]}]}
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
For blocker and artifact_reference candidates, set identity_ref to an exact stable
external identifier when one is present, such as a ticket ID, artifact path, or
artifact ID. Otherwise leave identity_ref empty and keep subject stable across updates.
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
	MaxTokens      int            `json:"max_tokens,omitempty"`
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
		PromptTokens          int `json:"prompt_tokens"`
		CompletionTokens      int `json:"completion_tokens"`
		PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	} `json:"usage"`
}

type candidateOutput struct {
	Candidates []teamnote.Candidate `json:"candidates"`
}
