package recallv2

import v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"

const (
	ReasonTrialIncomplete     = "trial_incomplete"
	ReasonTrialMissing        = "trial_missing"
	ReasonAgentSessionMissing = "agent_session_missing"
	ReasonRecallNotObserved   = "recall_not_observed"
	ReasonRecallFailed        = "recall_failed"
	ReasonProviderCallMissing = "provider_call_missing"
	ReasonTeamProviderMissing = "team_provider_missing"
	ReasonBaselineRecallLeak  = "baseline_recall_contamination"
	ReasonJudgeMissing        = "judge_missing"
	ReasonJudgeSessionMissing = "judge_session_missing"
	ReasonJudgeSessionReused  = "judge_session_reused"
	ReasonAgentSessionReused  = "agent_session_reused"
)

type AgentReport struct {
	SchemaVersion     string                   `json:"schema_version"`
	ExpectedTrials    int                      `json:"expected_trials"`
	ScoredTrials      int                      `json:"scored_trials"`
	UnscoredTrials    int                      `json:"unscored_trials"`
	UnscoredReasons   map[string]int           `json:"unscored_reasons"`
	AgentCoverage     float64                  `json:"agent_execution_coverage"`
	PairedCases       int                      `json:"paired_cases"`
	CandidateWins     int                      `json:"candidate_wins"`
	CandidateLosses   int                      `json:"candidate_losses"`
	CandidateTies     int                      `json:"candidate_ties"`
	CandidateAccuracy float64                  `json:"candidate_accuracy"`
	Trials            []TrialAudit             `json:"trials"`
	CategorySlices    map[string]AccuracySlice `json:"category_slices"`
	TemporalSlices    map[string]AccuracySlice `json:"temporal_slices"`
	IdentitySlices    map[string]AccuracySlice `json:"identity_slices"`
	KnowledgeSlices   map[string]AccuracySlice `json:"knowledge_source_slices"`
	Regressions       []string                 `json:"regressions,omitempty"`
}

type AccuracySlice struct {
	Pairs             int     `json:"pairs"`
	BaselineCorrect   int     `json:"baseline_correct"`
	CandidateCorrect  int     `json:"candidate_correct"`
	BaselineAccuracy  float64 `json:"baseline_accuracy"`
	CandidateAccuracy float64 `json:"candidate_accuracy"`
	AccuracyDelta     float64 `json:"accuracy_delta"`
}

type TrialAudit struct {
	CaseID                string         `json:"case_id"`
	Arm                   string         `json:"arm"`
	Category              string         `json:"category"`
	TemporalMode          string         `json:"temporal_mode"`
	KnowledgeSourceStatus string         `json:"knowledge_source_status"`
	StrictCrossAgent      bool           `json:"strict_cross_agent"`
	SessionID             string         `json:"session_id,omitempty"`
	RecallProviders       map[string]int `json:"recall_providers,omitempty"`
	Scored                bool           `json:"scored"`
	Reason                string         `json:"reason,omitempty"`
}

func Audit(results []v2.TrialResult, expectedCases int) AgentReport {
	report := AgentReport{
		SchemaVersion: "pax-recall-eval-v2.1", ExpectedTrials: expectedCases * 2,
		UnscoredReasons: make(map[string]int), CategorySlices: make(map[string]AccuracySlice),
		TemporalSlices: make(map[string]AccuracySlice), IdentitySlices: make(map[string]AccuracySlice),
		KnowledgeSlices: make(map[string]AccuracySlice),
	}
	pairs, candidateCorrect, candidateScored := collectScoredResults(results, &report)
	report.UnscoredTrials = report.ExpectedTrials - report.ScoredTrials
	if report.UnscoredTrials < 0 {
		report.UnscoredTrials = 0
	}
	observedUnscored := 0
	for _, count := range report.UnscoredReasons {
		observedUnscored += count
	}
	if missing := report.UnscoredTrials - observedUnscored; missing > 0 {
		report.UnscoredReasons[ReasonTrialMissing] = missing
	}
	report.AgentCoverage = ratio(report.ScoredTrials, report.ExpectedTrials)
	report.CandidateAccuracy = ratio(candidateCorrect, candidateScored)
	for _, current := range pairs {
		if current.baseline == nil || current.candidate == nil {
			continue
		}
		report.PairedCases++
		addSlice(report.CategorySlices, current.candidate.Category, *current.baseline, *current.candidate)
		addSlice(report.TemporalSlices, current.candidate.TemporalMode, *current.baseline, *current.candidate)
		identity := "non_strict_cross_agent"
		if current.candidate.StrictCrossAgent {
			identity = "strict_cross_agent"
		}
		addSlice(report.IdentitySlices, identity, *current.baseline, *current.candidate)
		addSlice(report.KnowledgeSlices, current.candidate.KnowledgeSourceStatus, *current.baseline, *current.candidate)
		switch {
		case current.candidate.Correct && !current.baseline.Correct:
			report.CandidateWins++
		case !current.candidate.Correct && current.baseline.Correct:
			report.CandidateLosses++
		default:
			report.CandidateTies++
		}
	}
	finalizeSlices("category", report.CategorySlices, &report.Regressions)
	finalizeSlices("temporal", report.TemporalSlices, &report.Regressions)
	finalizeSlices("identity", report.IdentitySlices, &report.Regressions)
	finalizeSlices("knowledge_source", report.KnowledgeSlices, &report.Regressions)
	return report
}

