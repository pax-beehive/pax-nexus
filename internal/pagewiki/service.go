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

	"golang.org/x/sync/errgroup"

	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

const (
	minPlannedPages    = 1
	maxPlannedPages    = 8
	editConcurrency    = 4
	treeReindexQuiet   = 5 * time.Second
	treeReindexMaxWait = 60 * time.Second
)

var (
	slugPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	sectionKeyPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

type Service struct {
	repository    Repository
	planner       Planner
	editor        Editor
	treeIndexer   TreeIndexer
	logger        *slog.Logger
	treeDirty     chan struct{}
	treeQuiet     time.Duration
	treeMaxWait   time.Duration
	treeReindexMu sync.Mutex
}

type ServiceOption func(*Service)

// WithTreeIndexer enables Page Wiki topic-tree reindexing. When set,
// InjectSession marks the topic tree dirty after any run that changed the
// catalog; the actual rebuild happens off that path, either in the
// background via StartTreeMaintenance (debounced) or synchronously via
// FlushTreeReindex. It remains optional: without it, marks are never
// recorded and no reindex ever runs.
func WithTreeIndexer(indexer TreeIndexer, logger *slog.Logger) ServiceOption {
	return func(s *Service) {
		s.treeIndexer = indexer
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
	service.treeDirty = make(chan struct{}, 1)
	service.treeQuiet = treeReindexQuiet
	service.treeMaxWait = treeReindexMaxWait
	for _, option := range options {
		option(service)
	}
	return service
}

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
	existingRun, err := s.repository.MaintenanceRun(ctx, runID)
	switch {
	case err == nil:
		return InjectResult{
			SourceRevisionID: sourceRevision.ID,
			Run:              existingRun,
		}, nil
	case !errors.Is(err, ErrNotFound):
		return InjectResult{}, fmt.Errorf("load MaintenanceRun: %w", err)
	}
	catalog, err := s.repository.PageCatalog(ctx)
	if err != nil {
		return InjectResult{}, fmt.Errorf("load Page catalog: %w", err)
	}
	briefs, err := s.planner.Plan(ctx, PlanInput{
		SourceRevision: sourceRevision,
		PageCatalog:    catalog,
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
	prepared := s.prepareTargets(ctx, run.ID, sourceRevision, catalog, briefs)
	for index := range prepared {
		run.Targets = append(run.Targets, s.commitTarget(ctx, sourceRevision, prepared[index]))
	}
	run.Status = summarizeRun(run.Targets)
	if err := s.repository.SaveMaintenanceRun(ctx, run); err != nil {
		return InjectResult{}, fmt.Errorf("save MaintenanceRun: %w", err)
	}
	if s.treeIndexer != nil && catalogChanged(briefs, run.Targets) {
		s.markTreeDirty()
	}
	return InjectResult{
		SourceRevisionID: sourceRevision.ID,
		Run:              run,
	}, nil
}

// markTreeDirty records that the Page catalog changed since the last topic
// tree rebuild. The channel is buffered(1): an already-pending mark absorbs
// further marks, and the eventual rebuild reads the latest catalog anyway.
func (s *Service) markTreeDirty() {
	select {
	case s.treeDirty <- struct{}{}:
	default:
	}
}

// StartTreeMaintenance runs topic-tree rebuilds in the background: a dirty
// mark opens a debounce window (treeQuiet of silence, capped at treeMaxWait)
// and then rebuilds once. This keeps the rebuild off the ingest critical
// path and coalesces a replay's many runs into one rebuild. A mark pending
// when ctx is cancelled is dropped; the tree is a derived view and the next
// catalog change re-marks it.
func (s *Service) StartTreeMaintenance(ctx context.Context) {
	if s.treeIndexer == nil {
		return
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.treeDirty:
				s.debounceThenReindex(ctx)
			}
		}
	}()
}

func (s *Service) debounceThenReindex(ctx context.Context) {
	quiet := time.NewTimer(s.treeQuiet)
	defer quiet.Stop()
	deadline := time.NewTimer(s.treeMaxWait)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.treeDirty:
			if !quiet.Stop() {
				<-quiet.C
			}
			quiet.Reset(s.treeQuiet)
		case <-quiet.C:
			s.reindexTree(ctx)
			return
		case <-deadline.C:
			s.reindexTree(ctx)
			return
		}
	}
}

// FlushTreeReindex rebuilds the topic tree now if a dirty mark is pending.
// Tests and the rebuild flow use it to make the async contract synchronous.
// If StartTreeMaintenance is already running, its background drainer may
// have already claimed the pending mark off treeDirty before Flush observes
// it; Flush then no-ops even though a debounced rebuild is still in flight.
// Flush guarantees "rebuild now if a mark is pending", not "the tree is
// fresh once this call returns".
func (s *Service) FlushTreeReindex(ctx context.Context) {
	if s.treeIndexer == nil {
		return
	}
	select {
	case <-s.treeDirty:
		s.reindexTree(ctx)
	default:
	}
}

// reindexTree is best-effort: any failure is logged and swallowed so a
// reindex problem never fails or delays ingestion, and the previously
// stored TopicTree stays in place. The mutex serializes concurrent calls
// from FlushTreeReindex and the background maintenance goroutine.
func (s *Service) reindexTree(ctx context.Context) {
	s.treeReindexMu.Lock()
	defer s.treeReindexMu.Unlock()
	catalog, err := s.repository.PageCatalog(ctx)
	if err != nil {
		s.logger.Warn("Page Wiki tree reindex skipped", "stage", "load catalog", "error", err)
		return
	}
	current, err := s.repository.TopicTree(ctx)
	if err != nil {
		s.logger.Warn("Page Wiki tree reindex skipped", "stage", "load tree", "error", err)
		return
	}
	tree, err := s.treeIndexer.Index(ctx, TreeIndexInput{Catalog: catalog, Current: current})
	if err != nil {
		s.logger.Warn("Page Wiki tree reindex skipped", "stage", "index", "error", err)
		return
	}
	if err := s.repository.ReplaceTopicTree(ctx, tree); err != nil {
		s.logger.Warn("Page Wiki tree reindex skipped", "stage", "replace", "error", err)
	}
}

