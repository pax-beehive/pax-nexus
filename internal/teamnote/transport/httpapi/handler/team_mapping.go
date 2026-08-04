package handler

// DTO mapping between the SaaS team control plane types and the generated
// teammemory API models, matching the identity_mapping.go convention.

import (
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/saas"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
)

func teamToAPI(team saas.Team) *api.Team {
	return &api.Team{
		TeamID: team.TeamID, Name: team.Name, Slug: team.Slug,
		CreatedAt:       team.CreatedAt.Format(time.RFC3339Nano),
		ResourceVersion: team.ResourceVersion,
	}
}

func teamSummariesToAPI(summaries []saas.TeamSummary) []*api.TeamSummary {
	result := make([]*api.TeamSummary, len(summaries))
	for index, summary := range summaries {
		result[index] = &api.TeamSummary{
			TeamID: summary.Team.TeamID, Name: summary.Team.Name, Slug: summary.Team.Slug,
			Role: string(summary.Role), MembershipID: summary.MembershipID,
		}
	}
	return result
}
