// Package stageeval scores observable memory stages while holding adjacent
// stages fixed.
package stageeval

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const SchemaVersion = "pax-stage-eval-v1"

type Stage string

const (
	StageExtraction Stage = "extraction"
	StageRecall     Stage = "recall"
)

type FixtureSet struct {
	SchemaVersion string    `json:"schema_version"`
	Dataset       string    `json:"dataset"`
	Cases         []Fixture `json:"cases"`
}

type Fixture struct {
	CaseID             string        `json:"case_id"`
	Category           string        `json:"category,omitempty"`
	SourceRevision     string        `json:"source_revision"`
	RecallContext      RecallContext `json:"recall_context"`
	RequiredAtoms      []Atom        `json:"required_atoms"`
	ForbiddenAtoms     []Atom        `json:"forbidden_atoms,omitempty"`
	ForbiddenEventIDs  []string      `json:"forbidden_event_ids,omitempty"`
	SupersededEventIDs []string      `json:"superseded_event_ids,omitempty"`
}

type RecallContext struct {
	ConsumerUserID    string `json:"consumer_user_id"`
	ConsumerAgentID   string `json:"consumer_agent_id,omitempty"`
	ConsumerSessionID string `json:"consumer_session_id,omitempty"`
	Query             string `json:"query"`
	TokenBudget       int    `json:"token_budget"`
}

type Atom struct {
	ID                 string   `json:"id"`
	Patterns           []string `json:"patterns"`
	SupportingEventIDs []string `json:"supporting_event_ids,omitempty"`
}

type Observation struct {
	CaseID         string            `json:"case_id"`
	Stage          Stage             `json:"stage"`
	SourceRevision string            `json:"source_revision"`
	RecallContext  *RecallContext    `json:"recall_context,omitempty"`
	Provenance     map[string]string `json:"provenance,omitempty"`
	Items          []Item            `json:"items"`
	DurationMS     int64             `json:"duration_ms,omitempty"`
	Error          string            `json:"error,omitempty"`
}

type Item struct {
	ID               string   `json:"id"`
	Text             string   `json:"text"`
	EvidenceEventIDs []string `json:"evidence_event_ids,omitempty"`
}

type Result struct {
	CaseID     string           `json:"case_id"`
	Category   string           `json:"category,omitempty"`
	Extraction ExtractionResult `json:"extraction"`
	Recall     RecallResult     `json:"recall"`
}

type ExtractionResult struct {
	Scored            bool     `json:"scored"`
	RequiredAtoms     int      `json:"required_atoms"`
	MatchedAtoms      int      `json:"matched_atoms"`
	FactRecall        float64  `json:"fact_recall"`
	EvidencePrecision float64  `json:"evidence_precision"`
	EvidenceRecall    float64  `json:"evidence_recall"`
	LeakageItems      int      `json:"leakage_items"`
	MissingAtomIDs    []string `json:"missing_atom_ids,omitempty"`
	DurationMS        int64    `json:"duration_ms,omitempty"`
	Error             string   `json:"error,omitempty"`
}

type RecallResult struct {
	Scored                 bool     `json:"scored"`
	ConditionalScored      bool     `json:"conditional_scored"`
	RequiredAtoms          int      `json:"required_atoms"`
	AvailableAtoms         int      `json:"available_atoms"`
	MatchedAtoms           int      `json:"matched_atoms"`
	MatchedAvailableAtoms  int      `json:"matched_available_atoms"`
	GoldRecall             float64  `json:"gold_recall"`
	ConditionalRecall      float64  `json:"conditional_recall"`
	ContextPrecision       float64  `json:"context_precision"`
	LeakageItems           int      `json:"leakage_items"`
	MissingAtomIDs         []string `json:"missing_atom_ids,omitempty"`
	MissedAvailableAtomIDs []string `json:"missed_available_atom_ids,omitempty"`
	DurationMS             int64    `json:"duration_ms,omitempty"`
	Error                  string   `json:"error,omitempty"`
}

