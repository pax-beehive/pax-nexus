package pagewiki_test

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/stretchr/testify/require"
)

// TestStableIDLocksKnownValues locks StableID's output byte-for-byte: the
// postgres legacy hydration derives topic IDs with it (formerly its own
// byte-identical legacyStableID), and those IDs are persisted — a changed
// hash would orphan every hydrated placement. The expected literals were
// computed with the pre-refactor implementation.
func TestStableIDLocksKnownValues(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		values []string
		want   string
	}{
		{
			name:   "root topic",
			prefix: "topic",
			values: []string{"", "projects"},
			want:   "topic_3c26d975a66d96b6664c5d58",
		},
		{
			name:   "nested topic chains the parent ID",
			prefix: "topic",
			values: []string{"topic_3c26d975a66d96b6664c5d58", "runbooks-incidents"},
			want:   "topic_5824d9539b753542f0fd469e",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, pagewiki.StableID(test.prefix, test.values...))
		})
	}
}

func TestExactTextRange(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		exactText string
		wantStart int
		wantEnd   int
		wantOK    bool
	}{
		{name: "unique occurrence", content: "alpha beta gamma", exactText: "beta", wantStart: 6, wantEnd: 10, wantOK: true},
		{name: "empty needle", content: "alpha", exactText: "", wantOK: false},
		{name: "missing needle", content: "alpha", exactText: "beta", wantOK: false},
		{name: "duplicate needle", content: "beta beta", exactText: "beta", wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start, end, ok := pagewiki.ExactTextRange(test.content, test.exactText)
			require.Equal(t, test.wantOK, ok)
			require.Equal(t, test.wantStart, start)
			require.Equal(t, test.wantEnd, end)
		})
	}
}
