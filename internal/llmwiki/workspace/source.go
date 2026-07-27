package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var safeNamePattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type SessionReader func(context.Context, string) ([]byte, error)

type BuildConfig struct {
	Root        string
	PaxmBinary  string
	ReadSession SessionReader
}

func Build(
	ctx context.Context,
	config BuildConfig,
	request BuildRequest,
) (BuildResult, error) {
	root := strings.TrimSpace(config.Root)
	if root == "" {
		return BuildResult{}, errors.New("workspace root is required")
	}
	reader := config.ReadSession
	if reader == nil {
		reader = paxmReader(config.PaxmBinary)
	}
	encoded, err := reader(ctx, strings.TrimSpace(request.SessionID))
	if err != nil {
		return BuildResult{}, fmt.Errorf("export native session: %w", err)
	}
	part, err := DecodeSessionPart(encoded, request)
	if err != nil {
		return BuildResult{}, err
	}
	if err := ensureScaffold(root); err != nil {
		return BuildResult{}, err
	}
	record, err := writeSource(root, part)
	if err != nil {
		return BuildResult{}, err
	}
	if err := appendManifest(root, record); err != nil {
		return BuildResult{}, err
	}
	return BuildResult{
		Source:       record,
		TurnCount:    part.TurnEnd - part.TurnStart,
		MessageCount: len(part.Messages),
	}, nil
}

func DecodeSessionPart(encoded []byte, request BuildRequest) (SessionPart, error) {
	var exported SessionExport
	if err := json.Unmarshal(encoded, &exported); err != nil {
		return SessionPart{}, fmt.Errorf("decode paxm session export: %w", err)
	}
	if exported.SchemaVersion != PaxmSessionSchema {
		return SessionPart{}, fmt.Errorf(
			"unsupported paxm session export schema %q",
			exported.SchemaVersion,
		)
	}
	requestedID := strings.TrimSpace(request.SessionID)
	exportedID := strings.TrimSpace(exported.SessionID)
	if requestedID == "" {
		return SessionPart{}, errors.New("session ID is required")
	}
	if exportedID != requestedID {
		return SessionPart{}, fmt.Errorf(
			"exported session %q does not match requested session %q",
			exportedID,
			requestedID,
		)
	}
	if request.TurnStart < 0 ||
		request.TurnEnd <= request.TurnStart ||
		request.TurnEnd > len(exported.Turns) {
		return SessionPart{}, fmt.Errorf(
			"turn range [%d,%d) is invalid for %d turns",
			request.TurnStart,
			request.TurnEnd,
			len(exported.Turns),
		)
	}
	part := SessionPart{
		SessionID: exportedID,
		Agent:     strings.TrimSpace(exported.Agent),
		Workspace: strings.TrimSpace(exported.Workspace),
		TurnStart: request.TurnStart,
		TurnEnd:   request.TurnEnd,
		Messages:  make([]SourceMessage, 0, (request.TurnEnd-request.TurnStart)*2),
	}
	seenTurns := make(map[string]struct{}, request.TurnEnd-request.TurnStart)
	for _, turn := range exported.Turns[request.TurnStart:request.TurnEnd] {
		turnID := strings.TrimSpace(turn.ID)
		if turnID == "" {
			return SessionPart{}, errors.New("session export contains a turn without an ID")
		}
		if _, exists := seenTurns[turnID]; exists {
			return SessionPart{}, fmt.Errorf("session export contains duplicate turn %q", turnID)
		}
		seenTurns[turnID] = struct{}{}
		for _, message := range []struct {
			role    string
			content string
		}{
			{role: "user", content: turn.User},
			{role: "assistant", content: turn.Assistant},
		} {
			if strings.TrimSpace(message.content) == "" {
				continue
			}
			part.Messages = append(part.Messages, SourceMessage{
				Anchor:    messageAnchor(turnID, message.role),
				TurnID:    turnID,
				Role:      message.role,
				Content:   message.content,
				CreatedAt: turn.CreatedAt.UTC(),
			})
		}
	}
	if len(part.Messages) == 0 {
		return SessionPart{}, errors.New("selected session part has no messages")
	}
	return part, nil
}

func paxmReader(binary string) SessionReader {
	if strings.TrimSpace(binary) == "" {
		binary = "paxm"
	}
	return func(ctx context.Context, sessionID string) ([]byte, error) {
		command := exec.CommandContext(
			ctx,
			binary,
			"backfill",
			"export",
			"--agent",
			"codex",
			"--session",
			sessionID,
			"--json",
		)
		output, err := command.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf(
				"run paxm export: %w: %s",
				err,
				strings.TrimSpace(string(output)),
			)
		}
		return output, nil
	}
}

