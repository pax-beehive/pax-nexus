package pagewiki

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

const (
	// placementAttempts is passed straight through to llm.CompleteJSON: a
	// single request that CompleteJSON itself may retry on transport or
	// decode failure.
	placementAttempts = 2
	// splitRounds is the number of independent requests SplitTopic may
	// issue: the first attempt, plus one retry that feeds the previous
	// validation error back to the model.
	splitRounds = 2
)

// LLMTreeNavigatorConfig configures NewLLMTreeNavigator.
type LLMTreeNavigatorConfig struct {
	Client llm.ChatClient
	Model  string
	Logger *slog.Logger
}

// LLMTreeNavigator places one page at a time into the topic tree and, when a
// topic grows too large, splits it into child topics. It never sees the
// whole tree at once: it only looks at the page (or overflowing topic) in
// front of it plus its immediate siblings, which keeps incremental
// maintenance cheap as the wiki grows.
type LLMTreeNavigator struct {
	client llm.ChatClient
	model  string
	logger *slog.Logger
}

func NewLLMTreeNavigator(config LLMTreeNavigatorConfig) (*LLMTreeNavigator, error) {
	if config.Client == nil {
		return nil, errors.New("create Page Wiki tree navigator: client is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("create Page Wiki tree navigator: model is required")
	}
	logger := config.Logger
	if logger == nil {
		logger = observability.DiscardLogger()
	}
	return &LLMTreeNavigator{
		client: config.Client,
		model:  strings.TrimSpace(config.Model),
		logger: logger,
	}, nil
}

// llmTreePageView is the page shape sent to the model, shared by placement
// and split requests.
type llmTreePageView struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}

type llmChildView struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Pages int    `json:"pages"`
}

type llmPlacementRequest struct {
	Page      llmTreePageView `json:"page"`
	Path      []string        `json:"path"`
	Children  []llmChildView  `json:"children"`
	MayCreate bool            `json:"may_create"`
}

type llmPlacementChoice struct {
	Action string `json:"action"`
	Slug   string `json:"slug,omitempty"`
	Title  string `json:"title,omitempty"`
}

// ChoosePlacement asks the model where a single page belongs at the current
// level of the topic tree. Every form of hallucination the model could
// produce — an unrecognized action, "enter" naming a slug that is not
// actually one of the listed children, or "create" used when it was not
// offered or without a usable title — is normalized to "stay" here, so
// callers never have to re-validate the result.
func (n *LLMTreeNavigator) ChoosePlacement(
	ctx context.Context,
	input TreePlacementInput,
) (TreePlacementChoice, error) {
	request := llmPlacementRequest{
		Page: llmTreePageView{
			Slug: input.Page.Slug, Title: input.Page.Title, Summary: input.Page.Summary,
		},
		Path:      input.Path,
		Children:  make([]llmChildView, 0, len(input.Children)),
		MayCreate: input.AllowCreate,
	}
	for _, child := range input.Children {
		request.Children = append(request.Children, llmChildView(child))
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return TreePlacementChoice{}, fmt.Errorf("encode Page Wiki placement request: %w", err)
	}
	systemPrompt := pageWikiPlacementPrompt + generationDirectivesPrompt(input.Directives)
	decoded, err := llm.CompleteJSON[llmPlacementChoice](ctx, n.client, llm.ChatRequest{
		Model: n.model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: string(payload)},
		},
	}, placementAttempts)
	if err != nil {
		return TreePlacementChoice{}, fmt.Errorf("choose Page Wiki placement: %w", err)
	}
	return normalizePlacementChoice(decoded, input), nil
}

// normalizePlacementChoice is the anti-hallucination backstop: any decoded
// action that is not safely actionable given input falls back to "stay".
func normalizePlacementChoice(decoded llmPlacementChoice, input TreePlacementInput) TreePlacementChoice {
	switch TreePlacementAction(decoded.Action) {
	case TreePlacementStay:
		return TreePlacementChoice{Action: TreePlacementStay}
	case TreePlacementEnter:
		for _, child := range input.Children {
			if child.Slug == decoded.Slug {
				return TreePlacementChoice{Action: TreePlacementEnter, Slug: child.Slug}
			}
		}
		return TreePlacementChoice{Action: TreePlacementStay}
	case TreePlacementCreate:
		title := strings.TrimSpace(decoded.Title)
		if !input.AllowCreate || title == "" {
			return TreePlacementChoice{Action: TreePlacementStay}
		}
		return TreePlacementChoice{Action: TreePlacementCreate, Title: title}
	default:
		return TreePlacementChoice{Action: TreePlacementStay}
	}
}

type llmSplitRequest struct {
	Path      []string          `json:"path"`
	Pages     []llmTreePageView `json:"pages"`
	Forbidden []string          `json:"forbidden"`
}

