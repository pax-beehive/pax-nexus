package workspace_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	"github.com/stretchr/testify/suite"
)

type sourceBuilderSuite struct {
	suite.Suite
	root     string
	exported workspace.SessionExport
}

func TestSourceBuilderSuite(t *testing.T) {
	suite.Run(t, new(sourceBuilderSuite))
}

func (s *sourceBuilderSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.T().Cleanup(func() {
		err := os.Chmod(filepath.Join(s.root, "sources"), 0o755)
		if err != nil && !os.IsNotExist(err) {
			s.Require().NoError(err)
		}
	})
	s.exported = workspace.SessionExport{
		SchemaVersion: workspace.PaxmSessionSchema,
		Agent:         "codex",
		SessionID:     "session-123",
		Workspace:     "/repo",
		Turns: []workspace.SessionTurn{
			{
				ID:        "turn-a",
				User:      "We chose a local-first wiki.",
				Assistant: "Use immutable sources and Markdown snapshots.",
				CreatedAt: time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC),
			},
			{
				ID:        "turn-b",
				User:      "How should concurrent publication work?",
				Assistant: "Use expected base revision CAS.",
				CreatedAt: time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC),
			},
		},
	}
}

func (s *sourceBuilderSuite) TestBuildsImmutableSourceWithStableMessageAnchors() {
	encoded, err := json.Marshal(s.exported)
	s.Require().NoError(err)

	result, err := workspace.Build(context.Background(), workspace.BuildConfig{
		Root: s.root,
		ReadSession: func(context.Context, string) ([]byte, error) {
			return encoded, nil
		},
	}, workspace.BuildRequest{
		SessionID: "session-123",
		TurnStart: 0,
		TurnEnd:   1,
	})
	s.Require().NoError(err)
	s.Equal(1, result.TurnCount)
	s.Equal(2, result.MessageCount)
	s.Len(result.Source.Anchors, 2)

	sourcePath := filepath.Join(s.root, result.Source.Path)
	before, err := os.ReadFile(sourcePath)
	s.Require().NoError(err)
	s.Contains(string(before), `<a id="msg-`)
	s.Contains(string(before), "We chose a local-first wiki.")
	s.Contains(string(before), "Use immutable sources and Markdown snapshots.")
	s.NotContains(string(before), "concurrent publication")

	info, err := os.Stat(sourcePath)
	s.Require().NoError(err)
	s.Zero(info.Mode().Perm() & 0o222)

	digest := sha256.Sum256(before)
	s.Equal(hex.EncodeToString(digest[:]), result.Source.SHA256)

	again, err := workspace.Build(context.Background(), workspace.BuildConfig{
		Root: s.root,
		ReadSession: func(context.Context, string) ([]byte, error) {
			return encoded, nil
		},
	}, workspace.BuildRequest{
		SessionID: "session-123",
		TurnStart: 1,
		TurnEnd:   2,
	})
	s.Require().NoError(err)
	s.NotEqual(result.Source.Path, again.Source.Path)

	after, err := os.ReadFile(sourcePath)
	s.Require().NoError(err)
	s.Equal(before, after)
	s.FileExists(filepath.Join(s.root, "AGENTS.md"))
	s.FileExists(filepath.Join(s.root, "wiki", "index.md"))
	s.FileExists(filepath.Join(s.root, "wiki", "log.md"))
	s.FileExists(filepath.Join(s.root, ".pax", "base.json"))
	s.FileExists(filepath.Join(s.root, ".pax", "manifest.json"))
}

func (s *sourceBuilderSuite) TestStableAnchorDoesNotDependOnSlicePosition() {
	encoded, err := json.Marshal(s.exported)
	s.Require().NoError(err)

	first, err := workspace.DecodeSessionPart(encoded, workspace.BuildRequest{
		SessionID: "session-123", TurnStart: 0, TurnEnd: 2,
	})
	s.Require().NoError(err)
	second, err := workspace.DecodeSessionPart(encoded, workspace.BuildRequest{
		SessionID: "session-123", TurnStart: 1, TurnEnd: 2,
	})
	s.Require().NoError(err)

	s.Equal(first.Messages[2].Anchor, second.Messages[0].Anchor)
	s.Equal(first.Messages[3].Anchor, second.Messages[1].Anchor)
}

