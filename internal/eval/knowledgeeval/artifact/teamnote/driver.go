package teamnote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	domain "github.com/pax-beehive/pax-nexus/internal/teamnote"
)

const (
	ArtifactKind   = "teamnote-snapshot"
	ArtifactSchema = "pax.teamnote.snapshot.v1"
	DriverID       = "teamnote"
	DriverVersion  = "v1"
)

type Snapshot struct {
	Notes []domain.Note `json:"notes"`
}

type Driver struct {
	store *knowledgeeval.ArtifactStore
	now   func() time.Time
}

func NewDriver(store *knowledgeeval.ArtifactStore, now func() time.Time) (*Driver, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: artifact store is required", knowledgeeval.ErrInvalidRecord)
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Driver{store: store, now: now}, nil
}

func (d *Driver) Descriptor() knowledgeeval.ArtifactDriverDescriptor {
	return knowledgeeval.ArtifactDriverDescriptor{
		ID: DriverID, Version: DriverVersion,
		ArtifactKinds: []string{ArtifactKind}, SchemaVersions: []string{ArtifactSchema},
		Capabilities: knowledgeeval.CapabilitySet{
			{Name: knowledgeeval.TeamNoteSnapshotCapability, Version: "v1"},
			{Name: knowledgeeval.PassiveRecallCapability, Version: "v1"},
		},
	}
}

func (d *Driver) Publish(
	ctx context.Context,
	snapshot Snapshot,
	group knowledgeeval.BenchmarkGroup,
	provenance knowledgeeval.Provenance,
) (knowledgeeval.ArtifactRecord, error) {
	if err := validateSnapshot(snapshot); err != nil {
		return knowledgeeval.ArtifactRecord{}, err
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, fmt.Errorf("encode Team Note snapshot: %w", err)
	}
	payload, err := d.store.PutBytes(ctx, ArtifactKind, ArtifactSchema, encoded)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, fmt.Errorf("store Team Note snapshot: %w", err)
	}
	return knowledgeeval.ArtifactRecord{
		ArtifactID: stableID(group.GroupID, group.CheckpointID, payload.SHA256),
		Kind:       ArtifactKind, WorldID: group.WorldID, GroupID: group.GroupID,
		CheckpointID: group.CheckpointID, Payload: payload,
		Provenance: provenance, CreatedAt: d.now(),
	}, nil
}

