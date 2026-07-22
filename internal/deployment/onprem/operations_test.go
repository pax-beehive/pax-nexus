package onprem_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/stretchr/testify/suite"
)

type operationsServiceSuite struct {
	suite.Suite
	repository *operationsRepository
	service    *onprem.OperationsService
	now        time.Time
}

func TestOperationsServiceSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(operationsServiceSuite))
}

func (s *operationsServiceSuite) SetupTest() {
	s.now = time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	s.repository = &operationsRepository{}
	service, err := onprem.NewOperationsService(s.repository, onprem.OperationsConfig{
		EventRetention: 7 * 24 * time.Hour, StorageRetention: 90 * 24 * time.Hour,
	}, onprem.WithOperationsClock(func() time.Time { return s.now }))
	s.Require().NoError(err)
	s.service = service
}

func (s *operationsServiceSuite) TestOwnerAndAdminCanReadNormalizedOperations() {
	for _, role := range []onprem.Role{onprem.RoleOwner, onprem.RoleAdmin} {
		s.Run(string(role), func() {
			principal := operationsPrincipal(role, onprem.MembershipStatusActive)
			_, err := s.service.Summary(context.Background(), principal, operations.TimeFilter{})
			s.Require().NoError(err)
			s.Equal(s.now.Add(-24*time.Hour), s.repository.summaryFilter.From)
			s.Equal(s.now, s.repository.summaryFilter.To)

			_, err = s.service.ListEvents(context.Background(), principal, operations.EventFilter{})
			s.Require().NoError(err)
			s.Equal(50, s.repository.eventFilter.Limit)
			s.Equal(s.now.Add(-7*24*time.Hour), s.repository.eventFilter.From)

			_, err = s.service.LatestStorage(context.Background(), principal)
			s.Require().NoError(err)

			_, err = s.service.ListStorage(context.Background(), principal, operations.StorageFilter{})
			s.Require().NoError(err)
			s.Equal(50, s.repository.storageFilter.Limit)
			s.Equal(s.now.Add(-90*24*time.Hour), s.repository.storageFilter.From)

			_, err = s.service.GetRecallDiagnostic(context.Background(), principal, 1)
			s.Require().NoError(err)
			s.Equal([]onprem.HumanCapability{onprem.CapabilityViewOperations}, principal.Capabilities())
		})
	}
}

func (s *operationsServiceSuite) TestMemberAndInactiveMembershipAreForbidden() {
	tests := []struct {
		name      string
		principal onprem.HumanPrincipal
	}{
		{name: "member", principal: operationsPrincipal(onprem.RoleMember, onprem.MembershipStatusActive)},
		{name: "inactive admin", principal: operationsPrincipal(onprem.RoleAdmin, onprem.MembershipStatusSuspended)},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := s.service.Summary(context.Background(), test.principal, operations.TimeFilter{})
			s.Require().ErrorIs(err, onprem.ErrForbidden)
			_, err = s.service.ListStorage(context.Background(), test.principal, operations.StorageFilter{})
			s.Require().ErrorIs(err, onprem.ErrForbidden)
			s.Empty(test.principal.Capabilities())
		})
	}
}

func (s *operationsServiceSuite) TestRepositoryErrorsKeepCauseAndOperationContext() {
	repositoryErr := errors.New("postgres unavailable")
	s.repository.err = repositoryErr
	principal := operationsPrincipal(onprem.RoleOwner, onprem.MembershipStatusActive)
	tests := []struct {
		name    string
		context string
		call    func() error
	}{
		{name: "summary", context: "get operations summary", call: func() error {
			_, err := s.service.Summary(context.Background(), principal, operations.TimeFilter{})
			return err
		}},
		{name: "events", context: "list operation events", call: func() error {
			_, err := s.service.ListEvents(context.Background(), principal, operations.EventFilter{})
			return err
		}},
		{name: "diagnostic", context: "get recall diagnostic", call: func() error {
			_, err := s.service.GetRecallDiagnostic(context.Background(), principal, 1)
			return err
		}},
		{name: "latest storage", context: "get latest operations storage", call: func() error {
			_, err := s.service.LatestStorage(context.Background(), principal)
			return err
		}},
		{name: "storage history", context: "list operations storage", call: func() error {
			_, err := s.service.ListStorage(context.Background(), principal, operations.StorageFilter{})
			return err
		}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			err := test.call()
			s.Require().ErrorIs(err, repositoryErr)
			s.Require().ErrorContains(err, test.context)
		})
	}
}

