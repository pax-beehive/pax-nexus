package pagewiki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

const (
	minPlannedPages = 1
	maxPlannedPages = 8
	editConcurrency = 4
)

var (
	slugPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	sectionKeyPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

type Service struct {
	repository Repository
	planner    Planner
	editor     Editor
	logger     *slog.Logger

	// Topic-tree maintenance: treeTasks is the keyed work queue (treeTaskKeys
	// deduplicates what is already pending, under treeTaskMu), and
	// treeReindexMu serializes the tree writes themselves, so the background
	// worker and a synchronous FlushTreeReindex never interleave.
	treeNavigator TreeNavigator
	treeMaxDepth  int
	treeTasks     chan treeTask
	treeTaskKeys  map[string]struct{}
	treeTaskMu    sync.Mutex
	treeReindexMu sync.Mutex

	curator          Curator
	curationEmbedder TextEmbedder
	curationConfig   CurationConfig
	curationMu       sync.Mutex
}

// CurationConfig controls the Page Wiki curation round: candidate limits and
// (in a later task) the background loop interval.
type CurationConfig struct {
	Interval  time.Duration // 0 disables the background loop (Task 8)
	PairLimit int           // 0 → curationDefaultPairLimit
	PageLimit int           // 0 → curationDefaultPageLimit
}

type ServiceOption func(*Service)

// WithTreeNavigator enables incremental Page Wiki topic-tree maintenance.
// When set, InjectSession queues each published page for placement and
// curation queues whatever it left unplaced; the navigator is then asked,
// one level at a time, where each page belongs — never for the whole tree at
// once. The work runs off the ingest path, either in the background via
// StartTreeMaintenance or synchronously via FlushTreeReindex. It remains
// optional: without it, tasks are never processed and the tree stays put.
func WithTreeNavigator(config TreeMaintenanceConfig) ServiceOption {
	return func(s *Service) {
		s.treeNavigator = config.Navigator
		s.treeMaxDepth = config.MaxDepth
		if s.treeMaxDepth <= 0 {
			s.treeMaxDepth = DefaultTopicTreeMaxDepth
		}
		if config.Logger != nil {
			s.logger = config.Logger
		}
	}
}

// WithCurator enables Page Wiki curation rounds: RunCurationRound judges
// duplicate-page and low-quality-page candidates and executes the verdicts
// it accepts. PairLimit/PageLimit zero-values fall back to
// curationDefaultPairLimit/curationDefaultPageLimit; Interval is stored for
// the background loop a later task adds and otherwise ignored here.
func WithCurator(
	curator Curator,
	embedder TextEmbedder,
	config CurationConfig,
	logger *slog.Logger,
) ServiceOption {
	return func(s *Service) {
		s.curator = curator
		s.curationEmbedder = embedder
		if config.PairLimit <= 0 {
			config.PairLimit = curationDefaultPairLimit
		}
		if config.PageLimit <= 0 {
			config.PageLimit = curationDefaultPageLimit
		}
		s.curationConfig = config
		if logger != nil {
			s.logger = logger
		}
	}
}

func NewService(
	repository Repository,
	planner Planner,
	editor Editor,
	options ...ServiceOption,
) *Service {
	service := &Service{
		repository: repository,
		planner:    planner,
		editor:     editor,
		logger:     observability.DiscardLogger(),
	}
	service.treeTasks = make(chan treeTask, treeTaskQueueSize)
	service.treeTaskKeys = make(map[string]struct{})
	for _, option := range options {
		option(service)
	}
	return service
}

// InjectSession turns one session's events into published Wiki pages:
// plan briefs, edit drafts concurrently, then commit targets in brief order.
//
// Retry architecture — three layers, each owning one failure class; a new
// retry belongs in exactly one of them:
//
//  1. LLM call level (llm.CompleteJSON/CompleteJSONAs, 2 attempts per call
//     site): absorbs transient provider noise — transport errors, truncated
//     or non-JSON replies, drafts rejected by validation. Cheap, and keeps
//     one flaky completion from failing a whole target.
//  2. Run level (succeededRun below): a failed or partial_success
//     MaintenanceRun re-runs in full under the same deterministic run ID,
//     overwriting the stored run — repositories keep only succeeded runs
//     immutable. resolvePage's publishModeSelfHeal makes the re-run converge
//     on pages an earlier attempt already created.
//  3. Session level (sessionconsumer.Controller): retries non-succeeded
//     injections on exponential backoff (in process memory, reset on
//     restart) and advances the stream cursor only after a succeeded run.
func (s *Service) InjectSession(
	ctx context.Context,
	request InjectSessionRequest,
) (InjectResult, error) {
	sourceRevision, err := buildSourceRevision(request)
	if err != nil {
		return InjectResult{}, err
	}
	if err := ValidateInjectSessionRequest(request); err != nil {
		return InjectResult{}, err
	}
	if err := s.repository.SaveSourceRevision(ctx, sourceRevision); err != nil {
		return InjectResult{}, fmt.Errorf("save SourceRevision: %w", err)
	}
	runID := stableID(
		"run",
		sourceRevision.ID,
		strings.TrimSpace(request.IdempotencyKey),
	)
	existingRun, done, err := s.succeededRun(ctx, runID)
	if err != nil {
		return InjectResult{}, err
	}
	if done {
		return InjectResult{
			SourceRevisionID: sourceRevision.ID,
			Run:              existingRun,
		}, nil
	}
	injection, err := s.loadInjectionContext(ctx)
	if err != nil {
		return InjectResult{}, err
	}
	catalog := injection.catalog
	directives := injection.directives
	briefs, err := s.planner.Plan(ctx, PlanInput{
		SourceRevision: sourceRevision,
		PageCatalog:    catalog,
		Directives:     directives,
		Types:          injection.types,
	})
	if err != nil {
		return InjectResult{}, fmt.Errorf("plan pages: %w", err)
	}
	if len(briefs) < minPlannedPages || len(briefs) > maxPlannedPages {
		return InjectResult{}, fmt.Errorf(
			"%w: planner returned %d briefs, want 1-8",
			ErrInvalidPageBrief,
			len(briefs),
		)
	}

	run := MaintenanceRun{
		ID:               runID,
		SourceRevisionID: sourceRevision.ID,
		Targets:          make([]MaintenanceTarget, 0, len(briefs)),
	}
	prepared := s.prepareTargets(ctx, run.ID, sourceRevision, catalog, briefs, directives)
	linkable := s.linkableCreateIDs(ctx, sourceRevision, prepared)
	var deferred []PagePublication
	var deferredSlots []int
	for index := range prepared {
		target, publication := s.commitTarget(ctx, sourceRevision, prepared[index], linkable)
		if publication != nil {
			deferred = append(deferred, *publication)
			deferredSlots = append(deferredSlots, len(run.Targets))
		}
		run.Targets = append(run.Targets, target)
	}
	// Publications that reference same-run Pages commit as one batch: the
	// repository validates link targets against existing Pages plus the batch,
	// so mutually linked Pages can land together.
	if len(deferred) > 0 {
		if err := s.repository.PublishPages(ctx, deferred); err != nil {
			for _, slot := range deferredSlots {
				run.Targets[slot] = failTarget(run.Targets[slot], TargetFailurePublicationConflict, err)
			}
		}
	}
	run.Status = summarizeRun(run.Targets)
	if err := s.repository.SaveMaintenanceRun(ctx, run); err != nil {
		return InjectResult{}, fmt.Errorf("save MaintenanceRun: %w", err)
	}
	if s.treeNavigator != nil {
		for _, target := range run.Targets {
			if target.Status == TargetStatusSucceeded && target.PageID != "" {
				s.enqueueTreeTask(treeTask{kind: treeTaskInsert, id: target.PageID})
			}
		}
	}
	return InjectResult{
		SourceRevisionID: sourceRevision.ID,
		Run:              run,
	}, nil
}

// succeededRun loads runID and reports whether it already completed
// successfully — only that satisfies the idempotency check. Failed and
// partial_success runs report false so the injection runs again: the
// consumer retries on non-succeeded statuses, and returning the stored run
// would pin the failure forever without ever re-running the injection.
func (s *Service) succeededRun(
	ctx context.Context,
	runID string,
) (MaintenanceRun, bool, error) {
	run, err := s.repository.MaintenanceRun(ctx, runID)
	switch {
	case errors.Is(err, ErrNotFound):
		return MaintenanceRun{}, false, nil
	case err != nil:
		return MaintenanceRun{}, false, fmt.Errorf("load MaintenanceRun: %w", err)
	}
	return run, run.Status == RunStatusSucceeded, nil
}

// injectionContext bundles the read-only state InjectSession loads before
// planning: the Page catalog, generation directives, and the type registry
// that normalizes the planner's entity/relation values.
type injectionContext struct {
	catalog    PageCatalog
	directives GenerationDirectives
	types      TypeRegistry
}

// loadInjectionContext loads catalog, directives, and the type registry as
// one unit, keeping InjectSession's own branching low enough to stay under
// the cyclop limit.
func (s *Service) loadInjectionContext(ctx context.Context) (injectionContext, error) {
	catalog, err := s.repository.PageCatalog(ctx)
	if err != nil {
		return injectionContext{}, fmt.Errorf("load Page catalog: %w", err)
	}
	directives, err := s.repository.GenerationSettings(ctx)
	if err != nil {
		return injectionContext{}, fmt.Errorf("load generation settings: %w", err)
	}
	entries, err := s.repository.TypeRegistry(ctx)
	if err != nil {
		return injectionContext{}, fmt.Errorf("load type registry: %w", err)
	}
	return injectionContext{
		catalog:    catalog,
		directives: directives,
		types:      NewTypeRegistry(entries),
	}, nil
}

// GenerationSettings returns the team's stored Page Wiki generation
// directives.
func (s *Service) GenerationSettings(ctx context.Context) (GenerationDirectives, error) {
	directives, err := s.repository.GenerationSettings(ctx)
	if err != nil {
		return GenerationDirectives{}, fmt.Errorf("load generation settings: %w", err)
	}
	return directives, nil
}

// SetGenerationSettings validates and persists the team's Page Wiki
// generation directives, returning the stored (trimmed) value.
func (s *Service) SetGenerationSettings(
	ctx context.Context,
	directives GenerationDirectives,
) (GenerationDirectives, error) {
	valid, err := ValidateGenerationDirectives(directives)
	if err != nil {
		return GenerationDirectives{}, err
	}
	if err := s.repository.SetGenerationSettings(ctx, valid); err != nil {
		return GenerationDirectives{}, fmt.Errorf("save generation settings: %w", err)
	}
	return valid, nil
}

// preparedTarget carries one brief's prepare-phase outcome into the serial
// commit phase. ready is true only when validation, page resolution, and the
// edit all succeeded; otherwise target already holds the final failed or
// terminal (source-only / ambiguous) state.
type preparedTarget struct {
	target          MaintenanceTarget
	brief           PageBrief
	page            *Page
	currentRevision *PageRevision
	draft           PageDraft
	ready           bool
	mode            publishMode
}

// publishMode names the way a target's publication lands on the repository.
// resolvePage picks it; commitTarget acts on it.
type publishMode int

const (
	// publishModeCreate mints a fresh Page.
	publishModeCreate publishMode = iota
	// publishModeUpdate advances the existing Page the planner targeted,
	// gated on its expected base revision.
	publishModeUpdate
	// publishModeRevive republishes a retired Page whose slug a create
	// brief collided with; the publication must carry Revive so the Page
	// flips back to active, even when the content is unchanged.
	publishModeRevive
	// publishModeSelfHeal updates the active Page that a prior attempt of
	// this same run created (same source revision and brief key mint the
	// same Page ID), so a retried injection converges in place instead of
	// failing on a publication conflict.
	publishModeSelfHeal
)

// duplicateUpdateTargets flags every update brief whose non-empty
// TargetPageID already appeared on an earlier brief. The first brief keeps
// the page; later ones would only burn an editor call before failing the
// base-revision check at publish, so they fail up front with the same
// conflict outcome. Create briefs carry no TargetPageID and are never
// flagged.
func duplicateUpdateTargets(briefs []PageBrief) map[int]struct{} {
	duplicates := make(map[int]struct{})
	seen := make(map[string]struct{}, len(briefs))
	for index, brief := range briefs {
		if brief.Action != PageActionUpdate || brief.TargetPageID == "" {
			continue
		}
		if _, found := seen[brief.TargetPageID]; found {
			duplicates[index] = struct{}{}
			continue
		}
		seen[brief.TargetPageID] = struct{}{}
	}
	return duplicates
}

// prepareTargets runs the expensive per-brief work (validation, page
// resolution, and the editor LLM call) concurrently. Each brief writes its
// outcome into its own slot so one target's failure never affects siblings.
// Publication stays out of this phase: commitTarget runs serially in brief
// order so publish semantics are identical to the previous sequential loop.
func (s *Service) prepareTargets(
	ctx context.Context,
	runID string,
	sourceRevision SourceRevision,
	catalog PageCatalog,
	briefs []PageBrief,
	directives GenerationDirectives,
) []preparedTarget {
	prepared := make([]preparedTarget, len(briefs))
	var wait sync.WaitGroup
	slots := make(chan struct{}, editConcurrency)
	duplicates := duplicateUpdateTargets(briefs)
	for index, brief := range briefs {
		if _, duplicate := duplicates[index]; duplicate {
			prepared[index] = preparedTarget{
				brief: brief,
				target: failTarget(MaintenanceTarget{
					ID:       stableID("target", runID, brief.Key),
					BriefKey: brief.Key,
					Action:   brief.Action,
					Status:   TargetStatusFailed,
				}, TargetFailurePublicationConflict, fmt.Errorf(
					"%w: Page %q is already targeted by an earlier brief in this run",
					ErrRevisionConflict,
					brief.TargetPageID,
				)),
			}
			continue
		}
		wait.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			prepared[index] = s.prepareTarget(ctx, runID, sourceRevision, catalog, brief, directives)
		})
	}
	wait.Wait()
	return prepared
}

