package sessionconsumer_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/sessionconsumer"
	"github.com/pax-beehive/pax-nexus/internal/session"
	"github.com/stretchr/testify/suite"
)

type consumerSuite struct {
	suite.Suite
	store     *consumerStore
	injector  *recordingInjector
	rebuilder *recordingRebuilder
	consumer  *sessionconsumer.Controller
}

func TestConsumerSuite(t *testing.T) {
	suite.Run(t, new(consumerSuite))
}

func (s *consumerSuite) SetupTest() {
	s.store = &consumerStore{
		enabled: map[string]bool{},
		streams: []sessionconsumer.Stream{{
			ScopeID: "local-team",
			Actor:   session.Actor{UserID: "owner", AgentID: "agent-1", SessionID: "runtime-demo"},
			Head:    2,
		}},
		events: []session.SessionEvent{
			{ID: "event-1", Sequence: 1, Type: "assistant", Content: "Runtime started."},
			{ID: "event-2", Sequence: 2, Type: "assistant", Content: "Runtime verified."},
		},
		advanced: make(chan struct{}, 1),
	}
	s.injector = &recordingInjector{}
	s.rebuilder = &recordingRebuilder{}
	var err error
	s.consumer, err = sessionconsumer.New(
		s.store, s.injector, s.rebuilder, slog.New(slog.DiscardHandler), time.Hour,
	)
	s.Require().NoError(err)
}

func (s *consumerSuite) TestManualInjectionBuildsCitedSourceAndAdvancesIndependentCursor() {
	result, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")

	s.Require().NoError(err)
	s.Equal(1, result.ProcessedStreams)
	s.Equal(1, s.store.advances)
	s.Require().Len(s.injector.requests, 1)
	request := s.injector.requests[0]
	s.Equal("page_wiki/knowledge-llm-v1/2", request.IdempotencyKey)
	s.Len(request.Events, 2)
	s.Contains(string(request.Raw), "[event:event-2 sequence:2 type:assistant] Runtime verified.")
}

func (s *consumerSuite) TestFailedInjectionDoesNotAdvanceCursor() {
	s.injector.err = errors.New("planner unavailable")

	_, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")

	s.Require().ErrorContains(err, "planner unavailable")
	s.Zero(s.store.advances)
}

func (s *consumerSuite) TestAutoSettingRoundTrips() {
	status, err := s.consumer.SetAutoInject(context.Background(), "local-team", true)
	s.Require().NoError(err)
	s.True(status.AutoInject)

	status, err = s.consumer.Status(context.Background(), "local-team")
	s.Require().NoError(err)
	s.True(status.AutoInject)
}

func (s *consumerSuite) TestRebuildResetsDerivedWikiStateAndSchedulesFreshConsumption() {
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	status, err := s.consumer.Rebuild(context.Background(), "local-team", cutoff)

	s.Require().NoError(err)
	s.True(status.AutoInject)
	s.Equal(1, s.rebuilder.calls)
	s.Equal("local-team", s.rebuilder.scopeID)
	s.Equal(sessionconsumer.ProcessorName, s.rebuilder.processorName)
	s.Equal(sessionconsumer.ProcessorVersion, s.rebuilder.processorVersion)
	s.Equal(cutoff, s.rebuilder.since)
}

func (s *consumerSuite) TestRejectsMissingSession() {
	_, err := s.consumer.InjectSession(context.Background(), "local-team", "missing")

	s.Require().ErrorIs(err, sessionconsumer.ErrSessionNotFound)
	s.Zero(s.store.advances)
}

func (s *consumerSuite) TestStartsBackgroundScanAndConsumesPendingStream() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.consumer.Start(ctx)

	select {
	case <-s.store.advanced:
	case <-time.After(time.Second):
		s.Fail("background consumer did not advance")
	}
}

