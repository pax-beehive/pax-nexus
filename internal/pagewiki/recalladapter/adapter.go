// Package recalladapter exposes PageWiki through the Agent Memory recall port.
package recalladapter

import (
	"context"
	"fmt"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/recall"
)

const revisionRefPrefix = "pagewiki:revision/"

type Reader interface {
	Search(context.Context, string) ([]pagewiki.SearchResult, error)
}

type Adapter struct {
	reader Reader
}

func New(reader Reader) (*Adapter, error) {
	if reader == nil {
		return nil, fmt.Errorf("create PageWiki recall adapter: reader is required")
	}
	return &Adapter{reader: reader}, nil
}

func (a *Adapter) Search(
	ctx context.Context,
	request recall.SearchRequest,
) ([]recall.MemoryHit, error) {
	results, err := a.reader.Search(ctx, request.Query)
	if err != nil {
		return nil, fmt.Errorf("search PageWiki: %w", err)
	}
	hits := make([]recall.MemoryHit, 0, len(results))
	usedTokens := 0
	for _, result := range results {
		if request.MaxItems > 0 && len(hits) >= request.MaxItems {
			break
		}
		tokens := estimateTokens(result.Passage)
		if usedTokens+tokens > request.TokenBudget {
			continue
		}
		hits = append(hits, recall.MemoryHit{
			Ref:    revisionRefPrefix + result.RevisionID,
			Text:   result.Passage,
			Score:  result.Score,
			Tokens: tokens,
			Metadata: map[string]string{
				"page_id":     result.Page.ID,
				"slug":        result.Page.Slug,
				"title":       result.Page.Title,
				"revision_id": result.RevisionID,
				"section_key": result.SectionKey,
			},
		})
		usedTokens += tokens
	}
	return hits, nil
}

func (a *Adapter) Hint(
	ctx context.Context,
	request recall.SearchRequest,
) (recall.MemoryHit, error) {
	hits, err := a.Search(ctx, request)
	if err != nil || len(hits) == 0 {
		return recall.MemoryHit{}, err
	}
	return hits[0], nil
}

func (a *Adapter) Get(
	context.Context,
	recall.GetRequest,
) (recall.MemoryDocument, error) {
	return recall.MemoryDocument{}, fmt.Errorf("get PageWiki revision: unavailable")
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len([]rune(text)) + 3) / 4
}

var _ recall.WikiPath = (*Adapter)(nil)
