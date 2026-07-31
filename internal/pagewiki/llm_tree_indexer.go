package pagewiki

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

const (
	treeIndexerAttempts = 2
	treeMinTopicPages   = 3
	treeMaxDirectPages  = 10
	treeDefaultMaxDepth = 5
)

type LLMTreeIndexerConfig struct {
	Client llm.ChatClient
	Model  string
	Logger *slog.Logger
	// MaxDepth caps topic nesting (root topics are level 1); 0 means the
	// default. Deeper LLM output is flattened into the level-MaxDepth topic.
	MaxDepth int
}

// LLMTreeIndexer organizes published pages into the reader-facing topic
// tree while deterministic code enforces coverage, depth, and density.
type LLMTreeIndexer struct {
	client   llm.ChatClient
	model    string
	logger   *slog.Logger
	maxDepth int
}

func NewLLMTreeIndexer(config LLMTreeIndexerConfig) (*LLMTreeIndexer, error) {
	if config.Client == nil {
		return nil, errors.New("create Page Wiki tree indexer: client is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("create Page Wiki tree indexer: model is required")
	}
	logger := config.Logger
	if logger == nil {
		logger = observability.DiscardLogger()
	}
	maxDepth := config.MaxDepth
	if maxDepth == 0 {
		maxDepth = treeDefaultMaxDepth
	}
	if maxDepth < 1 {
		return nil, errors.New("create Page Wiki tree indexer: max depth must be positive")
	}
	return &LLMTreeIndexer{
		client: config.Client, model: strings.TrimSpace(config.Model),
		logger: logger, maxDepth: maxDepth,
	}, nil
}

type llmTreePage struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}

type llmTreeNode struct {
	Title    string        `json:"title"`
	Pages    []string      `json:"pages,omitempty"`
	Children []llmTreeNode `json:"children,omitempty"`
}

type llmTreeRequest struct {
	Pages         []llmTreePage `json:"pages"`
	CurrentRoot   []string      `json:"current_root_pages"`
	CurrentTopics []llmTreeNode `json:"current_topics"`
}

type llmTreeResponse struct {
	RootPages []string      `json:"root_pages"`
	Topics    []llmTreeNode `json:"topics"`
}

func (x *LLMTreeIndexer) Index(
	ctx context.Context,
	input TreeIndexInput,
) (TopicTree, error) {
	payload, err := json.Marshal(treeRequest(input))
	if err != nil {
		return TopicTree{}, fmt.Errorf("encode Page Wiki tree request: %w", err)
	}
	systemPrompt := treeIndexerPrompt(x.maxDepth) + generationDirectivesPrompt(input.Directives)
	var lastErr error
	for attempt := 0; attempt < treeIndexerAttempts; attempt++ {
		response, err := x.client.Complete(ctx, llm.ChatRequest{
			Model: x.model,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: string(payload)},
			},
		})
		if err != nil {
			lastErr = err
			continue
		}
		var decoded llmTreeResponse
		if err := json.Unmarshal(
			[]byte(trimJSONFence(response.Message.Content)),
			&decoded,
		); err != nil {
			lastErr = err
			continue
		}
		return x.normalizeTree(decoded, input.Catalog), nil
	}
	return TopicTree{}, fmt.Errorf("index Page Wiki tree: %w", lastErr)
}

func treeRequest(input TreeIndexInput) llmTreeRequest {
	request := llmTreeRequest{
		Pages:         make([]llmTreePage, 0, len(input.Catalog)),
		CurrentRoot:   make([]string, 0),
		CurrentTopics: make([]llmTreeNode, 0),
	}
	for _, page := range input.Catalog {
		request.Pages = append(request.Pages, llmTreePage{
			Slug: page.Slug, Title: page.Title, Summary: page.Summary,
		})
	}
	slugsByID := make(map[string]string, len(input.Catalog))
	for _, page := range input.Catalog {
		slugsByID[page.ID] = page.Slug
	}
	placed := make(map[string]struct{}, len(input.Current.Placements))
	pagesByTopic := make(map[string][]string)
	for _, placement := range input.Current.Placements {
		slug, found := slugsByID[placement.PageID]
		if !found {
			continue
		}
		placed[placement.PageID] = struct{}{}
		pagesByTopic[placement.TopicID] = append(pagesByTopic[placement.TopicID], slug)
	}
	for _, page := range input.Catalog {
		if _, found := placed[page.ID]; !found {
			request.CurrentRoot = append(request.CurrentRoot, page.Slug)
		}
	}
	request.CurrentTopics = currentTreeNodes("", input.Current.Topics, pagesByTopic)
	return request
}