func (s *Service) prepareTarget(
	ctx context.Context,
	runID string,
	sourceRevision SourceRevision,
	catalog PageCatalog,
	brief PageBrief,
	directives GenerationDirectives,
) preparedTarget {
	result := preparedTarget{
		brief: brief,
		target: MaintenanceTarget{
			ID:       stableID("target", runID, brief.Key),
			BriefKey: brief.Key,
			Action:   brief.Action,
			Status:   TargetStatusFailed,
		},
	}
	if err := ValidatePageBrief(brief, catalog); err != nil {
		result.target = failTarget(result.target, TargetFailureInvalidBrief, err)
		return result
	}
	if err := validateBriefEvidence(brief, sourceRevision); err != nil {
		result.target = failTarget(result.target, TargetFailureInvalidBrief, err)
		return result
	}
	if brief.Action == PageActionSourceOnly || brief.Action == PageActionAmbiguous {
		if brief.Action == PageActionSourceOnly {
			result.target.Status = TargetStatusSucceeded
		} else {
			result.target.Status = TargetStatusPending
		}
		return result
	}
	page, currentRevision, mode, err := s.resolvePage(ctx, sourceRevision.ID, brief)
	if err != nil {
		result.target = failTarget(result.target, TargetFailurePublicationConflict, err)
		return result
	}
	draft, err := s.editor.Edit(ctx, EditInput{
		SourceRevision:  sourceRevision,
		Brief:           brief,
		CurrentPage:     page,
		CurrentRevision: currentRevision,
		Directives:      directives,
	})
	if err != nil {
		result.target = failTarget(result.target, TargetFailureInvalidDraft, err)
		return result
	}
	result.page = page
	result.currentRevision = currentRevision
	result.draft = draft
	result.mode = mode
	result.ready = true
	return result
}

