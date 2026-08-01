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

const curatorAttempts = 2

type LLMCuratorConfig struct {
	Client llm.ChatClient
	Model  string
	Logger *slog.Logger
}

// LLMCurator lets the model judge whether pages should merge, conflict,
// retire, or be rewritten, and skeptically double-checks its own destructive
// verdicts, while deterministic code in Service retains control of
// candidate selection, publication, and retirement. It is the only layer in
// the curation pipeline that retries a failed LLM call; Service degrades to
// keep immediately on any error this adapter returns.
type LLMCurator struct {
	client llm.ChatClient
	model  string
	logger *slog.Logger
}

func NewLLMCurator(config LLMCuratorConfig) (*LLMCurator, error) {
	if config.Client == nil {
		return nil, errors.New("create Page Wiki LLM curator: client is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("create Page Wiki LLM curator: model is required")
	}
	logger := config.Logger
	if logger == nil {
		logger = observability.DiscardLogger()
	}
	return &LLMCurator{
		client: config.Client, model: strings.TrimSpace(config.Model), logger: logger,
	}, nil
}

type llmCuratorQuote struct {
	Text        string `json:"text"`
	RecencyRank int    `json:"recency_rank"`
}

// llmCuratorPageView is the wire shape of a CurationPageView: it never
// carries PageID, so the model never sees internal page identifiers.
type llmCuratorPageView struct {
	Title    string            `json:"title"`
	Summary  string            `json:"summary"`
	Markdown string            `json:"markdown"`
	Quotes   []llmCuratorQuote `json:"quotes"`
}

type llmCuratorSectionDraft struct {
	Key      string `json:"key"`
	Heading  string `json:"heading"`
	Markdown string `json:"markdown"`
}

type llmCuratorDraft struct {
	Title    string                   `json:"title"`
	Summary  string                   `json:"summary"`
	Sections []llmCuratorSectionDraft `json:"sections"`
}

type llmCuratorPairRequest struct {
	A llmCuratorPageView `json:"a"`
	B llmCuratorPageView `json:"b"`
}

type llmCuratorPairResponse struct {
	Verdict   string           `json:"verdict"`
	Rationale string           `json:"rationale"`
	Draft     *llmCuratorDraft `json:"draft,omitempty"`
}

type llmCuratorPageRequest struct {
	Page    llmCuratorPageView `json:"page"`
	Signals []string           `json:"signals"`
}

type llmCuratorPageResponse struct {
	Verdict   string           `json:"verdict"`
	Rationale string           `json:"rationale"`
	Draft     *llmCuratorDraft `json:"draft,omitempty"`
}

type llmCuratorVerifyRequest struct {
	Action    string               `json:"action"`
	Rationale string               `json:"rationale"`
	Pages     []llmCuratorPageView `json:"pages"`
}

type llmCuratorVerifyResponse struct {
	Refuted   bool   `json:"refuted"`
	Rationale string `json:"rationale"`
}

func (c *LLMCurator) JudgePair(ctx context.Context, input PairJudgeInput) (PairVerdict, error) {
	payload, err := json.Marshal(llmCuratorPairRequest{
		A: curatorPageViewRequest(input.A),
		B: curatorPageViewRequest(input.B),
	})
	if err != nil {
		return PairVerdict{}, fmt.Errorf("encode Page Wiki curator pair request: %w", err)
	}
	systemPrompt := pageWikiCuratorPairPrompt + generationDirectivesPrompt(input.Directives)
	var lastErr error
	for attempt := 0; attempt < curatorAttempts; attempt++ {
		response, err := c.client.Complete(ctx, llm.ChatRequest{
			Model: c.model,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: string(payload)},
			},
		})
		if err != nil {
			lastErr = err
			continue
		}
		var decoded llmCuratorPairResponse
		if err := json.Unmarshal([]byte(trimJSONFence(response.Message.Content)), &decoded); err != nil {
			lastErr = err
			continue
		}
		verdict, err := decodePairVerdict(decoded)
		if err != nil {
			lastErr = err
			continue
		}
		return verdict, nil
	}
	return PairVerdict{}, fmt.Errorf("judge Page Wiki curator pair: %w", lastErr)
}

