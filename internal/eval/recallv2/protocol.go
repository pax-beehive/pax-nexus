// Package recallv2 defines the recall-specific agent evaluation protocol on
// top of the durable v2 execution engine.
package recallv2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/eval/recallreplay"
	"github.com/pax-beehive/pax-nexus/internal/eval/stageeval"
	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
	v3 "github.com/pax-beehive/pax-nexus/internal/eval/v3"
	"gopkg.in/yaml.v3"
)

const (
	ConfigVersion = "recall-v2"
	ArmNoMemory   = "no_memory"
	ArmTeamNote   = "team_note"
	ArmHintRecall = "hint_recall_v0"
)

type Config struct {
	v2.Config `yaml:",inline"`
	Recall    ProtocolConfig `json:"recall" yaml:"recall"`
}

type ProtocolConfig struct {
	DeterministicReplay string `json:"deterministic_replay" yaml:"deterministic_replay"`
	HintReplay          string `json:"hint_replay" yaml:"hint_replay"`
	CaseAnnotations     string `json:"case_annotations" yaml:"case_annotations"`
	MinReplayCases      int    `json:"min_replay_cases" yaml:"min_replay_cases"`
	MinAgentCases       int    `json:"min_agent_cases" yaml:"min_agent_cases"`
	MaxAgentCases       int    `json:"max_agent_cases" yaml:"max_agent_cases"`
	// Diagnostic permits a small, category-balanced Agent cohort. It is not
	// eligible to replace hard-cohort acceptance evidence.
	Diagnostic bool `json:"diagnostic" yaml:"diagnostic"`
}

type caseAnnotations struct {
	Cases map[string]caseAnnotation `json:"cases"`
}

type caseAnnotation struct {
	SupportingAgentIDs []string `json:"supporting_agent_ids"`
	KnowledgeSource    string   `json:"knowledge_source_status"`
}

type CohortReport struct {
	Cases                  int            `json:"cases"`
	Categories             map[string]int `json:"categories"`
	TemporalCases          int            `json:"temporal_cases"`
	IdentityDependentCases int            `json:"identity_dependent_cases"`
	ReviewedSourceCases    int            `json:"reviewed_knowledge_source_cases"`
	StrictCrossAgentCases  int            `json:"strict_cross_agent_cases"`
}

func LoadConfig(path string) (Config, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read recall eval v2 config: %w", err)
	}
	var config Config
	if err := yaml.Unmarshal(input, &config); err != nil {
		return Config{}, fmt.Errorf("decode recall eval v2 config: %w", err)
	}
	if err := Validate(config); err != nil {
		return Config{}, err
	}
	replayDigest, err := fileDigest(config.Recall.DeterministicReplay)
	if err != nil {
		return Config{}, err
	}
	hintReplayDigest, err := fileDigest(config.Recall.HintReplay)
	if err != nil {
		return Config{}, err
	}
	annotationDigest, err := fileDigest(config.Recall.CaseAnnotations)
	if err != nil {
		return Config{}, err
	}
	manifestDigest, err := fileDigest(config.Run.Manifest)
	if err != nil {
		return Config{}, err
	}
	config.ProtocolMetadata = map[string]string{
		"protocol": ConfigVersion, "deterministic_replay_sha256": replayDigest,
		"case_annotations_sha256": annotationDigest,
		"hint_replay_sha256":      hintReplayDigest,
		"agent_manifest_sha256":   manifestDigest,
		"min_replay_cases":        fmt.Sprintf("%d", config.Recall.MinReplayCases),
		"min_agent_cases":         fmt.Sprintf("%d", config.Recall.MinAgentCases),
		"max_agent_cases":         fmt.Sprintf("%d", config.Recall.MaxAgentCases),
		"diagnostic":              fmt.Sprintf("%t", config.Recall.Diagnostic),
	}
	return config, nil
}

func fileDigest(path string) (string, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("digest recall eval v2 input %q: %w", path, err)
	}
	digest := sha256.Sum256(input)
	return hex.EncodeToString(digest[:]), nil
}

func LoadCases(manifestPath, annotationsPath, answererSeed string, minCases, maxCases int) ([]v2.Case, string, CohortReport, error) {
	return loadCases(manifestPath, annotationsPath, answererSeed, minCases, maxCases, true)
}

// LoadDiagnosticCases loads a compact, category-balanced cohort for fast
// candidate comparison. Its results remain diagnostic rather than acceptance
// evidence for the hard 30-case recall cohort.
func LoadDiagnosticCases(manifestPath, annotationsPath, answererSeed string, minCases, maxCases int) ([]v2.Case, string, CohortReport, error) {
	return loadCases(manifestPath, annotationsPath, answererSeed, minCases, maxCases, false)
}

