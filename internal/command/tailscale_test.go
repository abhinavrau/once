package command

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
