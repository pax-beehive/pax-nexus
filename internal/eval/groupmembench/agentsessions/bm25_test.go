package agentsessions

import "testing"

func TestBM25RanksExactTermMatchFirst(t *testing.T) {
	idx := NewBM25(map[string]string{
		"d1": "the ESG policy deadline is 2025-07-18 for reporting",
		"d2": "lunch menu discussion pizza",
		"d3": "reporting cadence weekly sync",
	})
	got := idx.TopK("ESG policy deadline 2025-07-18", 2)
	if len(got) == 0 || got[0] != "d1" {
		t.Fatalf("want d1 first, got %v", got)
	}
}

func TestBM25DeterministicTieBreak(t *testing.T) {
	idx := NewBM25(map[string]string{"b": "same words", "a": "same words"})
	got := idx.TopK("same words", 2)
	if got[0] != "a" || got[1] != "b" {
		t.Fatalf("tie-break not deterministic: %v", got)
	}
}
