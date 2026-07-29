package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
	pagewikihttp "github.com/pax-beehive/pax-nexus/internal/pagewiki/transport/httpapi"
	pagewikirouter "github.com/pax-beehive/pax-nexus/internal/pagewiki/transport/httpapi/router"
)

const previewAddress = "127.0.0.1:58181"

type linkSpec struct {
	exactText  string
	targetSlug string
}

type pageSpec struct {
	slug         string
	title        string
	topicPath    []string
	summary      string
	heading      string
	markdown     string
	citationText string
	links        []linkSpec
}

func main() {
	ctx := context.Background()
	repository := memory.NewRepository()
	var landingPage string
	var runID string
	switch {
	case len(os.Args) >= 4 && os.Args[1] == "--generated-plan":
		var err error
		landingPage, runID, err = injectGeneratedPlan(
			ctx,
			repository,
			os.Args[2],
			os.Args[3:],
		)
		must(err)
	case len(os.Args) == 2 && os.Args[1] == "--fixture":
		injectPreviewSessions(ctx, repository)
		savePreviewRun(ctx, repository)
		landingPage = "project-xanadu"
		runID = "run_reader_preview"
	default:
		panic(
			"usage: pagewiki-preview --generated-plan PLAN SESSION_JSONL... " +
				"(or --fixture for the synthetic UI fixture)",
		)
	}

	handler, err := pagewikihttp.New(unavailableInjector{}, repository)
	must(err)
	hertz := server.Default(server.WithHostPorts(previewAddress))
	hertz.Use(pagewikihttp.InstanceMiddleware(handler))
	pagewikirouter.Register(hertz)
	fmt.Printf(
		"Page Wiki preview: http://%s/wiki?page=%s&run=%s\n",
		previewAddress,
		landingPage,
		runID,
	)
	hertz.Spin()
}