func (s *sourceBuilderSuite) TestRejectsInvalidOrOverlappingSlices() {
	encoded, err := json.Marshal(s.exported)
	s.Require().NoError(err)
	_, err = workspace.Build(context.Background(), workspace.BuildConfig{}, workspace.BuildRequest{})
	s.Require().ErrorContains(err, "root")
	_, err = workspace.Build(context.Background(), workspace.BuildConfig{
		Root: s.root,
		ReadSession: func(context.Context, string) ([]byte, error) {
			return nil, errors.New("reader failed")
		},
	}, workspace.BuildRequest{SessionID: "session-123"})
	s.Require().ErrorContains(err, "reader failed")

	tests := []struct {
		name    string
		request workspace.BuildRequest
		message string
	}{
		{
			name: "session mismatch",
			request: workspace.BuildRequest{
				SessionID: "other", TurnStart: 0, TurnEnd: 1,
			},
			message: "does not match requested session",
		},
		{
			name: "empty range",
			request: workspace.BuildRequest{
				SessionID: "session-123", TurnStart: 1, TurnEnd: 1,
			},
			message: "turn range",
		},
		{
			name: "past end",
			request: workspace.BuildRequest{
				SessionID: "session-123", TurnStart: 0, TurnEnd: 3,
			},
			message: "turn range",
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			_, decodeErr := workspace.DecodeSessionPart(encoded, test.request)
			s.Require().ErrorContains(decodeErr, test.message)
		})
	}
}

func (s *sourceBuilderSuite) TestDefaultPaxmReaderAndAdditionalDecodeFailures() {
	encoded, err := json.Marshal(s.exported)
	s.Require().NoError(err)
	exportPath := filepath.Join(s.T().TempDir(), "export.json")
	s.Require().NoError(os.WriteFile(exportPath, encoded, 0o600))
	binary := filepath.Join(s.T().TempDir(), "fake-paxm")
	script := "#!/bin/sh\n/bin/cat " + exportPath + "\n"
	s.Require().NoError(os.WriteFile(binary, []byte(script), 0o700))

	result, err := workspace.Build(context.Background(), workspace.BuildConfig{
		Root: s.root, PaxmBinary: binary,
	}, workspace.BuildRequest{
		SessionID: "session-123", TurnStart: 0, TurnEnd: 1,
	})
	s.Require().NoError(err)
	s.Equal(workspace.StableMessageAnchor("turn-a", "user"), result.Source.Anchors[0].ID)

	_, err = workspace.DecodeSessionPart([]byte(`not-json`), workspace.BuildRequest{})
	s.Require().ErrorContains(err, "decode")
	wrongSchema := s.exported
	wrongSchema.SchemaVersion = "wrong"
	wrongEncoded, marshalErr := json.Marshal(wrongSchema)
	s.Require().NoError(marshalErr)
	_, err = workspace.DecodeSessionPart(wrongEncoded, workspace.BuildRequest{
		SessionID: "session-123", TurnStart: 0, TurnEnd: 1,
	})
	s.Require().ErrorContains(err, "unsupported")

	missingTurn := s.exported
	missingTurn.Turns[0].ID = ""
	missingEncoded, marshalErr := json.Marshal(missingTurn)
	s.Require().NoError(marshalErr)
	_, err = workspace.DecodeSessionPart(missingEncoded, workspace.BuildRequest{
		SessionID: "session-123", TurnStart: 0, TurnEnd: 1,
	})
	s.Require().ErrorContains(err, "without an ID")
}

func (s *sourceBuilderSuite) TestRejectsOverlappingImportedRanges() {
	encoded, err := json.Marshal(s.exported)
	s.Require().NoError(err)
	reader := func(context.Context, string) ([]byte, error) { return encoded, nil }
	_, err = workspace.Build(context.Background(), workspace.BuildConfig{
		Root: s.root, ReadSession: reader,
	}, workspace.BuildRequest{
		SessionID: "session-123", TurnStart: 0, TurnEnd: 2,
	})
	s.Require().NoError(err)
	_, err = workspace.Build(context.Background(), workspace.BuildConfig{
		Root: s.root, ReadSession: reader,
	}, workspace.BuildRequest{
		SessionID: "session-123", TurnStart: 1, TurnEnd: 2,
	})
	s.Require().ErrorContains(err, "overlaps")
}
