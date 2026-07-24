package workspace_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	"github.com/stretchr/testify/suite"
)

type viewerSuite struct {
	suite.Suite
	root       string
	sourcePath string
	anchor     string
	handler    http.Handler
}

func TestViewerSuite(t *testing.T) {
	suite.Run(t, new(viewerSuite))
}

func (s *viewerSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.T().Cleanup(func() { chmodSources(s.root) })
	exported := workspace.SessionExport{
		SchemaVersion: workspace.PaxmSessionSchema,
		SessionID:     "viewer-session",
		Turns: []workspace.SessionTurn{{
			ID: "turn-viewer", User: "Viewer source fact.", Assistant: "Confirmed.",
		}},
	}
	encoded, err := json.Marshal(exported)
	s.Require().NoError(err)
	built, err := workspace.Build(context.Background(), workspace.BuildConfig{
		Root: s.root,
		ReadSession: func(context.Context, string) ([]byte, error) {
			return encoded, nil
		},
	}, workspace.BuildRequest{
		SessionID: "viewer-session", TurnStart: 0, TurnEnd: 1,
	})
	s.Require().NoError(err)
	s.sourcePath = built.Source.Path
	s.anchor = built.Source.Anchors[0].ID
	s.write("wiki/index.md", "# Topic Tree\n\n- [Architecture](pages/architecture.md)\n")
	s.write(
		"wiki/pages/architecture.md",
		"# Architecture\n\nThe viewer exposes exact evidence "+
			"([source](../../"+s.sourcePath+"#"+s.anchor+")).\n",
	)
	s.handler = workspace.NewViewer(s.root)
}

func (s *viewerSuite) TestRendersTopicTreePageBodyAndCitationSource() {
	home := s.request("/")
	s.Equal(http.StatusOK, home.Code)
	s.Contains(home.Body.String(), "Topic Tree")
	s.Contains(home.Body.String(), `/wiki/pages/architecture.md`)

	page := s.request("/wiki/pages/architecture.md")
	s.Equal(http.StatusOK, page.Code)
	s.Contains(page.Body.String(), "Architecture")
	s.Contains(page.Body.String(), "/sources/")
	s.Contains(page.Body.String(), "#"+s.anchor)

	sourceName := filepath.Base(s.sourcePath)
	source := s.request("/sources/" + sourceName)
	s.Equal(http.StatusOK, source.Code)
	s.Contains(source.Body.String(), "Immutable Source")
	s.Contains(source.Body.String(), `id="`+s.anchor+`"`)
	s.Contains(source.Body.String(), "Viewer source fact.")
	s.Contains(source.Header().Get("Content-Security-Policy"), "default-src 'none'")
}

func (s *viewerSuite) TestRejectsTraversalAndMissingFiles() {
	traversal := s.request("/wiki/%2e%2e/AGENTS.md")
	s.Equal(http.StatusBadRequest, traversal.Code)

	missing := s.request("/wiki/pages/missing.md")
	s.Equal(http.StatusNotFound, missing.Code)

	method := httptest.NewRequest(http.MethodPost, "/", nil)
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, method)
	s.Equal(http.StatusMethodNotAllowed, recorder.Code)

	unknown := s.request("/unknown")
	s.Equal(http.StatusBadRequest, unknown.Code)
	nonMarkdown := s.request("/wiki/index.txt")
	s.Equal(http.StatusBadRequest, nonMarkdown.Code)

	headRequest := httptest.NewRequest(http.MethodHead, "/", nil)
	head := httptest.NewRecorder()
	s.handler.ServeHTTP(head, headRequest)
	s.Equal(http.StatusOK, head.Code)
	s.Empty(head.Body.String())
}

func (s *viewerSuite) TestRendersListsCodeSubheadingsAndExternalLinksSafely() {
	s.write(
		"wiki/pages/architecture.md",
		"# Architecture\n\n### Details\n\n- One\n- [External](https://example.com)\n\n"+
			"```go\nif left < right {\n}\n```\n\n<script>alert(1)</script>\n",
	)
	page := s.request("/wiki/pages/architecture.md")
	s.Equal(http.StatusOK, page.Code)
	s.Contains(page.Body.String(), "<h3>Details</h3>")
	s.Contains(page.Body.String(), "<ul><li>One</li>")
	s.Contains(page.Body.String(), `href="https://example.com"`)
	s.Contains(page.Body.String(), "<pre><code>")
	s.Contains(page.Body.String(), "&lt;script&gt;")
	s.NotContains(page.Body.String(), "<script>")
}

func (s *viewerSuite) request(target string) *httptest.ResponseRecorder {
	s.T().Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	return recorder
}

func (s *viewerSuite) write(relative, content string) {
	s.T().Helper()
	target := filepath.Join(s.root, filepath.FromSlash(relative))
	s.Require().NoError(os.MkdirAll(filepath.Dir(target), 0o755))
	s.Require().NoError(os.WriteFile(target, []byte(content), 0o644))
}
