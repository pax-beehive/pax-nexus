package extractor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/sessionlake"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

const (
	extractionProtocolV2RevisionSourceSpanV1 = "source-span-v1"
	extractionProtocolV2RevisionSourceSpanV2 = "source-span-v2"
	maximumSourceSpanTopics                  = 8
	sourceSpanShardTargetTokens              = 180
)

// SourceSpan is immutable source content plus thin model-provided retrieval
// metadata. Its Body is always derived from the cited Session Events.
type SourceSpan struct {
	Subject          string   `json:"subject"`
	Topics           []string `json:"topics,omitempty"`
	EvidenceEventIDs []string `json:"evidence_event_ids"`
}

type sourceSpanAnnotation struct {
	Subject string   `json:"subject"`
	Topics  []string `json:"topics"`
}

type sourceSpanOutput struct {
	SourceSpan sourceSpanAnnotation `json:"source_span"`
}

// rollingSystemPromptSourceSpanV1 intentionally limits model work to labels.
// Raw session text and event provenance are constructed by deterministic code.
const rollingSystemPromptSourceSpanV1 = `You label one immutable source span of
new session events for later retrieval. Do not create canonical state, infer a
decision, resolve conflicts, summarize, or rewrite event content. Return one
JSON object exactly in this shape:
{"source_span":{"subject":"short neutral retrieval label","topics":["up to 8 literal topics or entities"]}}

subject and topics are navigation metadata only. Preserve uncertainty: a
proposal, question, correction, or contradiction remains source text and must
not be promoted into a fact. The application, not you, stores the exact event
text, author identity, timestamps, task, thread, and event IDs.`

func decodeExtractionResponseSourceSpanV1(body []byte) (Result, string, error) {
	var response chatResponse
	if err := json.Unmarshal(body, &response); err != nil || len(response.Choices) == 0 {
		return Result{}, "", fmt.Errorf("decode source span extractor response: %w", ErrInvalidModelResponse)
	}
	content := trimCodeFence(response.Choices[0].Message.Content)
	result, err := decodeExtractionContentSourceSpanV1(content)
	if err != nil {
		return Result{}, "", err
	}
	result.Usage = Usage{
		InputTokens: response.Usage.PromptTokens, OutputTokens: response.Usage.CompletionTokens,
		PromptCacheHitTokens:  response.Usage.PromptCacheHitTokens,
		PromptCacheMissTokens: response.Usage.PromptCacheMissTokens,
	}
	return result, content, nil
}

func decodeExtractionContentSourceSpanV1(content string) (Result, error) {
	var output sourceSpanOutput
	if err := json.Unmarshal([]byte(content), &output); err != nil {
		return Result{}, fmt.Errorf("decode source span extractor response: %w", errors.Join(ErrInvalidModelResponse, err))
	}
	return Result{SourceSpans: []SourceSpan{{
		Subject: output.SourceSpan.Subject, Topics: append([]string(nil), output.SourceSpan.Topics...),
	}}}, nil
}

func mapSourceSpanV1(result *Result, slice sessionlake.Slice) {
	if len(slice.NewEventIDs) == 0 {
		return
	}
	metadata := SourceSpan{}
	if len(result.SourceSpans) > 0 {
		metadata = result.SourceSpans[0]
	}
	evidence := append([]string(nil), slice.NewEventIDs...)
	metadata.Subject = sourceSpanSubject(metadata.Subject)
	metadata.Topics = sourceSpanTopics(metadata.Topics)
	metadata.EvidenceEventIDs = evidence
	result.SourceSpans = []SourceSpan{metadata}
	result.Candidates = []teamnote.Candidate{sourceSpanCandidate(slice, metadata)}
	result.ExtractionVersion = ExtractionVersionSourceSpanV1
}