type llmSplitResponse struct {
	Groups []llmSplitGroup `json:"groups"`
}

type llmSplitGroup struct {
	Title string   `json:"title"`
	Pages []string `json:"pages"`
}

// SplitTopic asks the model to break an overflowing topic's direct pages
// into a handful of child-topic groups. It allows at most two rounds: if the
// first response fails validateSplitGroups (or fails to decode at all), the
// validation error is appended to the user message as "previous_error" and
// the model gets one corrected attempt before SplitTopic gives up.
func (n *LLMTreeNavigator) SplitTopic(
	ctx context.Context,
	input TreeSplitInput,
) ([]TreeSplitGroup, error) {
	request := llmSplitRequest{
		Path:      input.Path,
		Pages:     make([]llmTreePageView, 0, len(input.Pages)),
		Forbidden: input.Forbidden,
	}
	for _, page := range input.Pages {
		request.Pages = append(request.Pages, llmTreePageView(page))
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode Page Wiki split request: %w", err)
	}
	systemPrompt := pageWikiSplitPrompt + generationDirectivesPrompt(input.Directives)
	userContent := string(payload)

	var lastErr error
	for round := 0; round < splitRounds; round++ {
		decoded, err := llm.CompleteJSON[llmSplitResponse](ctx, n.client, llm.ChatRequest{
			Model: n.model,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: userContent},
			},
		}, 1)
		if err != nil {
			lastErr = err
			userContent = appendPreviousError(string(payload), err.Error())
			continue
		}
		if verr := validateSplitGroups(decoded.Groups, input); verr != nil {
			lastErr = verr
			userContent = appendPreviousError(string(payload), verr.Error())
			continue
		}
		return toTreeSplitGroups(decoded.Groups), nil
	}
	return nil, fmt.Errorf("split Page Wiki topic: %w", lastErr)
}

// appendPreviousError appends a {"previous_error": "..."} object to the end
// of the original user message so the retry carries both the original
// request and what went wrong with the model's last answer.
func appendPreviousError(originalPayload, message string) string {
	errPayload, err := json.Marshal(struct {
		PreviousError string `json:"previous_error"`
	}{PreviousError: message})
	if err != nil {
		// json.Marshal on a plain string field cannot fail; this is
		// unreachable defensive code.
		return originalPayload
	}
	return originalPayload + "\n" + string(errPayload)
}

func toTreeSplitGroups(groups []llmSplitGroup) []TreeSplitGroup {
	result := make([]TreeSplitGroup, 0, len(groups))
	for _, group := range groups {
		result = append(result, TreeSplitGroup{
			Title: strings.TrimSpace(group.Title),
			Pages: append([]string(nil), group.Pages...),
		})
	}
	return result
}

// validateSplitGroups enforces every hard rule SplitTopic's caller relies
// on. Checks run in priority order — the first violation found is the one
// reported — so a single retry round gives the model one specific, concrete
// thing to fix rather than a dump of everything that might be wrong.
func validateSplitGroups(groups []llmSplitGroup, input TreeSplitInput) error {
	if len(groups) < 2 || len(groups) > 6 {
		return fmt.Errorf("分组数量必须在 2 到 6 之间,实际返回了 %d 组", len(groups))
	}
	slugs, err := splitGroupSlugs(groups, input.Forbidden)
	if err != nil {
		return err
	}
	for index, group := range groups {
		if len(group.Pages) < 2 {
			return fmt.Errorf("分组 %q 至少需要 2 个页面,实际只有 %d 个", slugs[index], len(group.Pages))
		}
	}
	return validateSplitCoverage(groups, input.Pages)
}

// splitGroupSlugs derives each group's topic slug from its title, rejecting
// an empty title, a slug repeated across groups, or a slug that collides
// with an existing sibling topic named in forbidden.
func splitGroupSlugs(groups []llmSplitGroup, forbiddenSlugs []string) ([]string, error) {
	slugs := make([]string, len(groups))
	for index, group := range groups {
		slug := topicSlug(group.Title)
		if slug == "" {
			return nil, fmt.Errorf("第 %d 组的标题不能为空", index+1)
		}
		slugs[index] = slug
	}
	forbidden := make(map[string]struct{}, len(forbiddenSlugs))
	for _, slug := range forbiddenSlugs {
		forbidden[slug] = struct{}{}
	}
	seen := make(map[string]struct{}, len(slugs))
	for _, slug := range slugs {
		if _, duplicate := seen[slug]; duplicate {
			return nil, fmt.Errorf("分组标题 %q 与另一组重复,请换一个名字", slug)
		}
		seen[slug] = struct{}{}
		if _, blocked := forbidden[slug]; blocked {
			return nil, fmt.Errorf("分组标题 %q 与现有的同级主题冲突,请换一个名字", slug)
		}
	}
	return slugs, nil
}

