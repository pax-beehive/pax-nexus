package llmwiki

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
	"slices"
	"strings"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

type DirectoryBuilder struct {
	store        *knowledgeeval.ArtifactStore
	now          func() time.Time
	codeRevision string
}

func NewDirectoryBuilder(
	store *knowledgeeval.ArtifactStore,
	now func() time.Time,
	codeRevision string,
) (*DirectoryBuilder, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: artifact store is required", knowledgeeval.ErrInvalidRecord)
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &DirectoryBuilder{store: store, now: now, codeRevision: codeRevision}, nil
}

func (b *DirectoryBuilder) Descriptor() knowledgeeval.BuilderDescriptor {
	return knowledgeeval.BuilderDescriptor{ID: "llmwiki-directory", Version: "v1"}
}

func (b *DirectoryBuilder) Build(
	ctx context.Context,
	request knowledgeeval.BuildRequest,
) (knowledgeeval.ArtifactRecord, error) {
	root, err := b.store.OpenDirectory(ctx, request.Group.BuildInput)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, fmt.Errorf("open LLM Wiki build input: %w", err)
	}
	payload, err := b.store.PutDirectory(ctx, ArtifactKind, ArtifactSchema, root)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, fmt.Errorf("publish LLM Wiki artifact: %w", err)
	}
	baseID := ""
	if request.BaseArtifact != nil {
		baseID = request.BaseArtifact.ArtifactID
	}
	descriptor := b.Descriptor()
	record := knowledgeeval.ArtifactRecord{
		ArtifactID: stableID(
			request.Group.WorldID,
			request.Group.GroupID,
			request.Group.CheckpointID,
			payload.SHA256,
		),
		Kind: ArtifactKind, WorldID: request.Group.WorldID,
		GroupID: request.Group.GroupID, CheckpointID: request.Group.CheckpointID,
		BaseID: baseID, Payload: payload, CreatedAt: b.now(),
		Provenance: knowledgeeval.Provenance{
			BuilderID: descriptor.ID, BuilderVersion: descriptor.Version,
			CodeRevision: b.codeRevision,
		},
	}
	if err := record.Validate(); err != nil {
		return knowledgeeval.ArtifactRecord{}, err
	}
	return record, nil
}

type AgentBuilderConfig struct {
	Model       string
	MaxRounds   int
	Instruction string
	Client      llm.ChatClient
	Resume      *knowledgeeval.OpaqueRef
}

type AgentBuildError struct {
	RunID          string
	FailurePayload knowledgeeval.OpaqueRef
	Audit          workspace.RunAudit
	Err            error
}

func (e *AgentBuildError) Error() string {
	return fmt.Sprintf("run LLM Wiki maintainer %s: %v", e.RunID, e.Err)
}

func (e *AgentBuildError) Unwrap() error {
	return e.Err
}

type AgentBuilder struct {
	store        *knowledgeeval.ArtifactStore
	now          func() time.Time
	codeRevision string
	config       AgentBuilderConfig
}

func NewAgentBuilder(
	store *knowledgeeval.ArtifactStore,
	now func() time.Time,
	codeRevision string,
	config AgentBuilderConfig,
) (*AgentBuilder, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: artifact store is required", knowledgeeval.ErrInvalidRecord)
	}
	if config.Client == nil && config.Resume == nil {
		return nil, fmt.Errorf("%w: maintainer client is required", knowledgeeval.ErrInvalidRecord)
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if strings.TrimSpace(config.Model) == "" {
		config.Model = "deepseek-v4-pro"
	}
	if config.MaxRounds <= 0 {
		config.MaxRounds = 30
	}
	if strings.TrimSpace(config.Instruction) == "" {
		config.Instruction = workspace.DefaultMaintenanceInstruction
	}
	return &AgentBuilder{
		store: store, now: now, codeRevision: codeRevision, config: config,
	}, nil
}

func (b *AgentBuilder) Descriptor() knowledgeeval.BuilderDescriptor {
	return knowledgeeval.BuilderDescriptor{ID: "llmwiki-maintainer", Version: "v1"}
}

