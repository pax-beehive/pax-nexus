package workspace

import (
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
	markdownLinkPattern = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+[^)]*)?\)`)
	htmlAnchorPattern   = regexp.MustCompile(`<a\s+id=["']([^"']+)["']\s*></a>`)
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
		for _, match := range markdownLinkPattern.FindAllSubmatch(content, -1) {
			raw := string(match[1])
			if isExternalLink(raw) {
				continue
			}
			pathPart, anchor, _ := strings.Cut(raw, "#")
			resolved, resolveErr := resolveLink(root, target, pathPart)
			if resolveErr != nil {
				report.add(relativeRoot, resolveErr.Error())
				continue
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(resolved))); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					report.add(relativeRoot, "broken internal link "+raw)
				} else {
					report.add(relativeRoot, fmt.Sprintf("inspect link %s: %v", raw, err))
				}
				continue
			}
			if strings.HasPrefix(resolved, "sources/") {
				report.Citations++
				if anchor == "" {
					report.add(relativeRoot, "source citation has no message anchor")
					continue
				}
				known, exists := sourceAnchors[resolved]
				if !exists {
					report.add(relativeRoot, "citation targets an unregistered source")
					continue
				}
				if _, exists := known[anchor]; !exists {
					report.add(relativeRoot, "unknown source anchor "+anchor)
				}
				continue
			}
			if strings.HasPrefix(resolved, "wiki/") {
				links[relativeRoot] = append(links[relativeRoot], resolved)
			}
		}
		return nil
	})
	if walkErr != nil {
		report.add("wiki", fmt.Sprintf("walk wiki: %v", walkErr))
	}
	return pages, links
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