// linkableCreateIDs computes the create-action page IDs that may serve as
// same-run Xanadu link targets: the fixed point of "the target's publication
// validates with these pending IDs allowed". Validating to a fixed point
// keeps a failed sibling from leaving a dangling link behind — every brief
// that links to a removed page fails with invalid_link at commit instead.
func (s *Service) linkableCreateIDs(
	ctx context.Context,
	sourceRevision SourceRevision,
	prepared []preparedTarget,
) map[string]struct{} {
	linkable := make(map[string]struct{})
	for index := range prepared {
		target := &prepared[index]
		if target.ready && target.brief.Action == PageActionCreate && target.page != nil {
			linkable[target.page.ID] = struct{}{}
		}
	}
	for changed := true; changed && len(linkable) > 0; {
		changed = false
		for index := range prepared {
			target := &prepared[index]
			if !target.ready || target.brief.Action != PageActionCreate || target.page == nil {
				continue
			}
			if _, ok := linkable[target.page.ID]; !ok {
				continue
			}
			_, _, _, err := s.buildPublication(
				ctx, sourceRevision, target.brief, target.page,
				target.currentRevision, target.draft, linkable,
			)
			if err != nil {
				delete(linkable, target.page.ID)
				changed = true
			}
		}
	}
	return linkable
}

