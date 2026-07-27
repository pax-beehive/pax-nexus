package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	"github.com/stretchr/testify/suite"
)

type PaxlSessionSuite struct {
	suite.Suite
}

func TestPaxlSessionSuite(t *testing.T) {
	suite.Run(t, new(PaxlSessionSuite))
}

func (suite *PaxlSessionSuite) TestGivenRealExportsAndModelPlanWhenInjectedThenContentIsUnchangedAndGrounded() {
	ctx := context.Background()
	repository := memory.NewRepository()
	directory := suite.T().TempDir()
	paths := []string{
		suite.writePaxlExport(directory, "session-a", "Hypertext", map[int64]string{
			7: "The source describes durable content identity.",
		}),
		suite.writePaxlExport(directory, "session-b", "Architecture", map[int64]string{
			12: "The source separates evidence from interpretation.",
		}),
		suite.writePaxlExport(directory, "session-c", "Reader", map[int64]string{
			34: "The source requires visible incoming and outgoing links.",
		}),
	}
	plan := generatedPlan{Pages: []generatedPage{
		generatedFixturePage(
			"session-a",
			"durable-hypertext",
			"Durable Hypertext",
			"Durable content identity connects the evidence architecture.",
			"message-7",
			"The source describes durable content identity.",
			[]generatedLink{{
				SectionKey: "overview",
				ExactText:  "evidence architecture",
				TargetSlug: "evidence-architecture",
			}},
		),
		generatedFixturePage(
			"session-b",
			"evidence-architecture",
			"Evidence Architecture",
			"The evidence architecture separates evidence from interpretation and complements Durable Hypertext.",
			"message-12",
			"The source separates evidence from interpretation.",
			[]generatedLink{{
				SectionKey: "overview",
				ExactText:  "Durable Hypertext",
				TargetSlug: "durable-hypertext",
			}},
		),
		generatedFixturePage(
			"session-c",
			"wiki-reader",
			"Wiki Reader",
			"The reader exposes visible incoming and outgoing links.",
			"message-34",
			"The source requires visible incoming and outgoing links.",
			nil,
		),
	}}
	planPath := suite.writeGeneratedPlan(directory, plan)

	landingPage, runID, err := injectGeneratedPlan(ctx, repository, planPath, paths)

	suite.Require().NoError(err)
	suite.Equal("durable-hypertext", landingPage)
	suite.NotEmpty(runID)
	suite.Equal(3, repository.SourceRevisionCount())
	navigation, err := repository.Navigation(ctx)
	suite.Require().NoError(err)
	suite.Equal(3, navigationPageCount(navigation.Roots))

	hypertext, err := repository.PageBySlug(ctx, landingPage)
	suite.Require().NoError(err)
	history, err := repository.PageRevisionHistory(ctx, hypertext.ID)
	suite.Require().NoError(err)
	suite.Len(history, 2)
	suite.Equal(history[0].ID, history[1].BaseRevisionID)
	suite.Contains(history[1].Markdown, plan.Pages[0].Sections[0].Markdown)
	suite.Equal(plan.Pages[0].Citations[0].ExactText, history[1].Citations[0].ExactText)
	suite.Equal(
		plan.Pages[0].Citations[0].Evidence[0].ExactText,
		history[1].Citations[0].SourceAnchors[0].ExactQuote,
	)

	links, err := repository.PageLinks(ctx, hypertext.ID)
	suite.Require().NoError(err)
	suite.Len(links.Outgoing, 1)
	suite.Len(links.Incoming, 1)
	run, err := repository.MaintenanceRun(ctx, runID)
	suite.Require().NoError(err)
	suite.Equal(pagewiki.RunStatusSucceeded, run.Status)
}

