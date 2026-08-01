package workspace

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

type AgentConfig struct {
	Root                     string
	Model                    string
	MaxRounds                int
	Client                   llm.ChatClient
	Now                      func() time.Time
	ValidationAfterToolRound bool
}

const DefaultMaintenanceInstruction = `Maintain the whole human Wiki from every
immutable Source in this workspace. Integrate new evidence into existing
knowledge instead of generating a Session transcript, broad topic dossier, or
duplicate Wiki. Create article-first person, journey, topic, timeline, and
portal pages with strong leads, current state, summary-style subtopics,
contextual cross-links, and exact message-anchor citations. Use precise edits
on established pages and preserve unrelated supported content.`

type AgentRequest struct {
	RunID       string
	Instruction string
}

type RunAudit struct {
	RunID         string           `json:"run_id"`
	Model         string           `json:"model"`
	StartedAt     time.Time        `json:"started_at"`
	FinishedAt    time.Time        `json:"finished_at"`
	DurationMS    int64            `json:"duration_ms"`
	Calls         int              `json:"calls"`
	ToolCalls     int              `json:"tool_calls"`
	InputTokens   int              `json:"input_tokens"`
	OutputTokens  int              `json:"output_tokens"`
	FailureReason string           `json:"failure_reason,omitempty"`
	Validation    ValidationReport `json:"validation"`
}

type AgentResult struct {
	Audit      RunAudit
	Validation ValidationReport
}

type RoundLimitError struct {
	MaxRounds  int
	Validation ValidationReport
}

func (e *RoundLimitError) Error() string {
	return fmt.Sprintf(
		"agent exhausted %d rounds with invalid Wiki: %s",
		e.MaxRounds,
		e.Validation.String(),
	)
}

func RunAgent(
	ctx context.Context,
	config AgentConfig,
	request AgentRequest,
) (AgentResult, error) {
	config = defaultAgentConfig(config)
	if err := validateAgentConfig(config, request); err != nil {
		return AgentResult{}, err
	}
	audit := RunAudit{
		RunID:     request.RunID,
		Model:     config.Model,
		StartedAt: config.Now().UTC(),
	}
	messages := []llm.ChatMessage{
		{Role: "system", Content: agentSystemPrompt},
		{Role: "user", Content: strings.TrimSpace(request.Instruction)},
	}
	tools := agentTools()
	var runErr error
	for round := 0; round < config.MaxRounds; round++ {
		var done bool
		messages, done, runErr = runAgentRound(ctx, config, tools, messages, &audit)
		if runErr != nil || done {
			break
		}
	}
	if runErr == nil && !audit.Validation.Valid {
		audit.Validation = Validate(config.Root)
		if !audit.Validation.Valid {
			runErr = &RoundLimitError{
				MaxRounds: config.MaxRounds, Validation: audit.Validation,
			}
		}
	}
	if runErr != nil {
		audit.FailureReason = runErr.Error()
	}
	audit.FinishedAt = config.Now().UTC()
	audit.DurationMS = audit.FinishedAt.Sub(audit.StartedAt).Milliseconds()
	if err := writeAudit(config.Root, audit); err != nil {
		if runErr != nil {
			return AgentResult{Audit: audit, Validation: audit.Validation}, errors.Join(runErr, err)
		}
		return AgentResult{Audit: audit, Validation: audit.Validation}, err
	}
	result := AgentResult{Audit: audit, Validation: audit.Validation}
	if runErr != nil {
		return result, runErr
	}
	return result, nil
}