func currentTreeNodes(
	parentID string,
	topics []Topic,
	pagesByTopic map[string][]string,
) []llmTreeNode {
	nodes := make([]llmTreeNode, 0)
	for _, topic := range topics {
		if topic.ParentID != parentID {
			continue
		}
		nodes = append(nodes, llmTreeNode{
			Title:    topic.Title,
			Pages:    pagesByTopic[topic.ID],
			Children: currentTreeNodes(topic.ID, topics, pagesByTopic),
		})
	}
	return nodes
}

type draftTopic struct {
	slug     string
	title    string
	pageIDs  []string
	children []*draftTopic
}

func (x *LLMTreeIndexer) normalizeTree(
	decoded llmTreeResponse,
	catalog PageCatalog,
) TopicTree {
	pageIDsBySlug := make(map[string]string, len(catalog))
	for _, page := range catalog {
		pageIDsBySlug[page.Slug] = page.ID
	}
	placed := make(map[string]struct{}, len(catalog))
	claim := func(slug string) (string, bool) {
		pageID, known := pageIDsBySlug[slug]
		if !known {
			return "", false
		}
		if _, duplicate := placed[pageID]; duplicate {
			return "", false
		}
		placed[pageID] = struct{}{}
		return pageID, true
	}
	for _, slug := range decoded.RootPages {
		claim(slug)
	}
	roots, absorbed := buildDraftTopics(decoded.Topics, claim, "", 1, x.maxDepth)
	kept, folded := pruneDraftTopics(roots)
	tree := TopicTree{Topics: make([]Topic, 0), Placements: make([]PagePlacement, 0)}
	unplacedBudget := len(absorbed) + len(folded)
	for _, page := range catalog {
		if _, found := placed[page.ID]; !found {
			unplacedBudget++
		}
	}
	x.emitTopics(&tree, "", kept)
	if unplacedBudget > treeMaxDirectPages {
		x.logger.Warn(
			"Page Wiki root exceeds the direct-page target",
			"pages", unplacedBudget,
		)
	}
	return tree
}

// buildDraftTopics converts LLM nodes at one level into draft topics,
// merging duplicate slugs and processing each node fully depth-first — a
// node's own pages are claimed and its children are recursed into
// immediately, before its next sibling is touched — matching the
// pre-refactor claim order so that a page slug repeated across branches is
// always won by the earliest branch in document order. Nodes at maxDepth
// keep their whole subtree's pages instead of recursing. Returns the
// topics plus page IDs the caller must absorb (child nodes whose slug is
// empty or repeats the parent's). At the root level (empty parentSlug) an
// empty-slug node is skipped without claiming, preserving the previous
// behavior.
func buildDraftTopics(
	nodes []llmTreeNode,
	claim func(string) (string, bool),
	parentSlug string,
	depth, maxDepth int,
) ([]*draftTopic, []string) {
	topics := make([]*draftTopic, 0, len(nodes))
	index := make(map[string]*draftTopic)
	absorbed := make([]string, 0)
	for _, node := range nodes {
		slug := topicSlug(node.Title)
		if slug == "" && parentSlug == "" {
			continue
		}
		if slug == "" || slug == parentSlug {
			absorbed = append(absorbed, claimPages(collectNodePages(node), claim)...)
			continue
		}
		topic, found := index[slug]
		if !found {
			topic = &draftTopic{slug: slug, title: strings.TrimSpace(node.Title)}
			index[slug] = topic
			topics = append(topics, topic)
		}
		claimNodeIntoTopic(topic, node, claim, depth, maxDepth)
	}
	return topics, absorbed
}

// claimNodeIntoTopic claims node's own pages onto topic, then — unless
// depth has reached maxDepth, in which case node's whole flattened subtree
// is claimed instead — immediately recurses into node's children and
// merges the resulting child topics into topic.children. Doing this before
// buildDraftTopics moves to node's next sibling is what makes claim order
// depth-first.
func claimNodeIntoTopic(
	topic *draftTopic,
	node llmTreeNode,
	claim func(string) (string, bool),
	depth, maxDepth int,
) {
	if depth == maxDepth {
		topic.pageIDs = append(topic.pageIDs, claimPages(collectNodePages(node), claim)...)
		return
	}
	topic.pageIDs = append(topic.pageIDs, claimPages(node.Pages, claim)...)
	if len(node.Children) == 0 {
		return
	}
	children, absorbed := buildDraftTopics(node.Children, claim, topic.slug, depth+1, maxDepth)
	topic.pageIDs = append(topic.pageIDs, absorbed...)
	mergeChildTopics(topic, children)
}

// claimPages claims each slug in order and returns the page IDs that were
// newly claimed.
func claimPages(slugs []string, claim func(string) (string, bool)) []string {
	ids := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		if pageID, ok := claim(slug); ok {
			ids = append(ids, pageID)
		}
	}
	return ids
}

