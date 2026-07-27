package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/pax-beehive/pax-nexus/internal/pagewiki/memory"
)

const (
	minGeneratedPages = 3
	maxGeneratedPages = 6
)

var generatedSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type generatedPlan struct {
	Pages []generatedPage `json:"pages"`
}

type generatedPage struct {
	SourceSessionID string              `json:"source_session_id"`
	Slug            string              `json:"slug"`
	Title           string              `json:"title"`
	Summary         string              `json:"summary"`
	TopicPath       []string            `json:"topic_path"`
	Sections        []generatedSection  `json:"sections"`
	Citations       []generatedCitation `json:"citations"`
	Links           []generatedLink     `json:"links"`
}

type generatedSection struct {
	Key      string `json:"key"`
	Heading  string `json:"heading"`
	Markdown string `json:"markdown"`
}

type generatedCitation struct {
	SectionKey string              `json:"section_key"`
	ExactText  string              `json:"exact_text"`
	Evidence   []generatedEvidence `json:"evidence"`
}

type generatedEvidence struct {
	EventID   string `json:"event_id"`
	ExactText string `json:"exact_text"`
}

type generatedLink struct {
	SectionKey string `json:"section_key"`
	ExactText  string `json:"exact_text"`
	TargetSlug string `json:"target_slug"`
}