// commitTarget validates and publishes one prepared target. When the new
// revision references same-run Pages (ids in linkable), the publication is
// returned for the caller's end-of-loop batch commit instead of being
// published immediately; the returned target is then optimistic and must be
// failed if the batch publish fails.
func (s *Service) commitTarget(
	ctx context.Context,
	sourceRevision SourceRevision,
	prepared preparedTarget,
	linkable map[string]struct{},
) (MaintenanceTarget, *PagePublication) {
	if !prepared.ready {
		return prepared.target, nil
	}
	target := prepared.target
	pageValue, revision, reason, err := s.buildPublication(
		ctx,
		sourceRevision,
		prepared.brief,
		prepared.page,
		prepared.currentRevision,
		prepared.draft,
		linkable,
	)
	if err != nil {
		return failTarget(target, reason, err), nil
	}
	// A revive must always land its publication, even when the new draft's
	// content is byte-for-byte equivalent to the retired Page's current
	// revision: skipping the publish here (as a plain no-op update would)
	// would leave the Page retired, since the status flip only happens as
	// part of applying a publication.
	if prepared.mode != publishModeRevive &&
		prepared.currentRevision != nil &&
		prepared.page.Slug == pageValue.Slug &&
		prepared.page.Title == pageValue.Title &&
		revisionsEquivalent(*prepared.currentRevision, revision) {
		target.PageID = prepared.page.ID
		target.PageRevisionID = prepared.currentRevision.ID
		target.Status = TargetStatusSucceeded
		return target, nil
	}
	publication := PagePublication{
		Page:     pageValue,
		Revision: revision,
		Revive:   prepared.mode == publishModeRevive,
	}
	target.PageID = pageValue.ID
	target.PageRevisionID = revision.ID
	target.Status = TargetStatusSucceeded
	if referencesPendingPage(revision.Links, linkable) {
		return target, &publication
	}
	if err := s.repository.PublishPage(ctx, publication); err != nil {
		return failTarget(target, TargetFailurePublicationConflict, err), nil
	}
	return target, nil
}

