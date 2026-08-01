package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/transport/httpapi"
	pagewikirouter "github.com/pax-beehive/pax-nexus/internal/pagewiki/transport/httpapi/router"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ContractAcceptanceSuite struct {
	suite.Suite
	server     *server.Hertz
	repository *memory.Repository
}

func TestContractAcceptanceSuite(t *testing.T) {
	suite.Run(t, new(ContractAcceptanceSuite))
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	t.Parallel()
	repository := memory.NewRepository()

	_, injectorErr := httpapi.New(nil, repository)
	_, readerErr := httpapi.New(unavailableInjector{}, nil)

	require.Error(t, injectorErr)
	require.Error(t, readerErr)
}

func (s *ContractAcceptanceSuite) SetupTest() {
	s.repository = memory.NewRepository()
	service := pagewiki.NewService(
		s.repository,
		contractPlanner{},
		pagewiki.ScriptedEditor{Drafts: map[string]pagewiki.PageDraft{
			"sqlite": contractSQLiteDraft(),
		}},
	)
	handler, err := httpapi.New(service, s.repository)
	s.Require().NoError(err)
	s.server = server.New()
	s.server.Use(httpapi.InstanceMiddleware(handler))
	pagewikirouter.Register(s.server)
}

func (s *ContractAcceptanceSuite) TestGivenInjectedSessionWhenReadThenEveryWikiRouteUsesItsRevision() {
	// Publish a second page directly through the repository and place it
	// under a topic, so navigation exercises both a placed page (nested
	// under "roots") and the unplaced "sqlite" page injected below (listed
	// under "pages"). Post-flat-write-path, every published page is
	// unplaced until ReplaceTopicTree is called explicitly. It also serves
	// as the link target for "sqlite"'s outgoing link below, and — being
	// untyped — exercises the empty-EntityType-maps-to-fallback rule on its
	// target_page payload.
	placedPage := pagewiki.Page{
		ID:                "page-placed",
		Slug:              "placed-topic-page",
		Title:             "Placed Topic Page",
		CurrentRevisionID: "revision-placed",
	}
	placedRevision := pagewiki.PageRevision{ID: "revision-placed", PageID: "page-placed"}
	s.Require().NoError(s.repository.PublishPage(context.Background(), pagewiki.PagePublication{
		Page:     placedPage,
		Revision: placedRevision,
	}))
	s.Require().NoError(s.repository.ReplaceTopicTree(context.Background(), pagewiki.TopicTree{
		Topics: []pagewiki.Topic{{ID: "topic-runtime", Slug: "runtime", Title: "Runtime"}},
		Placements: []pagewiki.PagePlacement{
			{PageID: "page-placed", TopicID: "topic-runtime"},
		},
	}))

	injected := s.performJSON(
		http.MethodPost,
		"/sessions/inject",
		`{
			"source_id":"session-http",
			"idempotency_key":"session-http-injection",
			"raw":"SQLite is searchable.",
			"events":[{"id":"event-sqlite","start_byte":0,"end_byte":21}]
		}`,
	)
	s.Require().Equal(http.StatusOK, injected.Code)
	var injection struct {
		SourceRevisionID string `json:"source_revision_id"`
		Run              struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Targets []struct {
				PageRevisionID string `json:"page_revision_id"`
			} `json:"targets"`
		} `json:"run"`
	}
	s.Require().NoError(json.Unmarshal(injected.Body.Bytes(), &injection))
	s.Require().Equal("succeeded", injection.Run.Status)
	s.Require().NotEmpty(injection.SourceRevisionID)
	s.Require().NotEmpty(injection.Run.ID)
	s.Require().Len(injection.Run.Targets, 1)
	revisionID := injection.Run.Targets[0].PageRevisionID

	tests := []struct {
		name       string
		path       string
		assertBody func(map[string]any)
	}{
		{
			name: "maintenance run",
			path: "/maintenance/runs/" + injection.Run.ID,
			assertBody: func(body map[string]any) {
				s.Equal(injection.Run.ID, body["id"])
				s.Equal("succeeded", body["status"])
			},
		},
		{
			name: "current page",
			path: "/v1/wiki/pages/sqlite",
			assertBody: func(body map[string]any) {
				s.Equal("sqlite", body["slug"])
				s.Equal(revisionID, body["current_revision_id"])
				s.Equal("system", body["entity_type"])
				revision := requireObject(s.T(), body["revision"])
				s.Equal(revisionID, revision["id"])
				s.Contains(revision["markdown"], "SQLite is searchable.")
				_, hasStatus := body["status"]
				s.False(hasStatus)
				_, hasSuccessorSlug := body["successor_slug"]
				s.False(hasSuccessorSlug)
			},
		},
		{
			name: "revision history",
			path: "/v1/wiki/pages/sqlite/revisions",
			assertBody: func(body map[string]any) {
				revisions := requireList(s.T(), body["revisions"])
				s.Require().Len(revisions, 1)
				s.Equal(revisionID, requireObject(s.T(), revisions[0])["id"])
			},
		},
		{
			name: "specific revision",
			path: "/v1/wiki/pages/sqlite/revisions/" + revisionID,
			assertBody: func(body map[string]any) {
				s.Equal(revisionID, body["id"])
				s.Equal("SQLite", body["title"])
			},
		},
		{
			name: "page backlinks",
			path: "/v1/wiki/pages/sqlite/backlinks",
			assertBody: func(body map[string]any) {
				s.Empty(body["incoming"])
				outgoing := requireList(s.T(), body["outgoing"])
				s.Require().Len(outgoing, 1)
				resolved := requireObject(s.T(), outgoing[0])
				link := requireObject(s.T(), resolved["link"])
				s.Equal("depends-on", link["relation_type"])
				sourcePage := requireObject(s.T(), resolved["source_page"])
				s.Equal("system", sourcePage["entity_type"])
				targetPage := requireObject(s.T(), resolved["target_page"])
				s.Equal("placed-topic-page", targetPage["slug"])
				s.Equal("concept", targetPage["entity_type"])
			},
		},
		{
			name: "search",
			path: "/v1/wiki/search?q=searchable",
			assertBody: func(body map[string]any) {
				results := requireList(s.T(), body["results"])
				s.Require().Len(results, 1)
				result := requireObject(s.T(), results[0])
				s.Equal(revisionID, result["revision_id"])
				s.Equal("decision", result["section_key"])
				s.NotEmpty(result["citations"])
			},
		},
		{
			name: "source revision",
			path: "/sources/" + injection.SourceRevisionID,
			assertBody: func(body map[string]any) {
				s.Equal(injection.SourceRevisionID, body["id"])
				s.Equal("SQLite is searchable.", body["raw"])
			},
		},
		{
			name: "source backlinks",
			path: "/sources/" + injection.SourceRevisionID + "/backlinks",
			assertBody: func(body map[string]any) {
				backlinks := requireList(s.T(), body["backlinks"])
				s.Require().Len(backlinks, 1)
				s.Equal(
					revisionID,
					requireObject(s.T(), backlinks[0])["revision_id"],
				)
			},
		},
		{
			name: "navigation",
			path: "/v1/wiki/navigation",
			assertBody: func(body map[string]any) {
				roots := requireList(s.T(), body["roots"])
				s.Require().Len(roots, 1)
				root := requireObject(s.T(), roots[0])
				s.Equal("runtime", root["slug"])
				rootPages := requireList(s.T(), root["pages"])
				s.Require().Len(rootPages, 1)
				s.Equal(
					"placed-topic-page",
					requireObject(s.T(), rootPages[0])["slug"],
				)

				pages := requireList(s.T(), body["pages"])
				s.Require().Len(pages, 1)
				s.Equal("sqlite", requireObject(s.T(), pages[0])["slug"])
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			response := s.performJSON(http.MethodGet, tt.path, "")
			s.Require().Equal(http.StatusOK, response.Code)
			var body map[string]any
			s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
			tt.assertBody(body)
		})
	}
}

