package pagewiki

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateGenerationDirectivesTrimsAndBoundsFields(t *testing.T) {
	valid, err := ValidateGenerationDirectives(GenerationDirectives{
		Language: "  简体中文  ", CustomInstructions: " prefer tables ",
	})
	require.NoError(t, err)
	require.Equal(t, "简体中文", valid.Language)
	require.Equal(t, "prefer tables", valid.CustomInstructions)

	_, err = ValidateGenerationDirectives(GenerationDirectives{
		Language: strings.Repeat("a", 65),
	})
	require.ErrorIs(t, err, ErrInvalidGenerationSettings)

	_, err = ValidateGenerationDirectives(GenerationDirectives{
		CustomInstructions: strings.Repeat("b", 2001),
	})
	require.ErrorIs(t, err, ErrInvalidGenerationSettings)
}

func TestGenerationDirectivesPromptIsEmptyForZeroValue(t *testing.T) {
	require.Empty(t, generationDirectivesPrompt(GenerationDirectives{}))
	require.True(t, GenerationDirectives{}.IsZero())
}

func TestGenerationDirectivesPromptCarriesLanguageAndInstructions(t *testing.T) {
	prompt := generationDirectivesPrompt(GenerationDirectives{
		Language: "简体中文", CustomInstructions: "prefer tables",
	})
	require.Contains(t, prompt, "Write all generated prose, page titles, and topic titles in 简体中文.")
	require.Contains(t, prompt, "prefer tables")
	require.Contains(t, prompt, "team style guidance")
	require.False(t, GenerationDirectives{Language: "en"}.IsZero())
}