func referencesPendingPage(links []PageLink, linkable map[string]struct{}) bool {
	for _, link := range links {
		if _, pending := linkable[link.TargetPageID]; pending {
			return true
		}
	}
	return false
}

func revisionsEquivalent(left, right PageRevision) bool {
	if left.Title != right.Title ||
		left.Summary != right.Summary ||
		left.Markdown != right.Markdown ||
		!reflect.DeepEqual(left.Sections, right.Sections) ||
		len(left.Citations) != len(right.Citations) ||
		len(left.Links) != len(right.Links) {
		return false
	}
	for index := range left.Citations {
		leftCitation := left.Citations[index]
		rightCitation := right.Citations[index]
		if leftCitation.SectionKey != rightCitation.SectionKey ||
			leftCitation.StartByte != rightCitation.StartByte ||
			leftCitation.EndByte != rightCitation.EndByte ||
			leftCitation.ExactText != rightCitation.ExactText ||
			!reflect.DeepEqual(leftCitation.SourceAnchors, rightCitation.SourceAnchors) {
			return false
		}
	}
	for index := range left.Links {
		leftLink := left.Links[index]
		rightLink := right.Links[index]
		if leftLink.SectionKey != rightLink.SectionKey ||
			leftLink.StartByte != rightLink.StartByte ||
			leftLink.EndByte != rightLink.EndByte ||
			leftLink.ExactText != rightLink.ExactText ||
			leftLink.TargetPageID != rightLink.TargetPageID {
			return false
		}
	}
	return true
}

// resolvePage loads the Page (and, for updates, its current revision) that a
// brief targets and names how its publication must land (publishMode). A
// create brief usually mints a fresh Page, with two exceptions: a slug
// collision with a RETIRED Page becomes a revive, and a collision with the
// active Page this same run's earlier attempt created becomes a self-heal
// update. A collision with an unrelated ACTIVE Page still mints a fresh
// Page — that conflict surfaces as a publication failure at commit.
func (s *Service) resolvePage(
	ctx context.Context,
	sourceRevisionID string,
	brief PageBrief,
) (*Page, *PageRevision, publishMode, error) {
	if brief.Action == PageActionCreate {
		existing, err := s.repository.PageBySlug(ctx, brief.ProposedSlug)
		switch {
		case err == nil:
			if existing.Retired() {
				revision, revErr := s.repository.PageRevision(ctx, existing.CurrentRevisionID)
				if revErr != nil {
					return nil, nil, publishModeCreate, fmt.Errorf(
						"load retired Page current PageRevision: %w",
						revErr,
					)
				}
				return &existing, &revision, publishModeRevive, nil
			}
			if existing.ID == stableID("page", sourceRevisionID, brief.Key) {
				revision, revErr := s.repository.PageRevision(ctx, existing.CurrentRevisionID)
				if revErr != nil {
					return nil, nil, publishModeCreate, fmt.Errorf(
						"load retried Page current PageRevision: %w",
						revErr,
					)
				}
				return &existing, &revision, publishModeSelfHeal, nil
			}
			// Slug is taken by an unrelated active Page: keep today's
			// behavior and mint a fresh Page below; the slug conflict
			// surfaces as a publication failure at commit, same as before.
		case !errors.Is(err, ErrNotFound):
			return nil, nil, publishModeCreate, fmt.Errorf("load Page by slug: %w", err)
		}
		page := Page{
			ID:    stableID("page", sourceRevisionID, brief.Key),
			Slug:  brief.ProposedSlug,
			Title: brief.ProposedTitle,
		}
		return &page, nil, publishModeCreate, nil
	}
	page, err := s.repository.PageByID(ctx, brief.TargetPageID)
	if err != nil {
		return nil, nil, publishModeUpdate, fmt.Errorf("load update Page: %w", err)
	}
	if page.CurrentRevisionID != brief.ExpectedBaseRevisionID {
		return nil, nil, publishModeUpdate, fmt.Errorf(
			"%w: Page %q changed after planning",
			ErrRevisionConflict,
			page.ID,
		)
	}
	revision, err := s.repository.PageRevision(ctx, page.CurrentRevisionID)
	if err != nil {
		return nil, nil, publishModeUpdate, fmt.Errorf("load current PageRevision: %w", err)
	}
	return &page, &revision, publishModeUpdate, nil
}