func (suite *PaxlSessionSuite) TestGivenAPlanWithoutLinksWhenInjectedThenNoSyntheticRevisionIsCreated() {
	ctx := context.Background()
	repository := memory.NewRepository()
	directory := suite.T().TempDir()
	var pages []generatedPage
	var paths []string
	slugs := []string{"page-one", "page-two", "page-three"}
	titles := []string{"Page One", "Page Two", "Page Three"}
	for index, slug := range slugs {
		sessionID := "session-" + slug
		eventID := messageEventID(int64(index + 1))
		quote := "Evidence for " + slug + "."
		paths = append(paths, suite.writePaxlExport(
			directory,
			sessionID,
			"Source "+slug,
			map[int64]string{int64(index + 1): quote},
		))
		pages = append(pages, generatedFixturePage(
			sessionID,
			slug,
			titles[index],
			"A durable article contains cited evidence.",
			eventID,
			quote,
			nil,
		))
	}

	landingPage, runID, err := injectGeneratedPlan(
		ctx,
		repository,
		suite.writeGeneratedPlan(directory, generatedPlan{Pages: pages}),
		paths,
	)

	suite.Require().NoError(err)
	suite.Equal("page-one", landingPage)
	suite.NotEmpty(runID)
	page, err := repository.PageBySlug(ctx, landingPage)
	suite.Require().NoError(err)
	history, err := repository.PageRevisionHistory(ctx, page.ID)
	suite.Require().NoError(err)
	suite.Len(history, 1)
}

func (suite *PaxlSessionSuite) TestGivenInvalidModelPlansWhenValidatedThenTheyAreRejected() {
	session := paxlSession{
		ID:       "codex:session-a",
		NativeID: "session-a",
		Title:    "Source",
		Messages: []paxlMessage{{
			Seq:     7,
			Role:    "assistant",
			Content: "Exact source evidence.",
		}},
	}
	tests := []struct {
		name    string
		mutate  func(*generatedPlan)
		wantErr string
	}{
		{
			name: "too few pages",
			mutate: func(plan *generatedPlan) {
				plan.Pages = plan.Pages[:2]
			},
			wantErr: "want 3-6",
		},
		{
			name: "duplicate slug",
			mutate: func(plan *generatedPlan) {
				plan.Pages[1].Slug = plan.Pages[0].Slug
			},
			wantErr: "duplicate slug",
		},
		{
			name: "missing session",
			mutate: func(plan *generatedPlan) {
				plan.Pages[0].SourceSessionID = "missing"
			},
			wantErr: "source session",
		},
		{
			name: "non English reader text",
			mutate: func(plan *generatedPlan) {
				plan.Pages[0].Sections[0].Markdown = "中文内容."
				plan.Pages[0].Citations[0].ExactText = "中文内容"
			},
			wantErr: "not English-only",
		},
		{
			name: "missing evidence event",
			mutate: func(plan *generatedPlan) {
				plan.Pages[0].Citations[0].Evidence[0].EventID = "message-999"
			},
			wantErr: "evidence event",
		},
		{
			name: "forged evidence quote",
			mutate: func(plan *generatedPlan) {
				plan.Pages[0].Citations[0].Evidence[0].ExactText = "Forged."
			},
			wantErr: "evidence quote",
		},
		{
			name: "missing link target",
			mutate: func(plan *generatedPlan) {
				plan.Pages[0].Links = []generatedLink{{
					SectionKey: "overview",
					ExactText:  "English claim",
					TargetSlug: "missing",
				}}
			},
			wantErr: "link target",
		},
	}
	for _, test := range tests {
		suite.Run(test.name, func() {
			plan := generatedPlan{Pages: []generatedPage{
				generatedFixturePage(
					"session-a",
					"valid-page",
					"Valid Page",
					"An English claim appears once.",
					"message-7",
					"Exact source evidence.",
					nil,
				),
				generatedFixturePage(
					"session-a",
					"valid-page-two",
					"Valid Page Two",
					"Another English claim appears once.",
					"message-7",
					"Exact source evidence.",
					nil,
				),
				generatedFixturePage(
					"session-a",
					"valid-page-three",
					"Valid Page Three",
					"A third English claim appears once.",
					"message-7",
					"Exact source evidence.",
					nil,
				),
			}}
			test.mutate(&plan)

			err := validateGeneratedPlan(plan, map[string]paxlSession{
				"session-a": session,
			})

			suite.ErrorContains(err, test.wantErr)
		})
	}
}