func Evaluate(fixture Fixture, extraction, recall Observation) (Result, error) {
	compiled, err := compileFixture(fixture)
	if err != nil {
		return Result{}, err
	}
	if err := validateObservation(fixture, StageExtraction, extraction); err != nil {
		return Result{}, err
	}
	if err := validateObservation(fixture, StageRecall, recall); err != nil {
		return Result{}, err
	}
	if extraction.Error == "" && recall.Error == "" {
		if err := validateRecallItems(extraction.Items, recall.Items); err != nil {
			return Result{}, err
		}
	}

	extractionItems := []Item(nil)
	extractionMatches := make(map[string][]int)
	if extraction.Error == "" {
		extractionItems = extraction.Items
		extractionMatches = matchAtoms(compiled.required, extractionItems)
	}
	recallItems := []Item(nil)
	recallMatches := make(map[string][]int)
	if recall.Error == "" {
		recallItems = recall.Items
		recallMatches = matchAtoms(compiled.required, recallItems)
	}
	available := keys(extractionMatches)
	matchedAvailable := matchedPairedAtomIDs(compiled.required, extractionMatches, extractionItems, recallItems)
	var extractionMissing, recallMissing []string
	if extraction.Error == "" {
		extractionMissing = missingIDs(compiled.required, extractionMatches)
	}
	if recall.Error == "" {
		recallMissing = missingIDs(compiled.required, recallMatches)
	}
	missedAvailable := difference(available, matchedAvailable)

	evidenceValid, evidenceCited, evidenceGold := evidenceCounts(compiled.required, extractionItems, extractionMatches)
	return Result{
		CaseID: fixture.CaseID, Category: fixture.Category,
		Extraction: ExtractionResult{
			Scored:        extraction.Error == "",
			RequiredAtoms: len(compiled.required), MatchedAtoms: len(extractionMatches),
			FactRecall:        ratio(len(extractionMatches), len(compiled.required)),
			EvidencePrecision: ratio(evidenceValid, evidenceCited), EvidenceRecall: ratio(evidenceValid, evidenceGold),
			LeakageItems: leakageCount(compiled, extractionItems), MissingAtomIDs: extractionMissing,
			DurationMS: extraction.DurationMS, Error: extraction.Error,
		},
		Recall: RecallResult{
			Scored: recall.Error == "", ConditionalScored: extraction.Error == "" && recall.Error == "",
			RequiredAtoms: len(compiled.required), AvailableAtoms: len(available), MatchedAtoms: len(recallMatches),
			MatchedAvailableAtoms: len(matchedAvailable), GoldRecall: ratio(len(recallMatches), len(compiled.required)),
			ConditionalRecall: ratio(len(matchedAvailable), len(available)), ContextPrecision: contextPrecision(compiled, recallItems),
			LeakageItems: leakageCount(compiled, recallItems), MissingAtomIDs: recallMissing,
			MissedAvailableAtomIDs: missedAvailable, DurationMS: recall.DurationMS, Error: recall.Error,
		},
	}, nil
}

type compiledAtom struct {
	Atom
	patterns []*regexp.Regexp
}

type compiledFixture struct {
	required        []compiledAtom
	forbidden       []compiledAtom
	forbiddenEvents map[string]struct{}
}