func (d *Driver) Open(
	ctx context.Context,
	artifact knowledgeeval.ArtifactRecord,
) (knowledgeeval.Subject, error) {
	if err := artifact.Validate(); err != nil {
		return nil, err
	}
	if artifact.Kind != ArtifactKind || artifact.Payload.SchemaVersion != ArtifactSchema {
		return nil, fmt.Errorf("%w: unsupported Team Note artifact", knowledgeeval.ErrInvalidRecord)
	}
	encoded, err := d.store.OpenBytes(ctx, artifact.Payload)
	if err != nil {
		return nil, fmt.Errorf("open Team Note snapshot: %w", err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return nil, fmt.Errorf("decode Team Note snapshot: %w", err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return nil, err
	}
	return &subject{
		id: artifact.ArtifactID, notes: snapshot.Notes,
		capabilities: d.Descriptor().Capabilities,
	}, nil
}

func (d *Driver) RenderView(
	ctx context.Context,
	request knowledgeeval.ArtifactViewRequest,
) (knowledgeeval.ArtifactViewRecord, error) {
	opened, err := d.Open(ctx, request.Artifact)
	if err != nil {
		return knowledgeeval.ArtifactViewRecord{}, err
	}
	current, ok := opened.(*subject)
	if !ok {
		return knowledgeeval.ArtifactViewRecord{}, fmt.Errorf(
			"%w: Team Note driver returned an unexpected subject",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	var content []byte
	switch request.Kind {
	case "native":
		content = renderNotes(current.notes)
	case "raw":
		encoded, err := json.MarshalIndent(Snapshot{Notes: current.notes}, "", "  ")
		if err != nil {
			return knowledgeeval.ArtifactViewRecord{}, fmt.Errorf("encode Team Note raw view: %w", err)
		}
		content = []byte("<!doctype html><html><body><pre>" +
			html.EscapeString(string(encoded)) + "</pre></body></html>")
	default:
		return knowledgeeval.ArtifactViewRecord{}, fmt.Errorf(
			"%w: unsupported Team Note view %q",
			knowledgeeval.ErrInvalidRecord,
			request.Kind,
		)
	}
	payload, err := d.store.PutBytes(
		ctx, "artifact-view/html", "pax.knowledge-eval.artifact-view.v1", content,
	)
	if err != nil {
		return knowledgeeval.ArtifactViewRecord{}, fmt.Errorf("store Team Note view: %w", err)
	}
	return knowledgeeval.ArtifactViewRecord{
		ViewID:     stableID(request.Artifact.ArtifactID, request.Kind, payload.SHA256),
		ArtifactID: request.Artifact.ArtifactID, Kind: request.Kind,
		RendererID: DriverID, RendererVersion: DriverVersion,
		Payload: payload, EntryPoint: payload.URI, CreatedAt: d.now(),
	}, nil
}

type subject struct {
	id           string
	notes        []domain.Note
	capabilities knowledgeeval.CapabilitySet
}

func (s *subject) ID() string {
	return s.id
}

func (s *subject) Capabilities() knowledgeeval.CapabilitySet {
	return s.capabilities.Clone()
}

func (s *subject) Recall(
	_ context.Context,
	request knowledgeeval.PassiveRecallRequest,
) (knowledgeeval.PassiveRecallResponse, error) {
	queryTerms := terms(request.Query)
	if len(queryTerms) == 0 {
		return knowledgeeval.PassiveRecallResponse{}, fmt.Errorf("%w: recall query is required", knowledgeeval.ErrInvalidRecord)
	}
	candidates := make([]knowledgeeval.SearchHit, 0)
	for _, note := range s.notes {
		if note.State != domain.StateActive {
			continue
		}
		score := overlap(queryTerms, terms(note.Subject+" "+note.Body))
		if score == 0 {
			continue
		}
		text := note.Subject + ": " + note.Body
		candidates = append(candidates, knowledgeeval.SearchHit{
			Ref: note.ID, Text: text, Score: score, Tokens: estimateTokens(text),
			Metadata: map[string]string{
				"kind": string(note.Kind), "state": string(note.State),
			},
		})
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].Score == candidates[right].Score {
			return candidates[left].Ref < candidates[right].Ref
		}
		return candidates[left].Score > candidates[right].Score
	})
	selected := selectItems(candidates, request.MaxItems, request.TokenBudget)
	selectedIDs := make([]string, 0, len(selected))
	for _, item := range selected {
		selectedIDs = append(selectedIDs, item.Ref)
	}
	trace, err := json.Marshal(map[string]any{
		"candidate_count": len(candidates),
		"selected_set":    selectedIDs,
		"budget_drops":    len(candidates) - len(selected),
	})
	if err != nil {
		return knowledgeeval.PassiveRecallResponse{}, fmt.Errorf("encode Team Note recall trace: %w", err)
	}
	return knowledgeeval.PassiveRecallResponse{Items: selected, Trace: trace}, nil
}

type RuntimeSubject struct {
	id           string
	runtime      domain.Runtime
	requestActor domain.Actor
}

func NewRuntimeSubject(id string, runtime domain.Runtime, actor domain.Actor) (*RuntimeSubject, error) {
	if strings.TrimSpace(id) == "" || runtime == nil {
		return nil, fmt.Errorf("%w: runtime subject ID and runtime are required", knowledgeeval.ErrInvalidRecord)
	}
	return &RuntimeSubject{id: id, runtime: runtime, requestActor: actor}, nil
}

func (s *RuntimeSubject) ID() string {
	return s.id
}

func (s *RuntimeSubject) Capabilities() knowledgeeval.CapabilitySet {
	return knowledgeeval.CapabilitySet{{
		Name: knowledgeeval.PassiveRecallCapability, Version: "v1",
	}}
}

func (s *RuntimeSubject) Recall(
	ctx context.Context,
	request knowledgeeval.PassiveRecallRequest,
) (knowledgeeval.PassiveRecallResponse, error) {
	envelope, err := s.runtime.RecallNotes(ctx, domain.RecallRequest{
		Actor: s.requestActor, Query: request.Query,
		MaxItems: request.MaxItems, TokenBudget: request.TokenBudget,
	})
	if err != nil {
		return knowledgeeval.PassiveRecallResponse{}, fmt.Errorf("recall Team Notes: %w", err)
	}
	items := make([]knowledgeeval.SearchHit, 0, len(envelope.Details))
	for _, detail := range envelope.Details {
		items = append(items, knowledgeeval.SearchHit{
			Ref: detail.NoteID, Text: detail.Text, Score: detail.Relevance,
			Tokens:   estimateTokens(detail.Text),
			Metadata: map[string]string{"certainty": string(detail.Certainty)},
		})
	}
	if len(items) == 0 {
		for index, text := range envelope.Items {
			items = append(items, knowledgeeval.SearchHit{
				Ref: fmt.Sprintf("item-%d", index+1), Text: text,
				Tokens: estimateTokens(text),
			})
		}
	}
	trace, err := json.Marshal(envelope.Decision)
	if err != nil {
		return knowledgeeval.PassiveRecallResponse{}, fmt.Errorf("encode Team Note decision trace: %w", err)
	}
	return knowledgeeval.PassiveRecallResponse{Items: items, Trace: trace}, nil
}

func validateSnapshot(snapshot Snapshot) error {
	if len(snapshot.Notes) == 0 {
		return fmt.Errorf("%w: Team Note snapshot requires notes", knowledgeeval.ErrInvalidRecord)
	}
	ids := make(map[string]struct{}, len(snapshot.Notes))
	for _, note := range snapshot.Notes {
		if strings.TrimSpace(note.ID) == "" || strings.TrimSpace(note.Body) == "" {
			return fmt.Errorf("%w: Team Note ID and body are required", knowledgeeval.ErrInvalidRecord)
		}
		if _, exists := ids[note.ID]; exists {
			return fmt.Errorf("%w: duplicate Team Note %s", knowledgeeval.ErrInvalidRecord, note.ID)
		}
		ids[note.ID] = struct{}{}
	}
	return nil
}

func renderNotes(notes []domain.Note) []byte {
	var output strings.Builder
	output.WriteString("<!doctype html><html><body><h1>Team Notes</h1><ul>")
	for _, note := range notes {
		fmt.Fprintf(
			&output,
			"<li><strong>%s</strong> %s <small>%s</small></li>",
			html.EscapeString(note.Subject),
			html.EscapeString(note.Body),
			html.EscapeString(string(note.State)),
		)
	}
	output.WriteString("</ul></body></html>")
	return []byte(output.String())
}

func terms(value string) map[string]struct{} {
	result := make(map[string]struct{})
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			result[current.String()] = struct{}{}
			current.Reset()
		}
	}
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			current.WriteRune(character)
		} else {
			flush()
		}
	}
	flush()
	return result
}

func overlap(query, document map[string]struct{}) float64 {
	matches := 0
	for term := range query {
		if _, exists := document[term]; exists {
			matches++
		}
	}
	if len(query) == 0 {
		return 0
	}
	return float64(matches) / float64(len(query))
}

func estimateTokens(value string) int {
	count := len(strings.Fields(value))
	if count == 0 {
		return 1
	}
	return count
}

func selectItems(
	items []knowledgeeval.SearchHit,
	maxItems,
	tokenBudget int,
) []knowledgeeval.SearchHit {
	if maxItems <= 0 || maxItems > len(items) {
		maxItems = len(items)
	}
	selected := make([]knowledgeeval.SearchHit, 0, maxItems)
	used := 0
	for _, item := range items {
		if len(selected) == maxItems {
			break
		}
		if tokenBudget > 0 && used+item.Tokens > tokenBudget {
			continue
		}
		selected = append(selected, item)
		used += item.Tokens
	}
	return selected
}

func stableID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}
