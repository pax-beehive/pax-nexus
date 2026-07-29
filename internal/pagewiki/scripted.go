package pagewiki

import (
	"context"
	"fmt"
)

type ScriptedPlanner struct {
	Briefs []PageBrief
	Err    error
}

func (p ScriptedPlanner) Plan(
	_ context.Context,
	_ PlanInput,
) ([]PageBrief, error) {
	if p.Err != nil {
		return nil, p.Err
	}
	return append([]PageBrief(nil), p.Briefs...), nil
}

type ScriptedEditor struct {
	Drafts map[string]PageDraft
	Errors map[string]error
}

func (e ScriptedEditor) Edit(
	_ context.Context,
	input EditInput,
) (PageDraft, error) {
	if err := e.Errors[input.Brief.Key]; err != nil {
		return PageDraft{}, err
	}
	draft, found := e.Drafts[input.Brief.Key]
	if !found {
		return PageDraft{}, fmt.Errorf("scripted draft %q: %w", input.Brief.Key, ErrNotFound)
	}
	return draft, nil
}