func (suite *PaxlSessionSuite) TestGivenMalformedPlanFilesWhenLoadedThenStrictJSONErrorsAreReturned() {
	directory := suite.T().TempDir()
	missing := filepath.Join(directory, "missing.json")
	_, err := loadGeneratedPlan(missing)
	suite.Require().ErrorContains(err, "read generated plan")

	unknown := filepath.Join(directory, "unknown.json")
	suite.Require().NoError(os.WriteFile(
		unknown,
		[]byte(`{"pages":[],"unexpected":true}`),
		0o600,
	))
	_, err = loadGeneratedPlan(unknown)
	suite.Require().ErrorContains(err, "unknown field")

	trailing := filepath.Join(directory, "trailing.json")
	suite.Require().NoError(os.WriteFile(
		trailing,
		[]byte(`{"pages":[]} {"pages":[]}`),
		0o600,
	))
	_, err = loadGeneratedPlan(trailing)
	suite.ErrorContains(err, "trailing JSON value")
}

func (suite *PaxlSessionSuite) TestParseBuildsStableMessageEvents() {
	input := strings.Join([]string{
		`{"schemaVersion":"paxl.session.snapshot.v1","sessionId":"codex:session-1","nativeId":"session-1","projectId":"/repo","title":"A real session"}`,
		`{"schemaVersion":"paxl.session.element.v1","type":"message","role":"user","seq":4,"contentText":"<recommended_plugins>noise</recommended_plugins><environment_context>noise</environment_context>","sessionId":"codex:session-1"}`,
		`{"schemaVersion":"paxl.session.element.v1","type":"message","role":"user","seq":7,"contentText":"What did we decide?","sessionId":"codex:session-1"}`,
		`{"schemaVersion":"paxl.session.element.v1","type":"message","role":"assistant","seq":8,"contentText":"The source stays immutable.","sessionId":"codex:session-1"}`,
	}, "\n")

	session, err := parsePaxlSession(strings.NewReader(input))

	suite.Require().NoError(err)
	suite.Equal("session-1", session.NativeID)
	suite.Len(session.Messages, 2)
	request := session.injectionRequest("preview-v1")
	suite.Equal("codex:session-1", request.SourceID)
	suite.Equal("message-7", request.Events[0].ID)
	suite.Equal(
		"What did we decide?",
		string(request.Raw[request.Events[0].StartByte:request.Events[0].EndByte]),
	)
}

func (suite *PaxlSessionSuite) TestRejectsIncompleteOrEmptyExports() {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "missing snapshot",
			input: `{"schemaVersion":"paxl.session.element.v1","type":"message","role":"assistant","seq":1,"contentText":"knowledge","sessionId":"codex:session-1"}`,
		},
		{
			name: "only environment boilerplate",
			input: `{"schemaVersion":"paxl.session.snapshot.v1","sessionId":"codex:session-1","nativeId":"session-1","projectId":"/repo","title":"Empty"}` +
				"\n" +
				`{"schemaVersion":"paxl.session.element.v1","type":"message","role":"user","seq":1,"contentText":"<environment_context>noise</environment_context>","sessionId":"codex:session-1"}`,
		},
		{name: "invalid json", input: `{`},
	}
	for _, test := range tests {
		suite.Run(test.name, func() {
			_, err := parsePaxlSession(strings.NewReader(test.input))
			suite.Error(err)
		})
	}
}

func (suite *PaxlSessionSuite) TestReportsMissingExportAndDuplicateSession() {
	_, err := loadPaxlSession(filepath.Join(suite.T().TempDir(), "missing.jsonl"))
	suite.Require().ErrorContains(err, "read paxl session")

	directory := suite.T().TempDir()
	path := suite.writePaxlExport(
		directory,
		"duplicate",
		"Duplicate",
		map[int64]string{1: "Evidence."},
	)
	_, err = loadGeneratedSessions([]string{path, path})
	suite.ErrorContains(err, "duplicate session")
}