func (s *ContractAcceptanceSuite) TestGivenRetiredPageWithSuccessorWhenFetchedThenStatusAndSuccessorSlugAreSurfaced() {
	ctx := context.Background()
	successor := pagewiki.Page{
		ID:                "page-successor",
		Slug:              "successor-page",
		Title:             "Successor Page",
		CurrentRevisionID: "revision-successor",
	}
	s.Require().NoError(s.repository.PublishPage(ctx, pagewiki.PagePublication{
		Page:     successor,
		Revision: pagewiki.PageRevision{ID: "revision-successor", PageID: "page-successor"},
	}))

	retired := pagewiki.Page{
		ID:                "page-retired",
		Slug:              "retired-page",
		Title:             "Retired Page",
		CurrentRevisionID: "revision-retired",
	}
	s.Require().NoError(s.repository.PublishPage(ctx, pagewiki.PagePublication{
		Page:     retired,
		Revision: pagewiki.PageRevision{ID: "revision-retired", PageID: "page-retired"},
	}))
	s.Require().NoError(s.repository.RetirePage(ctx, pagewiki.RetireRequest{
		PageID:                 "page-retired",
		ExpectedBaseRevisionID: "revision-retired",
		SuccessorPageID:        "page-successor",
		RunID:                  "run-retire-1",
	}))

	response := s.performJSON(http.MethodGet, "/v1/wiki/pages/retired-page", "")
	s.Require().Equal(http.StatusOK, response.Code)
	var body map[string]any
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
	s.Equal("retired", body["status"])
	s.Equal("successor-page", body["successor_slug"])
}

