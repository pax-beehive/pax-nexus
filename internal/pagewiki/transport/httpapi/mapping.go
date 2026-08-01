package httpapi

import (
	"fmt"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	api "github.com/pax-beehive/pax-nexus/internal/pagewiki/transport/httpapi/model/pagewiki/api"
)

func injectRequestToDomain(request *api.InjectRequest) (pagewiki.InjectSessionRequest, error) {
	if request == nil {
		return pagewiki.InjectSessionRequest{}, fmt.Errorf("map injection: request is required")
	}
	events := make([]pagewiki.SourceEventInput, 0, len(request.Events))
	for _, event := range request.Events {
		if event == nil {
			return pagewiki.InjectSessionRequest{}, fmt.Errorf(
				"map injection: event is required",
			)
		}
		events = append(events, pagewiki.SourceEventInput{
			ID:        event.ID,
			StartByte: int(event.StartByte),
			EndByte:   int(event.EndByte),
		})
	}
	return pagewiki.InjectSessionRequest{
		SourceID:       request.SourceID,
		IdempotencyKey: request.IdempotencyKey,
		Raw:            []byte(request.Raw),
		Events:         events,
	}, nil
}

func injectResultToAPI(result pagewiki.InjectResult) *api.InjectResponse {
	return &api.InjectResponse{
		SourceRevisionID: result.SourceRevisionID,
		Run:              maintenanceRunToAPI(result.Run),
	}
}