func loadCases(manifestPath, annotationsPath, answererSeed string, minCases, maxCases int, hardCohort bool) ([]v2.Case, string, CohortReport, error) {
	cases, revision, err := v3.LoadCases(manifestPath, answererSeed)
	if err != nil {
		return nil, "", CohortReport{}, err
	}
	if !hardCohort && len(cases) > maxCases {
		cases = selectDiagnosticCases(cases, maxCases)
	}
	annotations, err := loadAnnotations(annotationsPath)
	if err != nil {
		return nil, "", CohortReport{}, err
	}
	if len(cases) < minCases || len(cases) > maxCases {
		return nil, "", CohortReport{}, fmt.Errorf("load recall eval v2 cases: cohort has %d cases, require %d to %d", len(cases), minCases, maxCases)
	}
	report := CohortReport{Cases: len(cases), Categories: make(map[string]int)}
	domainProducer := filepath.Join(filepath.Dir(manifestPath), "domain", "producer")
	seenQuestions := make(map[string]struct{}, len(cases))
	for index := range cases {
		evalCase := &cases[index]
		annotation, annotated := annotations.Cases[evalCase.ID]
		coverage, prepareErr := prepareCohortCase(evalCase, annotation, annotated, domainProducer, seenQuestions)
		if prepareErr != nil {
			return nil, "", CohortReport{}, prepareErr
		}
		report.Categories[evalCase.Category]++
		report.TemporalCases += coverage.temporal
		report.IdentityDependentCases += coverage.identity
		report.ReviewedSourceCases += coverage.reviewedSource
	}
	cases, err = v3.AssignAnswerers(cases, revision, answererSeed)
	if err != nil {
		return nil, "", CohortReport{}, err
	}
	for _, evalCase := range cases {
		if evalCase.StrictCrossAgent {
			report.StrictCrossAgentCases++
		}
	}
	if err := validateCohortCoverage(report, hardCohort); err != nil {
		return nil, "", CohortReport{}, err
	}
	return cases, revision, report, nil
}

// selectDiagnosticCases builds a fixed, category-balanced pilot cohort from a
// larger manifest without changing the hard-cohort source fixture.
func selectDiagnosticCases(cases []v2.Case, limit int) []v2.Case {
	byCategory := make(map[string][]v2.Case)
	for _, evalCase := range cases {
		byCategory[evalCase.Category] = append(byCategory[evalCase.Category], evalCase)
	}
	categories := make([]string, 0, len(byCategory))
	for category := range byCategory {
		categories = append(categories, category)
	}
	slices.Sort(categories)

	selected := make([]v2.Case, 0, limit)
	for index := 0; len(selected) < limit; index++ {
		added := false
		for _, category := range categories {
			candidates := byCategory[category]
			if index >= len(candidates) {
				continue
			}
			selected = append(selected, candidates[index])
			added = true
			if len(selected) == limit {
				return selected
			}
		}
		if !added {
			break
		}
	}
	return selected
}

func validateCohortCoverage(report CohortReport, hardCohort bool) error {
	if !hardCohort {
		if len(report.Categories) != 6 || report.TemporalCases < 2 || report.IdentityDependentCases < 1 {
			return fmt.Errorf("load recall eval v2 cases: diagnostic cohort must cover six categories plus temporal and identity cases")
		}
		return nil
	}
	if len(report.Categories) != 6 || report.TemporalCases < 8 || report.IdentityDependentCases < 4 || report.ReviewedSourceCases < 2 || report.StrictCrossAgentCases < 2 {
		return fmt.Errorf("load recall eval v2 cases: hard cohort must cover six categories, temporal, identity, and reviewed knowledge origins")
	}
	for _, count := range report.Categories {
		if count < 4 {
			return fmt.Errorf("load recall eval v2 cases: hard cohort must cover six categories, temporal, identity, and reviewed knowledge origins")
		}
	}
	return nil
}

type caseCoverage struct{ temporal, identity, reviewedSource int }