func compileFixture(fixture Fixture) (compiledFixture, error) {
	if strings.TrimSpace(fixture.CaseID) == "" {
		return compiledFixture{}, fmt.Errorf("validate stage fixture: case_id is required")
	}
	if len(fixture.RequiredAtoms) == 0 {
		return compiledFixture{}, fmt.Errorf("validate stage fixture %q: required_atoms are required", fixture.CaseID)
	}
	if len(fixture.SourceRevision) != 64 {
		return compiledFixture{}, fmt.Errorf("validate stage fixture %q: source_revision must be a SHA-256 hex digest", fixture.CaseID)
	}
	if _, err := hex.DecodeString(fixture.SourceRevision); err != nil {
		return compiledFixture{}, fmt.Errorf("validate stage fixture %q source_revision: %w", fixture.CaseID, err)
	}
	if err := validateRecallContext(fixture.RecallContext); err != nil {
		return compiledFixture{}, fmt.Errorf("validate stage fixture %q recall context: %w", fixture.CaseID, err)
	}
	seen := make(map[string]struct{}, len(fixture.RequiredAtoms)+len(fixture.ForbiddenAtoms))
	required, err := compileAtoms("required", fixture.RequiredAtoms, seen)
	if err != nil {
		return compiledFixture{}, err
	}
	forbidden, err := compileAtoms("forbidden", fixture.ForbiddenAtoms, seen)
	if err != nil {
		return compiledFixture{}, err
	}
	forbiddenEvents := make(map[string]struct{}, len(fixture.ForbiddenEventIDs)+len(fixture.SupersededEventIDs))
	for _, eventID := range append(fixture.ForbiddenEventIDs, fixture.SupersededEventIDs...) {
		forbiddenEvents[eventID] = struct{}{}
	}
	return compiledFixture{required: required, forbidden: forbidden, forbiddenEvents: forbiddenEvents}, nil
}

func compileAtoms(label string, atoms []Atom, seen map[string]struct{}) ([]compiledAtom, error) {
	compiled := make([]compiledAtom, 0, len(atoms))
	for _, atom := range atoms {
		if strings.TrimSpace(atom.ID) == "" || len(atom.Patterns) == 0 {
			return nil, fmt.Errorf("validate stage fixture: %s atom id and patterns are required", label)
		}
		if _, exists := seen[atom.ID]; exists {
			return nil, fmt.Errorf("validate stage fixture: duplicate atom id %q", atom.ID)
		}
		seen[atom.ID] = struct{}{}
		entry := compiledAtom{Atom: atom, patterns: make([]*regexp.Regexp, 0, len(atom.Patterns))}
		for _, pattern := range atom.Patterns {
			value, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("validate stage fixture atom %q pattern: %w", atom.ID, err)
			}
			entry.patterns = append(entry.patterns, value)
		}
		compiled = append(compiled, entry)
	}
	return compiled, nil
}

func validateObservation(fixture Fixture, stage Stage, observation Observation) error {
	if observation.CaseID != fixture.CaseID {
		return fmt.Errorf("validate %s observation: case_id %q does not match fixture %q", stage, observation.CaseID, fixture.CaseID)
	}
	if observation.Stage != stage {
		return fmt.Errorf("validate %s observation: stage is %q", stage, observation.Stage)
	}
	if observation.SourceRevision != fixture.SourceRevision {
		return fmt.Errorf("validate %s observation: source_revision does not match fixture", stage)
	}
	if stage == StageRecall && (observation.RecallContext == nil || *observation.RecallContext != fixture.RecallContext) {
		return fmt.Errorf("validate recall observation: recall_context does not match fixture")
	}
	seen := make(map[string]struct{}, len(observation.Items))
	for _, item := range observation.Items {
		if strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("validate %s observation: item id is required", stage)
		}
		if _, exists := seen[item.ID]; exists {
			return fmt.Errorf("validate %s observation: duplicate item id %q", stage, item.ID)
		}
		seen[item.ID] = struct{}{}
		evidenceSeen := make(map[string]struct{}, len(item.EvidenceEventIDs))
		for _, eventID := range item.EvidenceEventIDs {
			if _, exists := evidenceSeen[eventID]; exists {
				return fmt.Errorf("validate %s observation: item %q has duplicate evidence event %q", stage, item.ID, eventID)
			}
			evidenceSeen[eventID] = struct{}{}
		}
	}
	return nil
}

func validateRecallContext(context RecallContext) error {
	if strings.TrimSpace(context.ConsumerUserID) == "" || strings.TrimSpace(context.Query) == "" || context.TokenBudget <= 0 {
		return fmt.Errorf("consumer_user_id, query, and positive token_budget are required")
	}
	return nil
}

