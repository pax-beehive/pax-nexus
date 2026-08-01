package knowledgeeval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ArtifactStore struct {
	root string
}

func NewArtifactStore(root string) (*ArtifactStore, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact store root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact store root: %w", err)
	}
	return &ArtifactStore{root: absolute}, nil
}

func (s *ArtifactStore) PutBytes(
	_ context.Context,
	kind,
	schemaVersion string,
	content []byte,
) (OpaqueRef, error) {
	digest := sha256.Sum256(content)
	encoded := hex.EncodeToString(digest[:])
	directory := filepath.Join(s.root, encoded)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return OpaqueRef{}, fmt.Errorf("create artifact directory: %w", err)
	}
	target := filepath.Join(directory, "payload")
	if existing, err := os.ReadFile(target); err == nil {
		if !bytes.Equal(existing, content) {
			return OpaqueRef{}, fmt.Errorf("%w: artifact digest collision", ErrConflict)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return OpaqueRef{}, fmt.Errorf("write artifact payload: %w", err)
		}
	} else {
		return OpaqueRef{}, fmt.Errorf("inspect artifact payload: %w", err)
	}
	return fileRef(kind, schemaVersion, target, encoded), nil
}

func (s *ArtifactStore) OpenBytes(_ context.Context, ref OpaqueRef) ([]byte, error) {
	path, err := s.resolveFileRef(ref)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read artifact payload: %w", err)
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != ref.SHA256 {
		return nil, fmt.Errorf("%w: artifact payload digest mismatch", ErrInvalidRecord)
	}
	return content, nil
}

func (s *ArtifactStore) PutDirectory(
	_ context.Context,
	kind,
	schemaVersion,
	source string,
) (OpaqueRef, error) {
	digest, err := DigestDirectory(source)
	if err != nil {
		return OpaqueRef{}, err
	}
	target := filepath.Join(s.root, digest, "tree")
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		if err := copyTree(source, target); err != nil {
			return OpaqueRef{}, err
		}
	} else if err != nil {
		return OpaqueRef{}, fmt.Errorf("inspect artifact tree: %w", err)
	}
	return fileRef(kind, schemaVersion, target, digest), nil
}

func (s *ArtifactStore) OpenDirectory(_ context.Context, ref OpaqueRef) (string, error) {
	path, err := s.resolveFileRef(ref)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect artifact tree: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: artifact payload is not a directory", ErrInvalidRecord)
	}
	digest, err := DigestDirectory(path)
	if err != nil {
		return "", err
	}
	if digest != ref.SHA256 {
		return "", fmt.Errorf("%w: artifact tree digest mismatch", ErrInvalidRecord)
	}
	return path, nil
}

func DigestDirectory(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve artifact tree: %w", err)
	}
	var files []string
	err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: artifact tree contains symlink %s", ErrInvalidRecord, path)
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(absolute, path)
		if err != nil {
			return fmt.Errorf("resolve artifact relative path: %w", err)
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk artifact tree: %w", err)
	}
	sort.Strings(files)
	hash := sha256.New()
	for _, relative := range files {
		if _, err := io.WriteString(hash, relative+"\x00"); err != nil {
			return "", fmt.Errorf("hash artifact path: %w", err)
		}
		content, err := os.ReadFile(filepath.Join(absolute, filepath.FromSlash(relative)))
		if err != nil {
			return "", fmt.Errorf("read artifact file %s: %w", relative, err)
		}
		if _, err := hash.Write(content); err != nil {
			return "", fmt.Errorf("hash artifact file %s: %w", relative, err)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyTree(source, target string) error {
	sourceAbsolute, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolve source tree: %w", err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("create artifact tree: %w", err)
	}
	type copiedDirectory struct {
		path string
		mode fs.FileMode
	}
	var directories []copiedDirectory
	err = filepath.WalkDir(sourceAbsolute, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: artifact tree contains symlink %s", ErrInvalidRecord, path)
		}
		relative, err := filepath.Rel(sourceAbsolute, path)
		if err != nil {
			return fmt.Errorf("resolve copied artifact path: %w", err)
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("inspect copied artifact directory: %w", err)
			}
			if err := os.MkdirAll(destination, info.Mode().Perm()|0o200); err != nil {
				return fmt.Errorf("create copied artifact directory: %w", err)
			}
			directories = append(directories, copiedDirectory{
				path: destination,
				mode: info.Mode().Perm(),
			})
			return nil
		}
		return copyTreeFile(path, destination, entry)
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		directory := directories[index]
		if err := os.Chmod(directory.path, directory.mode); err != nil {
			return fmt.Errorf("protect copied artifact directory: %w", err)
		}
	}
	return nil
}

func copyTreeFile(source, destination string, entry fs.DirEntry) (returnedErr error) {
	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("inspect copied artifact file: %w", err)
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open copied artifact file: %w", err)
	}
	defer func() {
		if err := input.Close(); err != nil {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("close source artifact file: %w", err))
		}
	}()
	output, err := os.OpenFile(
		destination,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		info.Mode().Perm(),
	)
	if err != nil {
		return fmt.Errorf("create copied artifact file: %w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		if closeErr := output.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close failed artifact file: %w", closeErr))
		}
		return fmt.Errorf("copy artifact file: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close copied artifact file: %w", err)
	}
	return nil
}

func fileRef(kind, schemaVersion, path, digest string) OpaqueRef {
	return OpaqueRef{
		Kind:          kind,
		SchemaVersion: schemaVersion,
		URI:           (&url.URL{Scheme: "file", Path: path}).String(),
		SHA256:        digest,
	}
}

func (s *ArtifactStore) resolveFileRef(ref OpaqueRef) (string, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	parsed, err := url.Parse(ref.URI)
	if err != nil || parsed.Scheme != "file" {
		return "", fmt.Errorf("%w: artifact store requires a file URI", ErrInvalidRecord)
	}
	path := filepath.Clean(parsed.Path)
	relative, err := filepath.Rel(s.root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: artifact path escapes store root", ErrInvalidRecord)
	}
	return path, nil
}