// catalogChanged reports whether any successfully processed target in this
// run actually changed the Page catalog: a new Page was created, or an
// existing Page was updated to a different revision. targets is appended in
// brief order by InjectSession, so index pairing with briefs is sound.
func catalogChanged(briefs []PageBrief, targets []MaintenanceTarget) bool {
	for index, target := range targets {
		if index >= len(briefs) || target.Status != TargetStatusSucceeded {
			continue
		}
		switch briefs[index].Action {
		case PageActionCreate:
			return true
		case PageActionUpdate:
			if target.PageRevisionID != briefs[index].ExpectedBaseRevisionID {
				return true
			}
		}
	}
	return false
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
}

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
) []preparedTarget {
	prepared := make([]preparedTarget, len(briefs))
	var group errgroup.Group
	group.SetLimit(editConcurrency)
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
		group.Go(func() error {
			prepared[index] = s.prepareTarget(ctx, runID, sourceRevision, catalog, brief)
			return nil
		})
	}
	_ = group.Wait()
	return prepared
}

func (s *Service) prepareTarget(
	ctx context.Context,
	runID string,
	sourceRevision SourceRevision,
	catalog PageCatalog,
	brief PageBrief,
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
	page, currentRevision, err := s.resolvePage(ctx, sourceRevision.ID, brief)
	if err != nil {
		result.target = failTarget(result.target, TargetFailurePublicationConflict, err)
		return result
	}
	draft, err := s.editor.Edit(ctx, EditInput{
		SourceRevision:  sourceRevision,
		Brief:           brief,
		CurrentPage:     page,
		CurrentRevision: currentRevision,
	})
	if err != nil {
		result.target = failTarget(result.target, TargetFailureInvalidDraft, err)
		return result
	}
	result.page = page
	result.currentRevision = currentRevision
	result.draft = draft
	result.ready = true
	return result
}

func (s *Service) commitTarget(
	ctx context.Context,
	sourceRevision SourceRevision,
	prepared preparedTarget,
) MaintenanceTarget {
	if !prepared.ready {
		return prepared.target
	}
	target := prepared.target
	pageValue, revision, reason, err := s.buildPublication(
		ctx,
		sourceRevision,
		prepared.brief,
		prepared.page,
		prepared.currentRevision,
		prepared.draft,
	)
	if err != nil {
		return failTarget(target, reason, err)
	}
	if prepared.currentRevision != nil &&
		prepared.page.Slug == pageValue.Slug &&
		prepared.page.Title == pageValue.Title &&
		revisionsEquivalent(*prepared.currentRevision, revision) {
		target.PageID = prepared.page.ID
		target.PageRevisionID = prepared.currentRevision.ID
		target.Status = TargetStatusSucceeded
		return target
	}
	publication := PagePublication{
		Page:     pageValue,
		Revision: revision,
	}
	if err := s.repository.PublishPage(ctx, publication); err != nil {
		return failTarget(target, TargetFailurePublicationConflict, err)
	}
	target.PageID = pageValue.ID
	target.PageRevisionID = revision.ID
	target.Status = TargetStatusSucceeded
	return target
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

func (s *Service) resolvePage(
	ctx context.Context,
	sourceRevisionID string,
	brief PageBrief,
) (*Page, *PageRevision, error) {
	if brief.Action == PageActionCreate {
		page := Page{
			ID:    stableID("page", sourceRevisionID, brief.Key),
			Slug:  brief.ProposedSlug,
			Title: brief.ProposedTitle,
		}
		return &page, nil, nil
	}
	page, err := s.repository.PageByID(ctx, brief.TargetPageID)
	if err != nil {
		return nil, nil, fmt.Errorf("load update Page: %w", err)
	}
	if page.CurrentRevisionID != brief.ExpectedBaseRevisionID {
		return nil, nil, fmt.Errorf(
			"%w: Page %q changed after planning",
			ErrRevisionConflict,
			page.ID,
		)
	}
	revision, err := s.repository.PageRevision(ctx, page.CurrentRevisionID)
	if err != nil {
		return nil, nil, fmt.Errorf("load current PageRevision: %w", err)
	}
	return &page, &revision, nil
}

func (s *Service) buildPublication(
	ctx context.Context,
	sourceRevision SourceRevision,
	brief PageBrief,
	page *Page,
	currentRevision *PageRevision,
	draft PageDraft,
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
	links, err := s.buildLinks(ctx, revisionID, sectionByKey, draft.Links)
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
		if _, err := s.repository.PageByID(ctx, draft.TargetPageID); err != nil {
			return nil, fmt.Errorf(
				"%w: target Page %q: %w",
				ErrInvalidLink,
				draft.TargetPageID,
				err,
			)
		}
		links = append(links, PageLink{
			ID:             stableID("link", revisionID, fmt.Sprint(index)),
			PageRevisionID: revisionID,
			SectionKey:     draft.SectionKey,
			StartByte:      start,
			EndByte:        end,
			ExactText:      draft.ExactText,
			TargetPageID:   draft.TargetPageID,
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
