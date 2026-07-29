package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
)

var legacySectionKeyCharacters = regexp.MustCompile(`[^a-z0-9]+`)

type legacyPage struct {
	pageID         string
	slug           string
	pageType       string
	revisionID     string
	title          string
	summary        string
	bodyMarkdown   string
	citationDrafts []legacyCitation
}

type legacyCitation struct {
	id               string
	exactText        string
	sourceRevisionID string
	eventID          string
	startByte        int
	endByte          int
	exactQuote       string
}

func (r *Repository) hydrateLegacyWiki(ctx context.Context) (int, error) {
	available, err := r.legacyWikiAvailable(ctx)
	if err != nil || !available {
		return 0, err
	}
	if err := r.hydrateLegacySources(ctx); err != nil {
		return 0, err
	}
	pages, err := r.legacyPages(ctx)
	if err != nil {
		return 0, err
	}
	citations, err := r.legacyCitations(ctx)
	if err != nil {
		return 0, err
	}
	for index := range pages {
		pages[index].citationDrafts = citations[pages[index].revisionID]
		publication := legacyPublication(pages[index])
		if err := r.memory.PublishPage(ctx, publication); err != nil {
			return 0, fmt.Errorf("publish legacy Page %q: %w", pages[index].slug, err)
		}
	}
	return len(pages), nil
}

func (r *Repository) legacyWikiAvailable(ctx context.Context) (bool, error) {
	var table *string
	if err := r.pool.QueryRow(ctx, `SELECT to_regclass('public.llmwiki_pages')::text`).Scan(&table); err != nil {
		return false, fmt.Errorf("detect legacy Wiki tables: %w", err)
	}
	return table != nil, nil
}

