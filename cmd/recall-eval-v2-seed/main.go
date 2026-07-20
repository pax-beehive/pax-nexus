package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallv2"
	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/platform/textembedding"
	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

const (
	defaultEmbeddingModel = "Qwen/Qwen3-Embedding-0.6B"
	seedPromptVersion     = "recall-eval-v2-fixed-observation-v1"
)

var fixedObservationTime = time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)

type seededCase struct {
	CaseID string                   `json:"case_id"`
	Runs   []teamnote.ExtractionRun `json:"runs"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		slog.Error("seed recall eval v2 notes", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("recall-eval-v2-seed", flag.ContinueOnError)
	dsn := flags.String("dsn", "", "PostgreSQL DSN")
	scopeID := flags.String("scope", "", "Team Memory scope")
	manifest := flags.String("manifest", "", "GroupMemBench manifest")
	annotations := flags.String("annotations", "", "Recall Eval v2 case annotations")
	answererSeed := flags.String("answerer-seed", "pax-recall-eval-v2-answerer-1", "deterministic answerer seed")
	embeddingURL := flags.String("embedding-url", "http://qwen-embedding:8080", "OpenAI-compatible embedding base URL")
	embeddingModel := flags.String("embedding-model", defaultEmbeddingModel, "embedding model")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse recall eval v2 seed flags: %w", err)
	}
	if strings.TrimSpace(*dsn) == "" || strings.TrimSpace(*scopeID) == "" || strings.TrimSpace(*manifest) == "" || strings.TrimSpace(*annotations) == "" {
		return fmt.Errorf("dsn, scope, manifest, and annotations are required")
	}
	cases, _, _, err := recallv2.LoadCases(*manifest, *annotations, *answererSeed, 30, 50)
	if err != nil {
		return fmt.Errorf("load fixed recall cohort: %w", err)
	}
	embedder, err := textembedding.NewOpenAI(textembedding.OpenAIConfig{
		BaseURL: *embeddingURL, Model: *embeddingModel, Dimensions: postgres.EmbeddingDimensions,
		Client: &http.Client{Timeout: 30 * time.Second},
	})
	if err != nil {
		return fmt.Errorf("create recall eval v2 embedding client: %w", err)
	}
	store, err := postgres.Open(ctx, *dsn)
	if err != nil {
		return fmt.Errorf("open recall eval v2 seed store: %w", err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate recall eval v2 seed store: %w", err)
	}
	notes, err := postgres.NewNoteStore(store, recallEvalTTLPolicy(), teamnote.SystemClock{}, postgres.RetrievalConfig{
		Embedder: embedder, EmbeddingModel: *embeddingModel, SemanticThreshold: 0.50, CandidateLimit: 16,
	})
	if err != nil {
		return fmt.Errorf("create recall eval v2 note store: %w", err)
	}
	seededCases, err := buildSeededCases(cases)
	if err != nil {
		return fmt.Errorf("build canonical recall observation: %w", err)
	}
	observationSHA, err := observationChecksum(seededCases)
	if err != nil {
		return fmt.Errorf("checksum canonical recall observation: %w", err)
	}
	manifestSHA, err := fileChecksum(*manifest)
	if err != nil {
		return fmt.Errorf("checksum recall Agent manifest: %w", err)
	}
	annotationsSHA, err := fileChecksum(*annotations)
	if err != nil {
		return fmt.Errorf("checksum recall knowledge-source annotations: %w", err)
	}
	seeded, err := persistSeededCases(ctx, store, notes, *scopeID, seededCases)
	if err != nil {
		return fmt.Errorf("persist canonical recall observation: %w", err)
	}
	receipt := map[string]any{
		"scope_id": *scopeID, "cases": len(cases), "notes": seeded, "observation": seedPromptVersion,
		"observation_time": fixedObservationTime.Format(time.RFC3339), "observation_sha256": observationSHA,
		"manifest_sha256": manifestSHA, "annotations_sha256": annotationsSHA,
	}
	if err := json.NewEncoder(os.Stdout).Encode(receipt); err != nil {
		return fmt.Errorf("encode recall eval v2 seed receipt: %w", err)
	}
	return nil
}

func buildSeededCases(cases []v2.Case) ([]seededCase, error) {
	seededCases := make([]seededCase, 0, len(cases))
	for _, evalCase := range cases {
		seeds, err := seedCandidates(evalCase, fixedObservationTime)
		if err != nil {
			return nil, fmt.Errorf("build fixed observation for case %q: %w", evalCase.ID, err)
		}
		seededCases = append(seededCases, seededCase{CaseID: evalCase.ID, Runs: seeds})
	}
	return seededCases, nil
}

func persistSeededCases(
	ctx context.Context,
	store *postgres.Store,
	notes *postgres.NoteStore,
	scopeID string,
	cases []seededCase,
) (int, error) {
	seeded := 0
	for _, evalCase := range cases {
		for index, seed := range evalCase.Runs {
			if _, err := store.AppendSession(ctx, scopeID, session.SessionBatch{
				Events: seed.Evidence, Complete: index == len(evalCase.Runs)-1,
			}); err != nil {
				return 0, fmt.Errorf("seed evidence for case %q: %w", evalCase.CaseID, err)
			}
			if _, err := notes.ApplyExtractionRun(ctx, scopeID, seed); err != nil {
				return 0, fmt.Errorf("seed case %q: %w", evalCase.CaseID, err)
			}
			seeded++
		}
	}
	return seeded, nil
}

func seedCandidates(evalCase v2.Case, now time.Time) ([]teamnote.ExtractionRun, error) {
	originAgent := firstSourceAgent(evalCase)
	origin := teamnote.Actor{
		UserID: evalCase.AskingUserID, AgentID: originAgent,
		SessionID: "recall-eval-v2-source-" + evalCase.ID,
	}
	anchor := queryAnchor(evalCase.Question)
	relationSubject := "recall bridge " + strings.ReplaceAll(evalCase.ID, "_", " ")
	leadBody := "Potentially relevant context is indexed by " + anchor + "."
	lead := teamnote.Candidate{
		ID: "recall-v2-lead-" + evalCase.ID, Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
		Subject: "memory navigation " + evalCase.Category + " " + strings.ReplaceAll(evalCase.ID, "_", " "), Body: leadBody, Origin: origin,
		RelatedSubjects: []string{relationSubject}, EvidenceEventIDs: []string{"recall-v2-lead-event-" + evalCase.ID},
		SourceOccurredAt: now,
	}
	evidence := teamnote.Candidate{
		ID: "recall-v2-evidence-" + evalCase.ID, Action: teamnote.ActionCreate, Kind: teamnote.KindStatus,
		Subject: relationSubject, Body: strings.TrimSpace(evalCase.Expected), Origin: origin,
		EvidenceEventIDs: []string{"recall-v2-evidence-event-" + evalCase.ID}, SourceOccurredAt: now,
	}
	leadRun, err := seedRun(evalCase.ID+"-lead", origin, lead, 1, now)
	if err != nil {
		return nil, err
	}
	runs := []teamnote.ExtractionRun{leadRun}
	if evalCase.Category == "abstention" {
		return runs, nil
	}
	evidenceRun, err := seedRun(evalCase.ID+"-evidence", origin, evidence, 2, now)
	if err != nil {
		return nil, err
	}
	return append(runs, evidenceRun), nil
}

func seedRun(id string, origin teamnote.Actor, candidate teamnote.Candidate, sequence int64, occurredAt time.Time) (teamnote.ExtractionRun, error) {
	event := teamnote.SessionEvent{
		ID: candidate.EvidenceEventIDs[0], Actor: origin, Sequence: sequence,
		Type: "assistant_message", Content: candidate.Body, OccurredAt: occurredAt, CapturedAt: occurredAt,
	}
	encoded, err := json.Marshal(struct {
		ID               string                `json:"id"`
		Origin           teamnote.Actor        `json:"origin"`
		Candidate        teamnote.Candidate    `json:"candidate"`
		SourceOccurredAt time.Time             `json:"source_occurred_at"`
		Event            teamnote.SessionEvent `json:"event"`
	}{id, origin, candidate, candidate.SourceOccurredAt, event})
	if err != nil {
		return teamnote.ExtractionRun{}, fmt.Errorf("encode seed run %q checksum: %w", id, err)
	}
	digest := sha256.Sum256(encoded)
	return teamnote.ExtractionRun{
		ID: "recall-eval-v2-seed-" + id, Actor: origin, FromSequence: sequence, ToSequence: sequence,
		InputChecksum: hex.EncodeToString(digest[:]), Model: "fixed-observation", PromptVersion: seedPromptVersion,
		Candidates: []teamnote.Candidate{candidate}, Evidence: []teamnote.SessionEvent{event},
	}, nil
}

func observationChecksum(cases []seededCase) (string, error) {
	encoded, err := json.Marshal(struct {
		ObservationTime time.Time    `json:"observation_time"`
		Cases           []seededCase `json:"cases"`
	}{fixedObservationTime, cases})
	if err != nil {
		return "", fmt.Errorf("encode fixed recall observation checksum: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func fileChecksum(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read recall eval v2 seed input %q: %w", path, err)
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func recallEvalTTLPolicy() teamnote.TTLPolicy {
	lease := teamnote.LeasePolicy{SoftTTL: 99 * 365 * 24 * time.Hour, HardTTL: 100 * 365 * 24 * time.Hour}
	return teamnote.TTLPolicy{
		teamnote.KindStatus: lease, teamnote.KindBlocker: lease,
		teamnote.KindHandoff: lease, teamnote.KindArtifactReference: lease,
	}
}

func firstSourceAgent(evalCase v2.Case) string {
	for _, agentID := range evalCase.SupportingAgentIDs {
		if agentID != "" && agentID != evalCase.AnsweringAgentID {
			return agentID
		}
	}
	for _, agentID := range evalCase.ParticipantAgentIDs {
		if agentID != "" && agentID != evalCase.AnsweringAgentID {
			return agentID
		}
	}
	return "groupmembench-source-" + evalCase.ID
}

func queryAnchor(query string) string {
	best := "topic"
	for _, field := range strings.FieldsFunc(query, func(character rune) bool {
		return character < '0' || (character > '9' && character < 'A') || (character > 'Z' && character < 'a') || character > 'z'
	}) {
		if len(field) > len(best) {
			best = strings.ToLower(field)
		}
	}
	return best
}
