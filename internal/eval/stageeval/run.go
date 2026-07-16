package stageeval

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type Summary struct {
	SchemaVersion               string  `json:"schema_version"`
	Dataset                     string  `json:"dataset,omitempty"`
	Cases                       int     `json:"cases"`
	RequiredAtoms               int     `json:"required_atoms"`
	ExtractionScoredAtoms       int     `json:"extraction_scored_atoms"`
	ExtractionMatchedAtoms      int     `json:"extraction_matched_atoms"`
	ExtractionFactRecall        float64 `json:"extraction_fact_recall"`
	ExtractionErrors            int     `json:"extraction_errors"`
	RecallScoredAtoms           int     `json:"recall_scored_atoms"`
	RecallMatchedAtoms          int     `json:"recall_matched_atoms"`
	RecallGoldRecall            float64 `json:"recall_gold_recall"`
	RecallErrors                int     `json:"recall_errors"`
	RecallMatchedAvailableAtoms int     `json:"recall_matched_available_atoms"`
	RecallConditionalRecall     float64 `json:"recall_conditional_recall"`
	UpstreamMissedAtoms         int     `json:"upstream_missed_atoms"`
	RecallMissedAvailableAtoms  int     `json:"recall_missed_available_atoms"`
	ExtractionLeakageItems      int     `json:"extraction_leakage_items"`
	RecallLeakageItems          int     `json:"recall_leakage_items"`
}

type observationPair struct {
	extraction *Observation
	recall     *Observation
}

func LoadFixtureSet(path string) (FixtureSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FixtureSet{}, fmt.Errorf("open stage fixture set: %w", err)
	}
	var fixtures FixtureSet
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixtures); err != nil {
		return FixtureSet{}, fmt.Errorf("decode stage fixture set: %w", err)
	}
	return fixtures, nil
}

func Run(fixtures FixtureSet, observations io.Reader) ([]Result, Summary, error) {
	if fixtures.SchemaVersion != SchemaVersion {
		return nil, Summary{}, fmt.Errorf("validate stage fixture set: schema_version must be %q", SchemaVersion)
	}
	fixtureByID := make(map[string]Fixture, len(fixtures.Cases))
	for _, fixture := range fixtures.Cases {
		if _, err := compileFixture(fixture); err != nil {
			return nil, Summary{}, err
		}
		if _, exists := fixtureByID[fixture.CaseID]; exists {
			return nil, Summary{}, fmt.Errorf("validate stage fixture set: duplicate case_id %q", fixture.CaseID)
		}
		fixtureByID[fixture.CaseID] = fixture
	}
	pairs, err := readObservations(observations, fixtureByID)
	if err != nil {
		return nil, Summary{}, err
	}
	results := make([]Result, 0, len(fixtures.Cases))
	for _, fixture := range fixtures.Cases {
		pair := pairs[fixture.CaseID]
		if pair.extraction == nil || pair.recall == nil {
			return nil, Summary{}, fmt.Errorf("evaluate stage case %q: extraction and recall observations are required", fixture.CaseID)
		}
		result, evaluateErr := Evaluate(fixture, *pair.extraction, *pair.recall)
		if evaluateErr != nil {
			return nil, Summary{}, evaluateErr
		}
		results = append(results, result)
	}
	return results, summarize(fixtures, results), nil
}

func readObservations(reader io.Reader, fixtures map[string]Fixture) (map[string]observationPair, error) {
	pairs := make(map[string]observationPair, len(fixtures))
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 4*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var observation Observation
		if err := json.Unmarshal(scanner.Bytes(), &observation); err != nil {
			return nil, fmt.Errorf("decode stage observation line %d: %w", line, err)
		}
		if _, exists := fixtures[observation.CaseID]; !exists {
			return nil, fmt.Errorf("decode stage observation line %d: unknown case_id %q", line, observation.CaseID)
		}
		pair := pairs[observation.CaseID]
		switch observation.Stage {
		case StageExtraction:
			if pair.extraction != nil {
				return nil, fmt.Errorf("decode stage observation line %d: duplicate extraction for %q", line, observation.CaseID)
			}
			copy := observation
			pair.extraction = &copy
		case StageRecall:
			if pair.recall != nil {
				return nil, fmt.Errorf("decode stage observation line %d: duplicate recall for %q", line, observation.CaseID)
			}
			copy := observation
			pair.recall = &copy
		default:
			return nil, fmt.Errorf("decode stage observation line %d: unknown stage %q", line, observation.Stage)
		}
		pairs[observation.CaseID] = pair
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stage observations: %w", err)
	}
	return pairs, nil
}

func summarize(fixtures FixtureSet, results []Result) Summary {
	summary := Summary{SchemaVersion: SchemaVersion, Dataset: fixtures.Dataset, Cases: len(results)}
	for _, result := range results {
		summary.RequiredAtoms += result.Extraction.RequiredAtoms
		if result.Extraction.Scored {
			summary.ExtractionScoredAtoms += result.Extraction.RequiredAtoms
			summary.ExtractionMatchedAtoms += result.Extraction.MatchedAtoms
			summary.UpstreamMissedAtoms += len(result.Extraction.MissingAtomIDs)
			summary.ExtractionLeakageItems += result.Extraction.LeakageItems
		} else {
			summary.ExtractionErrors++
		}
		if result.Recall.Scored {
			summary.RecallScoredAtoms += result.Recall.RequiredAtoms
			summary.RecallMatchedAtoms += result.Recall.MatchedAtoms
			summary.RecallLeakageItems += result.Recall.LeakageItems
		} else {
			summary.RecallErrors++
		}
		if result.Recall.ConditionalScored {
			summary.RecallMatchedAvailableAtoms += result.Recall.MatchedAvailableAtoms
			summary.RecallMissedAvailableAtoms += len(result.Recall.MissedAvailableAtomIDs)
		}
	}
	summary.ExtractionFactRecall = ratio(summary.ExtractionMatchedAtoms, summary.ExtractionScoredAtoms)
	summary.RecallGoldRecall = ratio(summary.RecallMatchedAtoms, summary.RecallScoredAtoms)
	available := summary.RecallMatchedAvailableAtoms + summary.RecallMissedAvailableAtoms
	summary.RecallConditionalRecall = ratio(summary.RecallMatchedAvailableAtoms, available)
	return summary
}

func WriteResultsJSONL(writer io.Writer, results []Result) error {
	encoder := json.NewEncoder(writer)
	for _, result := range results {
		if err := encoder.Encode(result); err != nil {
			return fmt.Errorf("encode stage result: %w", err)
		}
	}
	return nil
}

func WriteSummaryJSON(writer io.Writer, summary Summary) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(summary); err != nil {
		return fmt.Errorf("encode stage summary: %w", err)
	}
	return nil
}