func (s *ContractAcceptanceSuite) TestGivenRetiredPageWithoutSuccessorWhenFetchedThenSuccessorSlugIsOmitted() {
	ctx := context.Background()
	page := pagewiki.Page{
		ID:                "page-retired-solo",
		Slug:              "retired-solo",
		Title:             "Retired Solo",
		CurrentRevisionID: "revision-retired-solo",
	}
	s.Require().NoError(s.repository.PublishPage(ctx, pagewiki.PagePublication{
		Page:     page,
		Revision: pagewiki.PageRevision{ID: "revision-retired-solo", PageID: page.ID},
	}))
	s.Require().NoError(s.repository.RetirePage(ctx, pagewiki.RetireRequest{
		PageID:                 page.ID,
		ExpectedBaseRevisionID: "revision-retired-solo",
		RunID:                  "run-retire-2",
	}))

	response := s.performJSON(http.MethodGet, "/v1/wiki/pages/retired-solo", "")
	s.Require().Equal(http.StatusOK, response.Code)
	var body map[string]any
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
	s.Equal("retired", body["status"])
	_, hasSuccessorSlug := body["successor_slug"]
	s.False(hasSuccessorSlug)
}

func (s *ContractAcceptanceSuite) TestGivenRetiredPageWithDanglingSuccessorWhenFetchedThenNoSuccessorSlugAndNo500() {
	ctx := context.Background()
	page := pagewiki.Page{
		ID:                "page-retired-dangling",
		Slug:              "retired-dangling",
		Title:             "Retired Dangling",
		CurrentRevisionID: "revision-retired-dangling",
	}
	s.Require().NoError(s.repository.PublishPage(ctx, pagewiki.PagePublication{
		Page:     page,
		Revision: pagewiki.PageRevision{ID: "revision-retired-dangling", PageID: page.ID},
	}))
	s.Require().NoError(s.repository.RetirePage(ctx, pagewiki.RetireRequest{
		PageID:                 page.ID,
		ExpectedBaseRevisionID: "revision-retired-dangling",
		SuccessorPageID:        "page-does-not-exist",
		RunID:                  "run-retire-3",
	}))

	response := s.performJSON(http.MethodGet, "/v1/wiki/pages/retired-dangling", "")
	s.Require().Equal(http.StatusOK, response.Code)
	var body map[string]any
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
	s.Equal("retired", body["status"])
	_, hasSuccessorSlug := body["successor_slug"]
	s.False(hasSuccessorSlug)
}

