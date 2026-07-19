package recallv2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallreplay"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
)

type sourceManifest struct {
	DatasetRevision string               `json:"dataset_revision"`
	Cases           []sourceManifestCase `json:"cases"`
}

type sourceManifestCase struct {
	ID           string `json:"id"`
	Category     string `json:"category"`
	Question     string `json:"question"`
	Answer       string `json:"answer"`
	AskingUserID string `json:"asking_user_id"`
}

// BuildAnswerAtomFixtures creates a fixed first-pass gold-atom cohort from a
// GroupMemBench case-context manifest. Reviewed source-event annotations can
// replace these coarse answer atoms without changing the protocol.
func BuildAnswerAtomFixtures(manifestPath string) (stageeval.FixtureSet, error) {
	input, err := os.ReadFile(manifestPath)
	if err != nil {
		return stageeval.FixtureSet{}, fmt.Errorf("read recall eval v2 source manifest: %w", err)
	}
	var manifest sourceManifest
	if err := json.Unmarshal(input, &manifest); err != nil {
		return stageeval.FixtureSet{}, fmt.Errorf("decode recall eval v2 source manifest: %w", err)
	}
	if len(manifest.Cases) < 30 || len(manifest.Cases) > 50 {
		return stageeval.FixtureSet{}, fmt.Errorf("build recall eval v2 fixtures: manifest must contain 30 to 50 cases")
	}
	result := stageeval.FixtureSet{
		SchemaVersion: stageeval.SchemaVersion,
		Dataset:       "Recall Eval v2 hard cohort from " + manifest.DatasetRevision,
	}
	base := filepath.Dir(manifestPath)
	seen := make(map[string]struct{}, len(manifest.Cases))
	for _, evalCase := range manifest.Cases {
		if _, duplicate := seen[evalCase.ID]; duplicate {
			return stageeval.FixtureSet{}, fmt.Errorf("build recall eval v2 fixtures: duplicate case %q", evalCase.ID)
		}
		seen[evalCase.ID] = struct{}{}
		sourcePath := filepath.Join(base, "cases", evalCase.ID, "producer", "session-batches.json")
		source, err := os.ReadFile(sourcePath)
		if err != nil {
			return stageeval.FixtureSet{}, fmt.Errorf("read recall eval v2 case %q source: %w", evalCase.ID, err)
		}
		digest := sha256.Sum256(source)
		fixture := stageeval.Fixture{
			CaseID: evalCase.ID, Category: evalCase.Category, SourceRevision: hex.EncodeToString(digest[:]),
			RecallContext: stageeval.RecallContext{
				ConsumerUserID:  evalCase.AskingUserID,
				ConsumerAgentID: "groupmembench-" + evalCase.AskingUserID,
				Query:           evalCase.Question, TokenBudget: 500, MaxItems: 5,
			},
		}
		if evalCase.Category != "abstention" {
			answer := strings.TrimSpace(evalCase.Answer)
			if answer == "" {
				return stageeval.FixtureSet{}, fmt.Errorf("build recall eval v2 fixture %q: answer is required", evalCase.ID)
			}
			fixture.RequiredAtoms = []stageeval.Atom{{ID: "gold_answer", Patterns: []string{"(?i)" + regexp.QuoteMeta(answer)}}}
		} else {
			fixture.ForbiddenAtoms = []stageeval.Atom{{ID: "unsupported_answer", Patterns: []string{"(?i)unsupported designated answer"}}}
		}
		result.Cases = append(result.Cases, fixture)
	}
	return result, nil
}

