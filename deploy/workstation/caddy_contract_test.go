package workstation_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGivenProductionPortalWhenWikiReadAPIIsCalledThenCaddyProxiesIt(t *testing.T) {
	t.Parallel()
	config, err := os.ReadFile("Caddyfile")
	require.NoError(t, err)

	assert.Contains(t, string(config), "handle /v1/*")
	for _, legacyPath := range []string{
		"handle /navigation",
		"handle /pages",
		"handle /search",
	} {
		assert.NotContains(t, string(config), legacyPath)
	}
}
