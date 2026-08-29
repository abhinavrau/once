package command

import (
	"io"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestEnableTakesTheClientIDFlagAndHidesTheSecretFlag(t *testing.T) {
	_, enable := tailscaleSubcommand(t, "enable")

	assert.NotNil(t, enable.Flags().Lookup("client-id"))

	// The secret has no usable flag: it is registered only to reject an older
	// command line, so help must not advertise it.
	secret := enable.Flags().Lookup("client-secret")
	require.NotNil(t, secret)
	assert.True(t, secret.Hidden)
	assert.Contains(t, enable.Example, envClientSecret)

	tag := enable.Flags().Lookup("tag")
	require.NotNil(t, tag)
	assert.Equal(t, "tag:once", tag.DefValue)

	// The policy write flow is gone with the bootstrap credential.
	assert.Nil(t, enable.Flags().Lookup("yes"))
	assert.Nil(t, enable.Flags().Lookup("no-policy-write"))
}

// The secret is resolved in PreRunE, so these drive the command the way cobra
// does: it must fail before enable's RunE reaches Docker at all.
func TestEnableRejectsTheClientSecretFlag(t *testing.T) {
	t.Setenv(envClientSecret, "s3cret")
	_, enable := tailscaleSubcommand(t, "enable")
	require.NoError(t, enable.ParseFlags([]string{"--client-secret", "s3cret"}))

	err := enable.PreRunE(enable, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), envClientSecret)
	assert.Contains(t, err.Error(), "--client-secret")
}

func TestEnableRequiresTheClientSecretEnvVar(t *testing.T) {
	assertMissing := func(t *testing.T) {
		t.Helper()
		_, enable := tailscaleSubcommand(t, "enable")

		err := enable.PreRunE(enable, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), envClientSecret)
	}

	t.Run("unset", func(t *testing.T) {
		t.Setenv(envClientSecret, "")
		require.NoError(t, os.Unsetenv(envClientSecret))
		assertMissing(t)
	})
	t.Run("empty", func(t *testing.T) {
		t.Setenv(envClientSecret, "")
		assertMissing(t)
	})
	t.Run("whitespace only", func(t *testing.T) {
		t.Setenv(envClientSecret, "  \n")
		assertMissing(t)
	})
}

func TestEnableTrimsTheClientSecretEnvVar(t *testing.T) {
	t.Setenv(envClientSecret, "  s3cret\n")
	ts, enable := tailscaleSubcommand(t, "enable")

	require.NoError(t, enable.PreRunE(enable, nil))

	assert.Equal(t, "s3cret", ts.clientSecret)
}

func TestPolicyCommandNeedsNoDockerNamespace(t *testing.T) {
	_, policy := tailscaleSubcommand(t, "policy")
	require.NotNil(t, policy.Flags().Lookup("tag"))
	require.NotNil(t, policy.Flags().Lookup("topology"))
	assert.NotNil(t, policy.RunE, "policy must not need a Docker namespace")
}

func TestEnableHasATopologyFlagValidatedByTheDockerPackage(t *testing.T) {
	ts, enable := tailscaleSubcommand(t, "enable")

	topology := enable.Flags().Lookup("topology")
	require.NotNil(t, topology)
	assert.Empty(t, topology.DefValue, "unset keeps a stored topology, or defaults a fresh enable to nodes")

	require.NoError(t, enable.ParseFlags([]string{"--topology", docker.TailnetTopologyNodes}))
	assert.Equal(t, docker.TailnetTopologyNodes, ts.topology)
}

func TestPolicyPrintsTheRequestedTopologysSnippet(t *testing.T) {
	ts, policy := tailscaleSubcommand(t, "policy")
	require.NoError(t, policy.Flags().Set("topology", docker.TailnetTopologyNodes))
	require.NoError(t, policy.Flags().Set("tag", "tag:once"))

	output := captureStdout(t, func() {
		require.NoError(t, ts.policy())
	})

	assert.Contains(t, output, docker.NodesPolicySnippet("tag:once"))
}

// A refused enable's fix is the Merged Policy itself: printing it (plus the
// apply-and-re-run instruction) is the whole interaction, since the CLI never
// prompts (ADR-0004/0017).
func TestEnableRefusalPrintsTheMergedPolicyToApply(t *testing.T) {
	review := docker.PolicyReview{
		Merged: "{\n\t\"grants\": [],\n}",
		Added:  []string{"grants", "autoApprovers.services"},
	}

	output := captureStdout(t, func() {
		printMergedPolicy(review)
	})

	assert.Contains(t, output, review.Merged)
	assert.Contains(t, output, "grants, autoApprovers.services")
	assert.Contains(t, output, "re-run `once tailscale enable`")
	assert.Contains(t, output, "https://login.tailscale.com/admin/acls/file")
}

// policy is step one of the enable flow, so its caller is by definition not
// enabled yet - the snippet it prints unasked must be the one a fresh enable
// will need.
func TestPolicyDefaultsToWhatAFreshEnableResolvesTo(t *testing.T) {
	ts, _ := tailscaleSubcommand(t, "policy")

	output := captureStdout(t, func() {
		require.NoError(t, ts.policy())
	})

	assert.Contains(t, output, docker.NodesPolicySnippet("tag:once"))
}

func TestPolicyRejectsAnInvalidTopology(t *testing.T) {
	ts, policy := tailscaleSubcommand(t, "policy")
	require.NoError(t, policy.Flags().Set("topology", "both"))

	err := ts.policy()

	assert.ErrorContains(t, err, `invalid tailnet topology "both"`)
}

func TestResolvableServiceURLsSkipsRowsWithNothingToResolve(t *testing.T) {
	urls := resolvableServiceURLs([]docker.ServiceStatus{
		{Name: "once-admin", URL: "https://once-admin.tail1234.ts.net", State: "approved"},
		{Name: "writebook", URL: "https://writebook.tail1234.ts.net", State: "pending approval"},
		{Name: "campfire", URL: "https://campfire.tail1234.ts.net", State: stateNotRunning},
	})

	assert.Equal(t, []string{
		"https://once-admin.tail1234.ts.net",
		"https://writebook.tail1234.ts.net",
		"",
	}, urls)
}

// Helpers

// captureStdout runs fn with os.Stdout redirected, returning what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)

	original := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = original })

	fn()

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func tailscaleSubcommand(t *testing.T, name string) (*tailscaleCommand, *cobra.Command) {
	t.Helper()
	ts := newTailscaleCommand()
	for _, sub := range ts.cmd.Commands() {
		if sub.Name() == name {
			return ts, sub
		}
	}
	t.Fatalf("no %q subcommand", name)
	return nil, nil
}
