package extractor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

type eventScope struct {
	taskRef   string
	threadRef string
}

var protocolV1 = extractionProtocol{rollingSystemPrompt, decodeResponse, decodeCandidateContent}

func (e *OpenAI) extractRolling(ctx context.Context, slice sessionlake.Slice) (Result, error) {
	return e.extractRollingWith(ctx, slice, protocolV1, nil)
}

func (e *OpenAI) extractRollingV2(ctx context.Context, slice sessionlake.Slice) (Result, error) {
	return e.extractRollingV2With(ctx, slice)
}

// extractRollingV2With maps validated v2 products onto candidates after each
// episode group, keeping the trace merged across groups.
func (e *OpenAI) extractRollingV2With(ctx context.Context, slice sessionlake.Slice) (Result, error) {
	return e.extractRollingWith(ctx, slice, e.candidateStrategy.protocol, e.candidateStrategy.mapResult)
}

func (e *OpenAI) extractRollingWith(ctx context.Context, slice sessionlake.Slice, protocol extractionProtocol, mapResult func(*Result, sessionlake.Slice)) (Result, error) {
	scopeID, err := teamnote.ScopeFromContext(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("extract rolling context: %w", err)
	}
	groups := groupSlice(slice)
	result := Result{
		Model: e.config.Model, PromptVersion: e.config.PromptVersion,
		ExtractionVersion: e.resultExtractionVersion(),
	}
	for _, group := range groups {
		key := EpisodeKey{ScopeID: scopeID, TaskRef: group.scope.taskRef, ThreadRef: group.scope.threadRef}
		groupResult, err := e.advanceEpisode(ctx, key, group.slice, protocol)
		if err != nil {
			return Result{}, err
		}
		if mapResult != nil {
			mapResult(&groupResult, group.slice)
		}
		result.Candidates = append(result.Candidates, groupResult.Candidates...)
		result.SourceSpans = append(result.SourceSpans, groupResult.SourceSpans...)
		mergeTraceV2(&result, groupResult.Trace)
		addUsage(&result.Usage, groupResult.Usage)
	}
	if err := normalizeCandidates(&result, slice, e.candidateStrategy.candidateLimit); err != nil {
		return Result{}, err
	}
	return result, nil
}

// mergeTraceV2 folds one episode group's v2 trace into the slice result.
func mergeTraceV2(result *Result, trace *TraceV2) {
	if trace == nil {
		return
	}
	if result.Trace == nil {
		result.Trace = &TraceV2{}
	}
	merged := result.Trace
	merged.Claims = append(merged.Claims, trace.Claims...)
	merged.StateDecisions = append(merged.StateDecisions, trace.StateDecisions...)
	merged.InteractionObservations = append(merged.InteractionObservations, trace.InteractionObservations...)
	merged.ClaimRejections = append(merged.ClaimRejections, trace.ClaimRejections...)
	merged.DecisionRejections = append(merged.DecisionRejections, trace.DecisionRejections...)
	merged.InteractionRejections = append(merged.InteractionRejections, trace.InteractionRejections...)
	merged.NoStateEventIDs = append(merged.NoStateEventIDs, trace.NoStateEventIDs...)
	merged.UnreviewedEventIDs = append(merged.UnreviewedEventIDs, trace.UnreviewedEventIDs...)
	merged.InvalidNoStateEventIDs = append(merged.InvalidNoStateEventIDs, trace.InvalidNoStateEventIDs...)
	merged.OrphanClaimIDs = append(merged.OrphanClaimIDs, trace.OrphanClaimIDs...)
	for _, class := range trace.WouldVerify {
		duplicate := false
		for _, existing := range merged.WouldVerify {
			duplicate = duplicate || existing == class
		}
		if !duplicate {
			merged.WouldVerify = append(merged.WouldVerify, class)
		}
	}
}

type scopedSlice struct {
	scope eventScope
	slice sessionlake.Slice
}

