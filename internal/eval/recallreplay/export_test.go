package recallreplay

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/teamnote"
	"github.com/stretchr/testify/suite"
)

type exportSuite struct {
	suite.Suite
}

func TestExportSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(exportSuite))
}

func (s *exportSuite) TestPinnedHistoricalCandidateRetainsCanonicalIdentity() {
	candidates := []teamnote.RecallCandidate{{
		Note: teamnote.Note{
			ID: "note-1:1", Key: "status:release", Body: "Original state", Revision: 1,
			EvidenceEventIDs: []string{"event-1", "event-2"},
		},
		CanonicalNoteID: "note-1",
	}}

	pinned := pinCandidates(candidates)
	s.Require().Len(pinned, 1)
	s.Equal(candidates[0].CanonicalNoteID, pinned[0].CanonicalNoteID)
	s.Equal(candidates[0].Key, pinned[0].Key)

	replayed := (Case{Candidates: pinned}).recallCandidates()
	s.Require().Len(replayed, 1)
	s.Equal(candidates[0].CanonicalNoteID, replayed[0].CanonicalNoteID)
	s.Equal(candidates[0].Key, replayed[0].Key)
}
