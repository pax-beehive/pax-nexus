package workspace_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	"github.com/stretchr/testify/suite"
)

type snapshotSuite struct {
	suite.Suite
	ctx       context.Context
	seed      string
	store     string
	base      string
	source    workspace.SourceRecord
	sourceRaw []byte
}

func TestSnapshotSuite(t *testing.T) {
	suite.Run(t, new(snapshotSuite))
}

func (s *snapshotSuite) SetupTest() {
	s.ctx = context.Background()
	s.seed = s.T().TempDir()
	exported := workspace.SessionExport{
		SchemaVersion: workspace.PaxmSessionSchema,
		SessionID:     "snapshot-session",
		Turns: []workspace.SessionTurn{{
			ID: "turn-1", User: "Base source.", Assistant: "Base answer.",
		}},
	}
	encoded, err := json.Marshal(exported)
	s.Require().NoError(err)
	built, err := workspace.Build(s.ctx, workspace.BuildConfig{
		Root: s.seed,
		ReadSession: func(context.Context, string) ([]byte, error) {
			return encoded, nil
		},
	}, workspace.BuildRequest{
		SessionID: "snapshot-session", TurnStart: 0, TurnEnd: 1,
	})
	s.Require().NoError(err)
	s.source = built.Source
	s.sourceRaw, err = os.ReadFile(filepath.Join(s.seed, s.source.Path))
	s.Require().NoError(err)
	s.T().Cleanup(func() {
		for _, root := range []string{s.seed} {
			chmodSources(root)
		}
	})
	s.store = filepath.Join(s.T().TempDir(), "wiki.git")
	s.base, err = workspace.InitStore(s.ctx, s.store, s.seed)
	s.Require().NoError(err)
	s.NotEmpty(s.base)
}

func (s *snapshotSuite) TestPublishesByCASRejectsStaleBaseAndRollsBack() {
	first := filepath.Join(s.T().TempDir(), "first")
	second := filepath.Join(s.T().TempDir(), "second")
	s.Require().NoError(workspace.Checkout(s.ctx, s.store, first))
	s.Require().NoError(workspace.Checkout(s.ctx, s.store, second))
	s.T().Cleanup(func() {
		chmodSources(first)
		chmodSources(second)
	})

	s.writeIndex(first, "# Wiki\n\nFirst publication.\n")
	firstRevision, err := workspace.Commit(s.ctx, first, "first wiki snapshot")
	s.Require().NoError(err)
	diff, err := workspace.Diff(s.ctx, first, s.base, firstRevision)
	s.Require().NoError(err)
	s.Contains(diff, "First publication.")
	s.Contains(diff, "diff --git a/wiki/index.md b/wiki/index.md")
	s.Require().NoError(workspace.Publish(
		s.ctx, s.store, first, s.base, firstRevision,
	))
	head, err := workspace.Head(s.ctx, s.store)
	s.Require().NoError(err)
	s.Equal(firstRevision, head)

	s.writeIndex(second, "# Wiki\n\nStale publication.\n")
	secondRevision, err := workspace.Commit(s.ctx, second, "stale wiki snapshot")
	s.Require().NoError(err)
	err = workspace.Publish(s.ctx, s.store, second, s.base, secondRevision)
	s.Require().ErrorIs(err, workspace.ErrStaleBase)
	head, err = workspace.Head(s.ctx, s.store)
	s.Require().NoError(err)
	s.Equal(firstRevision, head)

	s.Require().NoError(workspace.Rollback(
		s.ctx, s.store, firstRevision, s.base,
	))
	head, err = workspace.Head(s.ctx, s.store)
	s.Require().NoError(err)
	s.Equal(s.base, head)

	restored := filepath.Join(s.T().TempDir(), "restored")
	s.Require().NoError(workspace.Checkout(s.ctx, s.store, restored))
	s.T().Cleanup(func() { chmodSources(restored) })
	index, err := os.ReadFile(filepath.Join(restored, "wiki/index.md"))
	s.Require().NoError(err)
	s.NotContains(string(index), "First publication.")
}

func (s *snapshotSuite) TestCheckoutPreservesSourceBytesAndWritesPrivateBase() {
	checkout := filepath.Join(s.T().TempDir(), "checkout")
	s.Require().NoError(workspace.Checkout(s.ctx, s.store, checkout))
	s.T().Cleanup(func() { chmodSources(checkout) })

	sourceAfter, err := os.ReadFile(filepath.Join(checkout, s.source.Path))
	s.Require().NoError(err)
	s.Equal(s.sourceRaw, sourceAfter)
	info, err := os.Stat(filepath.Join(checkout, s.source.Path))
	s.Require().NoError(err)
	s.Zero(info.Mode().Perm() & 0o222)

	baseBytes, err := os.ReadFile(filepath.Join(checkout, ".pax/base.json"))
	s.Require().NoError(err)
	var base struct {
		Revision string `json:"revision"`
	}
	s.Require().NoError(json.Unmarshal(baseBytes, &base))
	s.Equal(s.base, base.Revision)

	status, err := workspace.Status(s.ctx, checkout)
	s.Require().NoError(err)
	s.Empty(status)

	noChange, err := workspace.Commit(s.ctx, checkout, "no changes")
	s.Require().NoError(err)
	s.Equal(s.base, noChange)
	_, err = workspace.Commit(s.ctx, checkout, "")
	s.Require().ErrorContains(err, "message")
	err = workspace.Publish(s.ctx, s.store, checkout, s.base, "wrong-revision")
	s.Require().ErrorContains(err, "does not match")
	err = workspace.Rollback(s.ctx, s.store, "wrong-head", s.base)
	s.Require().ErrorIs(err, workspace.ErrStaleBase)
	err = workspace.Checkout(s.ctx, s.store, "")
	s.Require().ErrorContains(err, "destination")
	_, err = workspace.InitStore(s.ctx, s.store, s.seed)
	s.Require().ErrorContains(err, "already exists")
}

func (s *snapshotSuite) TestRefusesInvalidSnapshotAndNonAncestorRollback() {
	checkout := filepath.Join(s.T().TempDir(), "invalid")
	s.Require().NoError(workspace.Checkout(s.ctx, s.store, checkout))
	s.T().Cleanup(func() { chmodSources(checkout) })
	s.writeIndex(checkout, "# Wiki\n\n[broken](pages/missing.md)\n")

	_, err := workspace.Commit(s.ctx, checkout, "invalid")
	s.Require().ErrorContains(err, "validator")

	err = workspace.Rollback(s.ctx, s.store, s.base, "deadbeef")
	s.Require().Error(err)
	s.False(errors.Is(err, workspace.ErrStaleBase))
}

func (s *snapshotSuite) TestRejectsSymlinkWhenSeedingSnapshotStore() {
	s.Require().NoError(os.Symlink(
		s.T().TempDir(),
		filepath.Join(s.seed, "untrusted-link"),
	))
	otherStore := filepath.Join(s.T().TempDir(), "other.git")
	_, err := workspace.InitStore(s.ctx, otherStore, s.seed)
	s.Require().ErrorContains(err, "unsupported symlink")
}

func (s *snapshotSuite) writeIndex(root, content string) {
	s.T().Helper()
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, "wiki/index.md"),
		[]byte(content),
		0o644,
	))
}

func chmodSources(root string) {
	_ = os.Chmod(filepath.Join(root, "sources"), 0o755)
}
