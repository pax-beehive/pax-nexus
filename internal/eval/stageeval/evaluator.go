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
	MaxItems          int    `json:"max_items,omitempty"`
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
	SourceItemIDs    []string `json:"source_item_ids,omitempty"`
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
	Scored                 bool            `json:"scored"`
	RequiredAtoms          int             `json:"required_atoms"`
	MatchedAtoms           int             `json:"matched_atoms"`
	FactRecall             float64         `json:"fact_recall"`
	EvidencePrecision      float64         `json:"evidence_precision"`
	EvidenceRecall         float64         `json:"evidence_recall"`
	LeakageItems           int             `json:"leakage_items"`
	SuppressedLeakageItems int             `json:"suppressed_leakage_items"`
	LeakageDetails         []LeakageDetail `json:"leakage_details,omitempty"`
	MissingAtomIDs         []string        `json:"missing_atom_ids,omitempty"`
	DurationMS             int64           `json:"duration_ms,omitempty"`
	Error                  string          `json:"error,omitempty"`
}

type RecallResult struct {
	Scored                 bool            `json:"scored"`
	ConditionalScored      bool            `json:"conditional_scored"`
	RequiredAtoms          int             `json:"required_atoms"`
	AvailableAtoms         int             `json:"available_atoms"`
	MatchedAtoms           int             `json:"matched_atoms"`
	MatchedAvailableAtoms  int             `json:"matched_available_atoms"`
	GoldRecall             float64         `json:"gold_recall"`
	ConditionalRecall      float64         `json:"conditional_recall"`
	ContextPrecision       float64         `json:"context_precision"`
	LeakageItems           int             `json:"leakage_items"`
	SuppressedLeakageItems int             `json:"suppressed_leakage_items"`
	LeakageDetails         []LeakageDetail `json:"leakage_details,omitempty"`
	MissingAtomIDs         []string        `json:"missing_atom_ids,omitempty"`
	MissedAvailableAtomIDs []string        `json:"missed_available_atom_ids,omitempty"`
	DurationMS             int64           `json:"duration_ms,omitempty"`
	Error                  string          `json:"error,omitempty"`
}