func ensureScaffold(root string) error {
	for _, directory := range []string{
		"sources",
		"wiki",
		"wiki/portals",
		"wiki/topics",
		"wiki/pages",
		"wiki/pages/people",
		"wiki/pages/journeys",
		"wiki/pages/projects",
		"wiki/events",
		"wiki/timelines",
		".pax",
		".pax/runs",
	} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			return fmt.Errorf("create workspace directory %s: %w", directory, err)
		}
	}
	files := map[string]string{
		"AGENTS.md":  agentsInstructions,
		".gitignore": ".pax/base.json\n",
		"wiki/index.md": "---\ntype: portal\n---\n\n# Wiki\n\n" +
			"Start here to browse people, ongoing stories, themes, and meaningful changes.\n",
		"wiki/log.md":            "# Maintenance log\n",
		".pax/base.json":         "{\n  \"revision\": \"\"\n}\n",
		".pax/editorial-profile": "article-first-v1\n",
	}
	for relative, content := range files {
		target := filepath.Join(root, filepath.FromSlash(relative))
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect scaffold %s: %w", relative, err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write scaffold %s: %w", relative, err)
		}
	}
	return nil
}

func writeSource(root string, part SessionPart) (
	record SourceRecord,
	resultErr error,
) {
	var rendered strings.Builder
	fmt.Fprintf(
		&rendered,
		"# Immutable Session Source\n\n"+
			"- Session: `%s`\n"+
			"- Agent: `%s`\n"+
			"- Original workspace: `%s`\n"+
			"- Turn range: `[%d,%d)`\n\n",
		part.SessionID,
		part.Agent,
		part.Workspace,
		part.TurnStart,
		part.TurnEnd,
	)
	anchors := make([]SourceAnchor, 0, len(part.Messages))
	for _, message := range part.Messages {
		fmt.Fprintf(
			&rendered,
			"<a id=\"%s\"></a>\n## %s · `%s`\n\n",
			message.Anchor,
			message.Role,
			message.TurnID,
		)
		if !message.CreatedAt.IsZero() {
			fmt.Fprintf(&rendered, "_Created at %s_\n\n", message.CreatedAt.Format(timeFormat))
		}
		rendered.WriteString(message.Content)
		if !strings.HasSuffix(message.Content, "\n") {
			rendered.WriteByte('\n')
		}
		rendered.WriteByte('\n')
		anchors = append(anchors, SourceAnchor{
			ID:        message.Anchor,
			TurnID:    message.TurnID,
			Role:      message.Role,
			CreatedAt: message.CreatedAt,
		})
	}
	content := []byte(rendered.String())
	digest := sha256.Sum256(content)
	name := fmt.Sprintf(
		"%s-turns-%04d-%04d.md",
		sanitizeName(part.SessionID),
		part.TurnStart+1,
		part.TurnEnd,
	)
	relative := filepath.ToSlash(filepath.Join("sources", name))
	target := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.Chmod(filepath.Dir(target), 0o755); err != nil {
		return SourceRecord{}, fmt.Errorf("unlock sources directory: %w", err)
	}
	defer func() {
		if err := os.Chmod(filepath.Dir(target), 0o555); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("protect sources directory: %w", err))
		}
	}()
	if existing, err := os.ReadFile(target); err == nil {
		if string(existing) != string(content) {
			return SourceRecord{}, fmt.Errorf(
				"immutable source %s already exists with different bytes",
				relative,
			)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return SourceRecord{}, fmt.Errorf("read existing source %s: %w", relative, err)
	} else {
		if err := os.WriteFile(target, content, 0o444); err != nil {
			return SourceRecord{}, fmt.Errorf("write immutable source %s: %w", relative, err)
		}
	}
	if err := os.Chmod(target, 0o444); err != nil {
		return SourceRecord{}, fmt.Errorf("protect immutable source %s: %w", relative, err)
	}
	return SourceRecord{
		SessionID: part.SessionID,
		TurnStart: part.TurnStart,
		TurnEnd:   part.TurnEnd,
		Path:      relative,
		SHA256:    hex.EncodeToString(digest[:]),
		Bytes:     len(content),
		Anchors:   anchors,
	}, nil
}

