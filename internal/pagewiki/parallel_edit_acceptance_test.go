package pagewiki_test

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/require"
)

// barrierEditor blocks every Edit call until release is closed, so the test
// can observe whether two edits are in flight at the same time.
type barrierEditor struct {
	inner   pagewiki.ScriptedEditor
	entered chan string
	release chan struct{}
}

func (e *barrierEditor) Edit(
	ctx context.Context,
	input pagewiki.EditInput,
) (pagewiki.PageDraft, error) {
	e.entered <- input.Brief.Key
	<-e.release
	return e.inner.Edit(ctx, input)
}

func TestGivenTwoBriefsWhenInjectedThenEditsRunConcurrently(t *testing.T) {
	repository := memory.NewRepository()
	editor := &barrierEditor{
		inner:   pagewiki.ScriptedEditor{Drafts: multiPageDrafts(false)},
		entered: make(chan string, 2),
		release: make(chan struct{}),
	}
	service := pagewiki.NewService(
		repository,
		pagewiki.ScriptedPlanner{Briefs: multiPageBriefs()},
		editor,
	)

	type injectOutcome struct {
		result pagewiki.InjectResult
		err    error
	}
	done := make(chan injectOutcome, 1)
	go func() {
		result, err := service.InjectSession(context.Background(), multiPageSource())
		done <- injectOutcome{result: result, err: err}
	}()

	// Both edits must enter before either is released: proves concurrency.
	deadline := time.After(5 * time.Second)
	for range 2 {
		select {
		case <-editor.entered:
		case <-deadline:
			t.Fatal("edits did not run concurrently: second Edit never started")
		}
	}
	close(editor.release)

	outcome := <-done
	require.NoError(t, outcome.err)
	require.Equal(t, pagewiki.RunStatusSucceeded, outcome.result.Run.Status)
	require.Len(t, outcome.result.Run.Targets, 2)

	_, err := repository.PageBySlug(context.Background(), "sqlite")
	require.NoError(t, err)
	_, err = repository.PageBySlug(context.Background(), "wiki-search")
	require.NoError(t, err)
}
