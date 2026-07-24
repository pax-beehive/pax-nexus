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
)

func Validate(root string) ValidationReport {
	report := ValidationReport{Valid: true}
	manifest, err := readManifest(root)
	if err != nil {
		report.add(".pax/manifest.json", err.Error())
		return report
	}
	sourceAnchors := validateSources(root, manifest, &report)
	pages, links := validateWikiLinks(root, sourceAnchors, &report)
	validateReachability(pages, links, &report)
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
) (map[string]struct{}, map[string][]string) {
	pages := make(map[string]struct{})
	links := make(map[string][]string)
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
		validateMarkdownLinks(
			root, target, relativeRoot, content, sourceAnchors, report, links,
		)
		return nil
	})
	if walkErr != nil {
		report.add("wiki", fmt.Sprintf("walk wiki: %v", walkErr))
	}
	return pages, links
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
		"wiki/pages",
		"wiki/topics",
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