// mapSourceSpanV2 preserves every new-event byte in deterministic, bounded
// source shards. The Session Lake remains the complete immutable archive;
// shards are the retrieval-sized representation of that archive.
func mapSourceSpanV2(result *Result, slice sessionlake.Slice) {
	if len(slice.NewEventIDs) == 0 {
		return
	}
	metadata := SourceSpan{}
	if len(result.SourceSpans) > 0 {
		metadata = result.SourceSpans[0]
	}
	metadata.Subject = sourceSpanSubject(metadata.Subject)
	metadata.Topics = sourceSpanTopics(metadata.Topics)

	shards := sourceSpanShards(slice.Events, slice.NewEventIDs)
	result.SourceSpans = make([]SourceSpan, 0, len(shards))
	result.Candidates = make([]teamnote.Candidate, 0, len(shards))
	for _, shard := range shards {
		span := metadata
		span.EvidenceEventIDs = append([]string(nil), shard.evidenceEventIDs...)
		result.SourceSpans = append(result.SourceSpans, span)
		result.Candidates = append(result.Candidates, sourceSpanShardCandidate(slice, span, shard))
	}
	result.ExtractionVersion = ExtractionVersionSourceSpanV2
}

func sourceSpanCandidate(slice sessionlake.Slice, metadata SourceSpan) teamnote.Candidate {
	identifier := sourceSpanIdentifier(slice, metadata.EvidenceEventIDs)
	first := sourceSpanFirstEvent(slice, metadata.EvidenceEventIDs)
	return teamnote.Candidate{
		ID: "source-span-" + identifier, Action: teamnote.ActionCreate, Kind: teamnote.KindSourceSpan,
		Subject: metadata.Subject, IdentityRef: "source-span/" + identifier,
		Body:    sourceSpanBody(slice.Events, metadata.EvidenceEventIDs),
		TaskRef: first.TaskRef, ThreadRef: first.ThreadRef, Origin: slice.Actor,
		RelatedSubjects: metadata.Topics, EvidenceEventIDs: metadata.EvidenceEventIDs,
	}
}

type sourceSpanShard struct {
	evidenceEventIDs []string
	body             string
}

func sourceSpanShardCandidate(slice sessionlake.Slice, metadata SourceSpan, shard sourceSpanShard) teamnote.Candidate {
	identifier := sourceSpanShardIdentifier(slice, shard)
	first := sourceSpanFirstEvent(slice, shard.evidenceEventIDs)
	return teamnote.Candidate{
		ID: "source-span-" + identifier, Action: teamnote.ActionCreate, Kind: teamnote.KindSourceSpan,
		Subject: metadata.Subject, IdentityRef: "source-span/" + identifier,
		Body:    shard.body,
		TaskRef: first.TaskRef, ThreadRef: first.ThreadRef, Origin: slice.Actor,
		RelatedSubjects: metadata.Topics, EvidenceEventIDs: append([]string(nil), shard.evidenceEventIDs...),
	}
}

func sourceSpanIdentifier(slice sessionlake.Slice, evidence []string) string {
	digest := sha256.Sum256([]byte(strings.Join(append([]string{
		slice.InputChecksum, slice.Actor.UserID, slice.Actor.AgentID, slice.Actor.SessionID,
	}, evidence...), "\x00")))
	return hex.EncodeToString(digest[:])
}

func sourceSpanShardIdentifier(slice sessionlake.Slice, shard sourceSpanShard) string {
	digest := sha256.Sum256([]byte(strings.Join(append([]string{
		slice.InputChecksum, slice.Actor.UserID, slice.Actor.AgentID, slice.Actor.SessionID,
	}, append(shard.evidenceEventIDs, shard.body)...), "\x00")))
	return hex.EncodeToString(digest[:])
}

func sourceSpanFirstEvent(slice sessionlake.Slice, evidence []string) teamnote.SessionEvent {
	wanted := make(map[string]struct{}, len(evidence))
	for _, eventID := range evidence {
		wanted[eventID] = struct{}{}
	}
	for _, event := range slice.Events {
		if _, ok := wanted[event.ID]; ok {
			return event
		}
	}
	return teamnote.SessionEvent{}
}