func (s *Service) buildPublication(
	ctx context.Context,
	sourceRevision SourceRevision,
	brief PageBrief,
	page *Page,
	currentRevision *PageRevision,
	draft PageDraft,
	linkable map[string]struct{},
) (Page, PageRevision, TargetFailureReason, error) {
	sections, sectionByKey, err := validateDraft(brief, draft)
	if err != nil {
		return Page{}, PageRevision{}, TargetFailureInvalidDraft, err
	}
	baseRevisionID := ""
	if currentRevision != nil {
		baseRevisionID = currentRevision.ID
	}
	revisionID, err := revisionIdentity(page.ID, baseRevisionID, draft)
	if err != nil {
		return Page{}, PageRevision{}, TargetFailureInvalidDraft, err
	}
	citations, err := buildCitations(
		sourceRevision,
		brief,
		revisionID,
		sectionByKey,
		draft.Citations,
	)
	if err != nil {
		return Page{}, PageRevision{}, TargetFailureInvalidCitation, err
	}
	links, err := s.buildLinks(ctx, revisionID, sectionByKey, draft.Links, linkable)
	if err != nil {
		return Page{}, PageRevision{}, TargetFailureInvalidLink, err
	}
	revision := PageRevision{
		ID:             revisionID,
		PageID:         page.ID,
		BaseRevisionID: baseRevisionID,
		Title:          draft.Title,
		Summary:        draft.Summary,
		Sections:       sections,
		Markdown:       renderMarkdown(draft),
		Citations:      citations,
		Links:          links,
	}
	pageValue := *page
	pageValue.Slug = draft.Slug
	pageValue.Title = draft.Title
	pageValue.CurrentRevisionID = revision.ID
	pageValue.EntityType = brief.EntityType
	// An update never downgrades an established type to the untyped fallback:
	// keep the Page's existing EntityType when the brief is untyped or only
	// resolved to the fallback itself.
	if (brief.EntityType == "" || brief.EntityType == EntityTypeConcept) && page.EntityType != "" {
		pageValue.EntityType = page.EntityType
	}
	if pageValue.EntityType == "" {
		pageValue.EntityType = EntityTypeConcept
	}
	return pageValue, revision, TargetFailureNone, nil
}

func buildSourceRevision(request InjectSessionRequest) (SourceRevision, error) {
	if strings.TrimSpace(request.SourceID) == "" || len(request.Raw) == 0 {
		return SourceRevision{}, fmt.Errorf("%w: Source ID and raw bytes are required", ErrInvalidSource)
	}
	if len(request.Events) == 0 {
		return SourceRevision{}, fmt.Errorf("%w: at least one event is required", ErrInvalidSource)
	}
	events := make([]SourceEvent, 0, len(request.Events))
	seen := make(map[string]struct{}, len(request.Events))
	for _, input := range request.Events {
		if strings.TrimSpace(input.ID) == "" {
			return SourceRevision{}, fmt.Errorf("%w: event ID is required", ErrInvalidSource)
		}
		if _, exists := seen[input.ID]; exists {
			return SourceRevision{}, fmt.Errorf("%w: duplicate event ID %q", ErrInvalidSource, input.ID)
		}
		if input.StartByte < 0 || input.EndByte <= input.StartByte ||
			input.EndByte > len(request.Raw) {
			return SourceRevision{}, fmt.Errorf("%w: invalid range for event %q", ErrInvalidSource, input.ID)
		}
		seen[input.ID] = struct{}{}
		events = append(events, SourceEvent(input))
	}
	sum := sha256.Sum256(request.Raw)
	sha := hex.EncodeToString(sum[:])
	return SourceRevision{
		ID:       stableID("source-revision", request.SourceID, sha),
		SourceID: request.SourceID,
		SHA256:   sha,
		Raw:      append([]byte(nil), request.Raw...),
		Events:   events,
	}, nil
}

