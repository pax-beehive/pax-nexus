package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	pagewikipostgres "github.com/pax-beehive/pax-nexus/internal/pagewiki/postgres"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/recalladapter"
	platformpostgres "github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/pax-beehive/pax-nexus/internal/recall"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/mocks"
	"github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/handler"
	api "github.com/pax-beehive/pax-nexus/internal/teamnote/transport/httpapi/model/teammemory/api"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

type pageWikiRecallIntegrationSuite struct {
	suite.Suite
	ctx     context.Context
	store   *platformpostgres.Store
	scopeID string
}

func TestPageWikiRecallIntegrationSuite(t *testing.T) {
	suite.Run(t, new(pageWikiRecallIntegrationSuite))
}

func (s *pageWikiRecallIntegrationSuite) SetupSuite() {
	dsn := os.Getenv("TEAM_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		s.T().Skip("TEAM_MEMORY_TEST_POSTGRES_DSN is not configured")
	}
	s.ctx = context.Background()
	var err error
	s.store, err = platformpostgres.Open(s.ctx, dsn)
	s.Require().NoError(err)
	s.Require().NoError(s.store.Migrate(s.ctx))
}

func (s *pageWikiRecallIntegrationSuite) TearDownSuite() {
	if s.store != nil {
		s.store.Close()
	}
}

func (s *pageWikiRecallIntegrationSuite) SetupTest() {
	s.scopeID = fmt.Sprintf("pagewiki-agent-recall-%d", time.Now().UnixNano())
}

func (s *pageWikiRecallIntegrationSuite) TearDownTest() {
	if s.store == nil || s.scopeID == "" {
		return
	}
	for _, query := range []string{
		"DELETE FROM pagewiki_maintenance_runs WHERE scope_id = $1",
		"DELETE FROM pagewiki_publications WHERE scope_id = $1",
		"DELETE FROM pagewiki_source_revisions WHERE scope_id = $1",
	} {
		_, err := s.store.Pool().Exec(s.ctx, query, s.scopeID)
		s.Require().NoError(err)
	}
}

func (s *pageWikiRecallIntegrationSuite) TestGivenInjectedSessionWhenAgentSearchesAndGetsThenExactEvidenceIsReturned() {
	repository, err := pagewikipostgres.NewRepository(s.ctx, s.store.Pool(), s.scopeID)
	s.Require().NoError(err)
	raw := []byte("Radix Slot fails when it receives two direct children.")
	service := pagewiki.NewService(
		repository,
		pagewiki.ScriptedPlanner{Briefs: []pagewiki.PageBrief{{
			Key: "radix-slot", Action: pagewiki.PageActionCreate,
			ProposedSlug: "radix-slot", ProposedTitle: "Radix Slot",
			ReaderGoal: "Explain the crash.", TopicPath: []string{"UI reliability"},
			EvidenceEventIDs: []string{"event-radix"},
		}}},
		pagewiki.ScriptedEditor{Drafts: map[string]pagewiki.PageDraft{
			"radix-slot": {
				Slug: "radix-slot", Title: "Radix Slot",
				Summary: "Why the slotted link button crashed.",
				Sections: []pagewiki.SectionDraft{{
					Key: "root-cause", Heading: "Root cause",
					Markdown: "Radix Slot requires one direct child.",
				}},
				Citations: []pagewiki.CitationDraft{{
					SectionKey: "root-cause", ExactText: "Radix Slot",
					Evidence: []pagewiki.EvidenceQuoteDraft{{
						EventID: "event-radix", ExactText: string(raw),
					}},
				}},
			},
		}},
	)
	injected, err := service.InjectSession(s.ctx, pagewiki.InjectSessionRequest{
		SourceID: "session-radix", IdempotencyKey: "inject-radix", Raw: raw,
		Events: []pagewiki.SourceEventInput{{
			ID: "event-radix", StartByte: 0, EndByte: len(raw),
		}},
	})
	s.Require().NoError(err)
	s.Equal(pagewiki.RunStatusSucceeded, injected.Run.Status)

	pageWikiRecall, err := recalladapter.New(repository)
	s.Require().NoError(err)
	controller := gomock.NewController(s.T())
	runtime := mocks.NewMockRuntime(controller)
	memory, err := recall.NewRouter(runtime, pageWikiRecall)
	s.Require().NoError(err)
	principal := onprem.Principal{
		UserID: "owner", AgentID: "agent-1", ScopeID: s.scopeID,
		CredentialID: "credential-1",
		Permissions:  []onprem.Permission{onprem.PermissionSearch, onprem.PermissionGet},
	}
	httpHandler, err := handler.NewOnPrem(
		runtime,
		&credentialService{principal: &principal},
		memory,
		&channelService{},
		slog.New(slog.DiscardHandler),
	)
	s.Require().NoError(err)

	searchResponse := perform(
		httpHandler.SearchMemory,
		http.MethodPost,
		`{"intent":"active","source":"pagewiki","session_id":"agent-session","query":"Radix Slot","token_budget":512,"max_items":4}`,
		"agent",
	)
	s.Equal(http.StatusOK, searchResponse.Code)
	var search api.MemorySearchResponse
	s.Require().NoError(json.Unmarshal(searchResponse.Body.Bytes(), &search))
	s.Require().Len(search.Hits, 1)
	hit := search.Hits[0]
	s.Require().NotNil(hit.Ref)
	s.Require().NotNil(hit.Pagewiki)
	s.Equal("root-cause", hit.Pagewiki.GetSectionKey())
	s.Require().Len(hit.Pagewiki.Citations, 1)
	s.Equal(string(raw), hit.Pagewiki.Citations[0].SourceAnchors[0].ExactQuote)

	getBody, err := json.Marshal(map[string]string{
		"session_id": "agent-session",
		"ref":        *hit.Ref,
	})
	s.Require().NoError(err)
	getResponse := perform(httpHandler.GetMemory, http.MethodPost, string(getBody), "agent")
	s.Equal(http.StatusOK, getResponse.Code)
	var document api.MemoryDocument
	s.Require().NoError(json.Unmarshal(getResponse.Body.Bytes(), &document))
	s.Equal(*hit.Ref, document.Ref)
	s.Contains(document.Text, "Radix Slot requires one direct child.")
	s.Require().NotNil(document.Pagewiki)
	s.Equal(hit.Pagewiki.RevisionID, document.Pagewiki.RevisionID)
	s.Equal(string(raw), document.Pagewiki.Citations[0].SourceAnchors[0].ExactQuote)
}