func groupSlice(slice sessionlake.Slice) []scopedSlice {
	newIDs := stringSet(slice.NewEventIDs)
	groups := make(map[eventScope]*scopedSlice)
	for _, event := range slice.Events {
		scope := eventScope{taskRef: event.TaskRef, threadRef: event.ThreadRef}
		group, ok := groups[scope]
		if !ok {
			group = &scopedSlice{scope: scope, slice: sessionlake.Slice{
				Actor: slice.Actor, FromSequence: slice.FromSequence, ToSequence: slice.ToSequence,
				InputChecksum: slice.InputChecksum,
			}}
			groups[scope] = group
		}
		group.slice.Events = append(group.slice.Events, event)
		if _, ok := newIDs[event.ID]; ok {
			group.slice.NewEventIDs = append(group.slice.NewEventIDs, event.ID)
		} else {
			group.slice.OverlapEventIDs = append(group.slice.OverlapEventIDs, event.ID)
		}
	}
	keys := make([]eventScope, 0, len(groups))
	for key, group := range groups {
		if len(group.slice.NewEventIDs) > 0 {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].taskRef == keys[right].taskRef {
			return keys[left].threadRef < keys[right].threadRef
		}
		return keys[left].taskRef < keys[right].taskRef
	})
	result := make([]scopedSlice, 0, len(keys))
	for _, key := range keys {
		result = append(result, *groups[key])
	}
	return result
}

func (e *OpenAI) advanceEpisode(ctx context.Context, key EpisodeKey, slice sessionlake.Slice, protocol extractionProtocol) (Result, error) {
	releaseEpisode := e.acquireEpisode(key)
	defer releaseEpisode()
	var result Result
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		result, err = e.advanceEpisodeAttempt(ctx, key, slice, protocol)
		if !errors.Is(err, ErrEpisodeConflict) {
			return result, err
		}
	}
	return Result{}, fmt.Errorf("advance rolling extraction episode after conflict retry: %w", err)
}

func (e *OpenAI) advanceEpisodeAttempt(ctx context.Context, key EpisodeKey, slice sessionlake.Slice, protocol extractionProtocol) (Result, error) {

	if err := e.consumeReadySummary(key); err != nil {
		return Result{}, fmt.Errorf("persist rolling extraction summary: %w", err)
	}
	episode, found, err := e.config.EpisodeStore.LoadEpisode(ctx, key)
	if err != nil {
		return Result{}, fmt.Errorf("load rolling extraction episode: %w", err)
	}
	expectedVersion := episode.Version
	if found && !e.episodeCompatible(episode) {
		// A rolling transcript contains protocol-specific assistant responses.
		// Replaying it under a different model, prompt, or response protocol can
		// either fail decoding or silently carry stale state forward. Preserve
		// only the optimistic store version so the replacement remains atomic.
		episode = Episode{Key: key, Version: expectedVersion}
	}
	episode.ProtocolVersion = e.episodeProtocolVersion()
	episode.Model = e.config.Model
	episode.PromptVersion = e.config.PromptVersion
	if run, ok := episode.Runs[slice.InputChecksum]; ok {
		result, err := protocol.decodeSaved(run.Response)
		if err != nil {
			return Result{}, fmt.Errorf("replay rolling extraction episode: %w", err)
		}
		removeHistoricalEvidence(&result, episode, slice)
		result.Model = e.config.Model
		result.PromptVersion = e.config.PromptVersion
		result.ExtractionVersion = e.resultExtractionVersion()
		return result, nil
	}
	prompt, err := buildPrompt(slice, e.config.PromptVersion)
	if err != nil {
		return Result{}, err
	}
	compactionUsage, err := e.prepareEpisode(ctx, key, &episode, estimateTokens(prompt))
	if err != nil {
		return Result{}, err
	}
	messages, err := episodeMessages(episode, protocol.systemPrompt)
	if err != nil {
		return Result{}, fmt.Errorf("build rolling extraction context: %w", err)
	}
	messages = append(messages, chatMessage{Role: "user", Content: prompt})
	result, raw, err := e.completeWith(ctx, messages, protocol.decodeFresh)
	if err != nil {
		return Result{}, err
	}
	removeHistoricalEvidence(&result, episode, slice)
	contextTokens := result.Usage.InputTokens + result.Usage.OutputTokens
	addUsage(&result.Usage, compactionUsage)
	episode.Messages = append(episode.Messages,
		EpisodeMessage{Role: "user", Content: prompt},
		EpisodeMessage{Role: "assistant", Content: raw},
	)
	episode.EstimatedTokens = contextTokens
	episode.EventCount += len(slice.NewEventIDs)
	episode.ProtocolVersion = e.episodeProtocolVersion()
	episode.Model = e.config.Model
	episode.PromptVersion = e.config.PromptVersion
	updateSourceCursor(&episode.Checkpoint, slice)
	if episode.Runs == nil {
		episode.Runs = make(map[string]EpisodeRun)
	}
	episode.Runs[slice.InputChecksum] = EpisodeRun{Response: raw, Ordinal: episode.EventCount}
	pruneEpisodeRuns(episode.Runs, 64)
	if err := e.config.EpisodeStore.SaveEpisode(ctx, episode, expectedVersion); err != nil {
		if errors.Is(err, ErrEpisodeConflict) {
			return Result{}, fmt.Errorf("save rolling extraction episode: %w", err)
		}
		return Result{}, fmt.Errorf("save rolling extraction episode: %w", err)
	}
	episode.Version = expectedVersion + 1
	if e.config.CompactionEnabled && episode.EstimatedTokens >= e.config.CompactStartTokens && len(episode.Messages) > 0 {
		e.startCompaction(ctx, key, episode)
	}
	if e.shouldStartSummary(episode) {
		e.startSummary(ctx, key, episode)
	}
	result.Model = e.config.Model
	result.PromptVersion = e.config.PromptVersion
	result.ExtractionVersion = e.resultExtractionVersion()
	return result, nil
}

