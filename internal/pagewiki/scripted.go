package pagewiki

import (
	"context"
	"fmt"
)

type ScriptedPlanner struct {
	Briefs []PageBrief
	Err    error
	// Captured, when set, receives the PlanInput the last Plan call was
	// given, letting tests assert what the service threaded in (e.g. the
	// loaded GenerationDirectives).
	Captured *PlanInput
	// Calls, when set, is incremented on every Plan call, letting tests
	// assert the planner was never invoked.
	Calls *int
}

func (p ScriptedPlanner) Plan(
	_ context.Context,
	input PlanInput,
) ([]PageBrief, error) {
	if p.Calls != nil {
		*p.Calls++
	}
	if p.Captured != nil {
		*p.Captured = input
	}
	if p.Err != nil {
		return nil, p.Err
	}
	return append([]PageBrief(nil), p.Briefs...), nil
}

type ScriptedEditor struct {
	Drafts map[string]PageDraft
	Errors map[string]error
	// Captured, when set, receives the EditInput the last Edit call was
	// given, letting tests assert what the service threaded in (e.g. the
	// loaded GenerationDirectives).
	Captured *EditInput
}

func (e ScriptedEditor) Edit(
	_ context.Context,
	input EditInput,
) (PageDraft, error) {
	if e.Captured != nil {
		*e.Captured = input
	}
	if err := e.Errors[input.Brief.Key]; err != nil {
		return PageDraft{}, err
	}
	draft, found := e.Drafts[input.Brief.Key]
	if !found {
		return PageDraft{}, fmt.Errorf("scripted draft %q: %w", input.Brief.Key, ErrNotFound)
	}
	return draft, nil
}

type ScriptedTreeIndexer struct {
	Tree     TopicTree
	Err      error
	Captured *TreeIndexInput
}

func (t ScriptedTreeIndexer) Index(
	_ context.Context,
	input TreeIndexInput,
) (TopicTree, error) {
	if t.Captured != nil {
		*t.Captured = input
	}
	if t.Err != nil {
		return TopicTree{}, t.Err
	}
	return t.Tree, nil
}

type ScriptedCurator struct {
	PairVerdicts map[string]PairVerdict // key: sorted "idA|idB"
	PageVerdicts map[string]PageVerdict // key: pageID
	Verifies     map[string]VerifyVerdict // key: same as the judged candidate; zero value = not refuted
	Errs         map[string]error
	JudgeCalls   *int
	VerifyCalls  *int
}

func (c ScriptedCurator) JudgePair(
	_ context.Context,
	input PairJudgeInput,
) (PairVerdict, error) {
	if c.JudgeCalls != nil {
		*c.JudgeCalls++
	}
	key := PairKey(input.A.PageID, input.B.PageID)
	if err := c.Errs[key]; err != nil {
		return PairVerdict{}, err
	}
	verdict, found := c.PairVerdicts[key]
	if !found {
		return PairVerdict{}, fmt.Errorf("scripted pair verdict for %q: %w", key, ErrNotFound)
	}
	return verdict, nil
}

func (c ScriptedCurator) JudgePage(
	_ context.Context,
	input PageJudgeInput,
) (PageVerdict, error) {
	key := input.Page.PageID
	if err := c.Errs[key]; err != nil {
		return PageVerdict{}, err
	}
	verdict, found := c.PageVerdicts[key]
	if !found {
		return PageVerdict{}, fmt.Errorf("scripted page verdict for %q: %w", key, ErrNotFound)
	}
	return verdict, nil
}

func (c ScriptedCurator) Verify(
	_ context.Context,
	input VerifyInput,
) (VerifyVerdict, error) {
	if c.VerifyCalls != nil {
		*c.VerifyCalls++
	}
	key := string(input.Action) + "|" + input.Rationale
	if err := c.Errs[key]; err != nil {
		return VerifyVerdict{}, err
	}
	verdict, found := c.Verifies[key]
	if !found {
		return VerifyVerdict{}, fmt.Errorf("scripted verify verdict for %q: %w", key, ErrNotFound)
	}
	return verdict, nil
}

type ScriptedEmbedder struct {
	Vectors map[string][]float32 // key: exact input text; missing key = error
	Err     error
}

func (e ScriptedEmbedder) Embed(
	_ context.Context,
	texts []string,
) ([][]float32, error) {
	if e.Err != nil {
		return nil, e.Err
	}
	result := make([][]float32, 0, len(texts))
	for _, text := range texts {
		vector, found := e.Vectors[text]
		if !found {
			return nil, fmt.Errorf("scripted embedding for %q: %w", text, ErrNotFound)
		}
		result = append(result, vector)
	}
	return result, nil
}

// PairKey returns a sorted, "|"-joined key for two page IDs.
func PairKey(a, b string) string {
	if a <= b {
		return a + "|" + b
	}
	return b + "|" + a
}