func (c *LLMCurator) JudgePage(ctx context.Context, input PageJudgeInput) (PageVerdict, error) {
	payload, err := json.Marshal(llmCuratorPageRequest{
		Page:    curatorPageViewRequest(input.Page),
		Signals: input.Signals,
	})
	if err != nil {
		return PageVerdict{}, fmt.Errorf("encode Page Wiki curator page request: %w", err)
	}
	systemPrompt := pageWikiCuratorPagePrompt + generationDirectivesPrompt(input.Directives)
	var lastErr error
	for attempt := 0; attempt < curatorAttempts; attempt++ {
		response, err := c.client.Complete(ctx, llm.ChatRequest{
			Model: c.model,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: string(payload)},
			},
		})
		if err != nil {
			lastErr = err
			continue
		}
		var decoded llmCuratorPageResponse
		if err := json.Unmarshal([]byte(trimJSONFence(response.Message.Content)), &decoded); err != nil {
			lastErr = err
			continue
		}
		verdict, err := decodePageVerdict(decoded)
		if err != nil {
			lastErr = err
			continue
		}
		return verdict, nil
	}
	return PageVerdict{}, fmt.Errorf("judge Page Wiki curator page: %w", lastErr)
}

func (c *LLMCurator) Verify(ctx context.Context, input VerifyInput) (VerifyVerdict, error) {
	pages := make([]llmCuratorPageView, 0, len(input.Pages))
	for _, page := range input.Pages {
		pages = append(pages, curatorPageViewRequest(page))
	}
	payload, err := json.Marshal(llmCuratorVerifyRequest{
		Action: string(input.Action), Rationale: input.Rationale, Pages: pages,
	})
	if err != nil {
		return VerifyVerdict{}, fmt.Errorf("encode Page Wiki curator verify request: %w", err)
	}
	systemPrompt := pageWikiCuratorVerifyPrompt + generationDirectivesPrompt(input.Directives)
	var lastErr error
	for attempt := 0; attempt < curatorAttempts; attempt++ {
		response, err := c.client.Complete(ctx, llm.ChatRequest{
			Model: c.model,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: string(payload)},
			},
		})
		if err != nil {
			lastErr = err
			continue
		}
		var decoded llmCuratorVerifyResponse
		if err := json.Unmarshal([]byte(trimJSONFence(response.Message.Content)), &decoded); err != nil {
			lastErr = err
			continue
		}
		return VerifyVerdict(decoded), nil
	}
	return VerifyVerdict{}, fmt.Errorf("verify Page Wiki curator action: %w", lastErr)
}

// curatorPageViewRequest builds the wire payload for a page view. PageID is
// never serialized: the LLM judges subject matter and prose, never internal
// identifiers.
func curatorPageViewRequest(view CurationPageView) llmCuratorPageView {
	quotes := make([]llmCuratorQuote, 0, len(view.Quotes))
	for _, quote := range view.Quotes {
		quotes = append(quotes, llmCuratorQuote{
			Text: quote.ExactText, RecencyRank: quote.SourceOrdinal,
		})
	}
	return llmCuratorPageView{
		Title: view.Title, Summary: view.Summary, Markdown: view.Markdown, Quotes: quotes,
	}
}

func curatorDraft(decoded llmCuratorDraft) CurationDraft {
	sections := make([]SectionDraft, 0, len(decoded.Sections))
	for _, section := range decoded.Sections {
		sections = append(sections, SectionDraft(section))
	}
	return CurationDraft{Title: decoded.Title, Summary: decoded.Summary, Sections: sections}
}

func decodePairVerdict(decoded llmCuratorPairResponse) (PairVerdict, error) {
	switch CurationVerdict(decoded.Verdict) {
	case CurationVerdictMerge, CurationVerdictConflict:
		if decoded.Draft == nil {
			return PairVerdict{}, fmt.Errorf(
				"decode Page Wiki curator pair verdict %q: draft is required", decoded.Verdict,
			)
		}
		draft := curatorDraft(*decoded.Draft)
		return PairVerdict{
			Verdict: CurationVerdict(decoded.Verdict), Rationale: decoded.Rationale, Draft: &draft,
		}, nil
	case CurationVerdictDistinct:
		return PairVerdict{Verdict: CurationVerdictDistinct, Rationale: decoded.Rationale}, nil
	default:
		return PairVerdict{}, fmt.Errorf(
			"decode Page Wiki curator pair verdict: unknown verdict %q", decoded.Verdict,
		)
	}
}

