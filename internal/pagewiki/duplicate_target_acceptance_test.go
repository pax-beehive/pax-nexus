package pagewiki_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/require"
)

type countingEditor struct {
	inner pagewiki.ScriptedEditor
	calls atomic.Int32
}

func (e *countingEditor) Edit(
	ctx context.Context,
	input pagewiki.EditInput,
) (pagewiki.PageDraft, error) {
	e.calls.Add(1)
	return e.inner.Edit(ctx, input)
}

func TestGivenTwoUpdateBriefsForSamePageThenSecondFailsWithoutAnEditCall(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewRepository()

	// Seed run: create the page the update briefs will both target.
	seedService := pagewiki.NewService(
		repository,
		pagewiki.ScriptedPlanner{Briefs: multiPageBriefs()[:1]},
		pagewiki.ScriptedEditor{Drafts: multiPageDrafts(false)},
	)
	seedResult, err := seedService.InjectSession(ctx, multiPageSource())
	require.NoError(t, err)
	require.Equal(t, pagewiki.RunStatusSucceeded, seedResult.Run.Status)
	page, err := repository.PageBySlug(ctx, "sqlite")
	require.NoError(t, err)

	// Second run: two update briefs aimed at the same page.
	raw := "event-review: The team reaffirmed SQLite after the storage review."
	reviewText := "The team reaffirmed SQLite after the storage review."
	reviewStart := strings.Index(raw, reviewText)
	request := pagewiki.InjectSessionRequest{
		SourceID:       "session-review",
		IdempotencyKey: "session-review-injection",
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{{
			ID:        "event-review",
			StartByte: reviewStart,
			EndByte:   reviewStart + len(reviewText),
		}},
	}
	updateBrief := func(key string) pagewiki.PageBrief {
		return pagewiki.PageBrief{
			Key:                    key,
			Action:                 pagewiki.PageActionUpdate,
			TargetPageID:           page.ID,
			ExpectedBaseRevisionID: page.CurrentRevisionID,
			EvidenceEventIDs:       []string{"event-review"},
		}
	}
	updatedDraft := pagewiki.PageDraft{
		Slug:    "sqlite",
		Title:   "SQLite",
		Summary: "SQLite stores the local Wiki and survived the storage review.",
		Sections: []pagewiki.SectionDraft{{
			Key:      "decision",
			Heading:  "Decision",
			Markdown: "The team reaffirmed SQLite after the storage review.",
		}},
		Citations: []pagewiki.CitationDraft{{
			SectionKey: "decision",
			ExactText:  "reaffirmed SQLite",
			Evidence: []pagewiki.EvidenceQuoteDraft{{
				EventID:   "event-review",
				ExactText: reviewText,
			}},
		}},
	}
	editor := &countingEditor{inner: pagewiki.ScriptedEditor{
		Drafts: map[string]pagewiki.PageDraft{
			"first":  updatedDraft,
			"second": updatedDraft,
		},
	}}
	service := pagewiki.NewService(
		repository,
		pagewiki.ScriptedPlanner{Briefs: []pagewiki.PageBrief{
			updateBrief("first"),
			updateBrief("second"),
		}},
		editor,
	)

	result, err := service.InjectSession(ctx, request)

	require.NoError(t, err)
	require.Equal(t, pagewiki.RunStatusPartialSuccess, result.Run.Status)
	require.Equal(t, pagewiki.TargetStatusSucceeded, result.Run.Targets[0].Status)
	require.Equal(t, pagewiki.TargetStatusFailed, result.Run.Targets[1].Status)
	require.Equal(
		t,
		pagewiki.TargetFailurePublicationConflict,
		result.Run.Targets[1].FailureReason,
	)
	require.EqualValues(t, 1, editor.calls.Load(),
		"duplicate target must not burn an editor call")
}