func (e *OpenAI) resultExtractionVersion() string {
	if e.config.ExtractionVersion == ExtractionVersionV2 {
		switch e.candidateStrategy.name {
		case CandidateStrategySourceSpanV1:
			return ExtractionVersionSourceSpanV1
		case CandidateStrategySourceSpanV2:
			return ExtractionVersionSourceSpanV2
		case CandidateStrategyClaimCardV1:
			return ExtractionVersionClaimCardV1
		case CandidateStrategyClaimCardV2:
			return ExtractionVersionClaimCardV2
		}
	}
	return e.config.ExtractionVersion
}

func (e *OpenAI) episodeCompatible(episode Episode) bool {
	protocolVersion := episode.ProtocolVersion
	if protocolVersion == "" {
		// Episodes written before protocol versioning used the v1 response
		// shape. This preserves their warm prefix during the migration.
		protocolVersion = ExtractionVersionV1
	}
	expectedProtocol := ExtractionVersionV1
	if e.config.ExtractionVersion == ExtractionVersionV2 {
		expectedProtocol = e.candidateStrategy.protocolVersion
	}
	return protocolVersion == expectedProtocol &&
		episode.Model == e.config.Model && episode.PromptVersion == e.config.PromptVersion
}

func (e *OpenAI) episodeProtocolVersion() string {
	if e.config.ExtractionVersion == ExtractionVersionV2 {
		return e.candidateStrategy.protocolVersion
	}
	return ExtractionVersionV1
}

func removeHistoricalEvidence(result *Result, episode Episode, slice sessionlake.Slice) {
	historical := episodeEvidence(episode)
	current := stringSet(eventIDs(slice.Events))
	for index := range result.Candidates {
		evidence := result.Candidates[index].EvidenceEventIDs
		filtered := make([]string, 0, len(evidence))
		for _, eventID := range evidence {
			if _, ok := current[eventID]; ok {
				filtered = append(filtered, eventID)
				continue
			}
			if _, ok := historical[eventID]; !ok {
				filtered = append(filtered, eventID)
			}
		}
		result.Candidates[index].EvidenceEventIDs = filtered
	}
}

