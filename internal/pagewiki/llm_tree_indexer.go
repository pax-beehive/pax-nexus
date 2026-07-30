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
)

type LLMTreeIndexerConfig struct {
	Client llm.ChatClient
	Model  string
	Logger *slog.Logger
}

// LLMTreeIndexer organizes published pages into the reader-facing topic
// tree while deterministic code enforces coverage, depth, and density.
type LLMTreeIndexer struct {
	client llm.ChatClient
	model  string
	logger *slog.Logger
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
	return &LLMTreeIndexer{
		client: config.Client, model: strings.TrimSpace(config.Model), logger: logger,
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
	var lastErr error
	for attempt := 0; attempt < treeIndexerAttempts; attempt++ {
		response, err := x.client.Complete(ctx, llm.ChatRequest{
			Model: x.model,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: pageWikiTreeIndexerPrompt},
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
	roots := make([]*draftTopic, 0, len(decoded.Topics))
	rootIndex := make(map[string]*draftTopic)
	for _, node := range decoded.Topics {
		slug := topicSlug(node.Title)
		if slug == "" {
			continue
		}
		root, found := rootIndex[slug]
		if !found {
			root = &draftTopic{slug: slug, title: strings.TrimSpace(node.Title)}
			rootIndex[slug] = root
			roots = append(roots, root)
		}
		for _, pageSlug := range node.Pages {
			if pageID, ok := claim(pageSlug); ok {
				root.pageIDs = append(root.pageIDs, pageID)
			}
		}
		childIndex := make(map[string]*draftTopic)
		for _, existing := range root.children {
			childIndex[existing.slug] = existing
		}
		for _, childNode := range node.Children {
			childSlug := topicSlug(childNode.Title)
			if childSlug == "" || childSlug == slug {
				for _, pageSlug := range collectNodePages(childNode) {
					if pageID, ok := claim(pageSlug); ok {
						root.pageIDs = append(root.pageIDs, pageID)
					}
				}
				continue
			}
			child, exists := childIndex[childSlug]
			if !exists {
				child = &draftTopic{slug: childSlug, title: strings.TrimSpace(childNode.Title)}
				childIndex[childSlug] = child
				root.children = append(root.children, child)
			}
			for _, pageSlug := range collectNodePages(childNode) {
				if pageID, ok := claim(pageSlug); ok {
					child.pageIDs = append(child.pageIDs, pageID)
				}
			}
		}
	}
	tree := TopicTree{Topics: make([]Topic, 0), Placements: make([]PagePlacement, 0)}
	unplacedBudget := 0
	for _, page := range catalog {
		if _, found := placed[page.ID]; !found {
			unplacedBudget++
		}
	}
	for _, root := range roots {
		kept := root.children[:0]
		for _, child := range root.children {
			if len(child.pageIDs) < treeMinTopicPages {
				root.pageIDs = append(root.pageIDs, child.pageIDs...)
				continue
			}
			kept = append(kept, child)
		}
		root.children = kept
		total := len(root.pageIDs)
		for _, child := range root.children {
			total += len(child.pageIDs)
		}
		if total < treeMinTopicPages {
			unplacedBudget += total
			continue
		}
		rootID := stableID("topic", "", root.slug)
		tree.Topics = append(tree.Topics, Topic{
			ID: rootID, Slug: root.slug, Title: root.title,
		})
		appendPlacements(&tree, rootID, root.pageIDs)
		if len(root.pageIDs) > treeMaxDirectPages {
			x.logger.Warn(
				"Page Wiki topic exceeds the direct-page target",
				"topic", root.slug, "pages", len(root.pageIDs),
			)
		}
		for _, child := range root.children {
			childID := stableID("topic", rootID, child.slug)
			tree.Topics = append(tree.Topics, Topic{
				ID: childID, ParentID: rootID, Slug: child.slug, Title: child.title,
			})
			appendPlacements(&tree, childID, child.pageIDs)
			if len(child.pageIDs) > treeMaxDirectPages {
				x.logger.Warn(
					"Page Wiki topic exceeds the direct-page target",
					"topic", child.slug, "pages", len(child.pageIDs),
				)
			}
		}
	}
	if unplacedBudget > treeMaxDirectPages {
		x.logger.Warn(
			"Page Wiki root exceeds the direct-page target",
			"pages", unplacedBudget,
		)
	}
	return tree
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

const pageWikiTreeIndexerPrompt = `You are the librarian of a durable, evidence-backed team Wiki.
You organize finished pages into a reader-facing topic tree; you never rewrite pages.
You receive one JSON object: {"pages":[{"slug","title","summary"}],"current_root_pages":[...],"current_topics":[{"title","pages","children"}]}.
current_root_pages and current_topics describe the tree as it stands today.
Return exactly one JSON object and no Markdown fence:
{"root_pages":["slug"],"topics":[{"title":"English topic name","pages":["slug"],"children":[{"title":"...","pages":["slug"]}]}]}

Semantics are the only grouping principle. Group pages strictly by subject
matter. Never invent a catch-all topic such as "Misc", "Other", or
"General", and never group unrelated pages to satisfy a size rule; when no
coherent group exists, leave those pages in root_pages. Flat first: a
small wiki needs no topics at all. Introduce a topic only when more than
10 pages would otherwise sit together and a coherent group of at least 3
pages exists. Split a topic holding more than 10 direct pages into child
topics of at least 3 pages each, at most two levels deep. Preserve the
current tree's topic names and placements unless a rule above is violated
or a placement is clearly wrong: evolve the tree, do not reinvent it.
Every page slug must appear exactly once, in root_pages or under exactly
one topic. Return JSON only.`

var _ TreeIndexer = (*LLMTreeIndexer)(nil)