func maintenanceRunToAPI(run pagewiki.MaintenanceRun) *api.MaintenanceRun {
	targets := make([]*api.MaintenanceTarget, 0, len(run.Targets))
	for _, target := range run.Targets {
		mapped := &api.MaintenanceTarget{
			ID:       target.ID,
			BriefKey: target.BriefKey,
			Action:   string(target.Action),
			Status:   string(target.Status),
		}
		mapped.PageID = optionalString(target.PageID)
		mapped.PageRevisionID = optionalString(target.PageRevisionID)
		mapped.FailureReason = optionalString(string(target.FailureReason))
		mapped.Error = optionalString(target.Error)
		targets = append(targets, mapped)
	}
	return &api.MaintenanceRun{
		ID:               run.ID,
		SourceRevisionID: run.SourceRevisionID,
		Status:           string(run.Status),
		Targets:          targets,
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func currentPageToAPI(
	page pagewiki.Page,
	revision pagewiki.PageRevision,
	successorSlug string,
) *api.CurrentPageResponse {
	response := &api.CurrentPageResponse{
		ID:                page.ID,
		Slug:              page.Slug,
		Title:             page.Title,
		CurrentRevisionID: page.CurrentRevisionID,
		Revision:          pageRevisionToAPI(revision),
	}
	if page.Retired() {
		response.Status = optionalString(string(pagewiki.PageStatusRetired))
		response.SuccessorSlug = optionalString(successorSlug)
	}
	return response
}

func pageToAPI(page pagewiki.Page) *api.Page {
	return &api.Page{
		ID:                page.ID,
		Slug:              page.Slug,
		Title:             page.Title,
		CurrentRevisionID: page.CurrentRevisionID,
	}
}

func pageRevisionToAPI(revision pagewiki.PageRevision) *api.PageRevision {
	sections := make([]*api.PageSection, 0, len(revision.Sections))
	for _, section := range revision.Sections {
		sections = append(sections, &api.PageSection{
			Key:      section.Key,
			Heading:  section.Heading,
			Markdown: section.Markdown,
		})
	}
	citations := citationsToAPI(revision.Citations)
	links := linksToAPI(revision.Links)
	return &api.PageRevision{
		ID:             revision.ID,
		PageID:         revision.PageID,
		BaseRevisionID: optionalString(revision.BaseRevisionID),
		Title:          revision.Title,
		Summary:        revision.Summary,
		Sections:       sections,
		Markdown:       revision.Markdown,
		Citations:      citations,
		Links:          links,
	}
}

func citationsToAPI(citations []pagewiki.PageCitation) []*api.PageCitation {
	result := make([]*api.PageCitation, 0, len(citations))
	for _, citation := range citations {
		anchors := make([]*api.SourceAnchor, 0, len(citation.SourceAnchors))
		for _, anchor := range citation.SourceAnchors {
			anchors = append(anchors, &api.SourceAnchor{
				ID:               anchor.ID,
				SourceRevisionID: anchor.SourceRevisionID,
				EventID:          anchor.EventID,
				StartByte:        int32(anchor.StartByte),
				EndByte:          int32(anchor.EndByte),
				ExactQuote:       anchor.ExactQuote,
			})
		}
		result = append(result, &api.PageCitation{
			ID:             citation.ID,
			PageRevisionID: citation.PageRevisionID,
			SectionKey:     citation.SectionKey,
			StartByte:      int32(citation.StartByte),
			EndByte:        int32(citation.EndByte),
			ExactText:      citation.ExactText,
			SourceAnchors:  anchors,
		})
	}
	return result
}

func linksToAPI(links []pagewiki.PageLink) []*api.PageLink {
	result := make([]*api.PageLink, 0, len(links))
	for _, link := range links {
		result = append(result, &api.PageLink{
			ID:             link.ID,
			PageRevisionID: link.PageRevisionID,
			SectionKey:     link.SectionKey,
			StartByte:      int32(link.StartByte),
			EndByte:        int32(link.EndByte),
			ExactText:      link.ExactText,
			TargetPageID:   link.TargetPageID,
		})
	}
	return result
}

func revisionsToAPI(revisions []pagewiki.PageRevision) *api.PageRevisionList {
	result := make([]*api.PageRevision, 0, len(revisions))
	for _, revision := range revisions {
		result = append(result, pageRevisionToAPI(revision))
	}
	return &api.PageRevisionList{Revisions: result}
}

func pageLinksToAPI(links pagewiki.PageLinkSet) *api.PageLinksResponse {
	return &api.PageLinksResponse{
		Outgoing: resolvedLinksToAPI(links.Outgoing),
		Incoming: resolvedLinksToAPI(links.Incoming),
	}
}

func resolvedLinksToAPI(links []pagewiki.ResolvedPageLink) []*api.ResolvedPageLink {
	result := make([]*api.ResolvedPageLink, 0, len(links))
	for _, link := range links {
		result = append(result, &api.ResolvedPageLink{
			Link:             linksToAPI([]pagewiki.PageLink{link.Link})[0],
			SourcePage:       pageToAPI(link.SourcePage),
			SourceRevisionID: link.SourceRevisionID,
			TargetPage:       pageToAPI(link.TargetPage),
		})
	}
	return result
}

func searchResultsToAPI(results []pagewiki.SearchResult) *api.SearchResponse {
	mapped := make([]*api.SearchResult, 0, len(results))
	for _, result := range results {
		mapped = append(mapped, &api.SearchResult{
			Page:       pageToAPI(result.Page),
			RevisionID: result.RevisionID,
			SectionKey: result.SectionKey,
			Passage:    result.Passage,
			Score:      result.Score,
			Citations:  citationsToAPI(result.Citations),
			Links:      linksToAPI(result.Links),
		})
	}
	return &api.SearchResponse{Results: mapped}
}

func sourceRevisionToAPI(revision pagewiki.SourceRevision) *api.SourceRevision {
	events := make([]*api.SourceEvent, 0, len(revision.Events))
	for _, event := range revision.Events {
		events = append(events, &api.SourceEvent{
			ID:        event.ID,
			StartByte: int32(event.StartByte),
			EndByte:   int32(event.EndByte),
		})
	}
	return &api.SourceRevision{
		ID:       revision.ID,
		SourceID: revision.SourceID,
		Sha256:   revision.SHA256,
		Raw:      string(revision.Raw),
		Events:   events,
	}
}

func sourceBacklinksToAPI(
	backlinks []pagewiki.SourceBacklink,
) *api.SourceBacklinksResponse {
	result := make([]*api.SourceBacklink, 0, len(backlinks))
	for _, backlink := range backlinks {
		result = append(result, &api.SourceBacklink{
			Page:       pageToAPI(backlink.Page),
			RevisionID: backlink.Revision.ID,
			Citations:  citationsToAPI(backlink.Citations),
		})
	}
	return &api.SourceBacklinksResponse{Backlinks: result}
}

func navigationToAPI(navigation pagewiki.Navigation) *api.NavigationResponse {
	return &api.NavigationResponse{
		Roots: navigationTopicsToAPI(navigation.Roots),
		Pages: navigationPagesToAPI(navigation.Pages),
	}
}

func navigationTopicsToAPI(
	topics []pagewiki.NavigationTopic,
) []*api.NavigationTopic {
	result := make([]*api.NavigationTopic, 0, len(topics))
	for _, topic := range topics {
		result = append(result, &api.NavigationTopic{
			ID:       topic.ID,
			Slug:     topic.Slug,
			Title:    topic.Title,
			Children: navigationTopicsToAPI(topic.Children),
			Pages:    navigationPagesToAPI(topic.Pages),
		})
	}
	return result
}

func navigationPagesToAPI(
	pages []pagewiki.NavigationPage,
) []*api.NavigationPage {
	result := make([]*api.NavigationPage, 0, len(pages))
	for _, page := range pages {
		result = append(result, &api.NavigationPage{
			ID:    page.ID,
			Slug:  page.Slug,
			Title: page.Title,
			Rank:  int32(page.Rank),
		})
	}
	return result
}