// mergeChildTopics folds children into parent.children, matching by slug so
// that a parent slug repeated across separate LLM branches accumulates all
// occurrences' descendants into one subtree instead of duplicating topics.
func mergeChildTopics(parent *draftTopic, children []*draftTopic) {
	index := make(map[string]*draftTopic, len(parent.children))
	for _, existing := range parent.children {
		index[existing.slug] = existing
	}
	for _, child := range children {
		existing, found := index[child.slug]
		if !found {
			index[child.slug] = child
			parent.children = append(parent.children, child)
			continue
		}
		existing.pageIDs = append(existing.pageIDs, child.pageIDs...)
		mergeChildTopics(existing, child.children)
	}
}

// pruneDraftTopics enforces the minimum-pages rule bottom-up: a topic whose
// subtree holds fewer than treeMinTopicPages pages folds its pages into its
// parent (or, at the root level, back into the unplaced budget).
func pruneDraftTopics(topics []*draftTopic) ([]*draftTopic, []string) {
	kept := make([]*draftTopic, 0, len(topics))
	folded := make([]string, 0)
	for _, topic := range topics {
		children, childFolded := pruneDraftTopics(topic.children)
		topic.children = children
		topic.pageIDs = append(topic.pageIDs, childFolded...)
		if len(subtreePageIDs(topic)) < treeMinTopicPages {
			folded = append(folded, subtreePageIDs(topic)...)
			continue
		}
		kept = append(kept, topic)
	}
	return kept, folded
}

func subtreePageIDs(topic *draftTopic) []string {
	ids := append([]string(nil), topic.pageIDs...)
	for _, child := range topic.children {
		ids = append(ids, subtreePageIDs(child)...)
	}
	return ids
}

func (x *LLMTreeIndexer) emitTopics(
	tree *TopicTree,
	parentID string,
	topics []*draftTopic,
) {
	for _, topic := range topics {
		id := stableID("topic", parentID, topic.slug)
		tree.Topics = append(tree.Topics, Topic{
			ID: id, ParentID: parentID, Slug: topic.slug, Title: topic.title,
		})
		appendPlacements(tree, id, topic.pageIDs)
		if len(topic.pageIDs) > treeMaxDirectPages {
			x.logger.Warn(
				"Page Wiki topic exceeds the direct-page target",
				"topic", topic.slug, "pages", len(topic.pageIDs),
			)
		}
		x.emitTopics(tree, id, topic.children)
	}
}

func appendPlacements(tree *TopicTree, topicID string, pageIDs []string) {
	for rank, pageID := range pageIDs {
		tree.Placements = append(tree.Placements, PagePlacement{
			PageID: pageID, TopicID: topicID, Rank: rank,
		})
	}
}

func collectNodePages(node llmTreeNode) []string {
	slugs := append([]string(nil), node.Pages...)
	for _, child := range node.Children {
		slugs = append(slugs, collectNodePages(child)...)
	}
	return slugs
}

func topicSlug(title string) string {
	return strings.Trim(nonSlugCharacter.ReplaceAllString(
		strings.ToLower(strings.TrimSpace(title)), "-",
	), "-")
}

func treeIndexerPrompt(maxDepth int) string {
	return fmt.Sprintf(pageWikiTreeIndexerPromptTemplate, maxDepth)
}

const pageWikiTreeIndexerPromptTemplate = `You are the librarian of a durable, evidence-backed team Wiki.
You organize finished pages into a reader-facing topic tree; you never rewrite pages.
You receive one JSON object: {"pages":[{"slug","title","summary"}],"current_root_pages":[...],"current_topics":[{"title","pages","children"}]}.
current_root_pages and current_topics describe the tree as it stands today.
Return exactly one JSON object and no Markdown fence:
{"root_pages":["slug"],"topics":[{"title":"English topic name","pages":["slug"],"children":[{"title":"...","pages":["slug"],"children":[{"title":"...","pages":["slug"]}]}]}]}

Semantics are the only grouping principle. Group pages strictly by subject
matter. Never invent a catch-all topic such as "Misc", "Other", or
"General", and never group unrelated pages to satisfy a size rule; when no
coherent group exists, leave those pages in root_pages. Flat first: a
small wiki needs no topics at all. Introduce a topic only when more than
10 pages would otherwise sit together and a coherent group of at least 3
pages exists. Split a topic holding more than 10 direct pages into child
topics of at least 3 pages each, at most %d levels of topics deep. Preserve the
current tree's topic names and placements unless a rule above is violated
or a placement is clearly wrong: evolve the tree, do not reinvent it.
Every page slug must appear exactly once, in root_pages or under exactly
one topic. Return JSON only.`

var _ TreeIndexer = (*LLMTreeIndexer)(nil)
