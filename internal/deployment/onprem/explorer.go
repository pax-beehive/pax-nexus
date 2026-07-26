package onprem

import (
	"context"
	"fmt"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/explorer"
)

const (
	defaultExplorerLimit = 50
	maximumExplorerLimit = 100
)

type ExplorerService struct {
	repository explorer.Repository
}

func NewExplorerService(repository explorer.Repository) (*ExplorerService, error) {
	if repository == nil {
		return nil, fmt.Errorf("create on-prem explorer service: repository is required")
	}
	return &ExplorerService{repository: repository}, nil
}

func (s *ExplorerService) ListTeamNotes(
	ctx context.Context,
	principal HumanPrincipal,
	filter explorer.TeamNoteFilter,
) ([]explorer.TeamNoteSummary, error) {
	if err := authorizeHumanCapability(principal, CapabilityViewTeamMemory); err != nil {
		return nil, err
	}
	filter.Query = strings.TrimSpace(filter.Query)
	filter.Kind = strings.TrimSpace(filter.Kind)
	filter.State = strings.TrimSpace(filter.State)
	filter.AgentID = strings.TrimSpace(filter.AgentID)
	filter.TaskRef = strings.TrimSpace(filter.TaskRef)
	filter.ThreadRef = strings.TrimSpace(filter.ThreadRef)
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	switch {
	case filter.Limit == 0:
		filter.Limit = defaultExplorerLimit
	case filter.Limit < 1 || filter.Limit > maximumExplorerLimit:
		return nil, explorer.ErrInvalidInput
	}
	notes, err := s.repository.ListTeamNotes(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list explored team notes: %w", err)
	}
	return notes, nil
}

func (s *ExplorerService) GetTeamNote(
	ctx context.Context,
	principal HumanPrincipal,
	noteID string,
) (explorer.TeamNoteDetail, error) {
	if err := authorizeHumanCapability(principal, CapabilityViewTeamMemory); err != nil {
		return explorer.TeamNoteDetail{}, err
	}
	noteID = strings.TrimSpace(noteID)
	if noteID == "" {
		return explorer.TeamNoteDetail{}, explorer.ErrInvalidInput
	}
	result, err := s.repository.GetTeamNote(ctx, noteID)
	if err != nil {
		return explorer.TeamNoteDetail{}, fmt.Errorf("get explored team note: %w", err)
	}
	return result, nil
}

func (s *ExplorerService) GetExtractionDiagnostic(
	ctx context.Context,
	principal HumanPrincipal,
	runID string,
) (explorer.ExtractionDiagnostic, error) {
	if err := authorizeHumanCapability(principal, CapabilityViewTeamMemory); err != nil {
		return explorer.ExtractionDiagnostic{}, err
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return explorer.ExtractionDiagnostic{}, explorer.ErrInvalidInput
	}
	result, err := s.repository.GetExtractionDiagnostic(ctx, runID)
	if err != nil {
		return explorer.ExtractionDiagnostic{}, fmt.Errorf("get extraction diagnostic: %w", err)
	}
	return result, nil
}

func (s *ExplorerService) GetChannelDiagnostic(
	ctx context.Context,
	principal HumanPrincipal,
	envelopeID string,
) (explorer.ChannelDiagnostic, error) {
	if err := authorizeHumanCapability(principal, CapabilityViewTeamMemory); err != nil {
		return explorer.ChannelDiagnostic{}, err
	}
	envelopeID = strings.TrimSpace(envelopeID)
	if envelopeID == "" {
		return explorer.ChannelDiagnostic{}, explorer.ErrInvalidInput
	}
	result, err := s.repository.GetChannelDiagnostic(ctx, envelopeID)
	if err != nil {
		return explorer.ChannelDiagnostic{}, fmt.Errorf("get channel diagnostic: %w", err)
	}
	return result, nil
}