func sourceSpanBody(events []teamnote.SessionEvent, evidence []string) string {
	wanted := make(map[string]struct{}, len(evidence))
	for _, eventID := range evidence {
		wanted[eventID] = struct{}{}
	}
	parts := []string{"[source span; raw session content, not normalized state]"}
	for _, event := range events {
		if _, ok := wanted[event.ID]; !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf(
			"[event_id=%s occurred_at=%s actor=%s/%s/%s type=%s]\n%s",
			event.ID, event.OccurredAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			event.Actor.UserID, event.Actor.AgentID, event.Actor.SessionID, event.Type, strings.TrimSpace(event.Content),
		))
	}
	return strings.Join(parts, "\n\n")
}

func sourceSpanShards(events []teamnote.SessionEvent, evidence []string) []sourceSpanShard {
	wanted := make(map[string]struct{}, len(evidence))
	for _, eventID := range evidence {
		wanted[eventID] = struct{}{}
	}
	fragments := make([]sourceSpanShard, 0, len(evidence))
	for _, event := range events {
		if _, ok := wanted[event.ID]; !ok {
			continue
		}
		fragments = append(fragments, sourceSpanEventFragments(event)...)
	}
	return packSourceSpanFragments(fragments)
}

func sourceSpanEventFragments(event teamnote.SessionEvent) []sourceSpanShard {
	prefix := sourceSpanEventPrefix(event)
	parts := splitSourceSpanContent(event.Content, prefix)
	fragments := make([]sourceSpanShard, 0, len(parts))
	for index, part := range parts {
		body := fmt.Sprintf("%s part=%d/%d]\n%s", prefix, index+1, len(parts), part)
		fragments = append(fragments, sourceSpanShard{evidenceEventIDs: []string{event.ID}, body: body})
	}
	return fragments
}

func sourceSpanEventPrefix(event teamnote.SessionEvent) string {
	return fmt.Sprintf(
		"[event_id=%s occurred_at=%s actor=%s/%s/%s type=%s",
		event.ID, event.OccurredAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		event.Actor.UserID, event.Actor.AgentID, event.Actor.SessionID, event.Type,
	)
}

func splitSourceSpanContent(content, prefix string) []string {
	if content == "" {
		return []string{""}
	}
	parts := make([]string, 0, 1)
	start := 0
	for start < len(content) {
		end := start
		for end < len(content) {
			next := end + 1
			for next < len(content) && (content[next]&0xc0) == 0x80 {
				next++
			}
			candidate := content[start:next]
			if end > start && estimateTokens(fmt.Sprintf("%s part=1/1]\n%s", prefix, candidate)) > sourceSpanShardTargetTokens {
				break
			}
			end = next
		}
		if end == start {
			end++
			for end < len(content) && (content[end]&0xc0) == 0x80 {
				end++
			}
		}
		parts = append(parts, content[start:end])
		start = end
	}
	return parts
}

func packSourceSpanFragments(fragments []sourceSpanShard) []sourceSpanShard {
	if len(fragments) == 0 {
		return nil
	}
	const header = "[source span shard; raw session content, not normalized state]"
	shards := make([]sourceSpanShard, 0, len(fragments))
	current := sourceSpanShard{body: header}
	for _, fragment := range fragments {
		candidateBody := current.body + "\n\n" + fragment.body
		if len(current.evidenceEventIDs) > 0 && estimateTokens(candidateBody) > sourceSpanShardTargetTokens {
			shards = append(shards, current)
			current = sourceSpanShard{body: header}
		}
		current.body += "\n\n" + fragment.body
		if !sourceSpanContainsEvent(current.evidenceEventIDs, fragment.evidenceEventIDs[0]) {
			current.evidenceEventIDs = append(current.evidenceEventIDs, fragment.evidenceEventIDs[0])
		}
	}
	if len(current.evidenceEventIDs) > 0 {
		shards = append(shards, current)
	}
	return shards
}

func sourceSpanContainsEvent(eventIDs []string, wanted string) bool {
	for _, eventID := range eventIDs {
		if eventID == wanted {
			return true
		}
	}
	return false
}

func sourceSpanSubject(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "session source span"
	}
	return value
}

func sourceSpanTopics(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, min(len(values), maximumSourceSpanTopics))
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		key := strings.ToLower(value)
		if value == "" || len(result) == maximumSourceSpanTopics {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}
