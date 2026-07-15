package render

import (
	"fmt"
	"sort"
	"time"

	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
)

type reportData struct {
	RunID           string
	Dataset         string
	DatasetRevision string
	GeneratedAt     string
	TotalTrials     int
	FailedTrials    int
	CaseCount       int
	CostScope       string
	Runtime         []keyValue
	Arms            []armStat
	BaselineArm     string
	CandidateArm    string
	Pairwise        []pairwiseSummary
	Categories      []categoryRow
	QualityChart    barChart
	CostChart       barChart
	DurationChart   barChart
	AcceptanceCases []fieldNote
	FieldNotes      []fieldNote
	CaseGroups      []caseGroup
}

type keyValue struct {
	Key   string
	Value string
}

type armStat struct {
	Name            string
	SeriesClass     string
	IsBaseline      bool
	MeanF1Display   string
	CostDisplay     string
	DurationDisplay string
}

type pairwiseSummary struct {
	CandidateArm   string
	SeriesClass    string
	RecordDisplay  string
	DeltaF1Display string
	CostDisplay    string
}

type categoryRow struct {
	Name          string
	Cells         []categoryCell
	RecordDisplay string
	Flagged       bool
}

type categoryCell struct {
	Arm       string
	F1Display string
}

type barChart struct {
	Max        float64
	MaxDisplay string
	Bars       []chartBar
}

type chartBar struct {
	Arm          string
	SeriesClass  string
	Value        float64
	ValueDisplay string
}

type fieldNote struct {
	CaseID   string
	Category string
	Question string
	Expected string
	Tag      string
	Flagged  bool
	Answers  []caseAnswer
}

type caseGroup struct {
	Category string
	Cases    []caseDetail
}

type caseDetail struct {
	CaseID   string
	Question string
	Expected string
	Answers  []caseAnswer
}

type caseAnswer struct {
	Arm          string
	SeriesClass  string
	Status       string
	F1Display    string
	DeltaDisplay string
	Answer       string
	Error        string
}

func buildReportData(run v2.RunRecord, baselineArm string, results []v2.TrialResult) reportData {
	arms := armOrder(run.Config.Arms, results, baselineArm)
	summaries := v2.Summarize(results)
	pairwise := v2.Pairwise(results, baselineArm)
	stats := buildArmStats(arms, baselineArm, summaries)
	candidate := leadingCandidate(pairwise, arms, baselineArm)
	failed, caseCount := resultCounts(results)
	costs := v2.CostTotals(results)
	fieldNotes := selectFieldNotes(results, stats, baselineArm, candidate)
	return reportData{
		RunID: run.ID, Dataset: run.Dataset, DatasetRevision: run.DatasetRevision,
		GeneratedAt: time.Now().UTC().Format("2006-01-02 15:04 UTC"),
		TotalTrials: len(results), FailedTrials: failed, CaseCount: caseCount, CostScope: costs.Scope,
		Runtime: runtimeValues(run.Runtime), Arms: stats, BaselineArm: baselineArm, CandidateArm: candidate,
		Pairwise: buildPairwise(stats, pairwise), Categories: buildCategories(stats, candidate, summaries, pairwise),
		QualityChart:    buildChart(stats, summaries, func(row v2.SummaryRow) float64 { return row.MeanTokenF1 }, func(value float64) string { return fmt.Sprintf("%.3f", value) }),
		CostChart:       buildChart(stats, summaries, func(row v2.SummaryRow) float64 { return row.MeanCompletedCost }, func(value float64) string { return fmt.Sprintf("$%.5f", value) }),
		DurationChart:   buildChart(stats, summaries, func(row v2.SummaryRow) float64 { return row.MeanDurationMS / 1000 }, func(value float64) string { return fmt.Sprintf("%.1fs", value) }),
		AcceptanceCases: selectAcceptanceCases(results, stats, baselineArm, fieldNotes),
		FieldNotes:      fieldNotes,
		CaseGroups:      buildCaseGroups(results, stats, baselineArm),
	}
}

func buildChart(arms []armStat, summaries []v2.SummaryRow, value func(v2.SummaryRow) float64, display func(float64) string) barChart {
	overall := overallByArm(summaries)
	bars := make([]chartBar, 0, len(arms))
	maximum := 0.0
	for _, arm := range arms {
		current := value(overall[arm.Name])
		maximum = max(maximum, current)
		bars = append(bars, chartBar{Arm: arm.Name, SeriesClass: arm.SeriesClass, Value: current, ValueDisplay: display(current)})
	}
	scale := niceMax(maximum)
	return barChart{Max: scale, MaxDisplay: display(scale), Bars: bars}
}

