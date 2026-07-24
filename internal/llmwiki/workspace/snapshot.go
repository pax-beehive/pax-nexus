package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrStaleBase = errors.New("stale base revision")

func InitStore(ctx context.Context, store, seedWorkspace string) (string, error) {
	if report := Validate(seedWorkspace); !report.Valid {
		return "", fmt.Errorf("seed workspace validator failed: %s", report.String())
	}
	if _, err := os.Stat(store); err == nil {
		return "", errors.New("snapshot store already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect snapshot store: %w", err)
	}
	parent := filepath.Dir(store)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("create snapshot store parent: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".llmwiki-seed-")
	if err != nil {
		return "", fmt.Errorf("create seed staging directory: %w", err)
	}
	defer func() {
		_ = os.Chmod(filepath.Join(staging, "sources"), 0o755)
		_ = os.RemoveAll(staging)
	}()
	if err := copyWorkspace(seedWorkspace, staging); err != nil {
		return "", err
	}
	if _, err := runGit(ctx, staging, "init", "-q", "-b", "main"); err != nil {
		return "", err
	}
	if _, err := runGit(ctx, staging, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	if _, err := runGit(
		ctx,
		staging,
		"-c", "user.name=PAX Wiki",
		"-c", "user.email=wiki@localhost",
		"commit", "-q", "-m", "Initialize local-first Wiki",
	); err != nil {
		return "", err
	}
	revision, err := gitRevision(ctx, staging, "HEAD")
	if err != nil {
		return "", err
	}
	if _, err := runGit(ctx, parent, "init", "--bare", "-q", store); err != nil {
		return "", err
	}
	if _, err := runGit(
		ctx,
		staging,
		"push", "-q", store, "HEAD:refs/heads/main",
	); err != nil {
		return "", err
	}
	return revision, nil
}

func Checkout(ctx context.Context, store, destination string) error {
	if strings.TrimSpace(destination) == "" {
		return errors.New("checkout destination is required")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create checkout parent: %w", err)
	}
	if _, err := runGit(
		ctx,
		filepath.Dir(destination),
		"clone", "-q", "--branch", "main", store, destination,
	); err != nil {
		return err
	}
	base, err := gitRevision(ctx, destination, "HEAD")
	if err != nil {
		return err
	}
	if err := writeBase(destination, base); err != nil {
		return err
	}
	return protectSources(destination)
}

func Commit(ctx context.Context, root, message string) (string, error) {
	if report := Validate(root); !report.Valid {
		return "", fmt.Errorf("workspace validator failed: %s", report.String())
	}
	if strings.TrimSpace(message) == "" {
		return "", errors.New("snapshot commit message is required")
	}
	if _, err := runGit(ctx, root, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	_, diffErr := runGit(ctx, root, "diff", "--cached", "--quiet")
	if diffErr == nil {
		return gitRevision(ctx, root, "HEAD")
	}
	var commandErr *gitCommandError
	if !errors.As(diffErr, &commandErr) || commandErr.ExitCode != 1 {
		return "", diffErr
	}
	if _, err := runGit(
		ctx,
		root,
		"-c", "user.name=PAX Wiki",
		"-c", "user.email=wiki@localhost",
		"commit", "-q", "-m", message,
	); err != nil {
		return "", err
	}
	return gitRevision(ctx, root, "HEAD")
}

func Publish(
	ctx context.Context,
	store,
	workspace,
	expectedBase,
	revision string,
) error {
	head, err := Head(ctx, store)
	if err != nil {
		return err
	}
	if head != expectedBase {
		return fmt.Errorf("%w: expected %s, current %s", ErrStaleBase, expectedBase, head)
	}
	workspaceHead, err := gitRevision(ctx, workspace, "HEAD")
	if err != nil {
		return err
	}
	if workspaceHead != revision {
		return fmt.Errorf(
			"workspace HEAD %s does not match publish revision %s",
			workspaceHead,
			revision,
		)
	}
	if _, err := runGit(
		ctx,
		filepath.Dir(store),
		"--git-dir", store, "fetch", "-q", workspace, "HEAD",
	); err != nil {
		return err
	}
	fetched, err := gitBareRevision(ctx, store, "FETCH_HEAD")
	if err != nil {
		return err
	}
	if fetched != revision {
		return fmt.Errorf("fetched revision %s does not match %s", fetched, revision)
	}
	if _, err := runGit(
		ctx,
		filepath.Dir(store),
		"--git-dir", store, "merge-base", "--is-ancestor", expectedBase, revision,
	); err != nil {
		return fmt.Errorf("publish revision is not based on expected revision: %w", err)
	}
	if _, err := runGit(
		ctx,
		filepath.Dir(store),
		"--git-dir", store, "update-ref",
		"refs/heads/main", revision, expectedBase,
	); err != nil {
		current, headErr := Head(ctx, store)
		if headErr == nil && current != expectedBase {
			return fmt.Errorf(
				"%w: expected %s, current %s",
				ErrStaleBase,
				expectedBase,
				current,
			)
		}
		return fmt.Errorf("atomically publish Wiki revision: %w", err)
	}
	return nil
}

func Rollback(
	ctx context.Context,
	store,
	expectedHead,
	targetRevision string,
) error {
	current, err := Head(ctx, store)
	if err != nil {
		return err
	}
	if current != expectedHead {
		return fmt.Errorf(
			"%w: expected %s, current %s",
			ErrStaleBase,
			expectedHead,
			current,
		)
	}
	if _, err := runGit(
		ctx,
		filepath.Dir(store),
		"--git-dir", store, "cat-file", "-e", targetRevision+"^{commit}",
	); err != nil {
		return fmt.Errorf("resolve rollback target: %w", err)
	}
	if _, err := runGit(
		ctx,
		filepath.Dir(store),
		"--git-dir", store, "merge-base", "--is-ancestor",
		targetRevision, expectedHead,
	); err != nil {
		return fmt.Errorf("rollback target is not an ancestor of current HEAD: %w", err)
	}
	if _, err := runGit(
		ctx,
		filepath.Dir(store),
		"--git-dir", store, "update-ref",
		"refs/heads/main", targetRevision, expectedHead,
	); err != nil {
		current, headErr := Head(ctx, store)
		if headErr == nil && current != expectedHead {
			return fmt.Errorf(
				"%w: expected %s, current %s",
				ErrStaleBase,
				expectedHead,
				current,
			)
		}
		return fmt.Errorf("atomically rollback Wiki revision: %w", err)
	}
	return nil
}

func Diff(
	ctx context.Context,
	repository,
	base,
	revision string,
) (string, error) {
	output, err := runGit(
		ctx,
		repository,
		"diff", "--no-ext-diff", "--find-renames", base, revision, "--", "wiki",
	)
	if err != nil {
		return "", err
	}
	return output, nil
}

func Head(ctx context.Context, store string) (string, error) {
	return gitBareRevision(ctx, store, "refs/heads/main")
}

func Status(ctx context.Context, repository string) (string, error) {
	return runGit(ctx, repository, "status", "--short")
}

func writeBase(root, revision string) error {
	encoded, err := json.MarshalIndent(struct {
		Revision string `json:"revision"`
	}{Revision: revision}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode private base revision: %w", err)
	}
	encoded = append(encoded, '\n')
	target := filepath.Join(root, ".pax", "base.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create private base directory: %w", err)
	}
	if err := os.WriteFile(target, encoded, 0o644); err != nil {
		return fmt.Errorf("write private base revision: %w", err)
	}
	return nil
}

func protectSources(root string) error {
	manifest, err := readManifest(root)
	if err != nil {
		return err
	}
	for _, source := range manifest.Sources {
		target := filepath.Join(root, filepath.FromSlash(source.Path))
		if err := os.Chmod(target, 0o444); err != nil {
			return fmt.Errorf("protect source %s: %w", source.Path, err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "sources"), 0o555); err != nil {
		return fmt.Errorf("protect sources directory: %w", err)
	}
	return nil
}

func copyWorkspace(sourceRoot, destinationRoot string) error {
	return filepath.WalkDir(sourceRoot, func(
		sourcePath string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil {
			return err
		}
		if relative == ".git" || strings.HasPrefix(relative, ".git"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		destination := filepath.Join(destinationRoot, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			mode := info.Mode().Perm()
			if mode&0o200 == 0 {
				mode |= 0o700
			}
			return os.MkdirAll(destination, mode)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace contains unsupported symlink %s", relative)
		}
		input, err := os.Open(sourcePath)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(output, input); err != nil {
			_ = output.Close()
			return err
		}
		if err := output.Close(); err != nil {
			return err
		}
		return nil
	})
}

type gitCommandError struct {
	Args     []string
	ExitCode int
	Output   string
}

func (e *gitCommandError) Error() string {
	return fmt.Sprintf(
		"git %s failed with exit %d: %s",
		strings.Join(e.Args, " "),
		e.ExitCode,
		e.Output,
	)
}

func runGit(ctx context.Context, directory string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", &gitCommandError{
				Args: arguments, ExitCode: exitErr.ExitCode(),
				Output: strings.TrimSpace(string(output)),
			}
		}
		return "", fmt.Errorf("run git %s: %w", strings.Join(arguments, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func gitRevision(ctx context.Context, repository, revision string) (string, error) {
	return runGit(ctx, repository, "rev-parse", "--verify", revision+"^{commit}")
}

func gitBareRevision(ctx context.Context, store, revision string) (string, error) {
	return runGit(
		ctx,
		filepath.Dir(store),
		"--git-dir", store, "rev-parse", "--verify", revision+"^{commit}",
	)
}
