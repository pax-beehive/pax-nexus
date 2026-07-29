package postgres

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/stretchr/testify/suite"
)

type legacyHydrationSuite struct {
	suite.Suite
}

func TestLegacyHydrationSuite(t *testing.T) {
	suite.Run(t, new(legacyHydrationSuite))
}

func (s *legacyHydrationSuite) TestBuildsArticleSectionsAndSemanticTopics() {
	input := legacyPage{
		pageID: "page-wiki", slug: "wiki-model", pageType: "concept",
		revisionID: "revision-wiki", title: "Wiki 数据建模评审与设计决策",
		summary:      "Review the current Page Revision and Source Anchor model.",
		bodyMarkdown: "## Overview\n\nThe Wiki keeps immutable evidence.\n\n## Decisions\n\nUse optimistic CAS.",
	}

	publication := legacyPublication(input)

	s.Equal([]string{"Overview", "Decisions"}, []string{
		publication.Revision.Sections[0].Heading,
		publication.Revision.Sections[1].Heading,
	})
	s.Equal("Engineering", publication.Topics[0].Title)
	s.Equal("Wiki Architecture", publication.Topics[1].Title)
	s.NotEqual("Sessions", publication.Topics[0].Title)
}

func (s *legacyHydrationSuite) TestClassifiesRetrievalAndExperimentsWithoutSessionTopics() {
	tests := []struct {
		name string
		page legacyPage
		want []string
	}{
		{
			name: "retrieval",
			page: legacyPage{title: "LLM Wiki 的检索与整理", summary: "Agent recall and search."},
			want: []string{"Engineering", "Retrieval"},
		},
		{
			name: "experiment",
			page: legacyPage{title: "煤球颜色实验", summary: "观察到煤球呈白色。"},
			want: []string{"Research", "Experiments"},
		},
		{
			name: "procedure fallback",
			page: legacyPage{title: "Deploy", pageType: "procedure"},
			want: []string{"Operations", "Runbooks & Incidents"},
		},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.Equal(test.want, legacyTopicPath(test.page))
		})
	}
}

func (s *legacyHydrationSuite) TestMapsGroundedBindingsAndRejectsUnmatchedText() {
	sections := []pagewiki.PageSection{{
		Key: "decision", Heading: "Decision", Markdown: "Use optimistic CAS for publication.",
	}}
	drafts := []legacyCitation{
		{
			id: "binding-1", exactText: "optimistic CAS", sourceRevisionID: "source-1",
			eventID: "event-1", startByte: 12, endByte: 26, exactQuote: "optimistic CAS",
		},
		{
			id: "binding-2", exactText: "missing", sourceRevisionID: "source-1",
			eventID: "event-1", startByte: 0, endByte: 7, exactQuote: "missing",
		},
	}

	citations := legacyPageCitations("revision-1", sections, drafts)

	s.Require().Len(citations, 1)
	s.Equal("decision", citations[0].SectionKey)
	s.Equal("optimistic CAS", citations[0].ExactText)
	s.Require().Len(citations[0].SourceAnchors, 1)
	s.Equal("source-1", citations[0].SourceAnchors[0].SourceRevisionID)
}

func (s *legacyHydrationSuite) TestHidesGeneratedSessionWrappersOnlyWhenSemanticWikiExists() {
	s.True(isSessionPublication(pagewiki.PagePublication{
		Topics: []pagewiki.Topic{{Title: "Sessions"}},
	}))
	s.False(isSessionPublication(pagewiki.PagePublication{
		Topics: []pagewiki.Topic{{Title: "Engineering"}},
	}))
}