type resultPair struct{ baseline, candidate *v2.TrialResult }

func collectScoredResults(results []v2.TrialResult, report *AgentReport) (map[string]resultPair, int, int) {
	sessionCounts := make(map[string]int, len(results))
	judgeSessionCounts := make(map[string]int, len(results))
	for _, result := range results {
		if result.SessionID != "" {
			sessionCounts[result.SessionID]++
		}
		if result.JudgeSessionID != "" {
			judgeSessionCounts[result.JudgeSessionID]++
		}
	}
	pairs := make(map[string]resultPair)
	candidateCorrect, candidateScored := 0, 0
	for index := range results {
		result := &results[index]
		reason := evidenceFailure(*result, sessionCounts, judgeSessionCounts)
		report.Trials = append(report.Trials, trialAudit(*result, reason))
		if reason != "" {
			report.UnscoredReasons[reason]++
			continue
		}
		report.ScoredTrials++
		current := pairs[result.CaseID]
		switch result.Arm {
		case ArmNoMemory:
			current.baseline = result
		case ArmTeamNote:
			current.candidate = result
			candidateScored++
			if result.Correct {
				candidateCorrect++
			}
		}
		pairs[result.CaseID] = current
	}
	return pairs, candidateCorrect, candidateScored
}

func trialAudit(result v2.TrialResult, reason string) TrialAudit {
	return TrialAudit{
		CaseID: result.CaseID, Arm: result.Arm, Category: result.Category,
		TemporalMode: result.TemporalMode, KnowledgeSourceStatus: result.KnowledgeSourceStatus,
		StrictCrossAgent: result.StrictCrossAgent, SessionID: result.SessionID,
		RecallProviders: result.MemoryRecallProviders, Scored: reason == "", Reason: reason,
	}
}

func addSlice(target map[string]AccuracySlice, key string, baseline, candidate v2.TrialResult) {
	if key == "" {
		key = "unknown"
	}
	current := target[key]
	current.Pairs++
	if baseline.Correct {
		current.BaselineCorrect++
	}
	if candidate.Correct {
		current.CandidateCorrect++
	}
	target[key] = current
}

func finalizeSlices(dimension string, slices map[string]AccuracySlice, regressions *[]string) {
	for key, current := range slices {
		current.BaselineAccuracy = ratio(current.BaselineCorrect, current.Pairs)
		current.CandidateAccuracy = ratio(current.CandidateCorrect, current.Pairs)
		current.AccuracyDelta = current.CandidateAccuracy - current.BaselineAccuracy
		slices[key] = current
		if current.AccuracyDelta < 0 {
			*regressions = append(*regressions, dimension+":"+key)
		}
	}
}

func evidenceFailure(result v2.TrialResult, sessionCounts, judgeSessionCounts map[string]int) string {
	if result.Status != "completed" {
		return ReasonTrialIncomplete
	}
	if result.SessionID == "" {
		return ReasonAgentSessionMissing
	}
	if sessionCounts[result.SessionID] > 1 {
		return ReasonAgentSessionReused
	}
	if result.Arm == ArmNoMemory && (result.MemoryRecallObserved || result.MemoryRecallProviderCalls > 0 || len(result.MemoryRecallProviders) > 0) {
		return ReasonBaselineRecallLeak
	}
	if result.Arm == ArmTeamNote {
		if !result.MemoryRecallObserved {
			return ReasonRecallNotObserved
		}
		if !result.MemoryRecallSuccess {
			return ReasonRecallFailed
		}
		if result.MemoryRecallProviderCalls < 1 {
			return ReasonProviderCallMissing
		}
		if result.MemoryRecallProviders["team"] < 1 {
			return ReasonTeamProviderMissing
		}
	}
	if !result.Judged {
		return ReasonJudgeMissing
	}
	if result.JudgeSessionID == "" {
		return ReasonJudgeSessionMissing
	}
	if result.JudgeSessionID == result.SessionID || judgeSessionCounts[result.JudgeSessionID] > 1 {
		return ReasonJudgeSessionReused
	}
	return ""
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