func decodePageVerdict(decoded llmCuratorPageResponse) (PageVerdict, error) {
	switch CurationVerdict(decoded.Verdict) {
	case CurationVerdictRewrite:
		if decoded.Draft == nil {
			return PageVerdict{}, fmt.Errorf(
				"decode Page Wiki curator page verdict %q: draft is required", decoded.Verdict,
			)
		}
		draft := curatorDraft(*decoded.Draft)
		return PageVerdict{
			Verdict: CurationVerdictRewrite, Rationale: decoded.Rationale, Draft: &draft,
		}, nil
	case CurationVerdictRetire, CurationVerdictKeep:
		return PageVerdict{Verdict: CurationVerdict(decoded.Verdict), Rationale: decoded.Rationale}, nil
	default:
		return PageVerdict{}, fmt.Errorf(
			"decode Page Wiki curator page verdict: unknown verdict %q", decoded.Verdict,
		)
	}
}

const pageWikiCuratorPairPrompt = `You are the deduplication judge of a durable, evidence-backed team Wiki.
You receive one JSON object comparing two pages: {"a":{"title","summary","markdown","quotes":[{"text","recency_rank"}]},"b":{...}}. A higher recency_rank means that quote's evidence was gathered more recently.
Return exactly one JSON object and no Markdown fence:
{"verdict":"merge|conflict|distinct","rationale":"one English sentence","draft":{"title","summary","sections":[{"key","heading","markdown"}]}}

Default to distinct: two pages are distinct unless they clearly describe
the same subject — the same component, decision, convention, or domain
fact, not merely a related or overlapping topic. Use merge when both pages
describe the same subject and their facts agree or complement each other;
the draft folds both pages into one durable article covering the union of
their content. Use conflict only when the two pages state facts about the
same subject that cannot both be true — a superseded decision, a changed
value, a contradicted status. A conflict draft keeps the claim carried by
the higher recency_rank quotes and states in prose, plainly, what was
superseded and why the newer claim wins; it never silently drops the
older claim. draft.title is a concise noun phrase naming the merged or
surviving subject — at most five words, never a sentence, no trailing
period, and never opening with a verb or gerund such as "Fixing" or
"Updating". draft is required for merge and conflict; when the verdict is
distinct, omit draft or leave it empty — it is ignored either way. Return
JSON only.`

const pageWikiCuratorPagePrompt = `You are the maintenance reviewer of a durable, evidence-backed team Wiki.
You receive one JSON object: {"page":{"title","summary","markdown","quotes":[{"text","recency_rank"}]},"signals":["orphan","short-body","sentence-title"]}. signals are human-readable reasons this page was selected for review, not verdicts, and never dictate the outcome by themselves.
Return exactly one JSON object and no Markdown fence:
{"verdict":"retire|rewrite|keep","rationale":"one English sentence","draft":{"title","summary","sections":[{"key","heading","markdown"}]}}

Default to keep: when in doubt, keep the page as it stands. Use retire
only for content that has no durable team value at all — a one-off
session log, a transient operational or verification record, a fragment,
or noise that never should have become a page. Retiring removes the page
outright, so reserve it for content nobody will ever need again, not
merely content that is currently thin or poorly organized. Use rewrite
when the subject is durable and worth keeping but the page itself
violates the Wiki's title or structure rules — a sentence-shaped title, a
missing or vague summary, disorganized or padded sections — and a rewrite
would fix the presentation without inventing or dropping facts.
draft.title is a concise noun phrase naming the page's concept — at most
five words, never a sentence, no trailing period, and never opening with
a verb or gerund. draft is required for rewrite; when the verdict is
retire or keep, omit draft or leave it empty — it is ignored either way.
Return JSON only.`

const pageWikiCuratorVerifyPrompt = `You are the skeptic of a durable, evidence-backed team Wiki: the last brake before a destructive change is applied.
You receive one JSON object: {"action":"merge|conflict|retire","rationale":"...","pages":[{"title","summary","markdown","quotes":[{"text","recency_rank"}]}]}. action and rationale are another reviewer's proposed verdict and its justification; pages are the page or pages that verdict would apply to.
Return exactly one JSON object and no Markdown fence:
{"refuted":true|false,"rationale":"one English sentence"}

Your job is to argue against the action, not to rubber-stamp it. Actively
look for the strongest reason the proposed action is wrong: a subject
that is not actually the same, facts that are not actually incompatible,
content that still holds durable value the reviewer missed, a claim the
rationale overstates. Set refuted true whenever any real doubt remains.
Set refuted false only when the case for the action is unambiguous — you
looked hard for a reason to refute it and genuinely found none worth
stating. You are the conservative brake in this pipeline: when uncertain,
refute. Return JSON only.`

var _ Curator = (*LLMCurator)(nil)
