// Package recalladapter exposes PageWiki through the Agent Memory recall port.
package recalladapter

import (
	"context"
	"fmt"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/recall"
)

const revisionRefPrefix = "pagewiki:revision/"

type Reader interface {
	Search(context.Context, string) ([]pagewiki.SearchResult, error)
	PageByID(context.Context, string) (pagewiki.Page, error)
	PageRevision(context.Context, string) (pagewiki.PageRevision, error)
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
			PageWiki: pageWikiContext(
				result.Page,
				result.RevisionID,
				result.SectionKey,
				result.Citations,
				result.Links,
			),
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
	ctx context.Context,
	request recall.GetRequest,
) (recall.MemoryDocument, error) {
	revisionID, err := parseRevisionRef(request.Ref)
	if err != nil {
		return recall.MemoryDocument{}, err
	}
	revision, err := a.reader.PageRevision(ctx, revisionID)
	if err != nil {
		return recall.MemoryDocument{}, fmt.Errorf("get PageWiki revision %q: %w", revisionID, err)
	}
	page, err := a.reader.PageByID(ctx, revision.PageID)
	if err != nil {
		return recall.MemoryDocument{}, fmt.Errorf("get PageWiki page %q: %w", revision.PageID, err)
	}
	page.Title = revision.Title
	return recall.MemoryDocument{
		Ref:    request.Ref,
		Text:   revision.Markdown,
		Tokens: estimateTokens(revision.Markdown),
		Provenance: map[string]string{
			"page_id":     page.ID,
			"slug":        page.Slug,
			"revision_id": revision.ID,
		},
		PageWiki: pageWikiContext(
			page,
			revision.ID,
			"",
			revision.Citations,
			revision.Links,
		),
	}, nil
}

func parseRevisionRef(ref string) (string, error) {
	if strings.TrimSpace(ref) != ref || !strings.HasPrefix(ref, revisionRefPrefix) {
		return "", fmt.Errorf("get PageWiki revision: invalid ref %q", ref)
	}
	revisionID := strings.TrimPrefix(ref, revisionRefPrefix)
	if revisionID == "" || strings.ContainsAny(revisionID, "/?# \t\r\n") {
		return "", fmt.Errorf("get PageWiki revision: invalid ref %q", ref)
	}
	return revisionID, nil
}

func pageWikiContext(
	page pagewiki.Page,
	revisionID string,
	sectionKey string,
	citations []pagewiki.PageCitation,
	links []pagewiki.PageLink,
) *recall.PageWikiContext {
	return &recall.PageWikiContext{
		PageID:     page.ID,
		Slug:       page.Slug,
		Title:      page.Title,
		RevisionID: revisionID,
		SectionKey: sectionKey,
		Citations:  mapCitations(citations),
		Links:      mapLinks(page.ID, links),
	}
}

func mapCitations(values []pagewiki.PageCitation) []recall.PageWikiCitation {
	result := make([]recall.PageWikiCitation, len(values))
	for index, citation := range values {
		anchors := make([]recall.PageWikiSourceAnchor, len(citation.SourceAnchors))
		for anchorIndex, anchor := range citation.SourceAnchors {
			anchors[anchorIndex] = recall.PageWikiSourceAnchor{
				SourceRevisionID: anchor.SourceRevisionID,
				EventID:          anchor.EventID,
				StartByte:        anchor.StartByte,
				EndByte:          anchor.EndByte,
				ExactQuote:       anchor.ExactQuote,
			}
		}
		result[index] = recall.PageWikiCitation{
			CitationID: citation.ID, SectionKey: citation.SectionKey,
			StartByte: citation.StartByte, EndByte: citation.EndByte,
			ExactText: citation.ExactText, SourceAnchors: anchors,
		}
	}
	return result
}

func mapLinks(sourcePageID string, values []pagewiki.PageLink) []recall.PageWikiLink {
	result := make([]recall.PageWikiLink, len(values))
	for index, link := range values {
		result[index] = recall.PageWikiLink{
			Direction: "outgoing", SectionKey: link.SectionKey,
			ExactText: link.ExactText, SourcePageID: sourcePageID,
			TargetPageID: link.TargetPageID,
		}
	}
	return result
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len([]rune(text)) + 3) / 4
}

var _ recall.WikiPath = (*Adapter)(nil)