func loadGeneratedPlan(path string) (generatedPlan, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return generatedPlan{}, fmt.Errorf("read generated plan %q: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var plan generatedPlan
	if err := decoder.Decode(&plan); err != nil {
		return generatedPlan{}, fmt.Errorf("decode generated plan %q: %w", path, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return generatedPlan{}, fmt.Errorf("decode generated plan %q: trailing JSON value", path)
		}
		return generatedPlan{}, fmt.Errorf("decode generated plan %q: %w", path, err)
	}
	return plan, nil
}

func injectGeneratedPlan(
	ctx context.Context,
	repository *memory.Repository,
	planPath string,
	sessionPaths []string,
) (string, string, error) {
	plan, err := loadGeneratedPlan(planPath)
	if err != nil {
		return "", "", err
	}
	sessions, err := loadGeneratedSessions(sessionPaths)
	if err != nil {
		return "", "", err
	}
	if err := validateGeneratedPlan(plan, sessions); err != nil {
		return "", "", err
	}

	runID, err := injectGeneratedPass(ctx, repository, plan, sessions, false)
	if err != nil {
		return "", "", err
	}
	if hasGeneratedLinks(plan) {
		runID, err = injectGeneratedPass(ctx, repository, plan, sessions, true)
		if err != nil {
			return "", "", err
		}
	}
	return plan.Pages[0].Slug, runID, nil
}

func loadGeneratedSessions(paths []string) (map[string]paxlSession, error) {
	sessions := make(map[string]paxlSession, len(paths))
	for _, path := range paths {
		session, err := loadPaxlSession(path)
		if err != nil {
			return nil, err
		}
		if _, exists := sessions[session.NativeID]; exists {
			return nil, fmt.Errorf("load generated sessions: duplicate session %q", session.NativeID)
		}
		sessions[session.NativeID] = session
	}
	return sessions, nil
}

func validateGeneratedPlan(plan generatedPlan, sessions map[string]paxlSession) error {
	if len(plan.Pages) < minGeneratedPages || len(plan.Pages) > maxGeneratedPages {
		return fmt.Errorf(
			"validate generated plan: got %d pages, want %d-%d",
			len(plan.Pages),
			minGeneratedPages,
			maxGeneratedPages,
		)
	}
	pagesBySlug := make(map[string]generatedPage, len(plan.Pages))
	for _, page := range plan.Pages {
		if _, exists := pagesBySlug[page.Slug]; exists {
			return fmt.Errorf("validate generated plan: duplicate slug %q", page.Slug)
		}
		pagesBySlug[page.Slug] = page
	}
	for _, page := range plan.Pages {
		session, found := sessions[page.SourceSessionID]
		if !found {
			return fmt.Errorf(
				"validate generated page %q: source session %q is missing",
				page.Slug,
				page.SourceSessionID,
			)
		}
		if err := validateGeneratedPage(page, session, pagesBySlug); err != nil {
			return err
		}
	}
	return nil
}

func validateGeneratedPage(
	page generatedPage,
	session paxlSession,
	pagesBySlug map[string]generatedPage,
) error {
	if !generatedSlugPattern.MatchString(page.Slug) {
		return fmt.Errorf("validate generated page %q: invalid slug", page.Slug)
	}
	if len(page.TopicPath) == 0 || len(page.TopicPath) > 2 {
		return fmt.Errorf("validate generated page %q: topic path must have 1-2 segments", page.Slug)
	}
	if len(page.Sections) == 0 || len(page.Citations) == 0 {
		return fmt.Errorf("validate generated page %q: sections and citations are required", page.Slug)
	}
	sections, sectionText, err := validateGeneratedSections(page)
	if err != nil {
		return err
	}
	citationText, err := validateGeneratedCitations(page, session, sections)
	if err != nil {
		return err
	}
	linkText, err := validateGeneratedLinks(page, sections, pagesBySlug)
	if err != nil {
		return err
	}
	visible := []string{page.Title, page.Summary}
	visible = append(visible, page.TopicPath...)
	visible = append(visible, sectionText...)
	visible = append(visible, citationText...)
	visible = append(visible, linkText...)
	return validateGeneratedEnglish(page.Slug, visible)
}

func validateGeneratedSections(
	page generatedPage,
) (map[string]generatedSection, []string, error) {
	sections := make(map[string]generatedSection, len(page.Sections))
	visible := make([]string, 0, len(page.Sections)*2)
	for _, section := range page.Sections {
		if _, exists := sections[section.Key]; exists {
			return nil, nil, fmt.Errorf(
				"validate generated page %q: duplicate section %q",
				page.Slug,
				section.Key,
			)
		}
		sections[section.Key] = section
		visible = append(visible, section.Heading, section.Markdown)
	}
	return sections, visible, nil
}

func validateGeneratedCitations(
	page generatedPage,
	session paxlSession,
	sections map[string]generatedSection,
) ([]string, error) {
	events := make(map[string]paxlMessage, len(session.Messages))
	for _, message := range session.Messages {
		events[messageEventID(message.Seq)] = message
	}
	visible := make([]string, 0, len(page.Citations))
	for _, citation := range page.Citations {
		section, found := sections[citation.SectionKey]
		if !found {
			return nil, fmt.Errorf(
				"validate generated page %q: citation uses unknown section %q",
				page.Slug,
				citation.SectionKey,
			)
		}
		if strings.Count(section.Markdown, citation.ExactText) != 1 {
			return nil, fmt.Errorf(
				"validate generated page %q: citation text %q must occur once",
				page.Slug,
				citation.ExactText,
			)
		}
		if len(citation.Evidence) == 0 {
			return nil, fmt.Errorf(
				"validate generated page %q: citation evidence is required",
				page.Slug,
			)
		}
		visible = append(visible, citation.ExactText)
		for _, evidence := range citation.Evidence {
			message, found := events[evidence.EventID]
			if !found {
				return nil, fmt.Errorf(
					"validate generated page %q: evidence event %q is missing",
					page.Slug,
					evidence.EventID,
				)
			}
			if strings.Count(message.Content, evidence.ExactText) != 1 {
				return nil, fmt.Errorf(
					"validate generated page %q: evidence quote in %q must occur once",
					page.Slug,
					evidence.EventID,
				)
			}
		}
	}
	return visible, nil
}

func validateGeneratedLinks(
	page generatedPage,
	sections map[string]generatedSection,
	pagesBySlug map[string]generatedPage,
) ([]string, error) {
	visible := make([]string, 0, len(page.Links))
	for _, link := range page.Links {
		section, found := sections[link.SectionKey]
		if !found {
			return nil, fmt.Errorf(
				"validate generated page %q: link uses unknown section %q",
				page.Slug,
				link.SectionKey,
			)
		}
		if strings.Count(section.Markdown, link.ExactText) != 1 {
			return nil, fmt.Errorf(
				"validate generated page %q: link text %q must occur once",
				page.Slug,
				link.ExactText,
			)
		}
		if link.TargetSlug == page.Slug {
			return nil, fmt.Errorf(
				"validate generated page %q: self-link is not allowed",
				page.Slug,
			)
		}
		if _, found := pagesBySlug[link.TargetSlug]; !found {
			return nil, fmt.Errorf(
				"validate generated page %q: link target %q is missing",
				page.Slug,
				link.TargetSlug,
			)
		}
		visible = append(visible, link.ExactText)
	}
	return visible, nil
}

func validateGeneratedEnglish(slug string, visible []string) error {
	for _, value := range visible {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("validate generated page %q: reader-visible text is blank", slug)
		}
		if !isEnglishOnly(value) {
			return fmt.Errorf(
				"validate generated page %q: reader-visible text is not English-only",
				slug,
			)
		}
	}
	return nil
}

