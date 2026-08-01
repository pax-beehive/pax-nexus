package knowledgeeval

import (
	"fmt"
	"sort"
)

type MetricDelta struct {
	CaseID      string  `json:"case_id"`
	BenchmarkID string  `json:"benchmark_id"`
	Metric      string  `json:"metric"`
	Left        float64 `json:"left"`
	Right       float64 `json:"right"`
	Delta       float64 `json:"delta"`
	Comparable  bool    `json:"comparable"`
	Reason      string  `json:"reason,omitempty"`
}

func CompareRuns(left, right RunDetail) []MetricDelta {
	leftTrials := make(map[string]Trial, len(left.Trials))
	for _, trial := range left.Trials {
		leftTrials[trial.CaseID+"\x00"+trial.BenchmarkID] = trial
	}
	var result []MetricDelta
	for _, rightTrial := range right.Trials {
		key := rightTrial.CaseID + "\x00" + rightTrial.BenchmarkID
		leftTrial, exists := leftTrials[key]
		if !exists {
			continue
		}
		if leftTrial.BenchmarkFingerprint != rightTrial.BenchmarkFingerprint {
			result = append(result, MetricDelta{
				CaseID: rightTrial.CaseID, BenchmarkID: rightTrial.BenchmarkID,
				Comparable: false, Reason: "benchmark fingerprint differs",
			})
			continue
		}
		if leftTrial.Result == nil || rightTrial.Result == nil {
			result = append(result, MetricDelta{
				CaseID: rightTrial.CaseID, BenchmarkID: rightTrial.BenchmarkID,
				Comparable: false, Reason: "completed result is missing",
			})
			continue
		}
		leftMetrics := make(map[string]float64, len(leftTrial.Result.Metrics))
		for _, metric := range leftTrial.Result.Metrics {
			leftMetrics[metric.Name+"\x00"+metric.Unit] = metric.Value
		}
		for _, metric := range rightTrial.Result.Metrics {
			leftValue, exists := leftMetrics[metric.Name+"\x00"+metric.Unit]
			if !exists {
				continue
			}
			result = append(result, MetricDelta{
				CaseID: rightTrial.CaseID, BenchmarkID: rightTrial.BenchmarkID,
				Metric: metric.Name, Left: leftValue, Right: metric.Value,
				Delta: metric.Value - leftValue, Comparable: true,
			})
		}
	}
	sort.Slice(result, func(leftIndex, rightIndex int) bool {
		leftKey := fmt.Sprintf(
			"%s/%s/%s",
			result[leftIndex].CaseID,
			result[leftIndex].BenchmarkID,
			result[leftIndex].Metric,
		)
		rightKey := fmt.Sprintf(
			"%s/%s/%s",
			result[rightIndex].CaseID,
			result[rightIndex].BenchmarkID,
			result[rightIndex].Metric,
		)
		return leftKey < rightKey
	})
	return result
}