func niceMax(value float64) float64 {
	if value <= 0 {
		return 1
	}
	target, magnitude := value*1.15, 1.0
	for target >= 10 {
		target /= 10
		magnitude *= 10
	}
	for target < 1 {
		target *= 10
		magnitude /= 10
	}
	for _, step := range []float64{1, 2, 5, 10} {
		if step >= target {
			return step * magnitude
		}
	}
	return 10 * magnitude
}

func armOrder(configured []v2.ArmConfig, results []v2.TrialResult, baselineArm string) []string {
	present := make(map[string]bool)
	for _, result := range results {
		present[result.Arm] = true
	}
	order := make([]string, 0, len(present))
	seen := make(map[string]bool, len(present))
	if present[baselineArm] {
		order = append(order, baselineArm)
		seen[baselineArm] = true
	}
	for _, arm := range configured {
		if present[arm.Name] && !seen[arm.Name] {
			seen[arm.Name] = true
			order = append(order, arm.Name)
		}
	}
	for _, result := range results {
		if !seen[result.Arm] {
			seen[result.Arm] = true
			order = append(order, result.Arm)
		}
	}
	return order
}

func buildArmStats(arms []string, baselineArm string, summaries []v2.SummaryRow) []armStat {
	overall := overallByArm(summaries)
	stats := make([]armStat, 0, len(arms))
	for index, arm := range arms {
		row := overall[arm]
		stats = append(stats, armStat{
			Name: arm, SeriesClass: seriesClass(index), IsBaseline: arm == baselineArm,
			MeanF1Display: fmt.Sprintf("%.3f", row.MeanTokenF1), CostDisplay: fmt.Sprintf("$%.5f", row.MeanCompletedCost),
			DurationDisplay: fmt.Sprintf("%.1fs", row.MeanDurationMS/1000),
		})
	}
	return stats
}

func overallByArm(summaries []v2.SummaryRow) map[string]v2.SummaryRow {
	result := make(map[string]v2.SummaryRow)
	for _, row := range summaries {
		if row.DimensionType == "overall" {
			result[row.Arm] = row
		}
	}
	return result
}

func leadingCandidate(rows []v2.PairwiseRow, arms []string, baselineArm string) string {
	byArm := make(map[string]v2.PairwiseRow)
	for _, row := range rows {
		if row.Category == "all" {
			byArm[row.CandidateArm] = row
		}
	}
	best, hasBest := "", false
	for _, arm := range arms {
		if arm == baselineArm {
			continue
		}
		row, ok := byArm[arm]
		if !ok {
			continue
		}
		if !hasBest || row.MeanDeltaF1 > byArm[best].MeanDeltaF1 {
			best, hasBest = arm, true
		}
	}
	return best
}

func buildPairwise(arms []armStat, rows []v2.PairwiseRow) []pairwiseSummary {
	overall := make(map[string]v2.PairwiseRow)
	for _, row := range rows {
		if row.Category == "all" {
			overall[row.CandidateArm] = row
		}
	}
	result := make([]pairwiseSummary, 0, max(0, len(arms)-1))
	for _, arm := range arms {
		if arm.IsBaseline {
			continue
		}
		row, ok := overall[arm.Name]
		if !ok {
			continue
		}
		result = append(result, pairwiseSummary{
			CandidateArm: arm.Name, SeriesClass: arm.SeriesClass,
			RecordDisplay:  fmt.Sprintf("%d W / %d L / %d T", row.Wins, row.Losses, row.Ties),
			DeltaF1Display: fmt.Sprintf("%+.3f", row.MeanDeltaF1), CostDisplay: formatSignedCost(row.MeanDeltaCost),
		})
	}
	return result
}

func buildCategories(arms []armStat, candidate string, summaries []v2.SummaryRow, comparisons []v2.PairwiseRow) []categoryRow {
	values := make(map[string]map[string]float64)
	for _, row := range summaries {
		if row.DimensionType != "category" {
			continue
		}
		if values[row.DimensionValue] == nil {
			values[row.DimensionValue] = make(map[string]float64)
		}
		values[row.DimensionValue][row.Arm] = row.MeanTokenF1
	}
	comparisonByCategory := make(map[string]v2.PairwiseRow)
	for _, row := range comparisons {
		if row.Category != "all" && row.CandidateArm == candidate {
			comparisonByCategory[row.Category] = row
		}
	}
	categories := sortedKeys(values)
	result := make([]categoryRow, 0, len(categories))
	for _, category := range categories {
		cells := make([]categoryCell, 0, len(arms))
		for _, arm := range arms {
			cells = append(cells, categoryCell{Arm: arm.Name, F1Display: fmt.Sprintf("%.3f", values[category][arm.Name])})
		}
		comparison := comparisonByCategory[category]
		result = append(result, categoryRow{
			Name: category, Cells: cells,
			RecordDisplay: fmt.Sprintf("%dW-%dL-%dT", comparison.Wins, comparison.Losses, comparison.Ties),
			Flagged:       comparison.Pairs > 0 && comparison.Losses == comparison.Pairs,
		})
	}
	return result
}