// validateSplitCoverage checks that the groups' pages, taken together,
// exactly match the input page set: no page repeated across groups, none
// left out, and none invented.
func validateSplitCoverage(groups []llmSplitGroup, pages []TreeSplitPage) error {
	inputSlugs := make(map[string]struct{}, len(pages))
	for _, page := range pages {
		inputSlugs[page.Slug] = struct{}{}
	}
	occurrences := make(map[string]int, len(pages))
	for _, group := range groups {
		for _, slug := range group.Pages {
			occurrences[slug]++
		}
	}
	if duplicates := slugsWhere(occurrences, func(slug string, count int) bool {
		return count > 1
	}); len(duplicates) > 0 {
		return fmt.Errorf("页面 %s 在多个分组中重复出现,每个页面只能属于一个分组", strings.Join(duplicates, "、"))
	}
	missing := make([]string, 0)
	for slug := range inputSlugs {
		if _, found := occurrences[slug]; !found {
			missing = append(missing, slug)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("页面 %s 没有出现在任何分组中,必须全部分配", strings.Join(missing, "、"))
	}
	extra := make([]string, 0)
	for slug := range occurrences {
		if _, found := inputSlugs[slug]; !found {
			extra = append(extra, slug)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Errorf("分组中出现了未知页面 %s,只能使用给定的页面", strings.Join(extra, "、"))
	}
	return nil
}

// slugsWhere returns, sorted, the slugs whose occurrence count satisfies
// keep.
func slugsWhere(occurrences map[string]int, keep func(slug string, count int) bool) []string {
	matched := make([]string, 0)
	for slug, count := range occurrences {
		if keep(slug, count) {
			matched = append(matched, slug)
		}
	}
	sort.Strings(matched)
	return matched
}

const pageWikiPlacementPrompt = `You are the librarian of a durable, evidence-backed team Wiki.
You place one finished page into the topic tree, one level at a time; you never rewrite pages.
You receive one JSON object: {"page":{"slug","title","summary"},"path":["topic title", ...],"children":[{"slug","title","pages"}],"may_create":true|false}.
"path" lists the topic titles from the root down to the level you are looking at right now; an empty path means
you are looking at the root level. "children" lists the existing child topics at this level, each with its slug,
title, and current page count.
Decide where this one page belongs at this level and return exactly one JSON object and no Markdown fence:
{"action":"stay","slug":"","title":""}
Choose "stay" to leave the page at this level, whether that is the root or the topic named by the last entry in
"path". Choose "enter" only when one of the listed children is clearly the right subject-matter home for the
page; set "slug" to that child's slug exactly as given in "children" — never invent a slug that was not listed.
Choose "create" only when "may_create" is true and none of the listed children is a good fit for the page's
subject; set "title" to a new topic name that is a noun phrase of one to three words, never a full sentence, and
never a catch-all name such as "Misc", "Other", "General", or "Miscellaneous". When you are unsure whether an
existing child truly fits, or unsure whether a new topic is warranted, choose "stay" — leaving a page one level
higher than its ideal home is always safer than entering the wrong topic or inventing a redundant one. Return
JSON only.`

const pageWikiSplitPrompt = `You are the librarian of a durable, evidence-backed team Wiki.
One topic in the tree now holds too many pages directly and must be split into a small number of child topics;
you never rewrite pages, only regroup them.
You receive one JSON object: {"path":["topic title", ...],"pages":[{"slug","title","summary"}],"forbidden":["slug", ...]}.
"path" lists the topic titles from the root down to the topic being split; an empty path means you are splitting
the root level. "pages" lists every page that currently sits directly under that topic; each one must end up in
exactly one new group. "forbidden" lists sibling child-topic slugs that already exist at this level, so your new
group titles must not collide with any of them.
Group the pages strictly by subject matter into two to six coherent groups and return exactly one JSON object and
no Markdown fence:
{"groups":[{"title":"English topic name","pages":["slug","slug"]}]}
Each group's title is a noun phrase of one to three words naming the subject area, never a full sentence, and
never a catch-all name such as "Misc", "Other", "General", or "Miscellaneous". Every group must hold at least two
pages. Every page slug listed in "pages" must appear in exactly one group of your answer: none left out, none
invented, and none placed into more than one group. If the request also includes a "previous_error" field, it
describes exactly what was wrong with your previous answer to this same request; fix that specific problem and
return a corrected, complete JSON object — do not repeat the same mistake. Return JSON only.`

var _ TreeNavigator = (*LLMTreeNavigator)(nil)
