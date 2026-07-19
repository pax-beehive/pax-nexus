package recallreplay

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
)

// ErrInvalidUnauthorizedInfluencePair identifies an unusable contract pair.
var ErrInvalidUnauthorizedInfluencePair = errors.New("invalid unauthorized influence pair")

// RecallNotesContract is the production seam exercised by identity contracts.
// Both the PostgreSQL adapter and deterministic stores implement it.
type RecallNotesContract interface {
	RecallNotes(context.Context, string, teamnote.RecallRequest) (teamnote.NoteEnvelope, error)
}

// UnauthorizedInfluencePair compares isolated scopes containing the same
// eligible knowledge, where only the challenge scope contains the named
// unauthorized Note.
type UnauthorizedInfluencePair struct {
	BaselineScopeID    string
	ChallengeScopeID   string
	UnauthorizedNoteID string
	Request            teamnote.RecallRequest
}

// UnauthorizedInfluenceResult records both adapter exclusion and observable
// output equality through RecallNotes.
type UnauthorizedInfluenceResult struct {
	UnauthorizedExcluded bool `json:"unauthorized_excluded"`
	ZeroInfluence        bool `json:"zero_influence"`
	OutputDifferences    int  `json:"output_differences"`
}

// EvaluateUnauthorizedInfluencePair exercises the real RecallNotes path. Note
// identifiers are scope-local, so equality compares consumer-visible text,
// scores, certainty, revision, and Knowledge Origin rather than database IDs.
func EvaluateUnauthorizedInfluencePair(
	ctx context.Context,
	store RecallNotesContract,
	pair UnauthorizedInfluencePair,
) (UnauthorizedInfluenceResult, error) {
	if store == nil {
		return UnauthorizedInfluenceResult{}, fmt.Errorf("%w: recall store is required", ErrInvalidUnauthorizedInfluencePair)
	}
	if strings.TrimSpace(pair.BaselineScopeID) == "" || strings.TrimSpace(pair.ChallengeScopeID) == "" ||
		strings.TrimSpace(pair.UnauthorizedNoteID) == "" {
		return UnauthorizedInfluenceResult{}, fmt.Errorf("%w: scopes and unauthorized note id are required", ErrInvalidUnauthorizedInfluencePair)
	}
	baseline, err := store.RecallNotes(ctx, pair.BaselineScopeID, pair.Request)
	if err != nil {
		return UnauthorizedInfluenceResult{}, fmt.Errorf("recall baseline scope: %w", err)
	}
	challenge, err := store.RecallNotes(ctx, pair.ChallengeScopeID, pair.Request)
	if err != nil {
		return UnauthorizedInfluenceResult{}, fmt.Errorf("recall challenge scope: %w", err)
	}
	excluded := !envelopeContains(baseline, pair.UnauthorizedNoteID) && !envelopeContains(challenge, pair.UnauthorizedNoteID)
	differences := envelopeDifferences(baseline, challenge)
	return UnauthorizedInfluenceResult{
		UnauthorizedExcluded: excluded,
		ZeroInfluence:        excluded && differences == 0,
		OutputDifferences:    differences,
	}, nil
}

type recalledNoteSignature struct {
	Text      string
	Origin    teamnote.Actor
	Relevance float64
	Certainty teamnote.NoteCertainty
	Revision  int
}

func envelopeDifferences(left, right teamnote.NoteEnvelope) int {
	differences := 0
	if !reflect.DeepEqual(left.Items, right.Items) {
		differences++
	}
	if left.Tokens != right.Tokens {
		differences++
	}
	if !reflect.DeepEqual(envelopeSignatures(left), envelopeSignatures(right)) {
		differences++
	}
	return differences
}

func envelopeSignatures(envelope teamnote.NoteEnvelope) []recalledNoteSignature {
	result := make([]recalledNoteSignature, 0, len(envelope.Details))
	for _, detail := range envelope.Details {
		result = append(result, recalledNoteSignature{
			Text: detail.Text, Origin: detail.Origin, Relevance: detail.Relevance,
			Certainty: detail.Certainty, Revision: detail.Revision,
		})
	}
	return result
}

func envelopeContains(envelope teamnote.NoteEnvelope, noteID string) bool {
	for _, detail := range envelope.Details {
		if detail.NoteID == noteID || intersects(detail.SourceNoteIDs, map[string]struct{}{noteID: {}}) {
			return true
		}
	}
	return false
}
