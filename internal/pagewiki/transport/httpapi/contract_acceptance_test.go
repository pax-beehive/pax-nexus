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
			path: "/pages/sqlite",
			assertBody: func(body map[string]any) {
				s.Equal("sqlite", body["slug"])
				s.Equal(revisionID, body["current_revision_id"])
				revision := requireObject(s.T(), body["revision"])
				s.Equal(revisionID, revision["id"])
				s.Contains(revision["markdown"], "SQLite is searchable.")
			},
		},
		{
			name: "revision history",
			path: "/pages/sqlite/revisions",
			assertBody: func(body map[string]any) {
				revisions := requireList(s.T(), body["revisions"])
				s.Require().Len(revisions, 1)
				s.Equal(revisionID, requireObject(s.T(), revisions[0])["id"])
			},
		},
		{
			name: "specific revision",
			path: "/pages/sqlite/revisions/" + revisionID,
			assertBody: func(body map[string]any) {
				s.Equal(revisionID, body["id"])
				s.Equal("SQLite", body["title"])
			},
		},
		{
			name: "page backlinks",
			path: "/pages/sqlite/backlinks",
			assertBody: func(body map[string]any) {
				s.Empty(body["incoming"])
			},
		},
		{
			name: "search",
			path: "/search?q=searchable",
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
			path: "/navigation",
			assertBody: func(body map[string]any) {
				roots := requireList(s.T(), body["roots"])
				s.Require().Len(roots, 1)
				s.Equal("Engineering", requireObject(s.T(), roots[0])["title"])
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
			path:       "/pages/missing",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "blank search",
			path:       "/search?q=--",
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

	response := performJSON(hertz, http.MethodGet, "/navigation", "")

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
			TopicPath:        []string{"Engineering", "Storage"},
			EvidenceEventIDs: []string{"event-sqlite"},
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
	}
}
