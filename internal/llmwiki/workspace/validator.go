package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	markdownLinkPattern        = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+[^)]*)?\)`)
	sourceCitationStartPattern = regexp.MustCompile(`\[[^\]]*\]\([^)\n]*/sources/`)
	htmlAnchorPattern          = regexp.MustCompile(`<a\s+id=["']([^"']+)["']\s*></a>`)
	messageAnchorPattern       = regexp.MustCompile(`^msg-[a-f0-9]{16}$`)
	h1Pattern                  = regexp.MustCompile(`(?m)^# [^\n]+$`)
	sessionHeadingPattern      = regexp.MustCompile(`(?mi)^#{2,6}\s+(?:source\s+)?session\s+\d+\b`)
	relatedPagesHeadingPattern = regexp.MustCompile(`(?mi)^## Related pages\s*$`)
	h2Pattern                  = regexp.MustCompile(`(?m)^## [^\n]+$`)
)

func Validate(root string) ValidationReport {
	report := ValidationReport{Valid: true}
	manifest, err := readManifest(root)
	if err != nil {
		report.add(".pax/manifest.json", err.Error())
		return report
	}
	sourceAnchors := validateSources(root, manifest, &report)
	pages, links, contents := validateWikiLinks(root, sourceAnchors, &report)
	validateReachability(pages, links, &report)
	validateArticleFirst(root, pages, links, contents, &report)
	validateGitChangeBudget(root, &report)
	report.Valid = len(report.Errors) == 0
	return report
}

func readManifest(root string) (Manifest, error) {
	encoded, err := os.ReadFile(filepath.Join(root, ".pax", "manifest.json"))
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if manifest.SchemaVersion != manifestSchema {
		return Manifest{}, fmt.Errorf(
			"unsupported manifest schema %q",
			manifest.SchemaVersion,
		)
	}
	return manifest, nil
}

func validateSources(
	root string,
	manifest Manifest,
	report *ValidationReport,
) map[string]map[string]struct{} {
	result := make(map[string]map[string]struct{}, len(manifest.Sources))
	for _, source := range manifest.Sources {
		relative := filepath.ToSlash(filepath.Clean(source.Path))
		if !strings.HasPrefix(relative, "sources/") {
			report.add(source.Path, "source path is outside sources/")
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(relative))
		content, err := os.ReadFile(target)
		if err != nil {
			report.add(relative, fmt.Sprintf("read source: %v", err))
			continue
		}
		digest := sha256.Sum256(content)
		actual := hex.EncodeToString(digest[:])
		if actual != source.SHA256 {
			report.add(relative, "source hash mismatch")
		}
		info, err := os.Stat(target)
		if err != nil {
			report.add(relative, fmt.Sprintf("stat source: %v", err))
		} else if info.Mode().Perm()&0o222 != 0 {
			report.add(relative, "source is writable")
		}
		anchors := make(map[string]struct{}, len(source.Anchors))
		renderedAnchors := make(map[string]struct{})
		for _, match := range htmlAnchorPattern.FindAllSubmatch(content, -1) {
			renderedAnchors[string(match[1])] = struct{}{}
		}
		for _, anchor := range source.Anchors {
			anchors[anchor.ID] = struct{}{}
			if _, exists := renderedAnchors[anchor.ID]; !exists {
				report.add(relative, "manifest anchor "+anchor.ID+" is missing")
			}
		}
		result[relative] = anchors
	}
	return result
}

func validateWikiLinks(
	root string,
	sourceAnchors map[string]map[string]struct{},
	report *ValidationReport,
) (map[string]struct{}, map[string][]string, map[string][]byte) {
	pages := make(map[string]struct{})
	links := make(map[string][]string)
	contents := make(map[string][]byte)
	wikiRoot := filepath.Join(root, "wiki")
	walkErr := filepath.WalkDir(wikiRoot, func(target string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		relativeRoot, err := filepath.Rel(root, target)
		if err != nil {
			return err
		}
		relativeRoot = filepath.ToSlash(relativeRoot)
		if relativeRoot != "wiki/log.md" {
			pages[relativeRoot] = struct{}{}
			report.MarkdownFiles++
		}
		content, err := os.ReadFile(target)
		if err != nil {
			report.add(relativeRoot, fmt.Sprintf("read wiki page: %v", err))
			return nil
		}
		contents[relativeRoot] = content
		validateMarkdownLinks(
			root, target, relativeRoot, content, sourceAnchors, report, links,
		)
		return nil
	})
	if walkErr != nil {
		report.add("wiki", fmt.Sprintf("walk wiki: %v", walkErr))
	}
	return pages, links, contents
}