func runAgentRound(
	ctx context.Context,
	config AgentConfig,
	tools []llm.ToolDefinition,
	messages []llm.ChatMessage,
	audit *RunAudit,
) ([]llm.ChatMessage, bool, error) {
	audit.Calls++
	response, err := config.Client.Complete(ctx, llm.ChatRequest{
		Model: config.Model, Messages: messages, Tools: tools,
	})
	if err != nil {
		return messages, false, fmt.Errorf("DeepSeek completion %d: %w", audit.Calls, err)
	}
	audit.InputTokens += response.Usage.InputTokens
	audit.OutputTokens += response.Usage.OutputTokens
	messages = append(messages, response.Message)
	if len(response.Message.ToolCalls) == 0 {
		return validateAgentWorkspace(config.Root, messages, audit)
	}
	for _, call := range response.Message.ToolCalls {
		audit.ToolCalls++
		messages = append(messages, llm.ChatMessage{
			Role:       "tool",
			Content:    executeTool(config.Root, call),
			ToolCallID: call.ID,
		})
	}
	if !config.ValidationAfterToolRound {
		return messages, false, nil
	}
	return validateAgentWorkspace(config.Root, messages, audit)
}

func validateAgentWorkspace(
	root string,
	messages []llm.ChatMessage,
	audit *RunAudit,
) ([]llm.ChatMessage, bool, error) {
	audit.Validation = Validate(root)
	if audit.Validation.Valid {
		return messages, true, nil
	}
	return append(messages, llm.ChatMessage{
		Role: "user", Content: validationRepairMessage(audit.Validation),
	}), false, nil
}

func validationRepairMessage(validation ValidationReport) string {
	return "The deterministic validator failed. Repair the Wiki and " +
		"run validate before finishing:\n" + validation.String()
}

func defaultAgentConfig(config AgentConfig) AgentConfig {
	if strings.TrimSpace(config.Model) == "" {
		config.Model = "deepseek-v4-pro"
	}
	if config.MaxRounds <= 0 {
		config.MaxRounds = 30
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return config
}

func validateAgentConfig(config AgentConfig, request AgentRequest) error {
	if strings.TrimSpace(config.Root) == "" {
		return errors.New("agent workspace root is required")
	}
	if config.Client == nil {
		return errors.New("agent chat client is required")
	}
	if strings.TrimSpace(request.RunID) == "" {
		return errors.New("agent run ID is required")
	}
	if sanitizeName(request.RunID) != request.RunID {
		return errors.New("agent run ID must use only letters, numbers, dot, dash, or underscore")
	}
	if strings.TrimSpace(request.Instruction) == "" {
		return errors.New("agent instruction is required")
	}
	return nil
}

func writeAudit(root string, audit RunAudit) error {
	encoded, err := json.MarshalIndent(audit, "", "  ")
	if err != nil {
		return fmt.Errorf("encode run audit: %w", err)
	}
	encoded = append(encoded, '\n')
	directory := filepath.Join(root, ".pax", "runs")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create run audit directory: %w", err)
	}
	target := filepath.Join(directory, audit.RunID+".json")
	if err := os.WriteFile(target, encoded, 0o644); err != nil {
		return fmt.Errorf("write run audit: %w", err)
	}
	return nil
}

func executeTool(root string, call llm.ToolCall) string {
	var value any
	var err error
	switch call.Function.Name {
	case "list_files":
		value, err = toolListFiles(root, call.Function.Arguments)
	case "read_file":
		value, err = toolReadFile(root, call.Function.Arguments)
	case "grep":
		value, err = toolGrep(root, call.Function.Arguments)
	case "write_file":
		value, err = toolWriteFile(root, call.Function.Arguments)
	case "replace_text":
		value, err = toolReplaceText(root, call.Function.Arguments)
	case "move_file":
		value, err = toolMoveFile(root, call.Function.Arguments)
	case "delete_file":
		value, err = toolDeleteFile(root, call.Function.Arguments)
	case "validate":
		value = Validate(root)
	default:
		err = fmt.Errorf("unknown tool %q", call.Function.Name)
	}
	result := map[string]any{"ok": err == nil}
	if err != nil {
		result["error"] = err.Error()
	} else {
		result["result"] = value
	}
	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return `{"ok":false,"error":"encode tool result"}`
	}
	return string(encoded)
}