func validateRecallItems(extraction, recall []Item) error {
	extracted := make(map[string]struct{}, len(extraction))
	for _, item := range extraction {
		extracted[item.ID] = struct{}{}
	}
	for _, item := range recall {
		if _, exists := extracted[item.ID]; !exists {
			return fmt.Errorf("validate recall observation: item %q is absent from extraction observation", item.ID)
		}
	}
	return nil
}

func matchAtoms(atoms []compiledAtom, items []Item) map[string][]int {
	matches := make(map[string][]int)
	for _, atom := range atoms {
		for index, item := range items {
			if atomMatches(atom, item.Text) {
				matches[atom.ID] = append(matches[atom.ID], index)
			}
		}
	}
	return matches
}

func atomMatches(atom compiledAtom, text string) bool {
	for _, pattern := range atom.patterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

func evidenceCounts(atoms []compiledAtom, items []Item, matches map[string][]int) (int, int, int) {
	goldEvents := make(map[string]struct{})
	for _, atom := range atoms {
		for _, eventID := range atom.SupportingEventIDs {
			goldEvents[eventID] = struct{}{}
		}
	}
	citedEvents := make(map[string]struct{})
	for _, item := range items {
		for _, eventID := range item.EvidenceEventIDs {
			citedEvents[eventID] = struct{}{}
		}
	}
	validEvents := make(map[string]struct{})
	for _, atom := range atoms {
		indices, matched := matches[atom.ID]
		if !matched {
			continue
		}
		expected := stringSet(atom.SupportingEventIDs)
		for _, index := range indices {
			for _, eventID := range items[index].EvidenceEventIDs {
				if _, ok := expected[eventID]; ok {
					validEvents[eventID] = struct{}{}
				}
			}
		}
	}
	return len(validEvents), len(citedEvents), len(goldEvents)
}

func leakageCount(fixture compiledFixture, items []Item) int {
	count := 0
	for _, item := range items {
		leaked := false
		for _, atom := range fixture.forbidden {
			leaked = leaked || atomMatches(atom, item.Text)
		}
		for _, eventID := range item.EvidenceEventIDs {
			_, forbidden := fixture.forbiddenEvents[eventID]
			leaked = leaked || forbidden
		}
		if leaked {
			count++
		}
	}
	return count
}

func contextPrecision(fixture compiledFixture, items []Item) float64 {
	useful := 0
	for _, item := range items {
		for _, atom := range fixture.required {
			if atomMatches(atom, item.Text) {
				useful++
				break
			}
		}
	}
	return ratio(useful, len(items))
}

func missingIDs(atoms []compiledAtom, matches map[string][]int) []string {
	missing := make([]string, 0)
	for _, atom := range atoms {
		if _, ok := matches[atom.ID]; !ok {
			missing = append(missing, atom.ID)
		}
	}
	sort.Strings(missing)
	return missing
}

func keys(values map[string][]int) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func matchedPairedAtomIDs(atoms []compiledAtom, extractionMatches map[string][]int, extraction, recall []Item) []string {
	result := make([]string, 0)
	for _, atom := range atoms {
		extractionIndices, available := extractionMatches[atom.ID]
		if !available {
			continue
		}
		extractedIDs := make(map[string]struct{}, len(extractionIndices))
		for _, index := range extractionIndices {
			extractedIDs[extraction[index].ID] = struct{}{}
		}
		for _, item := range recall {
			_, extracted := extractedIDs[item.ID]
			if extracted && atomMatches(atom, item.Text) {
				result = append(result, atom.ID)
				break
			}
		}
	}
	sort.Strings(result)
	return result
}

func difference(left, right []string) []string {
	rightSet := stringSet(right)
	result := make([]string, 0)
	for _, value := range left {
		if _, ok := rightSet[value]; !ok {
			result = append(result, value)
		}
	}
	return result
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