type compactionFlight struct {
	done   chan struct{}
	result compactionResult
	err    error
}

type compactionResult struct {
	checkpoint       Checkpoint
	usage            Usage
	baseMessageCount int
	baseDigest       [sha256.Size]byte
}

func (e *OpenAI) prepareEpisode(ctx context.Context, key EpisodeKey, episode *Episode, appendTokens int) (Usage, error) {
	if !e.config.CompactionEnabled {
		return Usage{}, nil
	}
	if len(episode.Messages) == 0 {
		return Usage{}, nil
	}
	hardLimit := episode.EstimatedTokens+appendTokens >= e.config.CompactTokens
	flight := e.compactionFlight(key)
	if flight == nil && (hardLimit || episode.EstimatedTokens >= e.config.CompactStartTokens) {
		flight = e.startCompaction(ctx, key, *episode)
	}
	if flight == nil {
		return Usage{}, nil
	}
	ready, err := waitCompaction(ctx, flight, hardLimit)
	if err != nil {
		return Usage{}, err
	}
	if !ready {
		return Usage{}, nil
	}
	result, flightErr := e.consumeCompaction(key, flight)
	if flightErr == nil {
		if applyErr := applyCompaction(episode, result); applyErr == nil {
			return result.usage, nil
		} else {
			flightErr = applyErr
		}
	}
	if !hardLimit {
		return Usage{}, nil
	}
	result, err = e.computeCompaction(ctx, *episode)
	if err != nil {
		return Usage{}, errors.Join(flightErr, err)
	}
	if err := applyCompaction(episode, result); err != nil {
		return Usage{}, err
	}
	return result.usage, nil
}

func (e *OpenAI) startCompaction(ctx context.Context, key EpisodeKey, episode Episode) *compactionFlight {
	e.flightsMu.Lock()
	if existing := e.flights[key]; existing != nil {
		e.flightsMu.Unlock()
		return existing
	}
	flight := &compactionFlight{done: make(chan struct{})}
	e.flights[key] = flight
	owned, finishBackground := e.lifecycle.beginBackground(ctx)
	e.flightsMu.Unlock()

	go func() {
		background, cancel := context.WithTimeout(owned, 2*time.Minute)
		defer cancel()
		flight.result, flight.err = e.computeCompaction(background, episode)
		close(flight.done)
		finishBackground(flight.err)
	}()
	return flight
}

func (e *OpenAI) compactionFlight(key EpisodeKey) *compactionFlight {
	e.flightsMu.Lock()
	defer e.flightsMu.Unlock()
	return e.flights[key]
}

func (e *OpenAI) consumeCompaction(key EpisodeKey, flight *compactionFlight) (compactionResult, error) {
	e.flightsMu.Lock()
	if e.flights[key] == flight {
		delete(e.flights, key)
	}
	e.flightsMu.Unlock()
	return flight.result, flight.err
}

func waitCompaction(ctx context.Context, flight *compactionFlight, required bool) (bool, error) {
	if !required {
		select {
		case <-flight.done:
			return true, nil
		default:
			return false, nil
		}
	}
	select {
	case <-flight.done:
		return true, nil
	case <-ctx.Done():
		return false, fmt.Errorf("wait for extraction compaction: %w", ctx.Err())
	}
}

func (e *OpenAI) computeCompaction(ctx context.Context, episode Episode) (compactionResult, error) {
	messages, err := episodeMessages(episode, e.systemPrompt())
	if err != nil {
		return compactionResult{}, fmt.Errorf("build compaction context: %w", err)
	}
	messages = append(messages, chatMessage{Role: "user", Content: compactionPrompt})
	body, err := e.callWithType(ctx, messages, 0, ProviderCallCompaction)
	if err != nil {
		return compactionResult{}, fmt.Errorf("compact extraction episode: %w", err)
	}
	checkpoint, usage, err := decodeCheckpoint(body)
	if err != nil {
		return compactionResult{}, err
	}
	if err := validateCheckpoint(checkpoint, episode); err != nil {
		return compactionResult{}, err
	}
	return compactionResult{
		checkpoint: checkpoint, usage: usage,
		baseMessageCount: len(episode.Messages), baseDigest: digestMessages(episode.Messages),
	}, nil
}