func (s *consumerSuite) TestValidationRejectsIncompleteConfigurationAndInput() {
	_, err := sessionconsumer.New(nil, s.injector, s.rebuilder, slog.New(slog.DiscardHandler), time.Second)
	s.Require().Error(err)
	_, err = sessionconsumer.New(s.store, nil, s.rebuilder, slog.New(slog.DiscardHandler), time.Second)
	s.Require().Error(err)
	_, err = sessionconsumer.New(s.store, s.injector, nil, slog.New(slog.DiscardHandler), time.Second)
	s.Require().Error(err)
	_, err = sessionconsumer.New(s.store, s.injector, s.rebuilder, nil, time.Second)
	s.Require().Error(err)

	_, err = s.consumer.SetAutoInject(context.Background(), "", true)
	s.Require().Error(err)
	_, err = s.consumer.InjectSession(context.Background(), "", "runtime-demo")
	s.Require().Error(err)
	_, err = s.consumer.InjectSession(context.Background(), "local-team", "")
	s.Require().Error(err)
	_, err = s.consumer.Rebuild(context.Background(), "", time.Time{})
	s.Require().Error(err)
}

func (s *consumerSuite) TestEmptySessionDoesNotInjectOrAdvance() {
	s.store.events = nil

	result, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")

	s.Require().NoError(err)
	s.Equal(1, result.ProcessedStreams)
	s.Empty(s.injector.requests)
	s.Zero(s.store.advances)
}

func (s *consumerSuite) TestStatusIncludesProgress() {
	s.store.enabled["local-team"] = true
	processed := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	s.store.progress = sessionconsumer.Progress{PendingSessions: 3, LastProcessedAt: &processed}

	status, err := s.consumer.Status(context.Background(), "local-team")

	s.Require().NoError(err)
	s.True(status.AutoInject)
	s.Require().NotNil(status.Progress)
	s.Equal(3, status.Progress.PendingSessions)
	s.Equal(processed, *status.Progress.LastProcessedAt)
}

func (s *consumerSuite) TestStatusDegradesWhenProgressQueryFails() {
	s.store.enabled["local-team"] = true
	s.store.progressErr = errors.New("progress query failed")

	status, err := s.consumer.Status(context.Background(), "local-team")

	s.Require().NoError(err)
	s.True(status.AutoInject)
	s.Nil(status.Progress)
}

func (s *consumerSuite) TestDoesNotAdvanceWhenMaintenanceRunIsNotSuccessful() {
	s.injector.status = pagewiki.RunStatusPartialSuccess

	_, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")

	s.Require().ErrorContains(err, "partial_success")
	s.Zero(s.store.advances)
}

func (s *consumerSuite) TestStoreFailuresAreReported() {
	tests := []struct {
		name      string
		configure func()
		run       func() error
		contains  string
	}{
		{
			name: "read status",
			configure: func() {
				s.store.statusErr = errors.New("status unavailable")
			},
			run: func() error {
				_, err := s.consumer.Status(context.Background(), "local-team")
				return err
			},
			contains: "status unavailable",
		},
		{
			name: "write status",
			configure: func() {
				s.store.settingErr = errors.New("setting unavailable")
			},
			run: func() error {
				_, err := s.consumer.SetAutoInject(context.Background(), "local-team", true)
				return err
			},
			contains: "setting unavailable",
		},
		{
			name: "list session",
			configure: func() {
				s.store.streamErr = errors.New("stream unavailable")
			},
			run: func() error {
				_, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")
				return err
			},
			contains: "stream unavailable",
		},
		{
			name: "read events",
			configure: func() {
				s.store.eventsErr = errors.New("events unavailable")
			},
			run: func() error {
				_, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")
				return err
			},
			contains: "events unavailable",
		},
		{
			name: "advance cursor",
			configure: func() {
				s.store.advanceErr = errors.New("cursor unavailable")
			},
			run: func() error {
				_, err := s.consumer.InjectSession(context.Background(), "local-team", "runtime-demo")
				return err
			},
			contains: "cursor unavailable",
		},
		{
			name: "rebuild",
			configure: func() {
				s.rebuilder.err = errors.New("rebuild unavailable")
			},
			run: func() error {
				_, err := s.consumer.Rebuild(context.Background(), "local-team", time.Time{})
				return err
			},
			contains: "rebuild unavailable",
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.SetupTest()
			test.configure()

			err := test.run()

			s.Require().ErrorContains(err, test.contains)
		})
	}
}