func (suite *PaxlSessionSuite) TestFiltersNonKnowledgeRecordsAndRecognizesEnglish() {
	tests := []paxlRecord{
		{Type: "tool", Role: "assistant", ContentText: "result"},
		{Type: "message", Role: "developer", ContentText: "instructions"},
		{Type: "message", Role: "assistant", ContentText: " "},
		{Type: "message", Role: "assistant", ContentText: "<recommended_plugins>noise"},
		{Type: "message", Role: "user", ContentText: "<environment_context>noise"},
	}
	for _, record := range tests {
		suite.False(isKnowledgeMessage(record))
	}
	suite.True(isKnowledgeMessage(paxlRecord{
		Type:        "message",
		Role:        "assistant",
		ContentText: "durable knowledge",
	}))
	suite.True(isEnglishOnly("English prose with 2026 punctuation—fine."))
	suite.False(isEnglishOnly("English 与中文"))
}

func (suite *PaxlSessionSuite) TestKeepsSyntheticReaderFixtureExplicitlyAvailable() {
	ctx := context.Background()
	repository := memory.NewRepository()

	injectPreviewSessions(ctx, repository)
	savePreviewRun(ctx, repository)

	navigation, err := repository.Navigation(ctx)
	suite.Require().NoError(err)
	suite.Equal(8, navigationPageCount(navigation.Roots))
	run, err := repository.MaintenanceRun(ctx, "run_reader_preview")
	suite.Require().NoError(err)
	suite.Equal(pagewiki.RunStatusPartialSuccess, run.Status)
	result, err := unavailableInjector{}.InjectSession(ctx, pagewiki.InjectSessionRequest{})
	suite.Empty(result)
	suite.Require().ErrorIs(err, pagewiki.ErrUnavailable)
	suite.NotPanics(func() {
		must(nil)
	})
	suite.Panics(func() {
		must(errors.New("expected panic"))
	})
}

func generatedFixturePage(
	sourceSessionID string,
	slug string,
	title string,
	markdown string,
	eventID string,
	evidence string,
	links []generatedLink,
) generatedPage {
	return generatedPage{
		SourceSessionID: sourceSessionID,
		Slug:            slug,
		Title:           title,
		Summary:         "A durable encyclopedia summary.",
		TopicPath:       []string{"Knowledge", "Architecture"},
		Sections: []generatedSection{{
			Key:      "overview",
			Heading:  "Overview",
			Markdown: markdown,
		}},
		Citations: []generatedCitation{{
			SectionKey: "overview",
			ExactText:  strings.TrimSuffix(markdown, "."),
			Evidence: []generatedEvidence{{
				EventID:   eventID,
				ExactText: evidence,
			}},
		}},
		Links: links,
	}
}

func (suite *PaxlSessionSuite) writeGeneratedPlan(
	directory string,
	plan generatedPlan,
) string {
	suite.T().Helper()
	encoded, err := json.Marshal(plan)
	suite.Require().NoError(err)
	path := filepath.Join(directory, "plan.json")
	suite.Require().NoError(os.WriteFile(path, encoded, 0o600))
	return path
}

func (suite *PaxlSessionSuite) writePaxlExport(
	directory string,
	nativeID string,
	title string,
	messages map[int64]string,
) string {
	suite.T().Helper()
	records := []paxlRecord{{
		SchemaVersion: "paxl.session.snapshot.v1",
		SessionID:     "codex:" + nativeID,
		NativeID:      nativeID,
		ProjectID:     "/repo",
		Title:         title,
	}}
	for sequence, content := range messages {
		records = append(records, paxlRecord{
			SchemaVersion: "paxl.session.element.v1",
			Type:          "message",
			Role:          "assistant",
			ContentText:   content,
			SessionID:     "codex:" + nativeID,
			Seq:           sequence,
		})
	}
	var content strings.Builder
	for _, record := range records {
		encoded, err := json.Marshal(record)
		suite.Require().NoError(err)
		content.Write(encoded)
		content.WriteByte('\n')
	}
	path := filepath.Join(directory, nativeID+".jsonl")
	suite.Require().NoError(os.WriteFile(path, []byte(content.String()), 0o600))
	return path
}

func navigationPageCount(topics []pagewiki.NavigationTopic) int {
	count := 0
	for _, topic := range topics {
		count += len(topic.Pages)
		count += navigationPageCount(topic.Children)
	}
	return count
}