// BuildSyntheticHardReplay turns independent benchmark questions into a
// deterministic planner stress suite. It is a control fixture, not a claim
// about extraction quality from the original conversations.
func BuildSyntheticHardReplay(fixtures stageeval.FixtureSet) (recallreplay.FixtureSet, error) {
	observationTime := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	set := recallreplay.FixtureSet{
		SchemaVersion: recallreplay.SchemaVersion,
		Dataset:       fixtures.Dataset + " synthetic planner contrasts",
		ExportedFrom:  recallreplay.Provenance{RunID: "synthetic-hard30-v1", Arm: ArmTeamNote, ScopePrefix: "synthetic-"},
		Policy: recallreplay.Policy{
			SemanticThreshold: 0.65, CandidateLimit: 2, SuppressDuplicates: true, DegradeRelated: true,
		},
	}
	for index, fixture := range fixtures.Cases {
		actor := recallreplay.Actor{
			UserID: fixture.RecallContext.ConsumerUserID, AgentID: fixture.RecallContext.ConsumerAgentID,
			SessionID: "consumer-" + fixture.CaseID,
		}
		goldID := fixture.CaseID + "-gold"
		noiseID := fixture.CaseID + "-noise"
		goldText := ""
		if len(fixture.RequiredAtoms) > 0 {
			goldText = strings.TrimPrefix(fixture.RequiredAtoms[0].Patterns[0], "(?i)")
			goldText = strings.ReplaceAll(goldText, `\`, "")
		}
		noiseText := "This is a lexical distractor for " + fixture.RecallContext.Query
		if fixture.Category == "abstention" {
			noiseText = "Unsupported designated answer for " + fixture.RecallContext.Query
		}
		gold := recallreplay.Candidate{
			ID: goldID, Kind: "decision", Subject: fixture.RecallContext.Query, Body: goldText,
			Origin:           recallreplay.Actor{UserID: "source-user", AgentID: "source-agent", SessionID: "source-" + fixture.CaseID},
			EvidenceEventIDs: []string{"event-" + goldID}, Revision: 1,
			CreatedAt: observationTime.Add(-2 * time.Hour), UpdatedAt: observationTime.Add(-2 * time.Hour),
			SourceOccurredAt: observationTime.Add(-2 * time.Hour), LexicalScore: 0.82,
		}
		noise := recallreplay.Candidate{
			ID: noiseID, Kind: "status", Subject: fixture.RecallContext.Query + " archived copy", Body: noiseText,
			Origin:           recallreplay.Actor{UserID: "other-user", AgentID: "other-agent", SessionID: "noise-" + fixture.CaseID},
			EvidenceEventIDs: []string{"event-" + noiseID}, Revision: 1,
			CreatedAt: observationTime.Add(-24 * time.Hour), UpdatedAt: observationTime.Add(-24 * time.Hour),
			SourceOccurredAt: observationTime.Add(-24 * time.Hour), LexicalScore: 0.95,
		}
		var primary *recallreplay.Candidate
		if fixture.Category == "multi_hop" && index == 0 {
			gold.Subject = "supporting answer " + fixture.CaseID
			gold.Body = "Supporting answer for " + fixture.RecallContext.Query + ": " + goldText
			gold.LexicalScore = 0.1
			value := recallreplay.Candidate{
				ID: fixture.CaseID + "-primary", Kind: "status", Subject: fixture.RecallContext.Query,
				Body:            "Primary coordination record requiring its linked supporting answer.",
				Origin:          recallreplay.Actor{UserID: "source-user", AgentID: "source-agent", SessionID: "primary-" + fixture.CaseID},
				RelatedSubjects: []string{gold.Subject}, EvidenceEventIDs: []string{"event-" + fixture.CaseID + "-primary"},
				Revision: 1, CreatedAt: observationTime.Add(-3 * time.Hour), UpdatedAt: observationTime.Add(-3 * time.Hour),
				SourceOccurredAt: observationTime.Add(-3 * time.Hour), LexicalScore: 0.98,
			}
			primary = &value
		}
		if fixture.Category == "knowledge_update" {
			invalidAt := observationTime.Add(-time.Hour)
			noise.InvalidAt = &invalidAt
		}
		if fixture.Category == "temporal" {
			validAt := observationTime.Add(time.Hour)
			noise.ValidAt = &validAt
		}
		extractionItems := []stageeval.Item{
			{ID: noiseID, Text: noiseText, EvidenceEventIDs: slices.Clone(noise.EvidenceEventIDs)},
		}
		candidates := []recallreplay.Candidate{noise}
		if primary != nil {
			extractionItems = append(extractionItems, stageeval.Item{
				ID: primary.ID, Text: primary.Body, EvidenceEventIDs: slices.Clone(primary.EvidenceEventIDs),
			})
			candidates = append(candidates, *primary)
		}
		if fixture.Category != "abstention" {
			extractionItems = append(extractionItems, stageeval.Item{ID: goldID, Text: goldText, EvidenceEventIDs: slices.Clone(gold.EvidenceEventIDs)})
			candidates = append(candidates, gold)
		}
		if index == 5 {
			fixture.RecallContext.TokenBudget = 1
		}
		set.Cases = append(set.Cases, recallreplay.Case{
			Fixture: fixture, ScopeID: "synthetic-" + fixture.CaseID, Actor: actor,
			ObservationTime: observationTime, QueryTimezone: "UTC",
			ExtractionItems: extractionItems, Candidates: candidates,
		})
	}
	return set, nil
}