func applyCompaction(episode *Episode, result compactionResult) error {
	if result.baseMessageCount > len(episode.Messages) ||
		digestMessages(episode.Messages[:result.baseMessageCount]) != result.baseDigest {
		return fmt.Errorf("apply extraction compaction: snapshot is stale: %w", ErrEpisodeConflict)
	}
	tail := append([]EpisodeMessage(nil), episode.Messages[result.baseMessageCount:]...)
	result.checkpoint.SourceCursors = episode.Checkpoint.SourceCursors
	result.checkpoint.SummaryCount = episode.Checkpoint.SummaryCount
	result.checkpoint.SummaryAttempts = episode.Checkpoint.SummaryAttempts
	result.checkpoint.SummaryFailures = episode.Checkpoint.SummaryFailures
	result.checkpoint.SummaryLastError = episode.Checkpoint.SummaryLastError
	episode.Checkpoint = result.checkpoint
	episode.Messages = tail
	episode.EstimatedTokens = estimateEpisodeTokens(result.checkpoint, tail)
	episode.CompactionCount++
	return nil
}

func digestMessages(messages []EpisodeMessage) [sha256.Size]byte {
	encoded, err := json.Marshal(messages)
	if err != nil {
		return sha256.Sum256(nil)
	}
	return sha256.Sum256(encoded)
}

func estimateEpisodeTokens(checkpoint Checkpoint, messages []EpisodeMessage) int {
	total := estimateTokens(rollingSystemPrompt)
	if hasCheckpoint(checkpoint) {
		encoded, err := checkpointJSON(checkpoint)
		if err == nil {
			total += estimateTokens(encoded) + estimateTokens(`{"status":"checkpoint_loaded"}`)
		}
	}
	for _, message := range messages {
		total += estimateTokens(message.Content)
	}
	return total
}

func episodeMessages(episode Episode, system string) ([]chatMessage, error) {
	messages := []chatMessage{{Role: "system", Content: system}}
	if hasCheckpoint(episode.Checkpoint) {
		checkpoint, err := checkpointJSON(episode.Checkpoint)
		if err != nil {
			return nil, err
		}
		messages = append(messages,
			chatMessage{Role: "user", Content: "Continue from this extraction checkpoint:\n" + checkpoint},
			chatMessage{Role: "assistant", Content: `{"status":"checkpoint_loaded"}`},
		)
	}
	for _, message := range episode.Messages {
		messages = append(messages, chatMessage(message))
	}
	return messages, nil
}

func decodeCheckpoint(body []byte) (Checkpoint, Usage, error) {
	var response chatResponse
	if err := json.Unmarshal(body, &response); err != nil || len(response.Choices) == 0 {
		return Checkpoint{}, Usage{}, fmt.Errorf("decode compaction response: %w", ErrInvalidModelResponse)
	}
	content := trimCodeFence(response.Choices[0].Message.Content)
	var checkpoint Checkpoint
	if err := json.Unmarshal([]byte(content), &checkpoint); err != nil {
		return Checkpoint{}, Usage{}, fmt.Errorf("decode extraction checkpoint: %w", errors.Join(ErrInvalidModelResponse, err))
	}
	if checkpoint.ActiveKnowledge == nil || checkpoint.ResolvedKnowledge == nil || checkpoint.OpenQuestions == nil {
		return Checkpoint{}, Usage{}, fmt.Errorf("decode extraction checkpoint: required collections are missing: %w", ErrInvalidModelResponse)
	}
	usage := Usage{
		InputTokens: response.Usage.PromptTokens, OutputTokens: response.Usage.CompletionTokens,
		PromptCacheHitTokens:  response.Usage.PromptCacheHitTokens,
		PromptCacheMissTokens: response.Usage.PromptCacheMissTokens,
	}
	return checkpoint, usage, nil
}