func injectPreviewSessions(ctx context.Context, repository *memory.Repository) {
	injectCreate(ctx, repository, pageSpec{
		slug:         "sqlite",
		title:        "SQLite",
		topicPath:    []string{"Engineering", "Storage"},
		summary:      "SQLite is the local persistence authority for Page Wiki.",
		heading:      "Storage decision",
		markdown:     "SQLite stores local Page Wiki data with transactional writes.",
		citationText: "transactional writes",
	})
	injectUpdate(ctx, repository, pageSpec{
		slug:         "sqlite",
		title:        "SQLite",
		summary:      "SQLite now protects local concurrency with WAL mode.",
		heading:      "Storage decision",
		markdown:     "SQLite uses WAL mode to keep local Page Wiki writes durable and concurrent.",
		citationText: "WAL mode",
	})
	injectCreate(ctx, repository, pageSpec{
		slug:         "evidence-grounding",
		title:        "Evidence Grounding",
		topicPath:    []string{"Knowledge", "Grounding"},
		summary:      "Every durable claim retains its exact source provenance.",
		heading:      "Grounding rule",
		markdown:     "Evidence Grounding keeps every claim attached to exact source anchors.",
		citationText: "exact source anchors",
	})
	injectCreate(ctx, repository, pageSpec{
		slug:         "page-revisions",
		title:        "Page Revisions",
		topicPath:    []string{"Knowledge", "Versioning"},
		summary:      "Pages advance without rewriting their historical knowledge.",
		heading:      "Immutable history",
		markdown:     "Page Revisions preserve immutable history while SQLite advances the current pointer with compare-and-swap.",
		citationText: "immutable history",
		links: []linkSpec{
			{exactText: "SQLite", targetSlug: "sqlite"},
		},
	})
	injectCreate(ctx, repository, pageSpec{
		slug:         "wiki-search",
		title:        "Wiki Search",
		topicPath:    []string{"Engineering", "Retrieval"},
		summary:      "Search reads current revisions and returns grounded context.",
		heading:      "Retrieval",
		markdown:     "Wiki Search ranks current revision sections in SQLite and returns Evidence Grounding beside every result.",
		citationText: "current revision sections",
		links: []linkSpec{
			{exactText: "SQLite", targetSlug: "sqlite"},
			{exactText: "Evidence Grounding", targetSlug: "evidence-grounding"},
		},
	})
	injectCreate(ctx, repository, pageSpec{
		slug:         "maintenance-runs",
		title:        "Maintenance Runs",
		topicPath:    []string{"Operations", "Maintenance"},
		summary:      "A run exposes success, ambiguity, and failure target by target.",
		heading:      "Partial work",
		markdown:     "Maintenance Runs keep partial work visible and attach successful targets to Page Revisions.",
		citationText: "partial work visible",
		links: []linkSpec{
			{exactText: "Page Revisions", targetSlug: "page-revisions"},
		},
	})
	injectCreate(ctx, repository, pageSpec{
		slug:         "page-wiki-architecture",
		title:        "Page Wiki Architecture",
		topicPath:    []string{"Engineering", "Architecture"},
		summary:      "The system separates durable storage, retrieval, provenance, and history.",
		heading:      "System boundaries",
		markdown:     "Page Wiki Architecture separates SQLite storage, Wiki Search retrieval, Evidence Grounding, and Page Revisions.",
		citationText: "separates",
		links: []linkSpec{
			{exactText: "SQLite", targetSlug: "sqlite"},
			{exactText: "Wiki Search", targetSlug: "wiki-search"},
			{exactText: "Evidence Grounding", targetSlug: "evidence-grounding"},
			{exactText: "Page Revisions", targetSlug: "page-revisions"},
		},
	})
	injectCreate(ctx, repository, pageSpec{
		slug:         "project-xanadu",
		title:        "Project Xanadu",
		topicPath:    []string{"Concepts", "Hypertext"},
		summary:      "Knowledge remains traversable in both directions.",
		heading:      "Bidirectional reading",
		markdown:     "Project Xanadu connects Page Wiki Architecture to Evidence Grounding and Page Revisions through exact phrases.",
		citationText: "exact phrases",
		links: []linkSpec{
			{exactText: "Page Wiki Architecture", targetSlug: "page-wiki-architecture"},
			{exactText: "Evidence Grounding", targetSlug: "evidence-grounding"},
			{exactText: "Page Revisions", targetSlug: "page-revisions"},
		},
	})
	injectUpdate(ctx, repository, pageSpec{
		slug:    "project-xanadu",
		title:   "Project Xanadu",
		summary: "Exact-text links now travel forward and reveal every backlink.",
		heading: "Bidirectional reading",
		markdown: "Project Xanadu renders Page Wiki Architecture links inline, keeps Evidence Grounding beside claims, " +
			"and exposes backlinks across Page Revisions.",
		citationText: "exposes backlinks",
		links: []linkSpec{
			{exactText: "Page Wiki Architecture", targetSlug: "page-wiki-architecture"},
			{exactText: "Evidence Grounding", targetSlug: "evidence-grounding"},
			{exactText: "Page Revisions", targetSlug: "page-revisions"},
		},
	})
	injectCreate(ctx, repository, pageSpec{
		slug:         "session-injection",
		title:        "Session Injection",
		topicPath:    []string{"Operations", "Ingestion"},
		summary:      "Session events become independently reviewable maintenance targets.",
		heading:      "Ingestion flow",
		markdown:     "Session Injection turns source events into Maintenance Runs, validates Evidence Grounding, and publishes through Page Wiki Architecture.",
		citationText: "source events",
		links: []linkSpec{
			{exactText: "Maintenance Runs", targetSlug: "maintenance-runs"},
			{exactText: "Evidence Grounding", targetSlug: "evidence-grounding"},
			{exactText: "Page Wiki Architecture", targetSlug: "page-wiki-architecture"},
		},
	})
}

func injectCreate(
	ctx context.Context,
	repository *memory.Repository,
	spec pageSpec,
) pagewiki.InjectResult {
	return inject(
		ctx,
		repository,
		pagewiki.PageBrief{
			Key:              "create-" + spec.slug,
			Action:           pagewiki.PageActionCreate,
			ProposedSlug:     spec.slug,
			ProposedTitle:    spec.title,
			TopicPath:        spec.topicPath,
			EvidenceEventIDs: []string{"event-" + spec.slug},
		},
		draftForSpec(ctx, repository, spec),
		sourceRequest(spec, "create-"+spec.slug),
	)
}

func injectUpdate(
	ctx context.Context,
	repository *memory.Repository,
	spec pageSpec,
) pagewiki.InjectResult {
	page, err := repository.PageBySlug(ctx, spec.slug)
	must(err)
	return inject(
		ctx,
		repository,
		pagewiki.PageBrief{
			Key:                    "update-" + spec.slug,
			Action:                 pagewiki.PageActionUpdate,
			TargetPageID:           page.ID,
			ExpectedBaseRevisionID: page.CurrentRevisionID,
			EvidenceEventIDs:       []string{"event-" + spec.slug},
		},
		draftForSpec(ctx, repository, spec),
		sourceRequest(spec, "update-"+spec.slug),
	)
}