func isEnglishOnly(value string) bool {
	for _, character := range value {
		if unicode.IsLetter(character) && !unicode.In(character, unicode.Latin) {
			return false
		}
	}
	return true
}

func hasGeneratedLinks(plan generatedPlan) bool {
	for _, page := range plan.Pages {
		if len(page.Links) > 0 {
			return true
		}
	}
	return false
}

func injectGeneratedPass(
	ctx context.Context,
	repository *memory.Repository,
	plan generatedPlan,
	sessions map[string]paxlSession,
	includeLinks bool,
) (string, error) {
	var lastRunID string
	for _, sourceID := range orderedGeneratedSources(plan, includeLinks) {
		pages := generatedPagesForSource(plan, sourceID, includeLinks)
		result, err := injectGeneratedPages(
			ctx,
			repository,
			sessions[sourceID],
			pages,
			includeLinks,
		)
		if err != nil {
			return "", err
		}
		lastRunID = result.Run.ID
	}
	return lastRunID, nil
}

func orderedGeneratedSources(plan generatedPlan, linksOnly bool) []string {
	seen := make(map[string]struct{})
	sources := make([]string, 0, len(plan.Pages))
	for _, page := range plan.Pages {
		if linksOnly && len(page.Links) == 0 {
			continue
		}
		if _, exists := seen[page.SourceSessionID]; exists {
			continue
		}
		seen[page.SourceSessionID] = struct{}{}
		sources = append(sources, page.SourceSessionID)
	}
	return sources
}

func generatedPagesForSource(
	plan generatedPlan,
	sourceID string,
	linksOnly bool,
) []generatedPage {
	pages := make([]generatedPage, 0)
	for _, page := range plan.Pages {
		if page.SourceSessionID != sourceID || (linksOnly && len(page.Links) == 0) {
			continue
		}
		pages = append(pages, page)
	}
	return pages
}

func injectGeneratedPages(
	ctx context.Context,
	repository *memory.Repository,
	session paxlSession,
	pages []generatedPage,
	includeLinks bool,
) (pagewiki.InjectResult, error) {
	mode := "create"
	if includeLinks {
		mode = "link"
	}
	briefs := make([]pagewiki.PageBrief, 0, len(pages))
	drafts := make(map[string]pagewiki.PageDraft, len(pages))
	for _, page := range pages {
		key := "model-" + mode + "-" + page.Slug
		brief, err := generatedBrief(ctx, repository, page, key, includeLinks)
		if err != nil {
			return pagewiki.InjectResult{}, err
		}
		draft, err := generatedDraft(ctx, repository, page, includeLinks)
		if err != nil {
			return pagewiki.InjectResult{}, err
		}
		briefs = append(briefs, brief)
		drafts[key] = draft
	}
	result, err := pagewiki.NewService(
		repository,
		pagewiki.ScriptedPlanner{Briefs: briefs},
		pagewiki.ScriptedEditor{Drafts: drafts},
	).InjectSession(
		ctx,
		session.injectionRequest("model-"+mode+"-"+session.NativeID),
	)
	if err != nil {
		return pagewiki.InjectResult{}, fmt.Errorf(
			"inject model-generated %s pages from session %q: %w",
			mode,
			session.NativeID,
			err,
		)
	}
	if result.Run.Status != pagewiki.RunStatusSucceeded {
		return pagewiki.InjectResult{}, fmt.Errorf(
			"inject model-generated %s pages from session %q: run %q finished with %s: %+v",
			mode,
			session.NativeID,
			result.Run.ID,
			result.Run.Status,
			result.Run.Targets,
		)
	}
	return result, nil
}

