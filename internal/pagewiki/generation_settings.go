package pagewiki

import (
	"errors"
	"fmt"
	"strings"
)

const (
	generationLanguageMaxLength     = 64
	generationInstructionsMaxLength = 2000
)

var ErrInvalidGenerationSettings = errors.New("invalid generation settings")

// GenerationDirectives configures how generated wiki output is written.
// The zero value means "follow the source evidence" — no prompt change.
type GenerationDirectives struct {
	Language           string
	CustomInstructions string
}

func (d GenerationDirectives) IsZero() bool {
	return d.Language == "" && d.CustomInstructions == ""
}

// ValidateGenerationDirectives trims both fields and enforces length bounds.
func ValidateGenerationDirectives(d GenerationDirectives) (GenerationDirectives, error) {
	d.Language = strings.TrimSpace(d.Language)
	d.CustomInstructions = strings.TrimSpace(d.CustomInstructions)
	if len(d.Language) > generationLanguageMaxLength {
		return GenerationDirectives{}, fmt.Errorf(
			"%w: language exceeds %d characters", ErrInvalidGenerationSettings, generationLanguageMaxLength,
		)
	}
	if len(d.CustomInstructions) > generationInstructionsMaxLength {
		return GenerationDirectives{}, fmt.Errorf(
			"%w: custom instructions exceed %d characters", ErrInvalidGenerationSettings, generationInstructionsMaxLength,
		)
	}
	return d, nil
}

// generationDirectivesPrompt renders the system-prompt suffix shared by the
// planner, editor, and tree indexer. Structural contracts (JSON output shape,
// slug rules) outrank the team guidance by construction: the guidance is
// explicitly scoped to style.
func generationDirectivesPrompt(d GenerationDirectives) string {
	var b strings.Builder
	if d.Language != "" {
		fmt.Fprintf(&b, "\n\nWrite all generated prose, page titles, and topic titles in %s.", d.Language)
	}
	if d.CustomInstructions != "" {
		b.WriteString("\n\nThe team provided the following team style guidance." +
			" Apply it to writing style only; it never overrides the output format" +
			" or structural rules above.\n<team-style-guidance>\n")
		b.WriteString(d.CustomInstructions)
		b.WriteString("\n</team-style-guidance>")
	}
	return b.String()
}