func decodeCandidateContent(content string) (Result, error) {
	var output candidateOutput
	if err := json.Unmarshal([]byte(trimCodeFence(content)), &output); err != nil {
		return Result{}, fmt.Errorf("decode saved extraction candidates: %w", errors.Join(ErrInvalidModelResponse, err))
	}
	return Result{Candidates: output.Candidates}, nil
}

func validateCheckpoint(checkpoint Checkpoint, episode Episode) error {
	if checkpoint.EvidenceIndex == nil {
		return fmt.Errorf("validate extraction checkpoint: evidence index is missing: %w", ErrInvalidModelResponse)
	}
	knownEvidence := episodeEvidence(episode)
	seen := make(map[string]struct{})
	collections := [][]KnowledgeItem{checkpoint.ActiveKnowledge, checkpoint.ResolvedKnowledge, checkpoint.OpenQuestions}
	for _, items := range collections {
		for _, item := range items {
			if err := validateKnowledgeItem(item, checkpoint.EvidenceIndex, knownEvidence, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

func episodeEvidence(episode Episode) map[string]struct{} {
	known := checkpointEvidence(episode.Checkpoint)
	for _, message := range episode.Messages {
		if message.Role != "user" {
			continue
		}
		var input struct {
			Events []struct {
				ID string `json:"id"`
			} `json:"events"`
		}
		if json.Unmarshal([]byte(message.Content), &input) == nil {
			for _, event := range input.Events {
				known[event.ID] = struct{}{}
			}
		}
	}
	return known
}

func validateKnowledgeItem(item KnowledgeItem, index map[string][]string, known, seen map[string]struct{}) error {
	if strings.TrimSpace(item.MemoryID) == "" || strings.TrimSpace(item.Subject) == "" ||
		strings.TrimSpace(item.Body) == "" || len(item.EvidenceEventIDs) == 0 || !validCheckpointKind(item.Kind) {
		return fmt.Errorf("validate extraction checkpoint: incomplete knowledge item: %w", ErrInvalidModelResponse)
	}
	if _, duplicate := seen[item.MemoryID]; duplicate {
		return fmt.Errorf("validate extraction checkpoint: duplicate memory ID %q: %w", item.MemoryID, ErrInvalidModelResponse)
	}
	seen[item.MemoryID] = struct{}{}
	indexedEvidence, ok := index[item.MemoryID]
	if !ok || !sameStrings(indexedEvidence, item.EvidenceEventIDs) {
		return fmt.Errorf("validate extraction checkpoint: evidence index differs for %q: %w", item.MemoryID, ErrInvalidModelResponse)
	}
	for _, eventID := range item.EvidenceEventIDs {
		if _, ok := known[eventID]; !ok {
			return fmt.Errorf("validate extraction checkpoint: unknown evidence %q: %w", eventID, ErrInvalidModelResponse)
		}
	}
	return nil
}

func validCheckpointKind(kind string) bool {
	switch teamnote.NoteKind(kind) {
	case teamnote.KindStatus, teamnote.KindBlocker, teamnote.KindHandoff, teamnote.KindArtifactReference:
		return true
	default:
		return false
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftSet := stringSet(left)
	for _, value := range right {
		if _, ok := leftSet[value]; !ok {
			return false
		}
	}
	return true
}

func checkpointEvidence(checkpoint Checkpoint) map[string]struct{} {
	evidence := make(map[string]struct{})
	for _, items := range [][]KnowledgeItem{checkpoint.ActiveKnowledge, checkpoint.ResolvedKnowledge, checkpoint.OpenQuestions} {
		for _, item := range items {
			for _, eventID := range item.EvidenceEventIDs {
				evidence[eventID] = struct{}{}
			}
		}
	}
	return evidence
}

func pruneEpisodeRuns(runs map[string]EpisodeRun, limit int) {
	for len(runs) > limit {
		oldestKey := ""
		oldestOrdinal := int(^uint(0) >> 1)
		for key, run := range runs {
			if run.Ordinal < oldestOrdinal {
				oldestKey, oldestOrdinal = key, run.Ordinal
			}
		}
		delete(runs, oldestKey)
	}
}

type episodeLease struct {
	mutex      sync.Mutex
	references int
}

func (e *OpenAI) acquireEpisode(key EpisodeKey) func() {
	e.locksMu.Lock()
	lease, ok := e.locks[key]
	if !ok {
		lease = new(episodeLease)
		e.locks[key] = lease
	}
	lease.references++
	e.locksMu.Unlock()
	lease.mutex.Lock()
	return func() {
		lease.mutex.Unlock()
		e.locksMu.Lock()
		lease.references--
		if lease.references == 0 && e.locks[key] == lease {
			delete(e.locks, key)
		}
		e.locksMu.Unlock()
	}
}

func updateSourceCursor(checkpoint *Checkpoint, slice sessionlake.Slice) {
	if checkpoint.SourceCursors == nil {
		checkpoint.SourceCursors = make(map[string]int64)
	}
	key := strings.Join([]string{slice.Actor.UserID, slice.Actor.AgentID, slice.Actor.SessionID}, "/")
	if slice.ToSequence > checkpoint.SourceCursors[key] {
		checkpoint.SourceCursors[key] = slice.ToSequence
	}
}

func hasCheckpoint(checkpoint Checkpoint) bool {
	return strings.TrimSpace(checkpoint.Summary) != "" ||
		len(checkpoint.ActiveKnowledge)+len(checkpoint.ResolvedKnowledge)+len(checkpoint.OpenQuestions) > 0
}

func estimateTokens(value string) int {
	return 16 + (len(value)+3)/4
}

func addUsage(target *Usage, addition Usage) {
	target.InputTokens += addition.InputTokens
	target.OutputTokens += addition.OutputTokens
	target.PromptCacheHitTokens += addition.PromptCacheHitTokens
	target.PromptCacheMissTokens += addition.PromptCacheMissTokens
}

const rollingSystemPrompt = systemPrompt + `
You are maintaining one cumulative knowledge state for a task or thread across
multiple agents and sessions. Previous assistant responses are your own prior
state transitions. Reuse the same identity_ref for the same real-world fact,
decision, obligation, blocker, or artifact even when a different agent reports
the update. Prefer update or resolve over creating a parallel fact. A checkpoint
is a lossy handoff context, not new evidence; every emitted candidate must still
cite at least one event from the current new_event_ids.`

const compactionPrompt = `You are performing a KNOWLEDGE CONTEXT CHECKPOINT COMPACTION.
Create a structured handoff for another extraction LLM that will continue
maintaining the same team knowledge state.

Preserve:
- active decisions, obligations, owners, exact values, dates, blockers, handoffs, and artifacts;
- resolved or superseded knowledge needed to avoid reviving stale facts;
- unresolved questions and important constraints;
- stable memory IDs and every supporting evidence event ID;
- key changes and why the current state replaced the previous state.

If an earlier checkpoint is present, carry its still-relevant knowledge forward.
Do not treat assistant statements or the earlier checkpoint as factual evidence.
Do not invent facts, identifiers, dates, or evidence IDs. Be concise while
preserving everything needed for the next extractor to continue without the raw
history.

Return exactly one JSON object:
{"active_knowledge":[{"memory_id":"stable-id","kind":"status","subject":"stable subject","body":"current factual state","evidence_event_ids":["event-id"]}],"resolved_knowledge":[],"open_questions":[],"evidence_index":{"stable-id":["event-id"]},"source_cursors":{}}`