func (b *AgentBuilder) Build(
	ctx context.Context,
	request knowledgeeval.BuildRequest,
) (_ knowledgeeval.ArtifactRecord, returnedErr error) {
	input := request.Group.BuildInput
	if b.config.Resume != nil {
		input = *b.config.Resume
	}
	source, err := b.store.OpenDirectory(ctx, input)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, fmt.Errorf("open LLM Wiki build input: %w", err)
	}
	root, err := os.MkdirTemp("", "knowledge-eval-llmwiki-maintainer-")
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, fmt.Errorf("create maintainer workspace: %w", err)
	}
	defer func() {
		returnedErr = errors.Join(returnedErr, cleanupWorkspace(root))
	}()
	if err := copyWorkspace(source, root); err != nil {
		return knowledgeeval.ArtifactRecord{}, err
	}
	runID := sanitizeRunID(request.Group.GroupID + "-" + request.Group.CheckpointID)
	var (
		result         workspace.AgentResult
		linkRepairs    int
		initialFailure string
	)
	if b.config.Resume == nil {
		result, err = workspace.RunAgent(ctx, workspace.AgentConfig{
			Root: root, Model: b.config.Model, MaxRounds: b.config.MaxRounds,
			Client: b.config.Client, Now: b.now,
		}, workspace.AgentRequest{
			RunID: runID, Instruction: b.config.Instruction,
		})
	} else {
		result, linkRepairs, initialFailure, err = resumeMaintainerWorkspace(
			ctx,
			root,
			runID,
			b.config,
			b.now,
		)
	}
	if err != nil {
		failurePayload, storeErr := b.store.PutDirectory(
			ctx,
			"llmwiki-maintainer-failure",
			"pax.knowledge-eval.llmwiki-maintainer-failure.v1",
			root,
		)
		buildErr := &AgentBuildError{
			RunID: runID, FailurePayload: failurePayload, Audit: result.Audit, Err: err,
		}
		if storeErr != nil {
			buildErr.Err = errors.Join(err, fmt.Errorf("store failed maintainer workspace: %w", storeErr))
		}
		return knowledgeeval.ArtifactRecord{}, buildErr
	}
	payload, err := b.store.PutDirectory(ctx, ArtifactKind, ArtifactSchema, root)
	if err != nil {
		return knowledgeeval.ArtifactRecord{}, fmt.Errorf("publish maintained LLM Wiki artifact: %w", err)
	}
	baseID := ""
	if request.BaseArtifact != nil {
		baseID = request.BaseArtifact.ArtifactID
	}
	descriptor := b.Descriptor()
	record := knowledgeeval.ArtifactRecord{
		ArtifactID: stableID(
			request.Group.WorldID,
			request.Group.GroupID,
			request.Group.CheckpointID,
			descriptor.ID,
			payload.SHA256,
		),
		Kind: ArtifactKind, WorldID: request.Group.WorldID,
		GroupID: request.Group.GroupID, CheckpointID: request.Group.CheckpointID,
		BaseID: baseID, Payload: payload, CreatedAt: b.now(),
		Provenance: knowledgeeval.Provenance{
			BuilderID: descriptor.ID, BuilderVersion: descriptor.Version,
			CodeRevision: b.codeRevision, ConfigDigest: agentConfigDigest(b.config),
			Metadata: map[string]string{
				"model":         result.Audit.Model,
				"calls":         fmt.Sprint(result.Audit.Calls),
				"tool_calls":    fmt.Sprint(result.Audit.ToolCalls),
				"input_tokens":  fmt.Sprint(result.Audit.InputTokens),
				"output_tokens": fmt.Sprint(result.Audit.OutputTokens),
				"validation":    fmt.Sprint(result.Validation.Valid),
				"run_id":        result.Audit.RunID,
				"link_repairs":  fmt.Sprint(linkRepairs),
			},
		},
	}
	if b.config.Resume != nil {
		record.Provenance.Metadata["continued_from_failure_sha256"] = b.config.Resume.SHA256
		record.Provenance.Metadata["initial_failure"] = initialFailure
		record.Provenance.Metadata["continuation_run_id"] = result.Audit.RunID
	}
	if err := record.Validate(); err != nil {
		return knowledgeeval.ArtifactRecord{}, err
	}
	return record, nil
}

