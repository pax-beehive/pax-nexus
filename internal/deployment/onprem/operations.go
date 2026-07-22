package onprem

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/operations"
)

const (
	defaultOperationsWindow = 24 * time.Hour
	defaultOperationsLimit  = 50
	maximumOperationsLimit  = 100
)

type OperationsConfig struct {
	EventRetention   time.Duration
	StorageRetention time.Duration
}

type OperationsService struct {
	repository operations.Repository
	config     OperationsConfig
	clock      func() time.Time
}

type OperationsOption func(*OperationsService)

func WithOperationsClock(clock func() time.Time) OperationsOption {
	return func(service *OperationsService) { service.clock = clock }
}

func NewOperationsService(
	repository operations.Repository,
	config OperationsConfig,
	options ...OperationsOption,
) (*OperationsService, error) {
	if repository == nil || config.EventRetention <= 0 || config.StorageRetention <= 0 {
		return nil, fmt.Errorf("create on-prem operations service: repository and positive retention are required")
	}
	service := &OperationsService{repository: repository, config: config, clock: time.Now}
	for _, option := range options {
		option(service)
	}
	if service.clock == nil {
		return nil, fmt.Errorf("create on-prem operations service: clock is required")
	}
	return service, nil
}

func (s *OperationsService) Summary(
	ctx context.Context,
	principal HumanPrincipal,
	filter operations.TimeFilter,
) (operations.Summary, error) {
	if err := authorizeHumanCapability(principal, CapabilityViewOperations); err != nil {
		return operations.Summary{}, err
	}
	filter, err := s.normalizeTimeFilter(filter, s.config.EventRetention, defaultOperationsWindow)
	if err != nil {
		return operations.Summary{}, err
	}
	result, err := s.repository.Summary(ctx, filter, s.clock().UTC())
	if err != nil {
		return operations.Summary{}, fmt.Errorf("get operations summary: %w", err)
	}
	return result, nil
}

func (s *OperationsService) ListEvents(
	ctx context.Context,
	principal HumanPrincipal,
	filter operations.EventFilter,
) ([]operations.Event, error) {
	if err := authorizeHumanCapability(principal, CapabilityViewOperations); err != nil {
		return nil, err
	}
	timeFilter, err := s.normalizeTimeFilter(filter.TimeFilter, s.config.EventRetention, s.config.EventRetention)
	if err != nil {
		return nil, err
	}
	filter.TimeFilter = timeFilter
	filter.Kind, err = operations.ParseKind(string(filter.Kind))
	if err != nil {
		return nil, err
	}
	filter.Outcome, err = operations.ParseOutcome(string(filter.Outcome))
	if err != nil {
		return nil, err
	}
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	filter.Limit, err = normalizeOperationsLimit(filter.Limit)
	if err != nil {
		return nil, err
	}
	result, err := s.repository.ListEvents(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list operation events: %w", err)
	}
	return result, nil
}

func (s *OperationsService) GetRecallDiagnostic(
	ctx context.Context,
	principal HumanPrincipal,
	observationID int64,
) (operations.RecallDiagnostic, error) {
	if err := authorizeHumanCapability(principal, CapabilityViewOperations); err != nil {
		return operations.RecallDiagnostic{}, err
	}
	if observationID <= 0 {
		return operations.RecallDiagnostic{}, operations.ErrInvalidInput
	}
	result, err := s.repository.GetRecallDiagnostic(ctx, observationID)
	if err != nil {
		return operations.RecallDiagnostic{}, fmt.Errorf("get recall diagnostic: %w", err)
	}
	return result, nil
}

func (s *OperationsService) LatestStorage(
	ctx context.Context,
	principal HumanPrincipal,
) (operations.StorageSnapshot, error) {
	if err := authorizeHumanCapability(principal, CapabilityViewOperations); err != nil {
		return operations.StorageSnapshot{}, err
	}
	result, err := s.repository.LatestStorage(ctx)
	if err != nil {
		return operations.StorageSnapshot{}, fmt.Errorf("get latest operations storage: %w", err)
	}
	return result, nil
}

func (s *OperationsService) ListStorage(
	ctx context.Context,
	principal HumanPrincipal,
	filter operations.StorageFilter,
) ([]operations.StorageSnapshot, error) {
	if err := authorizeHumanCapability(principal, CapabilityViewOperations); err != nil {
		return nil, err
	}
	timeFilter, err := s.normalizeTimeFilter(
		operations.TimeFilter{From: filter.From, To: filter.To},
		s.config.StorageRetention,
		s.config.StorageRetention,
	)
	if err != nil {
		return nil, err
	}
	filter.From, filter.To = timeFilter.From, timeFilter.To
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	filter.Limit, err = normalizeOperationsLimit(filter.Limit)
	if err != nil {
		return nil, err
	}
	result, err := s.repository.ListStorage(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list operations storage: %w", err)
	}
	return result, nil
}

func (s *OperationsService) normalizeTimeFilter(
	filter operations.TimeFilter,
	maximumWindow time.Duration,
	defaultWindow time.Duration,
) (operations.TimeFilter, error) {
	now := s.clock().UTC()
	if filter.To.IsZero() {
		filter.To = now
	}
	if filter.From.IsZero() {
		filter.From = filter.To.Add(-defaultWindow)
	}
	filter.From = filter.From.UTC()
	filter.To = filter.To.UTC()
	filter.AgentID = strings.TrimSpace(filter.AgentID)
	if !filter.From.Before(filter.To) || filter.To.Sub(filter.From) > maximumWindow || filter.To.After(now.Add(time.Minute)) {
		return operations.TimeFilter{}, operations.ErrInvalidInput
	}
	return filter, nil
}

func authorizeHumanCapability(principal HumanPrincipal, capability HumanCapability) error {
	if !principal.HasCapability(capability) {
		return ErrForbidden
	}
	return nil
}

func normalizeOperationsLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultOperationsLimit, nil
	}
	if limit < 1 || limit > maximumOperationsLimit {
		return 0, operations.ErrInvalidInput
	}
	return limit, nil
}
