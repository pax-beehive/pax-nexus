package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLegacyPlacementTopicIDsAreByteStable locks the topic IDs the legacy
// hydration derives: they are persisted inside hydrated topic trees, so the
// shared pagewiki.StableID must keep producing exactly what the hydration's
// original private hash produced. The literals were computed with that
// original implementation.
func TestLegacyPlacementTopicIDsAreByteStable(t *testing.T) {
	topics, placement := legacyPlacement("page-1", []string{"Projects"})
	require.Len(t, topics, 1)
	require.Equal(t, "topic_3c26d975a66d96b6664c5d58", topics[0].ID)
	require.Equal(t, "projects", topics[0].Slug)
	require.Equal(t, "topic_3c26d975a66d96b6664c5d58", placement.TopicID)

	nested, nestedPlacement := legacyPlacement("page-2", []string{"Projects", "Runbooks & Incidents"})
	require.Len(t, nested, 2)
	require.Equal(t, "topic_3c26d975a66d96b6664c5d58", nested[0].ID)
	require.Equal(t, "topic_f062494051b5ab2b5b01b99e", nested[1].ID)
	require.Equal(t, "runbooks-&-incidents", nested[1].Slug)
	require.Equal(t, nested[1].ID, nestedPlacement.TopicID)
}