func generatedBrief(
	ctx context.Context,
	repository *memory.Repository,
	page generatedPage,
	key string,
	update bool,
) (pagewiki.PageBrief, error) {
	brief := pagewiki.PageBrief{
		Key:              key,
		ReaderGoal:       page.Summary,
		EvidenceEventIDs: generatedEvidenceEventIDs(page),
	}
	if !update {
		brief.Action = pagewiki.PageActionCreate
		brief.ProposedSlug = page.Slug
		brief.ProposedTitle = page.Title
		brief.TopicPath = append([]string(nil), page.TopicPath...)
		return brief, nil
	}
	existing, err := repository.PageBySlug(ctx, page.Slug)
	if err != nil {
		return pagewiki.PageBrief{}, fmt.Errorf(
			"resolve model-generated page %q for links: %w",
			page.Slug,
			err,
		)
	}
	brief.Action = pagewiki.PageActionUpdate
	brief.TargetPageID = existing.ID
	brief.ExpectedBaseRevisionID = existing.CurrentRevisionID
	return brief, nil
}

func generatedEvidenceEventIDs(page generatedPage) []string {
	seen := make(map[string]struct{})
	ids := make([]string, 0)
	for _, citation := range page.Citations {
		for _, evidence := range citation.Evidence {
			if _, exists := seen[evidence.EventID]; exists {
				continue
			}
			seen[evidence.EventID] = struct{}{}
			ids = append(ids, evidence.EventID)
		}
	}
	sort.Strings(ids)
	return ids
}

func generatedDraft(
	ctx context.Context,
	repository *memory.Repository,
	page generatedPage,
	includeLinks bool,
) (pagewiki.PageDraft, error) {
	draft := pagewiki.PageDraft{
		Slug:      page.Slug,
		Title:     page.Title,
		Summary:   page.Summary,
		Sections:  make([]pagewiki.SectionDraft, 0, len(page.Sections)),
		Citations: make([]pagewiki.CitationDraft, 0, len(page.Citations)),
	}
	for _, section := range page.Sections {
		draft.Sections = append(draft.Sections, pagewiki.SectionDraft(section))
	}
	for _, citation := range page.Citations {
		evidence := make([]pagewiki.EvidenceQuoteDraft, 0, len(citation.Evidence))
		for _, quote := range citation.Evidence {
			evidence = append(evidence, pagewiki.EvidenceQuoteDraft{
				EventID:   quote.EventID,
				ExactText: quote.ExactText,
			})
		}
		draft.Citations = append(draft.Citations, pagewiki.CitationDraft{
			SectionKey: citation.SectionKey,
			ExactText:  citation.ExactText,
			Evidence:   evidence,
		})
	}
	if !includeLinks {
		return draft, nil
	}
	for _, link := range page.Links {
		target, err := repository.PageBySlug(ctx, link.TargetSlug)
		if err != nil {
			return pagewiki.PageDraft{}, fmt.Errorf(
				"resolve model-generated link %q -> %q: %w",
				page.Slug,
				link.TargetSlug,
				err,
			)
		}
		draft.Links = append(draft.Links, pagewiki.LinkDraft{
			SectionKey:   link.SectionKey,
			ExactText:    link.ExactText,
			TargetPageID: target.ID,
		})
	}
	return draft, nil
}