func (s *ContractAcceptanceSuite) TestGivenFileSourceOnlyWhenInjectedThenRunSucceedsWithoutPage() {
	response := s.performJSON(
		http.MethodPost,
		"/files/inject",
		`{
			"source_id":"file-readme",
			"idempotency_key":"file-readme-injection",
			"raw":"No durable page is required.",
			"events":[{"id":"event-file","start_byte":0,"end_byte":28}]
		}`,
	)

	s.Require().Equal(http.StatusOK, response.Code)
	var body map[string]any
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
	run := requireObject(s.T(), body["run"])
	s.Equal("succeeded", run["status"])
	s.Zero(s.repository.PageCount())
}

func (s *ContractAcceptanceSuite) TestGivenInvalidReadWhenRequestedThenJSONErrorIsReturned() {
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing page",
			path:       "/v1/wiki/pages/missing",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "blank search",
			path:       "/v1/wiki/search?q=--",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_search",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			response := s.performJSON(http.MethodGet, tt.path, "")
			s.Require().Equal(tt.wantStatus, response.Code)
			var body map[string]any
			s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
			s.Equal(tt.wantCode, body["code"])
			s.NotEmpty(body["message"])
		})
	}
}

func (s *ContractAcceptanceSuite) TestGivenMalformedInjectionWhenRequestedThenJSONErrorIsReturned() {
	response := s.performJSON(http.MethodPost, "/sessions/inject", `{`)

	s.Require().Equal(http.StatusBadRequest, response.Code)
	var body map[string]any
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
	s.Equal("invalid_request", body["code"])
}

func (s *ContractAcceptanceSuite) TestGivenLocalReaderWhenOpenedThenItsAccessibleShellAndAssetsAreServed() {
	response := s.performJSON(http.MethodGet, "/wiki", "")

	s.Require().Equal(http.StatusOK, response.Code)
	s.Contains(response.Header().Get("Content-Type"), "text/html")
	html := response.Body.String()
	s.Contains(html, "Page Wiki")
	s.Contains(html, `data-testid="topic-navigation"`)
	s.Contains(html, `data-testid="article"`)
	s.Contains(html, `data-testid="evidence-panel"`)
	s.Contains(html, `data-testid="xanadu-summary"`)
	s.Contains(html, `data-testid="xanadu-panel"`)
	s.Contains(html, `id="wiki-search"`)
	s.Contains(html, `/wiki/assets/reader.css`)
	s.Contains(html, `/wiki/assets/reader.js`)

	for _, asset := range []string{"reader.css", "reader.js"} {
		assetResponse := s.performJSON(
			http.MethodGet,
			"/wiki/assets/"+asset,
			"",
		)
		s.Equal(http.StatusOK, assetResponse.Code)
		s.NotEmpty(assetResponse.Body.Bytes())
	}

	missingAsset := s.performJSON(
		http.MethodGet,
		"/wiki/assets/unknown.js",
		"",
	)
	s.Equal(http.StatusNotFound, missingAsset.Code)
	var body map[string]any
	s.Require().NoError(json.Unmarshal(missingAsset.Body.Bytes(), &body))
	s.Equal("not_found", body["code"])
}