func (r *Repository) hydrateLegacySources(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `
SELECT revision.source_revision_id, revision.source_id, revision.content,
       COALESCE(anchor.event_id, ''), COALESCE(anchor.start_offset, 0),
       COALESCE(anchor.end_offset, 0)
FROM llmwiki_source_revisions revision
LEFT JOIN llmwiki_source_anchors anchor
  ON anchor.scope_id = revision.scope_id
 AND anchor.source_revision_id = revision.source_revision_id
WHERE revision.scope_id = $1
ORDER BY revision.source_revision_id, anchor.start_offset`, r.scopeID)
	if err != nil {
		return fmt.Errorf("query legacy Wiki sources: %w", err)
	}
	defer rows.Close()
	var current *pagewiki.SourceRevision
	save := func() error {
		if current == nil {
			return nil
		}
		return r.memory.SaveSourceRevision(ctx, *current)
	}
	for rows.Next() {
		var revisionID, sourceID, content, eventID string
		var startByte, endByte int
		if err := rows.Scan(&revisionID, &sourceID, &content, &eventID, &startByte, &endByte); err != nil {
			return fmt.Errorf("scan legacy Wiki source: %w", err)
		}
		if current == nil || current.ID != revisionID {
			if err := save(); err != nil {
				return fmt.Errorf("load legacy Wiki source: %w", err)
			}
			sum := sha256.Sum256([]byte(content))
			current = &pagewiki.SourceRevision{
				ID: revisionID, SourceID: sourceID, SHA256: hex.EncodeToString(sum[:]), Raw: []byte(content),
			}
		}
		if eventID != "" && startByte >= 0 && endByte > startByte && endByte <= len(content) {
			current.Events = append(current.Events, pagewiki.SourceEvent{
				ID: eventID, StartByte: startByte, EndByte: endByte,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy Wiki sources: %w", err)
	}
	if err := save(); err != nil {
		return fmt.Errorf("load legacy Wiki source: %w", err)
	}
	return nil
}

func (r *Repository) legacyPages(ctx context.Context) ([]legacyPage, error) {
	rows, err := r.pool.Query(ctx, `
SELECT page.page_id, page.slug, page.page_type, revision.page_revision_id,
       revision.title, revision.summary, revision.body_markdown
FROM llmwiki_pages page
JOIN llmwiki_page_revisions revision
  ON revision.scope_id = page.scope_id
 AND revision.page_revision_id = page.current_revision_id
WHERE page.scope_id = $1 AND page.status <> 'archived'
ORDER BY page.created_at, page.page_id`, r.scopeID)
	if err != nil {
		return nil, fmt.Errorf("query legacy Wiki pages: %w", err)
	}
	defer rows.Close()
	result := make([]legacyPage, 0)
	for rows.Next() {
		var page legacyPage
		if err := rows.Scan(
			&page.pageID,
			&page.slug,
			&page.pageType,
			&page.revisionID,
			&page.title,
			&page.summary,
			&page.bodyMarkdown,
		); err != nil {
			return nil, fmt.Errorf("scan legacy Wiki page: %w", err)
		}
		result = append(result, page)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy Wiki pages: %w", err)
	}
	return result, nil
}

func (r *Repository) legacyCitations(ctx context.Context) (map[string][]legacyCitation, error) {
	rows, err := r.pool.Query(ctx, `
SELECT binding.page_revision_id, binding.binding_id, binding.exact_text,
       anchor.source_revision_id, anchor.event_id, anchor.start_offset,
       anchor.end_offset, anchor.exact_text
FROM llmwiki_semantic_bindings binding
JOIN llmwiki_knowledge_evidence evidence
  ON evidence.scope_id = binding.scope_id
 AND evidence.knowledge_revision_id = binding.knowledge_revision_id
JOIN llmwiki_source_anchors anchor
  ON anchor.scope_id = evidence.scope_id
 AND anchor.anchor_id = evidence.anchor_id
WHERE binding.scope_id = $1
ORDER BY binding.page_revision_id, binding.binding_id, anchor.anchor_id`, r.scopeID)
	if err != nil {
		return nil, fmt.Errorf("query legacy Wiki citations: %w", err)
	}
	defer rows.Close()
	result := make(map[string][]legacyCitation)
	for rows.Next() {
		var citation legacyCitation
		var revisionID string
		if err := rows.Scan(
			&revisionID,
			&citation.id,
			&citation.exactText,
			&citation.sourceRevisionID,
			&citation.eventID,
			&citation.startByte,
			&citation.endByte,
			&citation.exactQuote,
		); err != nil {
			return nil, fmt.Errorf("scan legacy Wiki citation: %w", err)
		}
		result[revisionID] = append(result[revisionID], citation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy Wiki citations: %w", err)
	}
	return result, nil
}

func legacyPublication(input legacyPage) pagewiki.PagePublication {
	sections := legacySections(input.bodyMarkdown)
	citations := legacyPageCitations(input.revisionID, sections, input.citationDrafts)
	markdown := "# " + strings.TrimSpace(input.title) + "\n\n" +
		strings.TrimSpace(input.summary) + "\n\n" + strings.TrimSpace(input.bodyMarkdown) + "\n"
	path := legacyTopicPath(input)
	topics, placement := legacyPlacement(input.pageID, path)
	return pagewiki.PagePublication{
		Page: pagewiki.Page{
			ID: input.pageID, Slug: input.slug, Title: input.title, CurrentRevisionID: input.revisionID,
		},
		Revision: pagewiki.PageRevision{
			ID: input.revisionID, PageID: input.pageID, Title: input.title,
			Summary: input.summary, Sections: sections, Markdown: markdown, Citations: citations,
		},
		Topics: topics, Placement: placement,
	}
}

func legacySections(markdown string) []pagewiki.PageSection {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	result := make([]pagewiki.PageSection, 0)
	heading := "Overview"
	key := "overview"
	content := make([]string, 0)
	seen := make(map[string]int)
	flush := func() {
		value := strings.TrimSpace(strings.Join(content, "\n"))
		if value == "" {
			return
		}
		sectionKey := uniqueLegacySectionKey(key, seen)
		result = append(result, pagewiki.PageSection{
			Key: sectionKey, Heading: heading, Markdown: value,
		})
		content = nil
	}
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "## ") {
			flush()
			heading = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "## "))
			key = legacySectionKey(heading)
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "# ") {
			continue
		}
		content = append(content, line)
	}
	flush()
	if len(result) == 0 {
		result = append(result, pagewiki.PageSection{
			Key: "overview", Heading: "Overview", Markdown: strings.TrimSpace(markdown),
		})
	}
	return result
}

