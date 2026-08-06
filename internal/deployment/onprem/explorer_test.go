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

func (s *explorerServiceSuite) TestOwnerReadsNoteMix() {
	at := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	s.repository.mix = []explorer.NoteKindCount{{Kind: "decision", Count: 2}, {Kind: "blocker", Count: 1}}

	mix, err := s.service.NoteMix(context.Background(), activeOwner(), at)

	s.Require().NoError(err)
	s.Equal(s.repository.mix, mix)
	s.True(at.Equal(s.repository.mixAt))
}

// NoteMix is an aggregate count, so it follows the Overview page's own gate
// (view.operations, Owner+Admin) rather than the Owner-only note-content
// capability. Note CONTENT reads (ListTeamNotes etc.) stay Owner-only.
func (s *explorerServiceSuite) TestNoteMixAllowsAdminAndRejectsMember() {
	at := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	s.repository.mix = []explorer.NoteKindCount{{Kind: "decision", Count: 2}}

	admin := activeOwner()
	admin.Role = onprem.RoleAdmin
	mix, err := s.service.NoteMix(context.Background(), admin, at)
	s.Require().NoError(err)
	s.Equal(s.repository.mix, mix)

	member := activeOwner()
	member.Role = onprem.RoleMember
	_, err = s.service.NoteMix(context.Background(), member, at)
	s.Require().ErrorIs(err, onprem.ErrForbidden)
}

func (s *explorerServiceSuite) TestOwnerReadsCountExpiringNotes() {
	at := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	s.repository.expiringCount = 4

	count, err := s.service.CountExpiringNotes(context.Background(), activeOwner(), at, 24*time.Hour)

	s.Require().NoError(err)
	s.Equal(int64(4), count)
	s.True(at.Equal(s.repository.expiringAt))
	s.Equal(24*time.Hour, s.repository.expiringWithin)
}

// CountExpiringNotes is an aggregate count, so it follows the same gate as
// NoteMix (view.operations, Owner+Admin) rather than the Owner-only
// note-content capability.
func (s *explorerServiceSuite) TestCountExpiringNotesAllowsAdminAndRejectsMember() {
	at := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	s.repository.expiringCount = 2

	admin := activeOwner()
	admin.Role = onprem.RoleAdmin
	count, err := s.service.CountExpiringNotes(context.Background(), admin, at, 24*time.Hour)
	s.Require().NoError(err)
	s.Equal(int64(2), count)

	member := activeOwner()
	member.Role = onprem.RoleMember
	_, err = s.service.CountExpiringNotes(context.Background(), member, at, 24*time.Hour)
	s.Require().ErrorIs(err, onprem.ErrForbidden)
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
	mix    []explorer.NoteKindCount
	mixAt  time.Time

	expiringCount  int64
	expiringAt     time.Time
	expiringWithin time.Duration
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

func (r *explorerRepository) NoteMix(
	_ context.Context,
	at time.Time,
) ([]explorer.NoteKindCount, error) {
	r.mixAt = at
	return r.mix, nil
}

func (r *explorerRepository) CountExpiringNotes(
	_ context.Context,
	at time.Time,
	within time.Duration,
) (int64, error) {
	r.expiringAt = at
	r.expiringWithin = within
	return r.expiringCount, nil
}