func toolListFiles(root, arguments string) ([]string, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeToolArguments(arguments, &input); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Path) == "" {
		input.Path = "."
	}
	target, relative, err := resolveAgentReadPath(root, input.Path)
	if err != nil {
		return nil, err
	}
	var result []string
	err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		pathRelative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		pathRelative = filepath.ToSlash(pathRelative)
		if entry.IsDir() &&
			(pathRelative == ".git" || strings.HasPrefix(pathRelative, ".git/") ||
				pathRelative == ".pax/runs") {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			result = append(result, pathRelative)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", relative, err)
	}
	sort.Strings(result)
	if len(result) > 500 {
		result = result[:500]
	}
	return result, nil
}

func toolReadFile(root, arguments string) (string, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeToolArguments(arguments, &input); err != nil {
		return "", err
	}
	target, _, err := resolveAgentReadPath(root, input.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return "", errors.New("read_file requires a file")
	}
	if info.Size() > 2<<20 {
		return "", errors.New("file exceeds 2 MiB read limit")
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	return string(content), nil
}

func toolGrep(root, arguments string) ([]string, error) {
	var input struct {
		Query string `json:"query"`
		Path  string `json:"path"`
	}
	if err := decodeToolArguments(arguments, &input); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Query) == "" {
		return nil, errors.New("grep query is required")
	}
	if strings.TrimSpace(input.Path) == "" {
		input.Path = "."
	}
	target, _, err := resolveAgentReadPath(root, input.Path)
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(input.Query)
	var matches []string
	err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.Size() > 2<<20 {
			return nil
		}
		return grepFile(root, path, query, &matches)
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return nil, fmt.Errorf("grep workspace: %w", err)
	}
	return matches, nil
}

func grepFile(root, path, query string, matches *[]string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	line := 0
	for scanner.Scan() {
		line++
		if !strings.Contains(strings.ToLower(scanner.Text()), query) {
			continue
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			closeErr := file.Close()
			return errors.Join(relErr, closeErr)
		}
		*matches = append(*matches, fmt.Sprintf(
			"%s:%d:%s",
			filepath.ToSlash(relative),
			line,
			scanner.Text(),
		))
		if len(*matches) >= 200 {
			closeErr := file.Close()
			return errors.Join(fs.SkipAll, closeErr)
		}
	}
	scanErr := scanner.Err()
	closeErr := file.Close()
	return errors.Join(scanErr, closeErr)
}

func toolWriteFile(root, arguments string) (string, error) {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeToolArguments(arguments, &input); err != nil {
		return "", err
	}
	target, relative, err := resolveAgentWritePath(root, input.Path)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(target); statErr == nil && info.Size() >= 256 {
		return "", errors.New(
			"existing substantial pages must use replace_text to preserve unrelated content",
		)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect existing wiki file: %w", statErr)
	}
	return writeWikiCandidate(root, target, relative, []byte(input.Content))
}

func toolReplaceText(root, arguments string) (string, error) {
	var input struct {
		Path                string `json:"path"`
		OldText             string `json:"old_text"`
		NewText             string `json:"new_text"`
		ExpectedOccurrences int    `json:"expected_occurrences"`
	}
	if err := decodeToolArguments(arguments, &input); err != nil {
		return "", err
	}
	if input.OldText == "" {
		return "", errors.New("replace_text old_text is required")
	}
	if input.ExpectedOccurrences == 0 {
		input.ExpectedOccurrences = 1
	}
	if input.ExpectedOccurrences < 1 {
		return "", errors.New("replace_text expected_occurrences must be positive")
	}
	target, relative, err := resolveAgentWritePath(root, input.Path)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return "", fmt.Errorf("read wiki file for precise edit: %w", err)
	}
	if len(content) > 2<<20 {
		return "", errors.New("file exceeds 2 MiB edit limit")
	}
	actual := strings.Count(string(content), input.OldText)
	if actual != input.ExpectedOccurrences {
		return "", fmt.Errorf(
			"replace_text found %d occurrences, expected %d",
			actual,
			input.ExpectedOccurrences,
		)
	}
	updated := strings.Replace(
		string(content),
		input.OldText,
		input.NewText,
		input.ExpectedOccurrences,
	)
	return writeWikiCandidate(root, target, relative, []byte(updated))
}