func buildCaseGroups(results []v2.TrialResult, arms []armStat, baselineArm string) []caseGroup {
	byCategory := make(map[string]map[string]map[string]v2.TrialResult)
	for _, result := range results {
		if byCategory[result.Category] == nil {
			byCategory[result.Category] = make(map[string]map[string]v2.TrialResult)
		}
		if byCategory[result.Category][result.CaseID] == nil {
			byCategory[result.Category][result.CaseID] = make(map[string]v2.TrialResult)
		}
		byCategory[result.Category][result.CaseID][result.Arm] = result
	}
	categories := sortedKeys(byCategory)
	groups := make([]caseGroup, 0, len(categories))
	for _, category := range categories {
		caseIDs := sortedKeys(byCategory[category])
		group := caseGroup{Category: category, Cases: make([]caseDetail, 0, len(caseIDs))}
		for _, caseID := range caseIDs {
			group.Cases = append(group.Cases, newCaseDetail(caseID, byCategory[category][caseID], arms, baselineArm))
		}
		groups = append(groups, group)
	}
	return groups
}

func newCaseDetail(caseID string, byArm map[string]v2.TrialResult, arms []armStat, baselineArm string) caseDetail {
	baseline := byArm[baselineArm]
	detail := caseDetail{CaseID: caseID}
	for _, result := range byArm {
		detail.Question, detail.Expected = result.Question, result.Expected
		break
	}
	for _, arm := range arms {
		result, ok := byArm[arm.Name]
		if !ok {
			detail.Answers = append(detail.Answers, caseAnswer{
				Arm: arm.Name, SeriesClass: arm.SeriesClass, Status: "missing",
				F1Display: "—", DeltaDisplay: "not comparable", Error: "No trial result for this arm.",
			})
			continue
		}
		delta := "not comparable"
		if arm.Name == baselineArm {
			delta = "baseline"
		} else if result.Status == "completed" && baseline.Status == "completed" {
			delta = fmt.Sprintf("%+.3f vs %s", result.TokenF1-baseline.TokenF1, baselineArm)
		}
		detail.Answers = append(detail.Answers, caseAnswer{
			Arm: arm.Name, SeriesClass: arm.SeriesClass, Status: result.Status,
			F1Display: fmt.Sprintf("%.3f", result.TokenF1), DeltaDisplay: delta, Answer: result.Answer, Error: result.Error,
		})
	}
	return detail
}

func selectFieldNotes(results []v2.TrialResult, arms []armStat, baselineArm, candidate string) []fieldNote {
	if candidate == "" {
		return nil
	}
	completed := completedByCase(results)
	pairs := pairedDeltas(completed, baselineArm, candidate)
	if len(pairs) == 0 {
		return nil
	}
	allResults := resultsByCase(results)
	selected := selectRepresentativeCases(pairs)
	notes := make([]fieldNote, 0, len(selected))
	for _, selection := range selected {
		detail := newCaseDetail(selection.caseID, allResults[selection.caseID], arms, baselineArm)
		notes = append(notes, fieldNote{
			CaseID: detail.CaseID, Category: caseCategory(allResults[selection.caseID]), Question: detail.Question, Expected: detail.Expected,
			Tag: selection.tag, Flagged: selection.flagged, Answers: detail.Answers,
		})
	}
	return notes
}

func selectAcceptanceCases(results []v2.TrialResult, arms []armStat, baselineArm string, fieldNotes []fieldNote) []fieldNote {
	const targetCases = 3
	selected := append([]fieldNote(nil), fieldNotes...)
	if len(selected) >= targetCases {
		return selected[:targetCases]
	}
	allResults := resultsByCase(results)
	usedCases := make(map[string]bool, len(selected))
	usedCategories := make(map[string]bool, len(selected))
	for _, note := range selected {
		usedCases[note.CaseID] = true
		usedCategories[note.Category] = true
	}
	appendCases := func(preferNewCategory bool) {
		for _, caseID := range sortedKeys(allResults) {
			if len(selected) >= targetCases {
				return
			}
			category := caseCategory(allResults[caseID])
			if usedCases[caseID] || (preferNewCategory && usedCategories[category]) {
				continue
			}
			detail := newCaseDetail(caseID, allResults[caseID], arms, baselineArm)
			selected = append(selected, fieldNote{
				CaseID: detail.CaseID, Category: category, Question: detail.Question, Expected: detail.Expected,
				Tag: "additional scenario", Answers: detail.Answers,
			})
			usedCases[caseID] = true
			usedCategories[category] = true
		}
	}
	appendCases(true)
	appendCases(false)
	return selected
}