func validateBriefEvidence(brief PageBrief, revision SourceRevision) error {
	events := eventIndex(revision.Events)
	for _, eventID := range brief.EvidenceEventIDs {
		if _, found := events[eventID]; !found {
			return fmt.Errorf("%w: planner used unknown event %q", ErrInvalidPageBrief, eventID)
		}
	}
	return nil
}

func validateDraft(
	brief PageBrief,
	draft PageDraft,
) ([]PageSection, map[string]SectionDraft, error) {
	if !slugPattern.MatchString(draft.Slug) || strings.TrimSpace(draft.Title) == "" ||
		strings.TrimSpace(draft.Summary) == "" {
		return nil, nil, fmt.Errorf("%w: slug, title, and summary are required", ErrInvalidDraft)
	}
	if brief.Action == PageActionCreate && draft.Slug != brief.ProposedSlug {
		return nil, nil, fmt.Errorf("%w: draft slug differs from reserved slug", ErrInvalidDraft)
	}
	if len(draft.Sections) == 0 {
		return nil, nil, fmt.Errorf("%w: at least one section is required", ErrInvalidDraft)
	}
	if len(draft.Citations) == 0 {
		return nil, nil, fmt.Errorf("%w: at least one citation is required", ErrInvalidDraft)
	}
	sections := make([]PageSection, 0, len(draft.Sections))
	byKey := make(map[string]SectionDraft, len(draft.Sections))
	for _, section := range draft.Sections {
		if !sectionKeyPattern.MatchString(section.Key) ||
			strings.TrimSpace(section.Heading) == "" ||
			strings.TrimSpace(section.Markdown) == "" {
			return nil, nil, fmt.Errorf("%w: invalid Section", ErrInvalidDraft)
		}
		if _, exists := byKey[section.Key]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate Section key %q", ErrInvalidDraft, section.Key)
		}
		byKey[section.Key] = section
		sections = append(sections, PageSection(section))
	}
	return sections, byKey, nil
}

func buildCitations(
	sourceRevision SourceRevision,
	brief PageBrief,
	revisionID string,
	sections map[string]SectionDraft,
	drafts []CitationDraft,
) ([]PageCitation, error) {
	events := eventIndex(sourceRevision.Events)
	allowedEvents := stringSet(brief.EvidenceEventIDs)
	citations := make([]PageCitation, 0, len(drafts))
	for index, draft := range drafts {
		section, found := sections[draft.SectionKey]
		if !found {
			return nil, fmt.Errorf("%w: unknown Section key %q", ErrInvalidCitation, draft.SectionKey)
		}
		start, end, err := uniqueTextRange(section.Markdown, draft.ExactText)
		if err != nil {
			return nil, fmt.Errorf("%w: page text: %w", ErrInvalidCitation, err)
		}
		if len(draft.Evidence) == 0 {
			return nil, fmt.Errorf("%w: evidence is required", ErrInvalidCitation)
		}
		anchors := make([]SourceAnchor, 0, len(draft.Evidence))
		for _, evidence := range draft.Evidence {
			if _, allowed := allowedEvents[evidence.EventID]; !allowed {
				return nil, fmt.Errorf(
					"%w: event %q is outside brief evidence",
					ErrInvalidCitation,
					evidence.EventID,
				)
			}
			event, found := events[evidence.EventID]
			if !found {
				return nil, fmt.Errorf("%w: unknown event %q", ErrInvalidCitation, evidence.EventID)
			}
			eventText := string(sourceRevision.Raw[event.StartByte:event.EndByte])
			localStart, localEnd, rangeErr := uniqueTextRange(eventText, evidence.ExactText)
			if rangeErr != nil {
				return nil, fmt.Errorf(
					"%w: Source quote: %w",
					ErrInvalidCitation,
					rangeErr,
				)
			}
			absoluteStart := event.StartByte + localStart
			absoluteEnd := event.StartByte + localEnd
			anchors = append(anchors, SourceAnchor{
				ID: stableID(
					"source-anchor",
					sourceRevision.ID,
					event.ID,
					fmt.Sprint(absoluteStart),
					fmt.Sprint(absoluteEnd),
				),
				SourceRevisionID: sourceRevision.ID,
				EventID:          event.ID,
				StartByte:        absoluteStart,
				EndByte:          absoluteEnd,
				ExactQuote:       evidence.ExactText,
			})
		}
		citations = append(citations, PageCitation{
			ID:             stableID("citation", revisionID, fmt.Sprint(index)),
			PageRevisionID: revisionID,
			SectionKey:     draft.SectionKey,
			StartByte:      start,
			EndByte:        end,
			ExactText:      draft.ExactText,
			SourceAnchors:  anchors,
		})
	}
	return citations, nil
}