type consumerStore struct {
	enabled     map[string]bool
	streams     []sessionconsumer.Stream
	events      []session.SessionEvent
	advances    int
	statusErr   error
	settingErr  error
	streamErr   error
	pendingErr  error
	eventsErr   error
	advanceErr  error
	advanced    chan struct{}
	progress    sessionconsumer.Progress
	progressErr error
}

func (s *consumerStore) AutoInjectEnabled(_ context.Context, scopeID string) (bool, error) {
	if s.statusErr != nil {
		return false, s.statusErr
	}
	return s.enabled[scopeID], nil
}

func (s *consumerStore) SetAutoInjectEnabled(_ context.Context, scopeID string, enabled bool) error {
	if s.settingErr != nil {
		return s.settingErr
	}
	s.enabled[scopeID] = enabled
	return nil
}

func (s *consumerStore) PendingStreams(context.Context) ([]sessionconsumer.Stream, error) {
	if s.pendingErr != nil {
		return nil, s.pendingErr
	}
	return append([]sessionconsumer.Stream(nil), s.streams...), nil
}

func (s *consumerStore) StreamsBySessionID(
	_ context.Context,
	scopeID string,
	sessionID string,
) ([]sessionconsumer.Stream, error) {
	if s.streamErr != nil {
		return nil, s.streamErr
	}
	result := make([]sessionconsumer.Stream, 0)
	for _, stream := range s.streams {
		if stream.ScopeID == scopeID && stream.Actor.SessionID == sessionID {
			result = append(result, stream)
		}
	}
	return result, nil
}

func (s *consumerStore) SessionEvents(
	context.Context,
	sessionconsumer.Stream,
) ([]session.SessionEvent, error) {
	if s.eventsErr != nil {
		return nil, s.eventsErr
	}
	return append([]session.SessionEvent(nil), s.events...), nil
}

func (s *consumerStore) AdvanceCursor(context.Context, sessionconsumer.Stream) error {
	if s.advanceErr != nil {
		return s.advanceErr
	}
	s.advances++
	select {
	case s.advanced <- struct{}{}:
	default:
	}
	return nil
}

func (s *consumerStore) Progress(context.Context, string) (sessionconsumer.Progress, error) {
	if s.progressErr != nil {
		return sessionconsumer.Progress{}, s.progressErr
	}
	return s.progress, nil
}

type recordingInjector struct {
	requests []pagewiki.InjectSessionRequest
	err      error
	status   pagewiki.RunStatus
}

type recordingRebuilder struct {
	calls            int
	scopeID          string
	processorName    string
	processorVersion string
	since            time.Time
	err              error
}

func (r *recordingRebuilder) RebuildPageWiki(
	_ context.Context,
	scopeID string,
	processorName string,
	processorVersion string,
	since time.Time,
) error {
	r.calls++
	r.scopeID = scopeID
	r.processorName = processorName
	r.processorVersion = processorVersion
	r.since = since
	return r.err
}

func (i *recordingInjector) InjectSession(
	_ context.Context,
	request pagewiki.InjectSessionRequest,
) (pagewiki.InjectResult, error) {
	i.requests = append(i.requests, request)
	if i.err != nil {
		return pagewiki.InjectResult{}, i.err
	}
	status := i.status
	if status == "" {
		status = pagewiki.RunStatusSucceeded
	}
	return pagewiki.InjectResult{
		Run: pagewiki.MaintenanceRun{Status: status},
	}, nil
}
