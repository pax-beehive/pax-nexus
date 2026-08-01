package workspace_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	s.Require().NoError(os.Remove(
		filepath.Join(s.root, ".pax", "editorial-profile"),
	))
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

func (s *validatorSuite) TestRepairsOnlyUniquelyResolvableWikiLinks() {
	anchor := s.source.Anchors[0].ID
	s.writeWiki("index.md", "# Wiki\n\n- [Profile](../profile.md)\n")
	s.writeWiki(
		"profile.md",
		"# Profile\n\nSee [home](../index.md). "+
			"Source fact ([source](../"+s.source.Path+"#"+anchor+")).\n",
	)
	before := workspace.Validate(s.root)
	s.False(before.Valid)
	s.Contains(before.String(), "broken internal link")

	repairs, err := workspace.RepairResolvableInternalLinks(s.root)
	s.Require().NoError(err)
	s.Equal(2, repairs.Files)
	s.Equal(2, repairs.Links)
	after := workspace.Validate(s.root)
	s.True(after.Valid, after.String())

	index, err := os.ReadFile(filepath.Join(s.root, "wiki", "index.md"))
	s.Require().NoError(err)
	s.Contains(string(index), "](profile.md)")
	profile, err := os.ReadFile(filepath.Join(s.root, "wiki", "profile.md"))
	s.Require().NoError(err)
	s.Contains(string(profile), "](index.md)")
	s.Contains(string(profile), "](../"+s.source.Path+"#")
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

func (s *validatorSuite) TestRejectsDestructiveShrinkAgainstGitBase() {
	s.writeWiki("index.md", "# Wiki\n\n- [Profile](pages/profile.md)\n")
	s.writeWiki(
		"pages/profile.md",
		"# Profile\n\n"+strings.Repeat(
			"Durable, cited knowledge that must not be silently discarded. ",
			12,
		)+"\n",
	)
	store := filepath.Join(s.T().TempDir(), "wiki.git")
	_, err := workspace.InitStore(context.Background(), store, s.root)
	s.Require().NoError(err)
	checkout := filepath.Join(s.T().TempDir(), "checkout")
	s.Require().NoError(workspace.Checkout(context.Background(), store, checkout))
	s.T().Cleanup(func() {
		s.Require().NoError(os.Chmod(filepath.Join(checkout, "sources"), 0o755))
	})
	s.Require().NoError(os.WriteFile(
		filepath.Join(checkout, "wiki/pages/profile.md"),
		[]byte("# Profile test\n\nTest.\n"),
		0o644,
	))

	report := workspace.Validate(checkout)
	s.False(report.Valid)
	s.Contains(report.String(), "destructive page shrink")
}

func (s *validatorSuite) TestRejectsBulkDeletionAgainstGitBase() {
	s.writeWiki(
		"index.md",
		"# Wiki\n\n- [One](pages/one.md)\n- [Two](pages/two.md)\n",
	)
	s.writeWiki("pages/one.md", "# One\n\nDurable page one.\n")
	s.writeWiki("pages/two.md", "# Two\n\nDurable page two.\n")
	store := filepath.Join(s.T().TempDir(), "wiki.git")
	_, err := workspace.InitStore(context.Background(), store, s.root)
	s.Require().NoError(err)
	checkout := filepath.Join(s.T().TempDir(), "checkout")
	s.Require().NoError(workspace.Checkout(context.Background(), store, checkout))
	s.T().Cleanup(func() {
		s.Require().NoError(os.Chmod(filepath.Join(checkout, "sources"), 0o755))
	})
	s.Require().NoError(os.Remove(filepath.Join(checkout, "wiki/pages/one.md")))
	s.Require().NoError(os.Remove(filepath.Join(checkout, "wiki/pages/two.md")))

	report := workspace.Validate(checkout)
	s.False(report.Valid)
	s.Contains(report.String(), "bulk deletion")
}

func (s *validatorSuite) TestArticleFirstProfileRejectsDossierShape() {
	s.enableArticleFirst()
	duplicate := strings.Repeat(
		"This substantial paragraph repeats the same source-shaped prose instead of "+
			"giving each article a distinct reader promise. ",
		3,
	)
	s.writeWiki(
		"index.md",
		"---\ntype: portal\n---\n\n# Wiki\n\n"+
			"Start with the people and ongoing stories in this world.\n\n"+
			"- [People](portals/people.md)\n",
	)
	s.writeWiki(
		"portals/people.md",
		"---\ntype: portal\n---\n\n# People\n\n"+
			"Browse the people represented in the source material.\n\n"+
			"- [Profile](../pages/profile.md)\n",
	)
	s.writeWiki(
		"pages/profile.md",
		"---\ntype: person\n---\n\n# Profile\n\n"+
			duplicate+"\n\n## Session 1\n\nChronological notes.\n\n"+
			"[Related profile](other.md)\n",
	)
	s.writeWiki(
		"pages/other.md",
		"---\ntype: person\n---\n\n# Other\n\n"+
			duplicate+"\n\n## Current snapshot\n\n"+
			"[Profile](profile.md)\n",
	)

	report := workspace.Validate(s.root)
	s.False(report.Valid)
	s.Contains(report.String(), "Session chronology heading")
	s.Contains(report.String(), "duplicates substantial prose")
	s.Contains(report.String(), "has no precise Source citation")
}

func (s *validatorSuite) TestArticleFirstProfileAcceptsLayeredLinkedArticles() {
	s.enableArticleFirst()
	anchor := s.source.Anchors[0].ID
	citation := "[source](../../" + s.source.Path + "#" + anchor + ")"
	s.writeWiki(
		"index.md",
		"---\ntype: portal\n---\n\n# Wiki\n\n"+
			"Start with people, ongoing stories, and a chronological overview.\n\n"+
			"- [People](portals/people.md)\n"+
			"- [Timeline](timelines/world.md)\n",
	)
	s.writeWiki(
		"portals/people.md",
		"---\ntype: portal\n---\n\n# People\n\n"+
			"Browse the people and follow their ongoing stories.\n\n"+
			"- [Profile](../pages/profile.md)\n",
	)
	s.writeWiki(
		"pages/profile.md",
		"---\ntype: person\naliases: [Example]\n---\n\n# Profile\n\n"+
			"This lead identifies the subject and explains why the page matters. "+
			citation+"\n\n## Current snapshot\n\n"+
			"The current state is grounded and links to the [timeline](../timelines/world.md).\n",
	)
	s.writeWiki(
		"timelines/world.md",
		"---\ntype: timeline\n---\n\n# Timeline\n\n"+
			"This timeline records meaningful changes without structuring every article "+
			"as a transcript. "+citation+"\n\n## Key changes\n\n"+
			"- The source fact connects back to the [profile](../pages/profile.md).\n",
	)

	report := workspace.Validate(s.root)
	s.True(report.Valid, report.String())
}

func (s *validatorSuite) TestArticleFirstContractAndNavigationFailureMatrix() {
	tests := []struct {
		name    string
		page    string
		message string
	}{
		{
			name:    "missing frontmatter",
			page:    "# Leaf\n\nThis page has a readable lead but no declared type.\n",
			message: "missing frontmatter",
		},
		{
			name: "unsupported type",
			page: "---\ntype: transcript\n---\n\n# Leaf\n\n" +
				"This page has a readable lead and an invalid editorial type.\n",
			message: "unsupported article type",
		},
		{
			name: "missing lead",
			page: "---\ntype: portal\n---\n\n# Leaf\n\n" +
				"- A list is not a prose lead with a reader promise.\n",
			message: "needs a prose lead",
		},
		{
			name: "multiple H1",
			page: "---\ntype: portal\n---\n\n# Leaf\n\n" +
				"This page starts with a valid prose lead for its readers.\n\n# Again\n",
			message: "exactly one H1",
		},
		{
			name: "related pages before article content",
			page: "---\ntype: portal\n---\n\n# Leaf\n\n" +
				"This page starts with a valid prose lead for its readers.\n\n" +
				"## Related pages\n\n- [Home](../index.md)\n\n" +
				"## Appended facts\n\nThis content is in the wrong position.\n",
			message: "Related pages must be the final H2",
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			root := s.T().TempDir()
			s.Require().NoError(os.MkdirAll(
				filepath.Join(root, ".pax"),
				0o755,
			))
			s.Require().NoError(os.WriteFile(
				filepath.Join(root, ".pax", "editorial-profile"),
				[]byte("article-first-v1\n"),
				0o644,
			))
			s.Require().NoError(os.MkdirAll(
				filepath.Join(root, "wiki", "portals"),
				0o755,
			))
			s.Require().NoError(os.WriteFile(
				filepath.Join(root, "wiki", "index.md"),
				[]byte("---\ntype: portal\n---\n\n# Wiki\n\n"+
					"This home page leads readers into a deliberately deep path.\n\n"+
					"- [One](portals/one.md)\n"),
				0o644,
			))
			for index, pair := range []struct {
				name string
				next string
			}{
				{name: "one.md", next: "two.md"},
				{name: "two.md", next: "three.md"},
				{name: "three.md", next: "leaf.md"},
			} {
				content := "---\ntype: portal\n---\n\n# Portal\n\n" +
					"This portal provides another meaningful step in the reader path.\n\n"
				if index < 2 {
					content += "- [Next](" + pair.next + ")\n"
				} else {
					content += "- [Leaf](" + pair.next + ")\n"
				}
				s.Require().NoError(os.WriteFile(
					filepath.Join(root, "wiki", "portals", pair.name),
					[]byte(content),
					0o644,
				))
			}
			s.Require().NoError(os.WriteFile(
				filepath.Join(root, "wiki", "portals", "leaf.md"),
				[]byte(test.page),
				0o644,
			))
			manifest, err := json.Marshal(workspace.Manifest{
				SchemaVersion: "pax.llmwiki.workspace.v1",
			})
			s.Require().NoError(err)
			s.Require().NoError(os.WriteFile(
				filepath.Join(root, ".pax", "manifest.json"),
				manifest,
				0o644,
			))

			report := workspace.Validate(root)
			s.False(report.Valid)
			s.Contains(report.String(), test.message)
			s.Contains(report.String(), "maximum is 3")
		})
	}
}

func (s *validatorSuite) enableArticleFirst() {
	s.T().Helper()
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.root, ".pax", "editorial-profile"),
		[]byte("article-first-v1\n"),
		0o644,
	))
}

func (s *validatorSuite) writeWiki(relative, content string) {
	s.T().Helper()
	target := filepath.Join(s.root, "wiki", filepath.FromSlash(relative))
	s.Require().NoError(os.MkdirAll(filepath.Dir(target), 0o755))
	s.Require().NoError(os.WriteFile(target, []byte(content), 0o644))
}