func appendManifest(root string, record SourceRecord) error {
	target := filepath.Join(root, ".pax", "manifest.json")
	manifest := Manifest{SchemaVersion: manifestSchema}
	if encoded, err := os.ReadFile(target); err == nil {
		if err := json.Unmarshal(encoded, &manifest); err != nil {
			return fmt.Errorf("decode workspace manifest: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read workspace manifest: %w", err)
	}
	for _, existing := range manifest.Sources {
		if existing.Path == record.Path {
			if existing.SHA256 != record.SHA256 {
				return fmt.Errorf("manifest source %s changed", record.Path)
			}
			return nil
		}
		if existing.SessionID == record.SessionID &&
			record.TurnStart < existing.TurnEnd &&
			existing.TurnStart < record.TurnEnd {
			return fmt.Errorf(
				"source turn range [%d,%d) overlaps existing range [%d,%d)",
				record.TurnStart,
				record.TurnEnd,
				existing.TurnStart,
				existing.TurnEnd,
			)
		}
	}
	manifest.Sources = append(manifest.Sources, record)
	sort.Slice(manifest.Sources, func(left, right int) bool {
		if manifest.Sources[left].SessionID != manifest.Sources[right].SessionID {
			return manifest.Sources[left].SessionID < manifest.Sources[right].SessionID
		}
		return manifest.Sources[left].TurnStart < manifest.Sources[right].TurnStart
	})
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workspace manifest: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(target, encoded, 0o644); err != nil {
		return fmt.Errorf("write workspace manifest: %w", err)
	}
	return nil
}

func messageAnchor(turnID, role string) string {
	digest := sha256.Sum256([]byte(turnID + ":" + role))
	return "msg-" + hex.EncodeToString(digest[:8])
}

func StableMessageAnchor(turnID, role string) string {
	return messageAnchor(turnID, role)
}

func sanitizeName(value string) string {
	result := strings.Trim(safeNamePattern.ReplaceAllString(value, "-"), "-.")
	if result == "" {
		return "session"
	}
	return result
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

const agentsInstructions = `# LLM Wiki workspace

This is a private maintenance workspace. Read this file before editing.

## Authority and safety

- Treat every file under sources/ as immutable evidence. Never edit, move, delete,
  rename, chmod, or replace a Source.
- Edit only Markdown files under wiki/.
- Freely split, merge, rename, move, and delete wiki pages when that improves the
  whole Wiki. Repair every affected internal link.
- Prefer updating an existing page over creating a duplicate page.
- Cite claims precisely with Markdown links to a message anchor, for example:
  [source](../../sources/<source-file>.md#msg-0123456789abcdef).
- Never invent a citation. Read the cited Source message and make sure it supports
  the nearby claim.
- Do not read or expose evaluator answers, gold labels, or private scoring data.

## Editorial mission

Build an article-first living Wiki, not a collection of Session summaries or
topic buckets. Every substantive page must have one stable subject and answer a
clear reader question. Synthesize repeated evidence; do not copy a conversation
or collect near-duplicate quotations.

- Person pages explain who someone is, their current state, relationships, and
  durable life threads.
- Journey and project pages explain a changing goal through current status,
  milestones, support, blockers, and meaningful development over time.
- Topic and concept pages synthesize a subject across people and events.
- Event pages exist only for consequential events referenced from several pages.
- Timeline pages carry chronology so ordinary articles do not use Session order.
- Portal pages orient readers with a short mental model and curated entry paths;
  they are not exhaustive file listings.

Create a page only when its subject recurs across Sources, represents an important
ongoing thread, or acts as a useful bridge among several pages. Otherwise keep it
as a section of an existing article.

## Article contract

Every page except wiki/log.md begins with minimal frontmatter:

---
type: portal | person | topic | concept | journey | project | event | timeline | place | organization
aliases: [optional names]
---

Then write:

1. exactly one H1;
2. a two-to-four sentence prose lead before the first H2 that identifies the
   subject, significance, and current state where applicable;
3. sections organized by reader meaning, never headings such as "Session 1";
4. contextual links in the relevant prose, not only a generic related-pages list;
5. precise Source citations for substantive claims.

Use summary style: keep an overview readable and move substantial subtopics into
standalone articles only when they satisfy the page-creation rule. Separate
current state from historical development. Avoid repeating the same prose across
person, journey, and topic pages. If a page has a Related pages section, keep it
as the final H2 section.

## Navigation and evolution

- Maintain wiki/index.md as a real home page with curated paths for people,
  ongoing stories, themes, timeline, and recently changed knowledge as relevant.
- Use portal pages for a stable primary tree no deeper than three clicks.
- Use inline links and backlinks for cross-cutting relationships.
- Keep wiki/log.md as a concise append-only maintenance summary, never a Source
  transcript.
- Read an established page before editing it. Use replace_text for precise
  changes so unrelated sections survive. Use write_file for new pages and small
  scaffolds.
- When new Sources arrive, update current snapshots and existing journeys before
  creating pages. Preserve still-supported prose and explicitly reconcile changed
  or contradictory evidence.

Before finishing, run the validator through the provided validate tool and resolve
every reported error.
`
