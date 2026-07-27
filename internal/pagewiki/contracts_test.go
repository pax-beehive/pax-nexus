package pagewiki_test

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/stretchr/testify/suite"
)

type ContractsSuite struct {
	suite.Suite
}

func TestContractsSuite(t *testing.T) {
	suite.Run(t, new(ContractsSuite))
}

func (s *ContractsSuite) TestGivenCatalogPageWhenUpdateBriefUsesCurrentBaseThenItIsValid() {
	catalog := pagewiki.PageCatalog{
		{
			ID:                "page-adoption",
			Slug:              "adoption",
			Title:             "Adoption",
			CurrentRevisionID: "revision-1",
		},
	}
	brief := pagewiki.PageBrief{
		Key:                    "adoption-update",
		Action:                 pagewiki.PageActionUpdate,
		TargetPageID:           "page-adoption",
		ExpectedBaseRevisionID: "revision-1",
		TopicPath:              []string{"Journeys", "Adoption"},
		EvidenceEventIDs:       []string{"event-2"},
	}

	err := pagewiki.ValidatePageBrief(brief, catalog)

	s.Require().NoError(err)
}

func (s *ContractsSuite) TestGivenCatalogWhenBriefIsInvalidThenValidationFails() {
	catalog := pagewiki.PageCatalog{
		{
			ID:                "page-adoption",
			Slug:              "adoption",
			Title:             "Adoption",
			CurrentRevisionID: "revision-1",
		},
	}
	tests := []struct {
		name  string
		brief pagewiki.PageBrief
	}{
		{
			name: "update invents page identity",
			brief: pagewiki.PageBrief{
				Key:                    "missing-update",
				Action:                 pagewiki.PageActionUpdate,
				TargetPageID:           "page-missing",
				ExpectedBaseRevisionID: "revision-1",
				EvidenceEventIDs:       []string{"event-2"},
			},
		},
		{
			name: "update uses stale catalog revision",
			brief: pagewiki.PageBrief{
				Key:                    "adoption-update",
				Action:                 pagewiki.PageActionUpdate,
				TargetPageID:           "page-adoption",
				ExpectedBaseRevisionID: "revision-stale",
				EvidenceEventIDs:       []string{"event-2"},
			},
		},
		{
			name: "topic path exceeds two levels",
			brief: pagewiki.PageBrief{
				Key:              "new-page",
				Action:           pagewiki.PageActionCreate,
				ProposedSlug:     "new-page",
				ProposedTitle:    "New Page",
				TopicPath:        []string{"Engineering", "Storage", "SQLite"},
				EvidenceEventIDs: []string{"event-1"},
			},
		},
		{
			name: "create omits topic path",
			brief: pagewiki.PageBrief{
				Key:              "new-page",
				Action:           pagewiki.PageActionCreate,
				ProposedSlug:     "new-page",
				ProposedTitle:    "New Page",
				EvidenceEventIDs: []string{"event-1"},
			},
		},
		{
			name: "create attempts to choose page identity",
			brief: pagewiki.PageBrief{
				Key:              "new-page",
				Action:           pagewiki.PageActionCreate,
				TargetPageID:     "invented-page",
				ProposedSlug:     "new-page",
				ProposedTitle:    "New Page",
				TopicPath:        []string{"Engineering"},
				EvidenceEventIDs: []string{"event-1"},
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			err := pagewiki.ValidatePageBrief(tt.brief, catalog)
			s.Require().ErrorIs(err, pagewiki.ErrInvalidPageBrief)
		})
	}
}