func resumeMaintainerWorkspace(
	ctx context.Context,
	root string,
	runID string,
	config AgentBuilderConfig,
	now func() time.Time,
) (workspace.AgentResult, int, string, error) {
	auditPath := filepath.Join(root, ".pax", "runs", runID+".json")
	encoded, err := os.ReadFile(auditPath)
	if err != nil {
		return workspace.AgentResult{}, 0, "", fmt.Errorf(
			"read failed maintainer audit: %w",
			err,
		)
	}
	var audit workspace.RunAudit
	if err := json.Unmarshal(encoded, &audit); err != nil {
		return workspace.AgentResult{}, 0, "", fmt.Errorf(
			"decode failed maintainer audit: %w",
			err,
		)
	}
	repairs, err := workspace.RepairResolvableInternalLinks(root)
	if err != nil {
		return workspace.AgentResult{Audit: audit}, 0, audit.FailureReason, err
	}
	validation := workspace.Validate(root)
	result := workspace.AgentResult{Audit: audit, Validation: validation}
	if validation.Valid {
		return result, repairs.Links, audit.FailureReason, nil
	}
	if config.Client == nil {
		return result, repairs.Links, audit.FailureReason, fmt.Errorf(
			"resumed maintainer workspace is invalid after %d link repairs: %s",
			repairs.Links,
			validation.String(),
		)
	}
	continuationRunID := runID + "-continue-" + shortDigest(config.Resume.SHA256)
	continuationInstruction := "Continue the existing Maintainer run in place. Do not restart " +
		"or reread every Source unless a specific repair needs evidence. Resolve every " +
		"validator error, complete the linked pages with cited content, connect them from " +
		"wiki/index.md, and run validate before finishing. Current errors:\n" +
		validation.String()
	result, err = workspace.RunAgent(ctx, workspace.AgentConfig{
		Root: root, Model: config.Model, MaxRounds: config.MaxRounds,
		Client: config.Client, Now: now, ValidationAfterToolRound: true,
	}, workspace.AgentRequest{RunID: continuationRunID, Instruction: continuationInstruction})
	return result, repairs.Links, audit.FailureReason, err
}

func shortDigest(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func agentConfigDigest(config AgentBuilderConfig) string {
	encoded, err := json.Marshal(struct {
		Model       string `json:"model"`
		MaxRounds   int    `json:"max_rounds"`
		Instruction string `json:"instruction"`
	}{
		Model: config.Model, MaxRounds: config.MaxRounds, Instruction: config.Instruction,
	})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func copyWorkspace(source, target string) error {
	directoryModes := make(map[string]fs.FileMode)
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return fmt.Errorf("resolve maintainer workspace path: %w", err)
		}
		destination := filepath.Join(target, relative)
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect maintainer input: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: maintainer input contains a symlink", knowledgeeval.ErrInvalidRecord)
		}
		if entry.IsDir() {
			directoryModes[destination] = info.Mode().Perm()
			if relative == "." {
				return nil
			}
			if err := os.Mkdir(destination, info.Mode().Perm()|0o200); err != nil {
				return fmt.Errorf("create maintainer directory: %w", err)
			}
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read maintainer input: %w", err)
		}
		if err := os.WriteFile(destination, content, info.Mode().Perm()); err != nil {
			return fmt.Errorf("copy maintainer input: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	directories := make([]string, 0, len(directoryModes))
	for directory := range directoryModes {
		directories = append(directories, directory)
	}
	slices.SortFunc(directories, func(left, right string) int {
		return strings.Count(right, string(filepath.Separator)) -
			strings.Count(left, string(filepath.Separator))
	})
	for _, directory := range directories {
		if err := os.Chmod(directory, directoryModes[directory]); err != nil {
			return fmt.Errorf("restore maintainer directory mode: %w", err)
		}
	}
	return nil
}

func cleanupWorkspace(root string) error {
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o755)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("make maintainer workspace removable: %w", err)
	}
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove maintainer workspace: %w", err)
	}
	return nil
}

func sanitizeRunID(value string) string {
	var result strings.Builder
	for _, current := range value {
		switch {
		case current >= 'a' && current <= 'z':
			result.WriteRune(current)
		case current >= 'A' && current <= 'Z':
			result.WriteRune(current)
		case current >= '0' && current <= '9':
			result.WriteRune(current)
		case current == '.', current == '-', current == '_':
			result.WriteRune(current)
		default:
			result.WriteRune('-')
		}
	}
	return strings.Trim(result.String(), "-")
}