// LeakageDetail audits one forbidden trigger on a delivered item: either a
// forbidden-atom regex match or a forbidden event citation. Suppressed marks
// an atom match neutralized by a conservative negation cue; event citations
// are never suppressed.
type LeakageDetail struct {
	ItemID         string `json:"item_id"`
	AtomID         string `json:"atom_id,omitempty"`
	EventID        string `json:"event_id,omitempty"`
	Suppressed     bool   `json:"suppressed"`
	SuppressionCue string `json:"suppression_cue,omitempty"`
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
	extractionLeakage, extractionSuppressed, extractionLeakageDetails := evaluateLeakage(compiled, extractionItems)
	recallLeakage, recallSuppressed, recallLeakageDetails := evaluateLeakage(compiled, recallItems)
	return Result{
		CaseID: fixture.CaseID, Category: fixture.Category,
		Extraction: ExtractionResult{
			Scored:        extraction.Error == "",
			RequiredAtoms: len(compiled.required), MatchedAtoms: len(extractionMatches),
			FactRecall:        ratio(len(extractionMatches), len(compiled.required)),
			EvidencePrecision: ratio(evidenceValid, evidenceCited), EvidenceRecall: ratio(evidenceValid, evidenceGold),
			LeakageItems: extractionLeakage, SuppressedLeakageItems: extractionSuppressed,
			LeakageDetails: extractionLeakageDetails, MissingAtomIDs: extractionMissing,
			DurationMS: extraction.DurationMS, Error: extraction.Error,
		},
		Recall: RecallResult{
			Scored: recall.Error == "", ConditionalScored: extraction.Error == "" && recall.Error == "",
			RequiredAtoms: len(compiled.required), AvailableAtoms: len(available), MatchedAtoms: len(recallMatches),
			MatchedAvailableAtoms: len(matchedAvailable), GoldRecall: ratio(len(recallMatches), len(compiled.required)),
			ConditionalRecall: ratio(len(matchedAvailable), len(available)), ContextPrecision: contextPrecision(compiled, recallItems),
			LeakageItems: recallLeakage, SuppressedLeakageItems: recallSuppressed,
			LeakageDetails: recallLeakageDetails, MissingAtomIDs: recallMissing,
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
	if len(fixture.RequiredAtoms) == 0 && len(fixture.ForbiddenAtoms) == 0 &&
		len(fixture.ForbiddenEventIDs) == 0 && len(fixture.SupersededEventIDs) == 0 {
		return compiledFixture{}, fmt.Errorf("validate stage fixture %q: at least one required or forbidden expectation is required", fixture.CaseID)
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
		sourceSeen := make(map[string]struct{}, len(item.SourceItemIDs))
		for _, sourceID := range item.SourceItemIDs {
			if strings.TrimSpace(sourceID) == "" {
				return fmt.Errorf("validate %s observation: item %q has an empty source item id", stage, item.ID)
			}
			if _, exists := sourceSeen[sourceID]; exists {
				return fmt.Errorf("validate %s observation: item %q has duplicate source item %q", stage, item.ID, sourceID)
			}
			sourceSeen[sourceID] = struct{}{}
		}
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
	if strings.TrimSpace(context.ConsumerUserID) == "" || strings.TrimSpace(context.Query) == "" || context.TokenBudget <= 0 || context.MaxItems < 0 {
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
		for _, sourceID := range sourceItemIDs(item) {
			if _, exists := extracted[sourceID]; !exists {
				return fmt.Errorf("validate recall observation: source item %q is absent from extraction observation", sourceID)
			}
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

// negationCuePattern lists conservative negation cues. Word boundaries keep
// false friends such as "noted" from matching "not"; "n't" anchors only on
// the right so contractions like "isn't" still match.
var negationCuePattern = regexp.MustCompile(`(?i)\bnot\b|n't\b|\bnever\b|\bno longer\b|\bneither\b|\bnor\b`)

// sentenceDelimiters bound the clause scope inspected for negation cues.
const sentenceDelimiters = ".;!?\n"

// evaluateLeakage scores forbidden triggers over delivered items. The raw
// count stays identical to the pre-negation-aware metric so earlier run
// records remain comparable; the suppressed count covers items whose every
// trigger is a negation-suppressed atom match.
func evaluateLeakage(fixture compiledFixture, items []Item) (raw, suppressed int, details []LeakageDetail) {
	for _, item := range items {
		leaked, unsuppressed, itemDetails := itemLeakage(fixture, item)
		if !leaked {
			continue
		}
		raw++
		if !unsuppressed {
			suppressed++
		}
		details = append(details, itemDetails...)
	}
	return raw, suppressed, details
}

// itemLeakage classifies every forbidden trigger on one item. It reports
// whether the item leaked at all and whether any trigger survives negation
// suppression. Event citations are never suppressed.
func itemLeakage(fixture compiledFixture, item Item) (leaked, unsuppressed bool, details []LeakageDetail) {
	for _, atom := range fixture.forbidden {
		matched, atomSuppressed, cue := classifyAtomMatch(atom, item.Text)
		if !matched {
			continue
		}
		leaked = true
		if !atomSuppressed {
			unsuppressed = true
		}
		details = append(details, LeakageDetail{
			ItemID: item.ID, AtomID: atom.ID, Suppressed: atomSuppressed, SuppressionCue: cue,
		})
	}
	for _, eventID := range item.EvidenceEventIDs {
		if _, forbidden := fixture.forbiddenEvents[eventID]; !forbidden {
			continue
		}
		leaked = true
		unsuppressed = true
		details = append(details, LeakageDetail{ItemID: item.ID, EventID: eventID})
	}
	return leaked, unsuppressed, details
}

// classifyAtomMatch reports whether the atom matches the text and whether
// every match occurrence is negation-suppressed. A single unsuppressed
// occurrence keeps the match counted as genuine leakage.
func classifyAtomMatch(atom compiledAtom, text string) (matched, suppressed bool, cue string) {
	for _, pattern := range atom.patterns {
		for _, span := range pattern.FindAllStringIndex(text, -1) {
			matched = true
			occurrenceSuppressed, occurrenceCue := occurrenceSuppressed(text, span[0], span[1])
			if !occurrenceSuppressed {
				return true, false, ""
			}
			if cue == "" {
				cue = occurrenceCue
			}
		}
	}
	return matched, matched, cue
}

// occurrenceSuppressed reports whether a conservative negation cue appears
// inside the matched span or its enclosing sentence. The cue is matched
// case-insensitively and reported lowercased.
func occurrenceSuppressed(text string, start, end int) (bool, string) {
	if cue := negationCueIn(text[start:end]); cue != "" {
		return true, cue
	}
	lo, hi := sentenceBounds(text, start, end)
	cue := negationCueIn(text[lo:hi])
	return cue != "", cue
}

// sentenceBounds returns the bounds of the sentence enclosing the span,
// splitting on '.', ';', '!', '?', and newlines.
func sentenceBounds(text string, start, end int) (int, int) {
	lo := strings.LastIndexAny(text[:start], sentenceDelimiters) + 1
	hi := len(text)
	if offset := strings.IndexAny(text[end:], sentenceDelimiters); offset >= 0 {
		hi = end + offset
	}
	return lo, hi
}

func negationCueIn(text string) string {
	return strings.ToLower(negationCuePattern.FindString(text))
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
			if sourceItemsIntersect(sourceItemIDs(item), extractedIDs) && atomMatches(atom, item.Text) {
				result = append(result, atom.ID)
				break
			}
		}
	}
	sort.Strings(result)
	return result
}

func sourceItemIDs(item Item) []string {
	if len(item.SourceItemIDs) > 0 {
		return item.SourceItemIDs
	}
	return []string{item.ID}
}

func sourceItemsIntersect(sourceIDs []string, extractedIDs map[string]struct{}) bool {
	for _, sourceID := range sourceIDs {
		if _, exists := extractedIDs[sourceID]; exists {
			return true
		}
	}
	return false
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
