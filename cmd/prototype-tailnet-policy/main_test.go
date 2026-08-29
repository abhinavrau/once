package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tailscale/hujson"
)

func TestFixturesAreValidHuJSON(t *testing.T) {
	for _, s := range scenarios {
		_, err := hujson.Parse([]byte(s.policy))
		require.NoError(t, err, s.name)
	}
}

func TestTagOwnersShapesAgree(t *testing.T) {
	assert.Contains(t, tagOwnersBlock, tagOwnersLine)

	_, err := hujson.Parse([]byte("{" + tagOwnersBlock + "}"))
	require.NoError(t, err, "the paste payload must be valid HuJSON in context")
}