func draftForSpec(
	ctx context.Context,
	repository *memory.Repository,
	spec pageSpec,
) pagewiki.PageDraft {
	links := make([]pagewiki.LinkDraft, 0, len(spec.links))
	for _, link := range spec.links {
		target, err := repository.PageBySlug(ctx, link.targetSlug)
		must(err)
		links = append(links, pagewiki.LinkDraft{
			SectionKey:   "knowledge",
			ExactText:    link.exactText,
			TargetPageID: target.ID,
		})
	}
	return pagewiki.PageDraft{
		Slug:    spec.slug,
		Title:   spec.title,
		Summary: spec.summary,
		Sections: []pagewiki.SectionDraft{{
			Key:      "knowledge",
			Heading:  spec.heading,
			Markdown: spec.markdown,
		}},
		Citations: []pagewiki.CitationDraft{{
			SectionKey: "knowledge",
			ExactText:  spec.citationText,
			Evidence: []pagewiki.EvidenceQuoteDraft{{
				EventID:   "event-" + spec.slug,
				ExactText: spec.markdown,
			}},
		}},
		Links: links,
	}
}

func inject(
	ctx context.Context,
	repository *memory.Repository,
	brief pagewiki.PageBrief,
	draft pagewiki.PageDraft,
	request pagewiki.InjectSessionRequest,
) pagewiki.InjectResult {
	service := pagewiki.NewService(
		repository,
		pagewiki.ScriptedPlanner{Briefs: []pagewiki.PageBrief{brief}},
		pagewiki.ScriptedEditor{Drafts: map[string]pagewiki.PageDraft{
			brief.Key: draft,
		}},
	)
	result, err := service.InjectSession(ctx, request)
	must(err)
	if result.Run.Status != pagewiki.RunStatusSucceeded {
		panic(fmt.Sprintf("preview injection failed: %+v", result.Run))
	}
	return result
}

func sourceRequest(
	spec pageSpec,
	idempotencyKey string,
) pagewiki.InjectSessionRequest {
	raw := "preview: " + spec.markdown
	start := strings.Index(raw, spec.markdown)
	return pagewiki.InjectSessionRequest{
		SourceID:       "session-" + idempotencyKey,
		IdempotencyKey: idempotencyKey,
		Raw:            []byte(raw),
		Events: []pagewiki.SourceEventInput{{
			ID:        "event-" + spec.slug,
			StartByte: start,
			EndByte:   start + len(spec.markdown),
		}},
	}
}

func savePreviewRun(ctx context.Context, repository *memory.Repository) {
	architecture := mustPage(ctx, repository, "page-wiki-architecture")
	injection := mustPage(ctx, repository, "session-injection")
	must(repository.SaveMaintenanceRun(ctx, pagewiki.MaintenanceRun{
		ID:               "run_reader_preview",
		SourceRevisionID: "source-revision-preview",
		Status:           pagewiki.RunStatusPartialSuccess,
		Targets: []pagewiki.MaintenanceTarget{
			{
				ID:             "target-architecture",
				BriefKey:       "architecture",
				Action:         pagewiki.PageActionCreate,
				PageID:         architecture.ID,
				PageRevisionID: architecture.CurrentRevisionID,
				Status:         pagewiki.TargetStatusSucceeded,
			},
			{
				ID:       "target-needs-review",
				BriefKey: "needs-review",
				Action:   pagewiki.PageActionAmbiguous,
				Status:   pagewiki.TargetStatusPending,
				Error:    "two existing pages are plausible targets",
			},
			{
				ID:            "target-broken-citation",
				BriefKey:      "broken-citation",
				Action:        pagewiki.PageActionCreate,
				Status:        pagewiki.TargetStatusFailed,
				FailureReason: pagewiki.TargetFailureInvalidCitation,
				Error:         "citation exact text is not grounded",
			},
			{
				ID:             "target-session-injection",
				BriefKey:       "session-injection",
				Action:         pagewiki.PageActionCreate,
				PageID:         injection.ID,
				PageRevisionID: injection.CurrentRevisionID,
				Status:         pagewiki.TargetStatusSucceeded,
			},
		},
	}))
}

func mustPage(
	ctx context.Context,
	repository *memory.Repository,
	slug string,
) pagewiki.Page {
	page, err := repository.PageBySlug(ctx, slug)
	must(err)
	return page
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

type unavailableInjector struct{}

func (unavailableInjector) InjectSession(
	context.Context,
	pagewiki.InjectSessionRequest,
) (pagewiki.InjectResult, error) {
	return pagewiki.InjectResult{}, fmt.Errorf(
		"%w: preview is read-only",
		pagewiki.ErrUnavailable,
	)
}
