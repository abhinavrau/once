package docker

import (
	"context"
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTailscaleTopology(t *testing.T) {
	assert.NoError(t, ValidateTailscaleTopology(""))
	assert.NoError(t, ValidateTailscaleTopology(TailnetTopologyServices))
	assert.NoError(t, ValidateTailscaleTopology(TailnetTopologyNodes))
	assert.ErrorContains(t, ValidateTailscaleTopology("both"), `invalid tailnet topology "both"`)
}

func TestResolveTopologyExplicitWinsWithoutDocker(t *testing.T) {
	// An explicit topology resolves without touching Docker (a nil client
	// would panic), the same guarantee resolveMode gives for --mode.
	ts := NewTailscale(&Namespace{name: "test"})

	topology, err := ts.resolveTopology(context.Background(), TailnetTopologyServices)
	require.NoError(t, err)
	assert.Equal(t, TailnetTopologyServices, topology)

	topology, err = ts.resolveTopology(context.Background(), TailnetTopologyNodes)
	require.NoError(t, err)
	assert.Equal(t, TailnetTopologyNodes, topology)
}

// TestEnableTailscaleNodesTopologyNotImplementedYet pins ticket 03's decision
// for what a "nodes" request does before ticket 04's adapter exists: a clear
// error, not a panic or a silent no-op. Both topology resolution (explicit
// wins) and adapter selection short-circuit before any Docker work, so this
// runs with no namespace client and no *Tailscale facade at all (nil-safe,
// like the other "before any Docker work" tests in namespace_test.go).
func TestEnableTailscaleNodesTopologyNotImplementedYet(t *testing.T) {
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())
	ln, err := net.Listen("unix", AdminSocketPath())
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	require.NoError(t, os.WriteFile(AdminNginxConfPath(), []byte(AdminNginxConfig()), 0o644))

	ns := &Namespace{name: "test"}
	ns.admin = NewAdmin(ns)

	_, err = ns.EnableTailscale(context.Background(),
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Topology: TailnetTopologyNodes}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), TailnetTopologyNodes)
	assert.Contains(t, err.Error(), "not implemented")
}

// TestLegacyLabelDecodesWithEmptyTopology pins the signal LoadSettings uses
// to refuse a pre-ADR-0015 label: UnmarshalTailscaleSettings tolerates
// unknown/missing fields (ADR-0008), so a label with no "topology" key
// decodes cleanly with Topology == "". LoadSettings itself needs a real
// Docker daemon to find the label in the first place, so its refusal
// (errOlderOnceSetup) and the self-heal on re-enable are integration-tested.
func TestLegacyLabelDecodesWithEmptyTopology(t *testing.T) {
	settings, err := UnmarshalTailscaleSettings(`{"clientId":"id","clientSecret":"secret"}`)
	require.NoError(t, err)
	assert.Empty(t, settings.Topology)
}
