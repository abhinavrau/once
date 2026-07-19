package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tailscale/hujson"
)

func TestPolicySnippetCoversAllSectionsPerTag(t *testing.T) {
	snippet := PolicySnippet("once,tag:extra")

	assert.Contains(t, snippet, `"tag:once": ["autogroup:admin"],`)
	assert.Contains(t, snippet, `"tag:extra": ["autogroup:admin"],`)
	assert.Contains(t, snippet, `"tag:once": ["tag:once","tag:extra"],`)
	assert.Contains(t, snippet, `{"src":["autogroup:member"],"dst":["tag:once","tag:extra"],"ip":["443"]},`)
	assert.Contains(t, snippet, `{"target":["tag:once","tag:extra"],"attr":["funnel"]},`)
}

func TestPolicySnippetIsValidHuJSON(t *testing.T) {
	// The snippet is pasted into the tailnet policy editor verbatim, so it must
	// parse as HuJSON.
	_, err := hujson.Parse([]byte(PolicySnippet("tag:once")))
	require.NoError(t, err)
}
