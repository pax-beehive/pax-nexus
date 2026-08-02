package agentsessions

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

type BM25 struct {
	docs   map[string][]string // docID → tokens
	df     map[string]int
	avgLen float64
}

func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func NewBM25(docs map[string]string) *BM25 {
	b := &BM25{docs: map[string][]string{}, df: map[string]int{}}
	var total int
	for id, text := range docs {
		tokens := tokenize(text)
		b.docs[id] = tokens
		total += len(tokens)
		seen := map[string]bool{}
		for _, tok := range tokens {
			if !seen[tok] {
				b.df[tok]++
				seen[tok] = true
			}
		}
	}
	if len(docs) > 0 {
		b.avgLen = float64(total) / float64(len(docs))
	}
	return b
}

func (b *BM25) TopK(query string, k int) []string {
	n := float64(len(b.docs))
	type scored struct {
		id    string
		score float64
	}
	var results []scored
	queryTokens := tokenize(query)
	for id, tokens := range b.docs {
		tf := map[string]int{}
		for _, tok := range tokens {
			tf[tok]++
		}
		var score float64
		for _, q := range queryTokens {
			f := float64(tf[q])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(b.df[q])+0.5)/(float64(b.df[q])+0.5))
			denom := f + bm25K1*(1-bm25B+bm25B*float64(len(tokens))/b.avgLen)
			score += idf * f * (bm25K1 + 1) / denom
		}
		if score > 0 {
			results = append(results, scored{id, score})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].id < results[j].id
	})
	if len(results) > k {
		results = results[:k]
	}
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.id
	}
	return out
}
