package command

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnableTakesOnlyBootstrapCredentialFlags(t *testing.T) {
	var enable *cobra.Command
	for _, sub := range newTailscaleCommand().cmd.Commands() {
		if sub.Name() == "enable" {
			enable = sub
		}
	}
	require.NotNil(t, enable)

	assert.NotNil(t, enable.Flags().Lookup("client-id"))
	assert.NotNil(t, enable.Flags().Lookup("client-secret"))
	assert.Nil(t, enable.Flags().Lookup("bootstrap-client-id"))
	assert.Nil(t, enable.Flags().Lookup("bootstrap-client-secret"))

	tag := enable.Flags().Lookup("tag")
	require.NotNil(t, tag)
	assert.Equal(t, "tag:once", tag.DefValue)

	assert.NotNil(t, enable.Flags().Lookup("yes"))
	assert.NotNil(t, enable.Flags().Lookup("no-policy-write"))
}

// go test runs without a TTY on stdin, so an undecided run must fail fast
// with the flag equivalents rather than reach a prompt.
func TestRequirePolicyDecisionFailsFastWithoutTerminal(t *testing.T) {
	err := (&tailscaleCommand{}).requirePolicyDecision()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--yes")
	assert.Contains(t, err.Error(), "--no-policy-write")

	assert.NoError(t, (&tailscaleCommand{yes: true}).requirePolicyDecision())
	assert.NoError(t, (&tailscaleCommand{noPolicyWrite: true}).requirePolicyDecision())
}
