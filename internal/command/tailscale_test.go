package command

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestEnableTakesDirectCredentialFlags(t *testing.T) {
	enable := tailscaleSubcommand(t, "enable")

	assert.NotNil(t, enable.Flags().Lookup("client-id"))
	assert.NotNil(t, enable.Flags().Lookup("client-secret"))

	tag := enable.Flags().Lookup("tag")
	require.NotNil(t, tag)
	assert.Equal(t, "tag:once", tag.DefValue)

	// The policy write flow is gone with the bootstrap credential.
	assert.Nil(t, enable.Flags().Lookup("yes"))
	assert.Nil(t, enable.Flags().Lookup("no-policy-write"))
}

func TestPolicyCommandNeedsNoDockerNamespace(t *testing.T) {
	policy := tailscaleSubcommand(t, "policy")
	require.NotNil(t, policy.Flags().Lookup("tag"))
	assert.NotNil(t, policy.Run, "policy must not need a Docker namespace")
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

func tailscaleSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	for _, sub := range newTailscaleCommand().cmd.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	t.Fatalf("no %q subcommand", name)
	return nil
}