func prepareCohortCase(evalCase *v2.Case, annotation caseAnnotation, annotated bool, domainProducer string, seenQuestions map[string]struct{}) (caseCoverage, error) {
	questionKey := strings.ToLower(strings.TrimSpace(evalCase.Question))
	if _, duplicate := seenQuestions[questionKey]; duplicate {
		return caseCoverage{}, fmt.Errorf("load recall eval v2 cases: duplicate question %q", evalCase.Question)
	}
	seenQuestions[questionKey] = struct{}{}
	evalCase.ProducerWorkspace = domainProducer
	evalCase.KnowledgeSourceStatus = "unknown"
	evalCase.TemporalMode = "current"
	coverage := caseCoverage{}
	switch evalCase.Category {
	case "temporal":
		evalCase.TemporalMode = "temporal_question"
		coverage.temporal = 1
	case "knowledge_update":
		evalCase.TemporalMode = "current_with_supersession"
		coverage.temporal = 1
	case "user_implicit":
		coverage.identity = 1
	}
	if !annotated || annotation.KnowledgeSource != "reviewed" {
		return coverage, nil
	}
	if len(annotation.SupportingAgentIDs) == 0 {
		return caseCoverage{}, fmt.Errorf("load recall eval v2 case %q: reviewed knowledge source requires supporting agents", evalCase.ID)
	}
	evalCase.SupportingAgentIDs = slices.Clone(annotation.SupportingAgentIDs)
	evalCase.KnowledgeSourceStatus = "reviewed"
	coverage.reviewedSource = 1
	return coverage, nil
}

func ValidateReplay(path string, minCases int) error {
	fixtures, err := recallreplay.LoadFixtureSet(path)
	if err != nil {
		return err
	}
	if len(fixtures.Cases) < minCases {
		return fmt.Errorf("validate recall eval v2 replay: have %d independent cases, require at least %d", len(fixtures.Cases), minCases)
	}
	seen := make(map[string]struct{}, len(fixtures.Cases))
	seenContent := make(map[string]string, len(fixtures.Cases))
	for _, evalCase := range fixtures.Cases {
		caseID := evalCase.Fixture.CaseID
		if _, duplicate := seen[caseID]; duplicate {
			return fmt.Errorf("validate recall eval v2 replay: duplicate case %q", caseID)
		}
		seen[caseID] = struct{}{}
		content, err := json.Marshal(struct {
			Query             string
			RequiredAtoms     []stageeval.Atom
			CandidateSnapshot string
		}{evalCase.Fixture.RecallContext.Query, evalCase.Fixture.RequiredAtoms, evalCase.CandidateSnapshotSHA256})
		if err != nil {
			return fmt.Errorf("validate recall eval v2 replay case %q fingerprint: %w", caseID, err)
		}
		digest := sha256.Sum256(content)
		fingerprint := hex.EncodeToString(digest[:])
		if prior, duplicate := seenContent[fingerprint]; duplicate {
			return fmt.Errorf("validate recall eval v2 replay: cases %q and %q repeat the same query, gold atoms, and candidates", prior, caseID)
		}
		seenContent[fingerprint] = caseID
	}
	return nil
}

func ValidateReplayReport(report recallreplay.Report) error {
	summary := report.RecallEval
	if summary.CandidateRecallAtLimit < 0.80 {
		return fmt.Errorf("validate recall eval v2 replay: candidate recall %.3f is below hard-cohort floor 0.800", summary.CandidateRecallAtLimit)
	}
	if summary.RelationExpandedRecall < 0.95 {
		return fmt.Errorf("validate recall eval v2 replay: relation-expanded recall %.3f is below 0.950", summary.RelationExpandedRecall)
	}
	if summary.DeliveredConditionalRecall < 0.90 {
		return fmt.Errorf("validate recall eval v2 replay: conditional recall %.3f is below 0.900", summary.DeliveredConditionalRecall)
	}
	if summary.SupersededLeakageItems > 0 {
		return fmt.Errorf("validate recall eval v2 replay: superseded leakage must be zero")
	}
	if summary.AvailableAtoms > 0 && float64(summary.BudgetDroppedAtoms)/float64(summary.AvailableAtoms) > 0.05 {
		return fmt.Errorf("validate recall eval v2 replay: more than five percent of available atoms were budget-dropped")
	}
	for name, slice := range summary.TemporalSlices {
		if slice.EligibleAtoms > 0 && (slice.RelationExpandedRecall < 0.95 || slice.DeliveredConditionalRecall < 0.90) {
			return fmt.Errorf("validate recall eval v2 replay: temporal slice %q regressed", name)
		}
	}
	if summary.StrictCrossAgent.EligibleAtoms > 0 &&
		(summary.StrictCrossAgent.RelationExpandedRecall < 0.95 || summary.StrictCrossAgent.DeliveredConditionalRecall < 0.90) {
		return fmt.Errorf("validate recall eval v2 replay: strict cross-agent slice regressed")
	}
	type categoryTotals struct{ matched, available int }
	categories := make(map[string]categoryTotals)
	for _, evalCase := range report.Cases {
		current := categories[evalCase.Result.Category]
		current.matched += evalCase.Result.Recall.MatchedAvailableAtoms
		current.available += evalCase.Result.Recall.AvailableAtoms
		categories[evalCase.Result.Category] = current
	}
	for category, current := range categories {
		if current.available > 0 && ratio(current.matched, current.available) < 0.80 {
			return fmt.Errorf("validate recall eval v2 replay: category %q conditional recall is below 0.800", category)
		}
	}
	return nil
}