func writeWikiCandidate(
	root,
	target,
	relative string,
	content []byte,
) (string, error) {
	if err := validateCandidateCitations(
		root,
		target,
		relative,
		content,
	); err != nil {
		return "", fmt.Errorf("reject invalid Wiki write: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create wiki directory: %w", err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		return "", fmt.Errorf("write wiki file: %w", err)
	}
	return relative, nil
}

func toolMoveFile(root, arguments string) (string, error) {
	var input struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decodeToolArguments(arguments, &input); err != nil {
		return "", err
	}
	from, _, err := resolveAgentWritePath(root, input.From)
	if err != nil {
		return "", err
	}
	to, relative, err := resolveAgentWritePath(root, input.To)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return "", fmt.Errorf("create destination directory: %w", err)
	}
	if err := os.Rename(from, to); err != nil {
		return "", fmt.Errorf("move wiki file: %w", err)
	}
	return relative, nil
}

func toolDeleteFile(root, arguments string) (string, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeToolArguments(arguments, &input); err != nil {
		return "", err
	}
	target, relative, err := resolveAgentWritePath(root, input.Path)
	if err != nil {
		return "", err
	}
	if err := os.Remove(target); err != nil {
		return "", fmt.Errorf("delete wiki file: %w", err)
	}
	return relative, nil
}

func decodeToolArguments(arguments string, target any) error {
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode tool arguments: %w", err)
	}
	return nil
}

func resolveAgentReadPath(root, input string) (string, string, error) {
	target, relative, err := secureWorkspacePath(root, input)
	if err != nil {
		return "", "", err
	}
	allowed := relative == "." ||
		relative == "AGENTS.md" ||
		relative == ".pax/base.json" ||
		relative == ".pax/manifest.json" ||
		relative == "sources" ||
		strings.HasPrefix(relative, "sources/") ||
		relative == "wiki" ||
		strings.HasPrefix(relative, "wiki/")
	if !allowed {
		return "", "", errors.New("reads are restricted to AGENTS.md, sources/, wiki/, and public .pax metadata")
	}
	evaluated, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", "", fmt.Errorf("resolve read path: %w", err)
	}
	if err := ensureWithinRoot(root, evaluated); err != nil {
		return "", "", err
	}
	return evaluated, relative, nil
}

func resolveAgentWritePath(root, input string) (string, string, error) {
	target, relative, err := secureWorkspacePath(root, input)
	if err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(relative, "wiki/") ||
		!strings.EqualFold(filepath.Ext(relative), ".md") {
		return "", "", errors.New("writes are restricted to wiki/*.md")
	}
	if err := rejectSymlinkComponents(root, relative); err != nil {
		return "", "", err
	}
	return target, relative, nil
}

func secureWorkspacePath(root, input string) (string, string, error) {
	if strings.TrimSpace(input) == "" {
		return "", "", errors.New("tool path is required")
	}
	if filepath.IsAbs(input) {
		return "", "", errors.New("absolute tool paths are forbidden")
	}
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace root: %w", err)
	}
	target := filepath.Clean(filepath.Join(rootAbsolute, filepath.FromSlash(input)))
	if err := ensureWithinRoot(rootAbsolute, target); err != nil {
		return "", "", err
	}
	relative, err := filepath.Rel(rootAbsolute, target)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace path: %w", err)
	}
	relative = filepath.ToSlash(relative)
	if relative == ".git" || strings.HasPrefix(relative, ".git/") {
		return "", "", errors.New("git internals are unavailable to the agent")
	}
	return target, relative, nil
}

