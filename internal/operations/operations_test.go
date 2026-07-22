package operations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/operations"
	"github.com/stretchr/testify/suite"
)

type operationsSuite struct {
	suite.Suite
}

func TestOperationsSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(operationsSuite))
}

func (s *operationsSuite) TestEventValidationMatrix() {
	now := time.Now().UTC()
	valid := operations.Event{
		AttemptID: "attempt", Kind: operations.KindMemorySearch, Outcome: operations.OutcomeSucceeded,
		Actor: operations.Actor{Kind: "agent"}, StartedAt: now, CompletedAt: now,
	}
	tests := []struct {
		name  string
		apply func(*operations.Event)
	}{
		{name: "missing attempt", apply: func(event *operations.Event) { event.AttemptID = "" }},
		{name: "unknown kind", apply: func(event *operations.Event) { event.Kind = "unknown" }},
		{name: "unknown outcome", apply: func(event *operations.Event) { event.Outcome = "unknown" }},
		{name: "unknown actor", apply: func(event *operations.Event) { event.Actor.Kind = "unknown" }},
		{name: "negative count", apply: func(event *operations.Event) { event.ResultItems = -1 }},
		{name: "reversed time", apply: func(event *operations.Event) { event.CompletedAt = now.Add(-time.Second) }},
		{name: "partial detail", apply: func(event *operations.Event) { event.DetailKind = "recall_observation" }},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			event := valid
			test.apply(&event)
			s.Require().ErrorIs(event.Validate(), operations.ErrInvalidInput)
		})
	}
	s.NoError(valid.Validate())
}

func (s *operationsSuite) TestCursorRoundTripAndInvalidInput() {
	events := []operations.Event{
		{OperationEventID: 9, StartedAt: time.Date(2026, time.July, 22, 1, 2, 3, 4, time.UTC)},
		{OperationEventID: 8, StartedAt: time.Date(2026, time.July, 22, 1, 2, 2, 4, time.UTC)},
	}
	cursor := operations.NextEventCursor(events, 1)
	timestamp, id, err := operations.DecodeCursor(cursor)
	s.Require().NoError(err)
	s.Equal(events[0].StartedAt, timestamp)
	s.Equal(int64(9), id)
	_, _, err = operations.DecodeCursor("not-a-cursor")
	s.Require().ErrorIs(err, operations.ErrInvalidInput)
	s.Empty(operations.NextEventCursor(events, 0))
	s.Empty(operations.NextEventCursor(events[:1], 1))
	storage := []operations.StorageSnapshot{
		{SnapshotID: 4, CapturedAt: events[0].StartedAt},
		{SnapshotID: 3, CapturedAt: events[1].StartedAt},
	}
	storageCursor := operations.NextStorageCursor(storage, 1)
	timestamp, id, err = operations.DecodeCursor(storageCursor)
	s.Require().NoError(err)
	s.Equal(storage[0].CapturedAt, timestamp)
	s.Equal(int64(4), id)
	s.Empty(operations.NextStorageCursor(storage, 0))
	s.Empty(operations.NextStorageCursor(storage[:1], 1))
	invalidCursors := []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "missing id", value: "aW52YWxpZA"},
		{name: "zero id", value: "MjAyNi0wNy0yMlQwMTowMjowM1p8MA"},
	}
	for _, test := range invalidCursors {
		s.Run(test.name, func() {
			_, _, decodeErr := operations.DecodeCursor(test.value)
			s.Require().ErrorIs(decodeErr, operations.ErrInvalidInput)
		})
	}
}

func (s *operationsSuite) TestAttemptIDsAreOpaqueAndUnique() {
	first, err := operations.NewAttemptID()
	s.Require().NoError(err)
	second, err := operations.NewAttemptID()
	s.Require().NoError(err)
	s.True(strings.HasPrefix(first, "op_"))
	s.NotEqual(first, second)
}

func (s *operationsSuite) TestDropCountingRecorderTracksOnlyFailedWrites() {
	recordErr := errors.New("storage unavailable")
	next := &recordingRecorder{err: recordErr}
	recorder, err := operations.NewDropCountingRecorder(next)
	s.Require().NoError(err)

	_, err = recorder.Record(context.Background(), operations.Event{})
	s.Require().ErrorIs(err, recordErr)
	s.Equal(uint64(1), recorder.Dropped())
	s.Equal(uint64(1), operations.DroppedObservations(recorder))

	next.err = nil
	_, err = recorder.Record(context.Background(), operations.Event{})
	s.Require().NoError(err)
	s.Equal(uint64(1), recorder.Dropped())
	s.Zero(operations.DroppedObservations(next))
	_, err = operations.NewDropCountingRecorder(nil)
	s.Require().Error(err)
}

func (s *operationsSuite) TestParseFilters() {
	tests := []struct {
		name    string
		parse   func(string) (string, error)
		input   string
		want    string
		wantErr error
	}{
		{name: "empty kind", parse: parseKindForTest, input: " ", want: ""},
		{name: "known kind", parse: parseKindForTest, input: " memory.search ", want: "memory.search"},
		{name: "unknown kind", parse: parseKindForTest, input: "memory.erase", wantErr: operations.ErrInvalidInput},
		{name: "empty outcome", parse: parseOutcomeForTest, input: "", want: ""},
		{name: "known outcome", parse: parseOutcomeForTest, input: " failed ", want: "failed"},
		{name: "unknown outcome", parse: parseOutcomeForTest, input: "partial", wantErr: operations.ErrInvalidInput},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			got, err := test.parse(test.input)
			s.Require().ErrorIs(err, test.wantErr)
			s.Equal(test.want, got)
		})
	}
}

func parseKindForTest(value string) (string, error) {
	parsed, err := operations.ParseKind(value)
	return string(parsed), err
}

func parseOutcomeForTest(value string) (string, error) {
	parsed, err := operations.ParseOutcome(value)
	return string(parsed), err
}

type recordingRecorder struct {
	err error
}

func (r *recordingRecorder) Record(
	_ context.Context,
	event operations.Event,
) (operations.Event, error) {
	return event, r.err
}
