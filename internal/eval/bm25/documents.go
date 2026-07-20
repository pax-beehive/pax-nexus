package bm25

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type Document struct {
	ID   string
	Text string
}

type ScoredDocument struct {
	ID    string
	Score float64
	Rank  int
}

// RankDocuments assigns deterministic BM25 scores to arbitrary documents.
func RankDocuments(documents []Document, query string) ([]ScoredDocument, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("rank BM25 documents: query is required")
	}
	type scoredDocument struct {
		id     string
		tokens []string
		score  float64
	}
	working := make([]scoredDocument, 0, len(documents))
	for index, document := range documents {
		if strings.TrimSpace(document.ID) == "" || strings.TrimSpace(document.Text) == "" {
			return nil, fmt.Errorf("rank BM25 documents: document %d has empty required fields", index)
		}
		working = append(working, scoredDocument{id: document.ID, tokens: tokens(document.Text)})
	}
	queryTerms := tokens(query)
	if len(working) == 0 || len(queryTerms) == 0 {
		return []ScoredDocument{}, nil
	}
	documentFrequency := make(map[string]int)
	totalLength := 0
	for _, document := range working {
		totalLength += len(document.tokens)
		seen := make(map[string]struct{})
		for _, term := range document.tokens {
			seen[term] = struct{}{}
		}
		for term := range seen {
			documentFrequency[term]++
		}
	}
	averageLength := float64(totalLength) / float64(len(working))
	for index := range working {
		frequencies := make(map[string]int)
		for _, term := range working[index].tokens {
			frequencies[term]++
		}
		for _, term := range queryTerms {
			frequency := frequencies[term]
			if frequency == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(working)-documentFrequency[term])+0.5)/(float64(documentFrequency[term])+0.5))
			denominator := float64(frequency) + bm25K1*(1-bm25B+bm25B*float64(len(working[index].tokens))/averageLength)
			working[index].score += idf * (float64(frequency) * (bm25K1 + 1) / denominator)
		}
	}
	sort.SliceStable(working, func(left, right int) bool {
		if working[left].score == working[right].score {
			return working[left].id < working[right].id
		}
		return working[left].score > working[right].score
	})
	result := make([]ScoredDocument, 0, len(working))
	for index, document := range working {
		result = append(result, ScoredDocument{ID: document.id, Score: document.score, Rank: index + 1})
	}
	return result, nil
}
