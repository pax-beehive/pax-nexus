package teamnote

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ExtractionRunStatus is the durable admission state of one extraction run.
type ExtractionRunStatus string

const (
	ExtractionRunProcessing  ExtractionRunStatus = "processing"
	ExtractionRunCompleted   ExtractionRunStatus = "completed"
	ExtractionRunQuarantined ExtractionRunStatus = "quarantined"
)

// NormalizeExtractionRun binds one idempotency key to the complete Candidate batch.
func NormalizeExtractionRun(run ExtractionRun) (ExtractionRun, error) {
	encoded, err := json.Marshal(run.Candidates)
	if err != nil {
		return ExtractionRun{}, fmt.Errorf("encode extraction run candidates: %w", err)
	}
	sum := sha256.Sum256(encoded)
	checksum := hex.EncodeToString(sum[:])
	if run.CandidateChecksum != "" && run.CandidateChecksum != checksum {
		return ExtractionRun{}, fmt.Errorf("candidate checksum for extraction run %q: %w", run.ID, ErrExtractionRunConflict)
	}
	run.CandidateChecksum = checksum
	return run, nil
}

// PrepareExtractionRun normalizes the immutable candidate batch before a
// store evaluates replay or admission.
func PrepareExtractionRun(run ExtractionRun) (ExtractionRun, error) {
	if err := validateRunIdentity(run); err != nil {
		return ExtractionRun{}, err
	}
	return NormalizeExtractionRun(run)
}

// ValidateDurableExtractionRun validates the identity required by stores that
// persist and replay extraction runs across process lifetimes.
func ValidateDurableExtractionRun(scopeID string, run ExtractionRun) error {
	if strings.TrimSpace(scopeID) == "" || strings.TrimSpace(run.ID) == "" ||
		strings.TrimSpace(run.Actor.UserID) == "" || strings.TrimSpace(run.Actor.AgentID) == "" ||
		strings.TrimSpace(run.Actor.SessionID) == "" || strings.TrimSpace(run.InputChecksum) == "" ||
		strings.TrimSpace(run.CandidateChecksum) == "" || run.FromSequence <= 0 ||
		run.ToSequence < run.FromSequence || run.InputTokens < 0 || run.OutputTokens < 0 {
		return fmt.Errorf("validate durable extraction run: scope and run identity are required")
	}
	return validateRunIdentity(run)
}

// ValidateExtractionRunReplay decides whether an attempted run may reuse a
// durable result. Candidate batches and usage are outputs and telemetry, so
// they do not change the stable input identity.
func ValidateExtractionRunReplay(stored, attempted ExtractionRun, status ExtractionRunStatus, reason string) error {
	if !SameExtractionRunInput(stored, attempted) {
		return fmt.Errorf("replay extraction run %q: %w", attempted.ID, ErrExtractionRunConflict)
	}
	switch status {
	case ExtractionRunCompleted:
		return nil
	case ExtractionRunQuarantined:
		cause := ErrExtractionRunQuarantined
		if reason != "" {
			cause = errors.Join(cause, errors.New(reason))
		}
		return fmt.Errorf("replay extraction run %q: %w", attempted.ID, cause)
	default:
		return fmt.Errorf("replay extraction run %q with status %q: %w", attempted.ID, status, ErrExtractionRunConflict)
	}
}

// SameExtractionRunInput compares the stable inputs to extraction. A saved
// durable result wins over recomputed candidates and usage telemetry.
func SameExtractionRunInput(left, right ExtractionRun) bool {
	return left.Actor == right.Actor && left.FromSequence == right.FromSequence &&
		left.ToSequence == right.ToSequence && left.InputChecksum == right.InputChecksum &&
		left.Model == right.Model && left.PromptVersion == right.PromptVersion
}

// ShouldQuarantineExtractionRun reports whether retrying admission with the
// same run inputs can never succeed.
func ShouldQuarantineExtractionRun(err error) bool {
	return errors.Is(err, ErrInvalidCandidate) ||
		errors.Is(err, ErrMissingEvidence) ||
		errors.Is(err, ErrNoteNotFound)
}

func extractionRunInput(run ExtractionRun) ExtractionRun {
	return ExtractionRun{
		ID: run.ID, Actor: run.Actor, FromSequence: run.FromSequence, ToSequence: run.ToSequence,
		InputChecksum: run.InputChecksum, Model: run.Model, PromptVersion: run.PromptVersion,
	}
}

func validateRunIdentity(run ExtractionRun) error {
	seen := make(map[string]struct{}, len(run.Candidates))
	for _, candidate := range run.Candidates {
		if _, ok := seen[candidate.ID]; ok {
			return fmt.Errorf("duplicate candidate %q in extraction run: %w", candidate.ID, ErrInvalidCandidate)
		}
		seen[candidate.ID] = struct{}{}
		if run.Actor != (Actor{}) && candidate.Origin != run.Actor {
			return fmt.Errorf("candidate %q origin differs from extraction run: %w", candidate.ID, ErrInvalidCandidate)
		}
	}
	if run.Actor != (Actor{}) {
		for _, event := range run.Evidence {
			if event.Actor != run.Actor {
				return fmt.Errorf("evidence %q actor differs from extraction run: %w", event.ID, ErrMissingEvidence)
			}
		}
	}
	return nil
}