func validateMarkdownLinks(
	root,
	target,
	relativeRoot string,
	content []byte,
	sourceAnchors map[string]map[string]struct{},
	report *ValidationReport,
	links map[string][]string,
) {
	expectedSourceCitations := len(sourceCitationStartPattern.FindAll(content, -1))
	parsedSourceCitations := 0
	for _, match := range markdownLinkPattern.FindAllSubmatch(content, -1) {
		if strings.Contains(string(match[1]), "sources/") {
			parsedSourceCitations++
		}
		validateMarkdownLink(
			root,
			target,
			relativeRoot,
			string(match[1]),
			sourceAnchors,
			report,
			links,
		)
	}
	if parsedSourceCitations != expectedSourceCitations {
		report.add(relativeRoot, "malformed source citation")
	}
}

func validateMarkdownLink(
	root,
	target,
	relativeRoot,
	raw string,
	sourceAnchors map[string]map[string]struct{},
	report *ValidationReport,
	links map[string][]string,
) {
	if isExternalLink(raw) {
		return
	}
	pathPart, anchor, _ := strings.Cut(raw, "#")
	resolved, err := resolveLink(root, target, pathPart)
	if err != nil {
		report.add(relativeRoot, err.Error())
		return
	}
	if !linkTargetExists(root, relativeRoot, raw, resolved, report) {
		return
	}
	if strings.HasPrefix(resolved, "sources/") {
		validateCitation(relativeRoot, resolved, anchor, sourceAnchors, report)
		return
	}
	if strings.HasPrefix(resolved, "wiki/") {
		links[relativeRoot] = append(links[relativeRoot], resolved)
	}
}

func linkTargetExists(
	root,
	relativeRoot,
	raw,
	resolved string,
	report *ValidationReport,
) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(resolved)))
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		report.add(relativeRoot, "broken internal link "+raw)
	} else {
		report.add(relativeRoot, fmt.Sprintf("inspect link %s: %v", raw, err))
	}
	return false
}

func validateCitation(
	relativeRoot,
	resolved,
	anchor string,
	sourceAnchors map[string]map[string]struct{},
	report *ValidationReport,
) {
	report.Citations++
	if anchor == "" {
		report.add(relativeRoot, "source citation has no message anchor")
		return
	}
	if !messageAnchorPattern.MatchString(anchor) {
		report.add(relativeRoot, "malformed source citation anchor "+anchor)
		return
	}
	known, exists := sourceAnchors[resolved]
	if !exists {
		report.add(relativeRoot, "citation targets an unregistered source")
		return
	}
	if _, exists := known[anchor]; !exists {
		report.add(relativeRoot, "unknown source anchor "+anchor)
	}
}

func validateCandidateCitations(
	root,
	target,
	relative string,
	content []byte,
) error {
	manifest, err := readManifest(root)
	if err != nil {
		return err
	}
	sourceAnchors := make(map[string]map[string]struct{}, len(manifest.Sources))
	for _, source := range manifest.Sources {
		anchors := make(map[string]struct{}, len(source.Anchors))
		for _, anchor := range source.Anchors {
			anchors[anchor.ID] = struct{}{}
		}
		sourceAnchors[filepath.ToSlash(filepath.Clean(source.Path))] = anchors
	}
	report := ValidationReport{Valid: true}
	validateMarkdownLinks(
		root,
		target,
		relative,
		content,
		sourceAnchors,
		&report,
		make(map[string][]string),
	)
	for _, issue := range report.Errors {
		if strings.Contains(issue.Message, "source citation") ||
			strings.Contains(issue.Message, "source anchor") ||
			strings.Contains(issue.Message, "unregistered source") {
			return errors.New(issue.Message)
		}
	}
	return nil
}