func (s *operationsServiceSuite) TestRejectsInvalidRangesLimitsAndDiagnosticIDs() {
	principal := operationsPrincipal(onprem.RoleOwner, onprem.MembershipStatusActive)
	tests := []struct {
		name   string
		filter operations.TimeFilter
	}{
		{name: "reversed", filter: operations.TimeFilter{From: s.now, To: s.now.Add(-time.Hour)}},
		{name: "beyond retention", filter: operations.TimeFilter{From: s.now.Add(-8 * 24 * time.Hour), To: s.now}},
		{name: "future", filter: operations.TimeFilter{From: s.now, To: s.now.Add(2 * time.Minute)}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, err := s.service.Summary(context.Background(), principal, test.filter)
			s.Require().ErrorIs(err, operations.ErrInvalidInput)
		})
	}
	validationTests := []struct {
		name string
		call func() error
	}{
		{name: "event limit", call: func() error {
			_, err := s.service.ListEvents(context.Background(), principal, operations.EventFilter{Limit: 101})
			return err
		}},
		{name: "event kind", call: func() error {
			_, err := s.service.ListEvents(context.Background(), principal, operations.EventFilter{Kind: "memory.erase"})
			return err
		}},
		{name: "event outcome", call: func() error {
			_, err := s.service.ListEvents(context.Background(), principal, operations.EventFilter{Outcome: "partial"})
			return err
		}},
		{name: "diagnostic id", call: func() error {
			_, err := s.service.GetRecallDiagnostic(context.Background(), principal, 0)
			return err
		}},
	}
	for _, test := range validationTests {
		s.Run(test.name, func() {
			s.Require().ErrorIs(test.call(), operations.ErrInvalidInput)
		})
	}
}

func operationsPrincipal(role onprem.Role, status onprem.MembershipStatus) onprem.HumanPrincipal {
	return onprem.HumanPrincipal{
		UserID: "user", MembershipID: "membership", Role: role, MembershipStatus: status,
	}
}

type operationsRepository struct {
	summaryFilter operations.TimeFilter
	eventFilter   operations.EventFilter
	storageFilter operations.StorageFilter
	err           error
}

func (r *operationsRepository) Record(_ context.Context, event operations.Event) (operations.Event, error) {
	return event, nil
}

func (r *operationsRepository) Summary(
	_ context.Context,
	filter operations.TimeFilter,
	generatedAt time.Time,
) (operations.Summary, error) {
	r.summaryFilter = filter
	if r.err != nil {
		return operations.Summary{}, r.err
	}
	return operations.Summary{From: filter.From, To: filter.To, GeneratedAt: generatedAt}, nil
}

func (r *operationsRepository) ListEvents(
	_ context.Context,
	filter operations.EventFilter,
) ([]operations.Event, error) {
	r.eventFilter = filter
	if r.err != nil {
		return nil, r.err
	}
	return nil, nil
}

func (r *operationsRepository) GetRecallDiagnostic(
	context.Context,
	int64,
) (operations.RecallDiagnostic, error) {
	if r.err != nil {
		return operations.RecallDiagnostic{}, r.err
	}
	return operations.RecallDiagnostic{}, nil
}

func (r *operationsRepository) CaptureStorage(
	context.Context,
	time.Time,
) (operations.StorageSnapshot, error) {
	return operations.StorageSnapshot{}, nil
}

func (r *operationsRepository) LatestStorage(context.Context) (operations.StorageSnapshot, error) {
	if r.err != nil {
		return operations.StorageSnapshot{}, r.err
	}
	return operations.StorageSnapshot{}, nil
}

func (r *operationsRepository) ListStorage(
	_ context.Context,
	filter operations.StorageFilter,
) ([]operations.StorageSnapshot, error) {
	r.storageFilter = filter
	if r.err != nil {
		return nil, r.err
	}
	return nil, nil
}

func (r *operationsRepository) DeleteBefore(
	context.Context,
	time.Time,
	time.Time,
) (int64, int64, error) {
	return 0, 0, nil
}
