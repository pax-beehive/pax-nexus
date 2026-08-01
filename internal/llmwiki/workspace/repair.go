package workspace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type LinkRepairReport struct {
	Files int `json:"files"`
	Links int `json:"links"`
}

// RepairResolvableInternalLinks removes only excess parent-directory prefixes
// when the corrected Wiki target resolves uniquely. It does not modify Source
// citations, external links, anchors, or prose.
func RepairResolvableInternalLinks(root string) (LinkRepairReport, error) {
	var report LinkRepairReport
	wikiRoot := filepath.Join(root, "wiki")
	err := filepath.WalkDir(
		wikiRoot,
		func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read Wiki page for link repair: %w", err)
			}
			links := 0
			updated := markdownLinkPattern.ReplaceAllStringFunc(
				string(content),
				func(markdownLink string) string {
					match := markdownLinkPattern.FindStringSubmatch(markdownLink)
					if len(match) < 2 {
						return markdownLink
					}
					repaired, ok := repairInternalLinkTarget(root, path, match[1])
					if !ok {
						return markdownLink
					}
					links++
					return strings.Replace(markdownLink, match[1], repaired, 1)
				},
			)
			if links == 0 {
				return nil
			}
			if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
				return fmt.Errorf("write repaired Wiki page: %w", err)
			}
			report.Files++
			report.Links += links
			return nil
		},
	)
	if err != nil {
		return LinkRepairReport{}, fmt.Errorf("repair Wiki internal links: %w", err)
	}
	return report, nil
}

func repairInternalLinkTarget(root, currentFile, raw string) (string, bool) {
	if isExternalLink(raw) || strings.Contains(raw, "sources/") {
		return "", false
	}
	pathPart, anchor, _ := strings.Cut(raw, "#")
	if pathPart == "" {
		return "", false
	}
	resolved, err := resolveLink(root, currentFile, pathPart)
	if err == nil && regularFile(filepath.Join(root, filepath.FromSlash(resolved))) {
		return "", false
	}
	trimmed := pathPart
	for strings.HasPrefix(trimmed, "../") {
		trimmed = strings.TrimPrefix(trimmed, "../")
	}
	if trimmed == pathPart || trimmed == "" {
		return "", false
	}
	candidates := []string{
		filepath.Clean(filepath.Join(filepath.Dir(currentFile), filepath.FromSlash(trimmed))),
		filepath.Clean(filepath.Join(root, "wiki", filepath.Base(filepath.FromSlash(trimmed)))),
	}
	var selected string
	for _, candidate := range candidates {
		if !withinWiki(root, candidate) || !regularFile(candidate) {
			continue
		}
		if selected != "" && selected != candidate {
			return "", false
		}
		selected = candidate
	}
	if selected == "" {
		return "", false
	}
	relative, err := filepath.Rel(filepath.Dir(currentFile), selected)
	if err != nil {
		return "", false
	}
	repaired := filepath.ToSlash(relative)
	if anchor != "" {
		repaired += "#" + anchor
	}
	return repaired, true
}

func withinWiki(root, target string) bool {
	wikiRoot := filepath.Clean(filepath.Join(root, "wiki"))
	relative, err := filepath.Rel(wikiRoot, target)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
