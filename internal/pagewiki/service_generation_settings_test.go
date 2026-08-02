package pagewiki_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/require"
)

func TestGivenValidDirectivesWhenSetThenServiceValidatesAndRoundTripsThem(t *testing.T) {
	t.Parallel()
	repository := memory.NewRepository()
	service := pagewiki.NewService(repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{})

	stored, err := service.SetGenerationSettings(context.Background(), pagewiki.GenerationDirectives{
		Language:           "  English  ",
		CustomInstructions: " prefer tables ",
	})

	require.NoError(t, err)
	require.Equal(t, pagewiki.GenerationDirectives{
		Language:           "English",
		CustomInstructions: "prefer tables",
	}, stored)

	got, err := service.GenerationSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, stored, got)
}

func TestGivenOverLimitDirectivesWhenSetThenServiceRejectsAndPersistsNothing(t *testing.T) {
	t.Parallel()
	repository := memory.NewRepository()
	service := pagewiki.NewService(repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{})

	_, err := service.SetGenerationSettings(context.Background(), pagewiki.GenerationDirectives{
		Language: strings.Repeat("a", 65),
	})

	require.ErrorIs(t, err, pagewiki.ErrInvalidGenerationSettings)

	got, err := service.GenerationSettings(context.Background())
	require.NoError(t, err)
	require.Zero(t, got)
}

func TestGivenStoredDirectivesWhenInjectSessionRunsThenPlannerEditorAndIndexerReceiveThem(t *testing.T) {
	t.Parallel()
	repository := memory.NewRepository()

	directives, err := pagewiki.ValidateGenerationDirectives(pagewiki.GenerationDirectives{
		Language:           "简体中文",
		CustomInstructions: "prefer tables",
	})
	require.NoError(t, err)

	seedService := pagewiki.NewService(repository, pagewiki.ScriptedPlanner{}, pagewiki.ScriptedEditor{})
	_, err = seedService.SetGenerationSettings(context.Background(), directives)
	require.NoError(t, err)

	raw := []byte("event-1: The team selected SQLite for the local store.")
	eventText := "The team selected SQLite for the local store."
	eventStart := len("event-1: ")

	var capturedPlan pagewiki.PlanInput
	var capturedEdit pagewiki.EditInput
	planner := pagewiki.ScriptedPlanner{
		Captured: &capturedPlan,
		Briefs: []pagewiki.PageBrief{
			{
				Key:              "sqlite",
				Action:           pagewiki.PageActionCreate,
				ProposedSlug:     "sqlite",
				ProposedTitle:    "SQLite",
				ReaderGoal:       "Explain why SQLite is the local store.",
				EvidenceEventIDs: []string{"event-1"},
			},
		},
	}
	editor := pagewiki.ScriptedEditor{
		Captured: &capturedEdit,
		Drafts: map[string]pagewiki.PageDraft{
			"sqlite": {
				Slug:    "sqlite",
				Title:   "SQLite",
				Summary: "SQLite is the selected local persistence layer.",
				Sections: []pagewiki.SectionDraft{
					{
						Key:      "decision",
						Heading:  "Decision",
						Markdown: "The team selected SQLite for the local store.",
					},
				},
				Citations: []pagewiki.CitationDraft{
					{
						SectionKey: "decision",
						ExactText:  "selected SQLite",
						Evidence: []pagewiki.EvidenceQuoteDraft{
							{EventID: "event-1", ExactText: eventText},
						},
					},
				},
			},
		},
	}
	navigator := &fakeTreeNavigator{}
	service := pagewiki.NewService(
		repository, planner, editor,
		pagewiki.WithTreeNavigator(pagewiki.TreeMaintenanceConfig{Navigator: navigator}),
	)

	result, err := service.InjectSession(context.Background(), pagewiki.InjectSessionRequest{
		SourceID:       "session-1",
		IdempotencyKey: "session-1-injection",
		Raw:            raw,
		Events: []pagewiki.SourceEventInput{
			{ID: "event-1", StartByte: eventStart, EndByte: eventStart + len(eventText)},
		},
	})

	require.NoError(t, err)
	require.Equal(t, pagewiki.RunStatusSucceeded, result.Run.Status)
	require.Equal(t, directives, capturedPlan.Directives)
	require.Equal(t, directives, capturedEdit.Directives)
	// Placement runs off the ingest path; flush runs it now. The navigator
	// reads the stored settings at placement time.
	service.FlushTreeReindex(context.Background())
	calls := navigator.placementCalls()
	require.Len(t, calls, 1)
	require.Equal(t, directives, calls[0].Directives)
}

type failingSettingsRepository struct {
	pagewiki.Repository
}

func (failingSettingsRepository) GenerationSettings(context.Context) (pagewiki.GenerationDirectives, error) {
	return pagewiki.GenerationDirectives{}, errors.New("settings unavailable")
}

func TestGivenGenerationSettingsErrorWhenInjectSessionRunsThenItFailsAndPlannerIsNeverCalled(t *testing.T) {
	t.Parallel()
	repository := failingSettingsRepository{Repository: memory.NewRepository()}

	calls := 0
	planner := pagewiki.ScriptedPlanner{
		Calls: &calls,
		Briefs: []pagewiki.PageBrief{
			{
				Key:              "sqlite",
				Action:           pagewiki.PageActionCreate,
				ProposedSlug:     "sqlite",
				ProposedTitle:    "SQLite",
				EvidenceEventIDs: []string{"event-1"},
			},
		},
	}
	editor := pagewiki.ScriptedEditor{}
	service := pagewiki.NewService(repository, planner, editor)

	raw := []byte("event-1: The team selected SQLite.")
	_, err := service.InjectSession(context.Background(), pagewiki.InjectSessionRequest{
		SourceID:       "session-1",
		IdempotencyKey: "session-1-injection",
		Raw:            raw,
		Events: []pagewiki.SourceEventInput{
			{ID: "event-1", StartByte: 0, EndByte: len(raw)},
		},
	})

	require.Error(t, err)
	require.Equal(t, "settings unavailable", errors.Unwrap(err).Error())
	require.Equal(t, 0, calls)
}
