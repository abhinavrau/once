package main

import (
	"strings"
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

func TestMergeAddsMissingSections(t *testing.T) {
	original, merged, added, err := mergePolicy(fixture(t, "Fresh tailnet").policy)
	require.NoError(t, err)
	assert.Equal(t, []string{"grants", "autoApprovers.services", "nodeAttrs"}, added)

	_, err = hujson.Parse([]byte(merged))
	require.NoError(t, err)
	assert.Contains(t, merged, "Default Tailnet Policy", "comments must survive the merge")
	assert.Contains(t, merged, `"443"`)

	nonAdded := 0
	for _, line := range diffLines(original, merged) {
		if !line.added {
			nonAdded++
		}
	}
	assert.Equal(t, len(strings.Split(original, "\n")), nonAdded, "the diff must show every original line as unchanged")
}

func TestMergeNothingToAdd(t *testing.T) {
	original, merged, added, err := mergePolicy(fixture(t, "Everything already present").policy)
	require.NoError(t, err)
	assert.Empty(t, added)
	assert.Equal(t, original, merged, "present sections are never duplicated")
}

func TestMergePartial(t *testing.T) {
	_, merged, added, err := mergePolicy(fixture(t, "Missing funnel nodeAttr").policy)
	require.NoError(t, err)
	assert.Equal(t, []string{"nodeAttrs"}, added)
	assert.Equal(t, 1, strings.Count(merged, `"funnel"`))
}

func TestLargeFixtureScrolls(t *testing.T) {
	policy := fixture(t, "Large GitOps policy").policy
	assert.GreaterOrEqual(t, strings.Count(policy, "\n"), 200)
}

func fixture(t *testing.T, name string) scenario {
	for _, s := range scenarios {
		if s.name == name {
			return s
		}
	}
	t.Fatalf("no fixture named %q", name)
	return scenario{}
}

func TestAllOutcomesReachable(t *testing.T) {
	seen := map[outcome]bool{}
	for _, s := range scenarios {
		seen[s.outcome] = true
	}
	assert.Len(t, seen, 4, "the switcher must reach proceed, warn, refuse and stale")

	_, _, added, err := mergePolicy(fixture(t, "Everything already present").policy)
	require.NoError(t, err)
	assert.Empty(t, added, "the nothing-to-add outcome needs a fixture with no additions")
}
