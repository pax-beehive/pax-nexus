package explorer_test

import (
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/explorer"
	"github.com/stretchr/testify/suite"
)

type cursorSuite struct {
	suite.Suite
}

func TestCursorSuite(t *testing.T) {
	suite.Run(t, new(cursorSuite))
}

func (s *cursorSuite) TestNextCursorRoundTripsLastVisibleNote() {
	updatedAt := time.Date(2026, time.July, 26, 12, 34, 56, 789, time.FixedZone("offset", -7*60*60))
	notes := []explorer.TeamNoteSummary{
		{NoteID: "note-2", UpdatedAt: updatedAt.Add(time.Minute)},
		{NoteID: "note-1", UpdatedAt: updatedAt},
		{NoteID: "note-0", UpdatedAt: updatedAt.Add(-time.Minute)},
	}

	cursor := explorer.NextTeamNoteCursor(notes, 2)
	decodedTime, decodedID, err := explorer.DecodeTeamNoteCursor(cursor)

	s.Require().NoError(err)
	s.Equal("note-1", decodedID)
	s.True(updatedAt.UTC().Equal(decodedTime))
}

func (s *cursorSuite) TestNextCursorStopsAtTerminalPage() {
	tests := []struct {
		name  string
		notes []explorer.TeamNoteSummary
		limit int
	}{
		{name: "empty", limit: 50},
		{name: "exact page", notes: []explorer.TeamNoteSummary{{NoteID: "note-1"}}, limit: 1},
		{name: "invalid limit", notes: []explorer.TeamNoteSummary{{NoteID: "note-1"}}, limit: 0},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.Empty(explorer.NextTeamNoteCursor(test.notes, test.limit))
		})
	}
}

func (s *cursorSuite) TestDecodeCursorValidatesOpaqueInput() {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "empty", value: "", valid: true},
		{name: "whitespace", value: "  ", valid: true},
		{name: "not base64", value: "%%%", valid: false},
		{name: "not json", value: "bm90LWpzb24", valid: false},
		{name: "missing note", value: "eyJ1cGRhdGVkX2F0IjoiMjAyNi0wNy0yNlQxMjowMDowMFoifQ", valid: false},
		{name: "invalid time", value: "eyJ1cGRhdGVkX2F0IjoidG9tb3Jyb3ciLCJub3RlX2lkIjoibm90ZS0xIn0", valid: false},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, _, err := explorer.DecodeTeamNoteCursor(test.value)
			if test.valid {
				s.NoError(err)
				return
			}
			s.ErrorIs(err, explorer.ErrInvalidInput)
		})
	}
}