func (s *Service) buildLinks(
	ctx context.Context,
	revisionID string,
	sections map[string]SectionDraft,
	drafts []LinkDraft,
	linkable map[string]struct{},
) ([]PageLink, error) {
	links := make([]PageLink, 0, len(drafts))
	for index, draft := range drafts {
		section, found := sections[draft.SectionKey]
		if !found {
			return nil, fmt.Errorf("%w: unknown Section key %q", ErrInvalidLink, draft.SectionKey)
		}
		start, end, err := uniqueTextRange(section.Markdown, draft.ExactText)
		if err != nil {
			return nil, fmt.Errorf("%w: page text: %w", ErrInvalidLink, err)
		}
		// A link target is either an already-published Page or a same-run
		// create whose publication was validated by linkableCreateIDs; anything
		// else is rejected so a published revision never dangles a link.
		if _, pending := linkable[draft.TargetPageID]; !pending {
			if _, err := s.repository.PageByID(ctx, draft.TargetPageID); err != nil {
				return nil, fmt.Errorf(
					"%w: target Page %q: %w",
					ErrInvalidLink,
					draft.TargetPageID,
					err,
				)
			}
		}
		links = append(links, PageLink{
			ID:             stableID("link", revisionID, fmt.Sprint(index)),
			PageRevisionID: revisionID,
			SectionKey:     draft.SectionKey,
			StartByte:      start,
			EndByte:        end,
			ExactText:      draft.ExactText,
			TargetPageID:   draft.TargetPageID,
			RelationType:   relationOrFallback(draft.RelationType),
		})
	}
	return links, nil
}

func uniqueTextRange(content, exactText string) (int, int, error) {
	if exactText == "" {
		return 0, 0, errors.New("exact text is required")
	}
	if strings.Count(content, exactText) != 1 {
		return 0, 0, fmt.Errorf("exact text must occur once")
	}
	start := strings.Index(content, exactText)
	return start, start + len(exactText), nil
}

func renderMarkdown(draft PageDraft) string {
	var builder strings.Builder
	builder.WriteString("# ")
	builder.WriteString(draft.Title)
	builder.WriteString("\n\n")
	builder.WriteString(strings.TrimSpace(draft.Summary))
	for _, section := range draft.Sections {
		builder.WriteString("\n\n## ")
		builder.WriteString(section.Heading)
		builder.WriteString("\n\n")
		builder.WriteString(strings.TrimSpace(section.Markdown))
	}
	builder.WriteByte('\n')
	return builder.String()
}

func revisionIdentity(pageID, baseRevisionID string, draft PageDraft) (string, error) {
	encoded, err := json.Marshal(draft)
	if err != nil {
		return "", fmt.Errorf("encode PageDraft identity: %w", err)
	}
	return stableID("page-revision", pageID, baseRevisionID, string(encoded)), nil
}

func stableID(prefix string, values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	return prefix + "_" + hex.EncodeToString(hash.Sum(nil))[:24]
}

func eventIndex(events []SourceEvent) map[string]SourceEvent {
	result := make(map[string]SourceEvent, len(events))
	for _, event := range events {
		result[event.ID] = event
	}
	return result
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func failTarget(
	target MaintenanceTarget,
	reason TargetFailureReason,
	err error,
) MaintenanceTarget {
	target.Status = TargetStatusFailed
	target.FailureReason = reason
	target.Error = err.Error()
	return target
}

func summarizeRun(targets []MaintenanceTarget) RunStatus {
	succeeded := 0
	failed := 0
	for _, target := range targets {
		switch target.Status {
		case TargetStatusSucceeded:
			succeeded++
		case TargetStatusFailed:
			failed++
		}
	}
	switch {
	case succeeded == len(targets):
		return RunStatusSucceeded
	case failed == len(targets):
		return RunStatusFailed
	case succeeded > 0 && failed > 0:
		return RunStatusPartialSuccess
	default:
		return RunStatusPending
	}
}
