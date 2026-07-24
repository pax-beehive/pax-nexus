package workspace_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	"github.com/stretchr/testify/suite"
)

type validatorSuite struct {
	suite.Suite
	root   string
	source workspace.SourceRecord
}

func TestValidatorSuite(t *testing.T) {
	suite.Run(t, new(validatorSuite))
}

func (s *validatorSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.T().Cleanup(func() {
		s.Require().NoError(os.Chmod(filepath.Join(s.root, "sources"), 0o755))
	})
	exported := workspace.SessionExport{
		SchemaVersion: workspace.PaxmSessionSchema,
		SessionID:     "session-validator",
		Turns: []workspace.SessionTurn{{
			ID: "turn-1", User: "Source fact.", Assistant: "Confirmed.",
		}},
	}
	encoded, err := json.Marshal(exported)
	s.Require().NoError(err)
	result, err := workspace.Build(context.Background(), workspace.BuildConfig{
		Root: s.root,
		ReadSession: func(context.Context, string) ([]byte, error) {
			return encoded, nil
		},
	}, workspace.BuildRequest{
		SessionID: "session-validator", TurnStart: 0, TurnEnd: 1,
	})
	s.Require().NoError(err)
	s.source = result.Source
}

func (s *validatorSuite) TestAcceptsReachablePagesLinksAndExactCitations() {
	anchor := s.source.Anchors[0].ID
	s.writeWiki("index.md", "# Wiki\n\n- [Architecture](topics/architecture.md)\n")
	s.writeWiki("topics/architecture.md",
		"# Architecture\n\nSee [Publishing](../pages/publishing.md).\n")
	s.writeWiki("pages/publishing.md",
		"# Publishing\n\nThe source says so "+
			"([source](../../"+s.source.Path+"#"+anchor+")).\n")

	report := workspace.Validate(s.root)
	s.True(report.Valid, report.String())
	s.Equal(3, report.MarkdownFiles)
	s.Equal(1, report.Citations)
	s.Empty(report.Errors)
}

func (s *validatorSuite) TestRejectsMutatedSourcesBrokenLinksAndOrphanPages() {
	s.writeWiki("index.md", "# Wiki\n\n- [Broken](topics/missing.md)\n")
	s.writeWiki("pages/orphan.md",
		"# Orphan\n\n([bad source](../../"+s.source.Path+"#msg-0000000000000000)).\n")

	sourcePath := filepath.Join(s.root, s.source.Path)
	s.Require().NoError(os.Chmod(sourcePath, 0o644))
	file, err := os.OpenFile(sourcePath, os.O_APPEND|os.O_WRONLY, 0)
	s.Require().NoError(err)
	_, err = file.WriteString("\nmutated\n")
	s.Require().NoError(err)
	s.Require().NoError(file.Close())

	report := workspace.Validate(s.root)
	s.False(report.Valid)
	s.Contains(report.String(), "source hash mismatch")
	s.Contains(report.String(), "broken internal link")
	s.Contains(report.String(), "unknown source anchor")
	s.Contains(report.String(), "is not reachable from wiki/index.md")
}

func (s *validatorSuite) TestRequiresTopicTreeToReachEveryMajorPage() {
	s.writeWiki("index.md", "# Wiki\n\nNo links yet.\n")
	s.writeWiki("pages/major.md", "# Major\n\nSubstantial knowledge.\n")

	report := workspace.Validate(s.root)
	s.False(report.Valid)
	s.Contains(report.String(), "is not reachable from wiki/index.md")
}

func (s *validatorSuite) TestRejectsMalformedCitationSyntaxAndAnchorPunctuation() {
	anchor := s.source.Anchors[0].ID
	s.writeWiki(
		"index.md",
		"# Wiki\n\n"+
			"Unclosed [source](../"+s.source.Path+"#"+anchor+"].\n\n"+
			"Punctuated [source](../"+s.source.Path+"#"+anchor+"].).\n",
	)

	report := workspace.Validate(s.root)
	s.False(report.Valid)
	s.Contains(report.String(), "malformed source citation")
	s.Contains(report.String(), "malformed source citation anchor")
}

func (s *validatorSuite) writeWiki(relative, content string) {
	s.T().Helper()
	target := filepath.Join(s.root, "wiki", filepath.FromSlash(relative))
	s.Require().NoError(os.MkdirAll(filepath.Dir(target), 0o755))
	s.Require().NoError(os.WriteFile(target, []byte(content), 0o644))
}
