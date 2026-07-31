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