func ensureWithinRoot(root, target string) error {
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	targetAbsolute, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve tool target: %w", err)
	}
	if within(rootAbsolute, targetAbsolute) {
		return nil
	}
	evaluatedRoot, rootErr := filepath.EvalSymlinks(rootAbsolute)
	evaluatedTarget, targetErr := filepath.EvalSymlinks(targetAbsolute)
	if rootErr == nil && targetErr == nil && within(evaluatedRoot, evaluatedTarget) {
		return nil
	}
	return errors.New("tool path escapes workspace")
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func rejectSymlinkComponents(root, relative string) error {
	current := root
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect wiki path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlinks are forbidden in agent write paths")
		}
	}
	return nil
}

func agentTools() []llm.ToolDefinition {
	object := func(properties map[string]any, required ...string) map[string]any {
		result := map[string]any{
			"type":                 "object",
			"properties":           properties,
			"additionalProperties": false,
		}
		if len(required) > 0 {
			result["required"] = required
		}
		return result
	}
	stringProperty := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return []llm.ToolDefinition{
		{
			Type: "function",
			Function: llm.ToolFunctionSchema{
				Name:        "list_files",
				Description: "List readable workspace files recursively.",
				Parameters: object(map[string]any{
					"path": stringProperty("Relative directory, or dot for the workspace."),
				}),
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunctionSchema{
				Name:        "read_file",
				Description: "Read AGENTS.md, immutable sources, or Wiki Markdown.",
				Parameters: object(map[string]any{
					"path": stringProperty("Relative file path."),
				}, "path"),
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunctionSchema{
				Name:        "grep",
				Description: "Case-insensitive literal text search over readable files.",
				Parameters: object(map[string]any{
					"query": stringProperty("Literal text to find."),
					"path":  stringProperty("Relative file or directory; defaults to dot."),
				}, "query"),
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunctionSchema{
				Name:        "write_file",
				Description: "Create a Wiki Markdown file or replace a small scaffold. Use replace_text for an established page.",
				Parameters: object(map[string]any{
					"path":    stringProperty("Relative wiki/*.md path."),
					"content": stringProperty("Complete Markdown file content."),
				}, "path", "content"),
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunctionSchema{
				Name:        "replace_text",
				Description: "Precisely edit an existing Wiki page by replacing an exact text span while retaining all unrelated content.",
				Parameters: object(map[string]any{
					"path":     stringProperty("Existing relative wiki/*.md path."),
					"old_text": stringProperty("Exact text to replace."),
					"new_text": stringProperty("Replacement text."),
					"expected_occurrences": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"description": "Exact match count; defaults to one.",
					},
				}, "path", "old_text", "new_text"),
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunctionSchema{
				Name:        "move_file",
				Description: "Move or rename one Markdown file within wiki/.",
				Parameters: object(map[string]any{
					"from": stringProperty("Existing relative wiki/*.md path."),
					"to":   stringProperty("New relative wiki/*.md path."),
				}, "from", "to"),
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunctionSchema{
				Name:        "delete_file",
				Description: "Delete one Markdown file under wiki/.",
				Parameters: object(map[string]any{
					"path": stringProperty("Relative wiki/*.md path."),
				}, "path"),
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunctionSchema{
				Name:        "validate",
				Description: "Run deterministic source, link, citation, and reachability checks.",
				Parameters:  object(map[string]any{}),
			},
		},
	}
}

const agentSystemPrompt = `You are the filesystem maintainer for a local-first human Wiki.
Start by reading AGENTS.md, listing the workspace, and inspecting the immutable
Sources and existing Wiki. Work globally across the Markdown tree: update,
merge, split, rename, move, link, or delete Wiki pages as needed. Organize by
durable topic instead of Session chronology. Every evidence-based claim needs a
precise Source message citation. Never duplicate an existing Wiki just because
new Source material arrived. Use replace_text for established pages so unrelated
sections survive incremental maintenance; reserve write_file for new pages and
small scaffolds. Use the provided filesystem tools only. Run the deterministic
validator and repair all errors before finishing. Write all generated Wiki
titles, summaries, headings, and prose in English, while preserving proper nouns
and verbatim Source quotations in their original language.`
