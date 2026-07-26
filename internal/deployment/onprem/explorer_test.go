package onprem_test

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/explorer"
	"github.com/stretchr/testify/suite"
)

type explorerServiceSuite struct {
	suite.Suite
	repository *explorerRepository
	service    *onprem.ExplorerService
}

func TestExplorerServiceSuite(t *testing.T) {
	suite.Run(t, new(explorerServiceSuite))
}

func (s *explorerServiceSuite) SetupTest() {
	s.repository = &explorerRepository{notes: []explorer.TeamNoteSummary{{
		NoteID: "note-1", Kind: "blocker", Subject: "Release is blocked", State: "active",
		OriginAgentID: "codex", Revision: 2, UpdatedAt: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
	}}}
	service, err := onprem.NewExplorerService(s.repository)
	s.Require().NoError(err)
	s.service = service
}

func (s *explorerServiceSuite) TestOwnerListsNormalizedTeamNotes() {
	notes, err := s.service.ListTeamNotes(context.Background(), activeOwner(), explorer.TeamNoteFilter{
		Query: "  release  ", Limit: 0,
	})

	s.Require().NoError(err)
	s.Require().Len(notes, 1)
	s.Equal("release", s.repository.filter.Query)
	s.Equal(50, s.repository.filter.Limit)
	s.Equal("note-1", notes[0].NoteID)
}

func activeOwner() onprem.HumanPrincipal {
	return onprem.HumanPrincipal{
		UserID: "owner", MembershipID: "membership", Role: onprem.RoleOwner,
		MembershipStatus: onprem.MembershipStatusActive,
	}
}

type explorerRepository struct {
	notes  []explorer.TeamNoteSummary
	filter explorer.TeamNoteFilter
}

func (r *explorerRepository) ListTeamNotes(
	_ context.Context,
	filter explorer.TeamNoteFilter,
) ([]explorer.TeamNoteSummary, error) {
	r.filter = filter
	return r.notes, nil
}

func (r *explorerRepository) GetTeamNote(context.Context, string) (explorer.TeamNoteDetail, error) {
	return explorer.TeamNoteDetail{}, nil
}

func (r *explorerRepository) GetExtractionDiagnostic(
	context.Context,
	string,
) (explorer.ExtractionDiagnostic, error) {
	return explorer.ExtractionDiagnostic{}, nil
}

func (r *explorerRepository) GetChannelDiagnostic(
	context.Context,
	string,
) (explorer.ChannelDiagnostic, error) {
	return explorer.ChannelDiagnostic{}, nil
}