func loadAnnotations(path string) (caseAnnotations, error) {
	if strings.TrimSpace(path) == "" {
		return caseAnnotations{}, fmt.Errorf("load recall eval v2 annotations: path is required")
	}
	input, err := os.ReadFile(path)
	if err != nil {
		return caseAnnotations{}, fmt.Errorf("read recall eval v2 annotations: %w", err)
	}
	var annotations caseAnnotations
	if err := json.Unmarshal(input, &annotations); err != nil {
		return caseAnnotations{}, fmt.Errorf("decode recall eval v2 annotations: %w", err)
	}
	return annotations, nil
}

func Validate(config Config) error {
	if config.Version != ConfigVersion {
		return fmt.Errorf("validate recall eval v2 config: version must be %q", ConfigVersion)
	}
	if err := config.ValidateBase(); err != nil {
		return err
	}
	if config.BaselineArm != ArmNoMemory {
		return fmt.Errorf("validate recall eval v2 config: baseline_arm must be %q", ArmNoMemory)
	}
	if config.BeforeRun == nil {
		return fmt.Errorf("validate recall eval v2 config: before_run must construct production Team Note memory")
	}
	if config.Judge == nil {
		return fmt.Errorf("validate recall eval v2 config: judge is required")
	}
	if strings.TrimSpace(config.AnswererSeed) == "" {
		return fmt.Errorf("validate recall eval v2 config: answerer_seed is required")
	}
	if strings.TrimSpace(config.Recall.DeterministicReplay) == "" {
		return fmt.Errorf("validate recall eval v2 config: recall.deterministic_replay is required")
	}
	if strings.TrimSpace(config.Recall.HintReplay) == "" {
		return fmt.Errorf("validate recall eval v2 config: recall.hint_replay is required")
	}
	if strings.TrimSpace(config.Recall.CaseAnnotations) == "" {
		return fmt.Errorf("validate recall eval v2 config: recall.case_annotations is required")
	}
	if config.Recall.MinReplayCases < 30 {
		return fmt.Errorf("validate recall eval v2 config: min_replay_cases must be at least 30")
	}
	if config.Recall.Diagnostic {
		if config.Recall.MinAgentCases < 6 || config.Recall.MaxAgentCases > 15 || config.Recall.MaxAgentCases < config.Recall.MinAgentCases {
			return fmt.Errorf("validate recall eval v2 config: diagnostic agent cohort must contain 6 to 15 cases")
		}
	} else if config.Recall.MinAgentCases < 30 || config.Recall.MaxAgentCases > 50 || config.Recall.MaxAgentCases < config.Recall.MinAgentCases {
		return fmt.Errorf("validate recall eval v2 config: agent cohort must contain 30 to 50 cases")
	}
	names := make([]string, 0, len(config.Arms))
	for _, arm := range config.Arms {
		names = append(names, arm.Name)
	}
	slices.Sort(names)
	want := []string{ArmNoMemory, ArmTeamNote, ArmHintRecall}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		return fmt.Errorf("validate recall eval v2 config: arms must be %s, %s, and %s", ArmNoMemory, ArmTeamNote, ArmHintRecall)
	}
	return nil
}

func ValidateHintReplayReport(report recallreplay.Report) error {
	hint := report.HintEval
	if !hint.HintScored || hint.ScoredOpportunities < 12 {
		return fmt.Errorf("validate Hint Recall replay: require at least 12 scored opportunities")
	}
	if hint.HintPrecision < 0.80 || hint.HintRecall < 0.90 {
		return fmt.Errorf("validate Hint Recall replay: precision %.3f or recall %.3f is below gate", hint.HintPrecision, hint.HintRecall)
	}
	if hint.UnauthorizedLeakageItems+hint.UnauthorizedLeadInfluence+hint.WrongTimeLeakageItems+
		hint.WrongTimeLeadInfluence+hint.FutureLeakageItems+hint.FutureLeadInfluence+
		hint.ForbiddenLeakageItems+hint.SupersededLeakageItems+hint.ProvenanceErrors+
		hint.IdentityErrors+hint.TemporalPreservationErrors+hint.DeliveryClaimViolations > 0 {
		return fmt.Errorf("validate Hint Recall replay: safety, identity, temporal, provenance, or delivery violation")
	}
	return nil
}