func legacyPageCitations(
	revisionID string,
	sections []pagewiki.PageSection,
	drafts []legacyCitation,
) []pagewiki.PageCitation {
	type citationKey struct {
		id         string
		sectionKey string
		start      int
		end        int
		exactText  string
	}
	index := make(map[citationKey]int)
	result := make([]pagewiki.PageCitation, 0)
	for _, draft := range drafts {
		sectionKey, start, end, ok := legacyCitationRange(sections, draft.exactText)
		if !ok {
			continue
		}
		key := citationKey{
			id: draft.id, sectionKey: sectionKey, start: start, end: end, exactText: draft.exactText,
		}
		position, found := index[key]
		if !found {
			position = len(result)
			index[key] = position
			result = append(result, pagewiki.PageCitation{
				ID: draft.id, PageRevisionID: revisionID, SectionKey: sectionKey,
				StartByte: start, EndByte: end, ExactText: draft.exactText,
			})
		}
		result[position].SourceAnchors = append(result[position].SourceAnchors, pagewiki.SourceAnchor{
			ID: draft.id + ":" + draft.eventID, SourceRevisionID: draft.sourceRevisionID,
			EventID: draft.eventID, StartByte: draft.startByte, EndByte: draft.endByte,
			ExactQuote: draft.exactQuote,
		})
	}
	return result
}

func legacyCitationRange(
	sections []pagewiki.PageSection,
	exactText string,
) (string, int, int, bool) {
	if strings.TrimSpace(exactText) == "" {
		return "", 0, 0, false
	}
	for _, section := range sections {
		if strings.Count(section.Markdown, exactText) != 1 {
			continue
		}
		start := strings.Index(section.Markdown, exactText)
		return section.Key, start, start + len(exactText), true
	}
	return "", 0, 0, false
}

func legacyTopicPath(page legacyPage) []string {
	value := strings.ToLower(page.title + " " + page.summary + " " + page.bodyMarkdown)
	switch {
	case containsAny(value, "retrieval", "search", "检索", "召回"):
		return []string{"Engineering", "Retrieval"}
	case containsAny(value, "llm wiki", "wiki 数据", "wiki data", "source anchor", "page revision"):
		return []string{"Engineering", "Wiki Architecture"}
	case containsAny(value, "experiment", "实验", "coal", "briquette", "煤球"):
		return []string{"Research", "Experiments"}
	}
	switch page.pageType {
	case "architecture", "decision":
		return []string{"Engineering", "Architecture & Decisions"}
	case "procedure", "incident":
		return []string{"Operations", "Runbooks & Incidents"}
	case "project":
		return []string{"Projects"}
	default:
		return []string{"Knowledge", "Concepts"}
	}
}

func legacyPlacement(pageID string, path []string) ([]pagewiki.Topic, *pagewiki.PagePlacement) {
	topics := make([]pagewiki.Topic, 0, len(path))
	parentID := ""
	for _, title := range path {
		slug := strings.ReplaceAll(strings.ToLower(title), " ", "-")
		id := legacyStableID("topic", parentID, slug)
		topics = append(topics, pagewiki.Topic{
			ID: id, ParentID: parentID, Slug: slug, Title: title,
		})
		parentID = id
	}
	return topics, &pagewiki.PagePlacement{PageID: pageID, TopicID: parentID}
}

func legacySectionKey(value string) string {
	key := strings.Trim(legacySectionKeyCharacters.ReplaceAllString(strings.ToLower(value), "-"), "-")
	if key == "" {
		return "section"
	}
	return key
}

func uniqueLegacySectionKey(key string, seen map[string]int) string {
	seen[key]++
	if seen[key] == 1 {
		return key
	}
	return fmt.Sprintf("%s-%d", key, seen[key])
}

func legacyStableID(prefix string, values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	return prefix + "_" + hex.EncodeToString(hash.Sum(nil))[:24]
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