func (s *ContractAcceptanceSuite) TestGivenUnavailableInjectorWhenRequestedThenServiceUnavailableIsReturned() {
	handler, err := httpapi.New(unavailableInjector{}, s.repository)
	s.Require().NoError(err)
	hertz := server.New()
	hertz.Use(httpapi.InstanceMiddleware(handler))
	pagewikirouter.Register(hertz)
	body := `{
		"source_id":"session-http",
		"idempotency_key":"unavailable",
		"raw":"text",
		"events":[{"id":"event","start_byte":0,"end_byte":4}]
	}`

	response := performJSON(hertz, http.MethodPost, "/sessions/inject", body)

	s.Require().Equal(http.StatusServiceUnavailable, response.Code)
	var mapped map[string]any
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &mapped))
	s.Equal("unavailable", mapped["code"])
}

func (s *ContractAcceptanceSuite) TestGivenMissingMiddlewareWhenRequestedThenServerErrorIsReturned() {
	hertz := server.New()
	pagewikirouter.Register(hertz)

	response := performJSON(hertz, http.MethodGet, "/v1/wiki/navigation", "")

	s.Equal(http.StatusInternalServerError, response.Code)
}

func (s *ContractAcceptanceSuite) performJSON(
	method string,
	path string,
	body string,
) *ut.ResponseRecorder {
	s.T().Helper()
	return performJSON(s.server, method, path, body)
}

func performJSON(
	hertz *server.Hertz,
	method string,
	path string,
	body string,
) *ut.ResponseRecorder {
	var requestBody *ut.Body
	if body != "" {
		requestBody = &ut.Body{
			Body: bytes.NewBufferString(body),
			Len:  len(body),
		}
	}
	return ut.PerformRequest(
		hertz.Engine,
		method,
		path,
		requestBody,
		ut.Header{Key: "Content-Type", Value: "application/json"},
	)
}

func requireObject(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	require.True(t, ok)
	return result
}

func requireList(t *testing.T, value any) []any {
	t.Helper()
	result, ok := value.([]any)
	require.True(t, ok)
	return result
}

type unavailableInjector struct{}

func (unavailableInjector) InjectSession(
	context.Context,
	pagewiki.InjectSessionRequest,
) (pagewiki.InjectResult, error) {
	return pagewiki.InjectResult{}, pagewiki.ErrUnavailable
}

type contractPlanner struct{}

func (contractPlanner) Plan(
	_ context.Context,
	input pagewiki.PlanInput,
) ([]pagewiki.PageBrief, error) {
	if input.SourceRevision.SourceID == "file-readme" {
		return []pagewiki.PageBrief{
			{
				Key:              "file-source-only",
				Action:           pagewiki.PageActionSourceOnly,
				EvidenceEventIDs: []string{"event-file"},
			},
		}, nil
	}
	return []pagewiki.PageBrief{
		{
			Key:              "sqlite",
			Action:           pagewiki.PageActionCreate,
			ProposedSlug:     "sqlite",
			ProposedTitle:    "SQLite",
			EvidenceEventIDs: []string{"event-sqlite"},
			EntityType:       pagewiki.EntityTypeSystem,
		},
	}, nil
}

func contractSQLiteDraft() pagewiki.PageDraft {
	return pagewiki.PageDraft{
		Slug:    "sqlite",
		Title:   "SQLite",
		Summary: "SQLite content is searchable.",
		Sections: []pagewiki.SectionDraft{
			{
				Key:      "decision",
				Heading:  "Decision",
				Markdown: "SQLite is searchable.",
			},
		},
		Citations: []pagewiki.CitationDraft{
			{
				SectionKey: "decision",
				ExactText:  "searchable",
				Evidence: []pagewiki.EvidenceQuoteDraft{
					{
						EventID:   "event-sqlite",
						ExactText: "SQLite is searchable.",
					},
				},
			},
		},
		Links: []pagewiki.LinkDraft{
			{
				SectionKey:   "decision",
				ExactText:    "SQLite",
				TargetPageID: "page-placed",
				RelationType: pagewiki.RelationTypeDependsOn,
			},
		},
	}
}