func validateReachability(
	pages map[string]struct{},
	links map[string][]string,
	report *ValidationReport,
) {
	const index = "wiki/index.md"
	if _, exists := pages[index]; !exists {
		report.add(index, "topic tree is missing")
		return
	}
	reached := map[string]struct{}{index: {}}
	queue := []string{index}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, linked := range links[current] {
			if _, isPage := pages[linked]; !isPage {
				continue
			}
			if _, seen := reached[linked]; seen {
				continue
			}
			reached[linked] = struct{}{}
			queue = append(queue, linked)
		}
	}
	var orphans []string
	for page := range pages {
		if _, seen := reached[page]; !seen {
			orphans = append(orphans, page)
		}
	}
	sort.Strings(orphans)
	for _, orphan := range orphans {
		report.add(orphan, "is not reachable from wiki/index.md")
	}
}

func validateArticleFirst(
	root string,
	pages map[string]struct{},
	links map[string][]string,
	contents map[string][]byte,
	report *ValidationReport,
) {
	profile, err := os.ReadFile(filepath.Join(root, ".pax", "editorial-profile"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		report.add(".pax/editorial-profile", fmt.Sprintf("read editorial profile: %v", err))
		return
	}
	if strings.TrimSpace(string(profile)) != "article-first-v1" {
		report.add(".pax/editorial-profile", "unsupported editorial profile")
		return
	}

	paths := make([]string, 0, len(pages))
	for path := range pages {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		validateArticlePage(path, contents[path], links[path], report)
	}
	validateDuplicateArticleProse(paths, contents, report)
	validateArticleNavigationDepth(pages, links, report)
}

func validateArticlePage(
	path string,
	content []byte,
	links []string,
	report *ValidationReport,
) {
	pageType, body, err := parseArticleFrontmatter(content)
	if err != nil {
		report.add(path, err.Error())
		return
	}
	if !validArticlePageType(pageType) {
		report.add(path, "unsupported article type "+pageType)
	}
	if count := len(h1Pattern.FindAll(body, -1)); count != 1 {
		report.add(path, fmt.Sprintf("article must contain exactly one H1; found %d", count))
	}
	if !hasArticleLead(body) {
		report.add(path, "article needs a prose lead before its first H2")
	}
	if pageType != "timeline" && sessionHeadingPattern.Match(body) {
		report.add(path, "Session chronology heading is forbidden outside timeline and log views")
	}
	if related := relatedPagesHeadingPattern.FindIndex(body); related != nil &&
		h2Pattern.Match(body[related[1]:]) {
		report.add(path, "Related pages must be the final H2 section")
	}
	if pageType == "portal" {
		return
	}
	if len(sourceCitationStartPattern.FindAll(body, -1)) == 0 {
		report.add(path, "article has no precise Source citation")
	}
	if len(links) == 0 {
		report.add(path, "article has no contextual link to another Wiki page")
	}
}

func validateDuplicateArticleProse(
	paths []string,
	contents map[string][]byte,
	report *ValidationReport,
) {
	paragraphOwners := make(map[string]string)
	for _, path := range paths {
		_, body, err := parseArticleFrontmatter(contents[path])
		if err != nil {
			continue
		}
		for _, paragraph := range substantialParagraphs(body) {
			normalized := strings.Join(strings.Fields(strings.ToLower(paragraph)), " ")
			if owner, duplicate := paragraphOwners[normalized]; duplicate && owner != path {
				report.add(path, "duplicates substantial prose from "+owner)
				continue
			}
			paragraphOwners[normalized] = path
		}
	}
}

func parseArticleFrontmatter(content []byte) (string, []byte, error) {
	const delimiter = "---\n"
	if !strings.HasPrefix(string(content), delimiter) {
		return "", nil, errors.New("article is missing frontmatter with a type")
	}
	remainder := string(content[len(delimiter):])
	end := strings.Index(remainder, "\n---\n")
	if end < 0 {
		return "", nil, errors.New("article frontmatter is not closed")
	}
	var pageType string
	for _, line := range strings.Split(remainder[:end], "\n") {
		key, value, found := strings.Cut(line, ":")
		if found && strings.TrimSpace(key) == "type" {
			pageType = strings.TrimSpace(value)
		}
	}
	if pageType == "" {
		return "", nil, errors.New("article frontmatter is missing type")
	}
	return pageType, []byte(strings.TrimSpace(remainder[end+len("\n---\n"):])), nil
}

func validArticlePageType(value string) bool {
	switch value {
	case "portal", "person", "topic", "concept", "journey", "project",
		"event", "timeline", "place", "organization":
		return true
	default:
		return false
	}
}

func hasArticleLead(body []byte) bool {
	lines := strings.Split(string(body), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "# ") {
		return false
	}
	var leadLines []string
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "## ") {
			break
		}
		leadLines = append(leadLines, line)
	}
	for _, paragraph := range strings.Split(strings.Join(leadLines, "\n"), "\n\n") {
		trimmed := strings.TrimSpace(paragraph)
		if len(strings.Fields(trimmed)) < 6 {
			continue
		}
		if strings.HasPrefix(trimmed, "-") ||
			strings.HasPrefix(trimmed, "*") ||
			strings.HasPrefix(trimmed, "|") ||
			strings.HasPrefix(trimmed, ">") {
			continue
		}
		return true
	}
	return false
}