func caseCategory(byArm map[string]v2.TrialResult) string {
	for _, result := range byArm {
		return result.Category
	}
	return ""
}

type caseDelta struct {
	caseID   string
	category string
	delta    float64
}

type caseSelection struct {
	caseID  string
	tag     string
	flagged bool
}

func completedByCase(results []v2.TrialResult) map[string]map[string]v2.TrialResult {
	return resultsByCaseMatching(results, func(result v2.TrialResult) bool { return result.Status == "completed" })
}

func resultsByCase(results []v2.TrialResult) map[string]map[string]v2.TrialResult {
	return resultsByCaseMatching(results, func(v2.TrialResult) bool { return true })
}

func resultsByCaseMatching(results []v2.TrialResult, include func(v2.TrialResult) bool) map[string]map[string]v2.TrialResult {
	byCase := make(map[string]map[string]v2.TrialResult)
	for _, result := range results {
		if !include(result) {
			continue
		}
		if byCase[result.CaseID] == nil {
			byCase[result.CaseID] = make(map[string]v2.TrialResult)
		}
		byCase[result.CaseID][result.Arm] = result
	}
	return byCase
}

func pairedDeltas(byCase map[string]map[string]v2.TrialResult, baselineArm, candidate string) []caseDelta {
	caseIDs := sortedKeys(byCase)
	result := make([]caseDelta, 0, len(caseIDs))
	for _, caseID := range caseIDs {
		baseline, baselineOK := byCase[caseID][baselineArm]
		comparison, comparisonOK := byCase[caseID][candidate]
		if baselineOK && comparisonOK {
			result = append(result, caseDelta{caseID: caseID, category: comparison.Category, delta: comparison.TokenF1 - baseline.TokenF1})
		}
	}
	return result
}

func selectRepresentativeCases(pairs []caseDelta) []caseSelection {
	best := pairs[0]
	for _, pair := range pairs[1:] {
		if pair.delta > best.delta {
			best = pair
		}
	}
	selected := []caseSelection{{caseID: best.caseID, tag: "largest lexical win"}}
	used := map[string]bool{best.caseID: true}
	worst, hasLoss := caseDelta{}, false
	for _, pair := range pairs {
		if pair.delta < 0 && (!hasLoss || pair.delta < worst.delta) {
			worst, hasLoss = pair, true
		}
	}
	if hasLoss && !used[worst.caseID] {
		selected = append(selected, caseSelection{caseID: worst.caseID, tag: "largest lexical loss", flagged: true})
		used[worst.caseID] = true
	}
	shiftCategory := largestShiftCategory(pairs)
	for _, pair := range pairs {
		if pair.category == shiftCategory && !used[pair.caseID] {
			selected = append(selected, caseSelection{caseID: pair.caseID, tag: "largest category shift"})
			break
		}
	}
	return selected
}

func largestShiftCategory(pairs []caseDelta) string {
	totals := make(map[string]float64)
	counts := make(map[string]int)
	for _, pair := range pairs {
		totals[pair.category] += pair.delta
		counts[pair.category]++
	}
	categories := sortedKeys(totals)
	best := ""
	bestAbsoluteMean := -1.0
	for _, category := range categories {
		mean := totals[category] / float64(counts[category])
		absoluteMean := mean
		if absoluteMean < 0 {
			absoluteMean = -absoluteMean
		}
		if absoluteMean > bestAbsoluteMean {
			best = category
			bestAbsoluteMean = absoluteMean
		}
	}
	return best
}

func resultCounts(results []v2.TrialResult) (int, int) {
	failed := 0
	cases := make(map[string]struct{})
	for _, result := range results {
		cases[result.CaseID] = struct{}{}
		if result.Status != "completed" {
			failed++
		}
	}
	return failed, len(cases)
}

func runtimeValues(values map[string]string) []keyValue {
	keys := sortedKeys(values)
	result := make([]keyValue, 0, len(keys))
	for _, key := range keys {
		result = append(result, keyValue{Key: key, Value: values[key]})
	}
	return result
}

func seriesClass(index int) string { return fmt.Sprintf("series-%d", index) }

func formatSignedCost(value float64) string {
	if value < 0 {
		return fmt.Sprintf("-$%.5f/trial", -value)
	}
	return fmt.Sprintf("+$%.5f/trial", value)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
