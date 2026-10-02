package docker

import (
	"context"
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

// TestTopologyForReturnsBothAdapters pins that every resolved topology has a
// real adapter (ticket 04 closed the "nodes" gap ticket 03 left): resolution
// alone touches no Docker, so this runs with no namespace client at all.
func TestTopologyForReturnsBothAdapters(t *testing.T) {
	ts := NewTailscale(&Namespace{name: "test"})

	services, err := ts.topologyFor(TailnetTopologyServices)
	require.NoError(t, err)
	assert.IsType(t, &servicesTopology{}, services)

	nodes, err := ts.topologyFor(TailnetTopologyNodes)
	require.NoError(t, err)
	assert.IsType(t, &nodesTopology{}, nodes)
}

// TestServicesRemoveAppTouchesNothing pins that removing an app under
// Services deletes no device: its apps share one sidecar node, so there is
// no per-app device to delete. No Docker client and no API: any call would
// panic.
func TestServicesRemoveAppTouchesNothing(t *testing.T) {
	s := newServicesTopology(&Namespace{name: "test"})

	assert.NotPanics(t, func() {
		s.removeApp(context.Background(), TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once"}, "whoami")
	})
}

// TestTailscaleTopologyFacts pins that facts are reachable with no namespace
// at all - frontends call this before any install exists (the enable form's
// topology selector, the CLI's flag help).
func TestTailscaleTopologyFacts(t *testing.T) {
	services, err := TailscaleTopologyFacts(TailnetTopologyServices)
	require.NoError(t, err)
	assert.Equal(t, []string{TailscaleModeSidecar, TailscaleModeHost}, services.ModeChoices)

	nodes, err := TailscaleTopologyFacts(TailnetTopologyNodes)
	require.NoError(t, err)
	assert.Nil(t, nodes.ModeChoices)

	_, err = TailscaleTopologyFacts("both")
	assert.ErrorContains(t, err, `invalid tailnet topology "both"`)
}

// TestLegacyLabelDecodesWithEmptyTopology pins the signal LoadSettings uses
// to refuse a pre-ADR-0015 label: UnmarshalTailscaleSettings tolerates
// unknown/missing fields (ADR-0008), so a label with no "topology" key
// decodes cleanly with Topology == "". LoadSettings itself needs a real
// Docker daemon to find the label in the first place, so its refusal
// (ErrOlderOnceSetup) and the self-heal on re-enable are integration-tested.
func TestLegacyLabelDecodesWithEmptyTopology(t *testing.T) {
	settings, err := UnmarshalTailscaleSettings(`{"clientId":"id","clientSecret":"secret"}`)
	require.NoError(t, err)
	assert.Empty(t, settings.Topology)
}