func substantialParagraphs(body []byte) []string {
	var result []string
	for _, paragraph := range strings.Split(string(body), "\n\n") {
		trimmed := strings.TrimSpace(paragraph)
		if len([]rune(trimmed)) < 160 ||
			strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "-") ||
			strings.HasPrefix(trimmed, "|") {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

func validateArticleNavigationDepth(
	pages map[string]struct{},
	links map[string][]string,
	report *ValidationReport,
) {
	const index = "wiki/index.md"
	depth := map[string]int{index: 0}
	queue := []string{index}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, linked := range links[current] {
			if _, exists := pages[linked]; !exists {
				continue
			}
			if _, seen := depth[linked]; seen {
				continue
			}
			depth[linked] = depth[current] + 1
			queue = append(queue, linked)
		}
	}
	for page, pageDepth := range depth {
		if pageDepth > 3 {
			report.add(page, fmt.Sprintf(
				"article is %d links from wiki/index.md; maximum is 3",
				pageDepth,
			))
		}
	}
}

func validateGitChangeBudget(root string, report *ValidationReport) {
	if _, err := os.Stat(filepath.Join(root, ".git")); errors.Is(err, os.ErrNotExist) {
		return
	} else if err != nil {
		report.add(".git", fmt.Sprintf("inspect Git workspace: %v", err))
		return
	}
	output, err := runGit(
		context.Background(),
		root,
		"ls-tree",
		"-r",
		"--name-only",
		"HEAD",
		"--",
		"wiki/index.md",
		"wiki/portals",
		"wiki/pages",
		"wiki/topics",
		"wiki/events",
		"wiki/timelines",
	)
	if err != nil {
		report.add(".git", fmt.Sprintf("inspect base Wiki pages: %v", err))
		return
	}
	var tracked []string
	if strings.TrimSpace(output) != "" {
		tracked = strings.Split(output, "\n")
	}
	deleted := 0
	for _, relative := range tracked {
		base, showErr := runGit(
			context.Background(),
			root,
			"show",
			"HEAD:"+relative,
		)
		if showErr != nil {
			report.add(relative, fmt.Sprintf("read base Wiki page: %v", showErr))
			continue
		}
		current, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if errors.Is(readErr, os.ErrNotExist) {
			deleted++
			continue
		}
		if readErr != nil {
			report.add(relative, fmt.Sprintf("read current Wiki page: %v", readErr))
			continue
		}
		if len(base) >= 256 && len(current)*3 < len(base) {
			report.add(
				relative,
				fmt.Sprintf(
					"destructive page shrink from %d to %d bytes requires explicit approval",
					len(base),
					len(current),
				),
			)
		}
	}
	if deleted >= 2 && deleted*4 >= len(tracked) {
		report.add(
			"wiki",
			fmt.Sprintf(
				"bulk deletion of %d/%d existing major pages requires explicit approval",
				deleted,
				len(tracked),
			),
		)
	}
}

func resolveLink(root, currentFile, rawPath string) (string, error) {
	if rawPath == "" {
		relative, err := filepath.Rel(root, currentFile)
		if err != nil {
			return "", fmt.Errorf("resolve same-page link: %w", err)
		}
		return filepath.ToSlash(relative), nil
	}
	decoded := strings.ReplaceAll(rawPath, "%20", " ")
	target := filepath.Clean(filepath.Join(filepath.Dir(currentFile), filepath.FromSlash(decoded)))
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("resolve internal link %s: %w", rawPath, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("internal link escapes workspace: %s", rawPath)
	}
	return filepath.ToSlash(relative), nil
}

func isExternalLink(value string) bool {
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:")
}

func (r *ValidationReport) add(path, message string) {
	r.Valid = false
	r.Errors = append(r.Errors, ValidationIssue{Path: path, Message: message})
}
