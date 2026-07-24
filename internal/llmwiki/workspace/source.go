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
		"wiki/topics",
		"wiki/pages",
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
		"wiki/index.md": "# Wiki\n\n" +
			"This topic tree is maintained from immutable Session sources.\n",
		"wiki/log.md":    "# Maintenance log\n",
		".pax/base.json": "{\n  \"revision\": \"\"\n}\n",
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

func writeSource(root string, part SessionPart) (SourceRecord, error) {
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
		_ = os.Chmod(filepath.Dir(target), 0o555)
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

## Contract

- Treat every file under sources/ as immutable evidence. Never edit, move, delete,
  rename, chmod, or replace a Source.
- Edit only Markdown files under wiki/.
- Maintain wiki/index.md as the human topic tree. It must link to every major page.
- Organize durable knowledge by topic, not by Session chronology.
- Freely split, merge, rename, move, and delete wiki pages when that improves the
  whole Wiki. Repair every affected internal link.
- Prefer updating an existing page over creating a duplicate page.
- Put topic navigation under wiki/topics/ and substantive pages under wiki/pages/.
- Cite claims precisely with Markdown links to a message anchor, for example:
  [source](../../sources/<source-file>.md#msg-0123456789abcdef).
- Never invent a citation. Read the cited Source message and make sure it supports
  the nearby claim.
- Keep wiki/log.md as a concise maintenance summary. Do not turn it into a Session
  transcript.
- Do not read or expose evaluator answers, gold labels, or private scoring data.

Before finishing, run the validator through the provided validate tool and resolve
every reported error.
`
