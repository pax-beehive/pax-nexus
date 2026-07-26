package pagewiki_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type ServiceValidationSuite struct {
	suite.Suite
}

func TestServiceValidationSuite(t *testing.T) {
	suite.Run(t, new(ServiceValidationSuite))
}

func (s *ServiceValidationSuite) TestGivenInvalidSessionSourceWhenInjectedThenItIsRejected() {
	tests := []struct {
		name    string
		request pagewiki.InjectSessionRequest
	}{
		{
			name: "missing Source ID",
			request: pagewiki.InjectSessionRequest{
				Raw:    []byte("text"),
				Events: []pagewiki.SourceEventInput{{ID: "event-1", StartByte: 0, EndByte: 4}},
			},
		},
		{
			name: "missing raw bytes",
			request: pagewiki.InjectSessionRequest{
				SourceID: "session-1",
				Events:   []pagewiki.SourceEventInput{{ID: "event-1", StartByte: 0, EndByte: 4}},
			},
		},
		{
			name: "missing events",
			request: pagewiki.InjectSessionRequest{
				SourceID: "session-1",
				Raw:      []byte("text"),
			},
		},
		{
			name: "duplicate event ID",
			request: pagewiki.InjectSessionRequest{
				SourceID: "session-1",
				Raw:      []byte("text"),
				Events: []pagewiki.SourceEventInput{
					{ID: "event-1", StartByte: 0, EndByte: 2},
					{ID: "event-1", StartByte: 2, EndByte: 4},
				},
			},
		},
		{
			name: "event range exceeds Source",
			request: pagewiki.InjectSessionRequest{
				SourceID: "session-1",
				Raw:      []byte("text"),
				Events: []pagewiki.SourceEventInput{
					{ID: "event-1", StartByte: 0, EndByte: 5},
				},
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			service := pagewiki.NewService(memory.NewRepository(), nil, nil)

			_, err := service.InjectSession(context.Background(), tt.request)

			s.Require().ErrorIs(err, pagewiki.ErrInvalidSource)
		})
	}
}

func (s *ServiceValidationSuite) TestGivenPlannerFailureWhenInjectedThenErrorIsReturned() {
	plannerFailure := errors.New("planner unavailable")
	service := pagewiki.NewService(
		memory.NewRepository(),
		pagewiki.ScriptedPlanner{Err: plannerFailure},
		pagewiki.ScriptedEditor{},
	)

	_, err := service.InjectSession(context.Background(), validInjectRequest())

	s.Require().ErrorIs(err, plannerFailure)
}

func (s *ServiceValidationSuite) TestGivenInvalidPlannerOutputWhenInjectedThenItIsRejected() {
	service := pagewiki.NewService(
		memory.NewRepository(),
		pagewiki.ScriptedPlanner{},
		pagewiki.ScriptedEditor{},
	)

	_, err := service.InjectSession(context.Background(), validInjectRequest())

	s.Require().ErrorIs(err, pagewiki.ErrInvalidPageBrief)
}

func (s *ServiceValidationSuite) TestGivenMissingIdempotencyKeyWhenInjectedThenItIsRejected() {
	repository := memory.NewRepository()
	service := pagewiki.NewService(repository, nil, nil)
	request := validInjectRequest()
	request.IdempotencyKey = ""

	_, err := service.InjectSession(context.Background(), request)

	s.Require().ErrorIs(err, pagewiki.ErrInvalidRequest)
	s.Require().Zero(repository.SourceRevisionCount())
}

func (s *ServiceValidationSuite) TestGivenUnknownBriefEvidenceWhenInjectedThenTargetFails() {
	repository := memory.NewRepository()
	service := pagewiki.NewService(
		repository,
		pagewiki.ScriptedPlanner{
			Briefs: []pagewiki.PageBrief{
				{
					Key:              "sqlite",
					Action:           pagewiki.PageActionCreate,
					ProposedSlug:     "sqlite",
					ProposedTitle:    "SQLite",
					TopicPath:        []string{"Engineering"},
					EvidenceEventIDs: []string{"event-forged"},
				},
			},
		},
		pagewiki.ScriptedEditor{},
	)

	result, err := service.InjectSession(context.Background(), validInjectRequest())

	s.Require().NoError(err)
	s.Require().Equal(pagewiki.RunStatusFailed, result.Run.Status)
	s.Require().Equal(
		pagewiki.TargetFailureInvalidBrief,
		result.Run.Targets[0].FailureReason,
	)
	s.Require().Zero(repository.PageCount())
}

func validInjectRequest() pagewiki.InjectSessionRequest {
	return pagewiki.InjectSessionRequest{
		SourceID:       "session-1",
		IdempotencyKey: "session-1-injection",
		Raw:            []byte("text"),
		Events: []pagewiki.SourceEventInput{
			{ID: "event-1", StartByte: 0, EndByte: 4},
		},
	}
}
