package command

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestAppReportJSONShape(t *testing.T) {
	local := &docker.Application{
		Settings: docker.ApplicationSettings{
			Name:     "campfire",
			Host:     "campfire.localhost",
			Exposure: docker.ExposureLocal,
		},
		Running: true,
	}
	b, err := json.Marshal(newAppReport(local, ""))
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"name": "campfire",
		"host": "campfire.localhost",
		"exposure": "local",
		"running": true,
		"url": "http://campfire.localhost"
	}`, string(b))

	tailnet := &docker.Application{
		Settings: docker.ApplicationSettings{
			Name:     "writebook",
			Host:     "writebook",
			Exposure: docker.ExposureTailnet,
		},
	}
	b, err = json.Marshal(newAppReport(tailnet, "https://writebook.tail1234.ts.net"))
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"name": "writebook",
		"host": "writebook",
		"exposure": "tailnet",
		"running": false,
		"tailnetUrl": "https://writebook.tail1234.ts.net"
	}`, string(b))
}

func TestTailscaleStatusReportJSONShape(t *testing.T) {
	b, err := json.Marshal(tailscaleStatusReport{})
	require.NoError(t, err)
	assert.JSONEq(t, `{"enabled": false}`, string(b))

	b, err = json.Marshal(tailscaleStatusReport{
		Enabled:      true,
		DomainSuffix: "tail1234.ts.net",
		Services: []docker.ServiceStatus{
			{Name: "once-admin", URL: "https://once-admin.tail1234.ts.net", State: "approved"},
		},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"enabled": true,
		"domainSuffix": "tail1234.ts.net",
		"services": [
			{"name": "once-admin", "url": "https://once-admin.tail1234.ts.net", "state": "approved"}
		]
	}`, string(b))
}

func TestReadCommandsTakeJSONFlag(t *testing.T) {
	assert.NotNil(t, newListCommand().cmd.Flags().Lookup("json"))
	assert.NotNil(t, newStatusCommand().cmd.Flags().Lookup("json"))

	for _, sub := range newTailscaleCommand().cmd.Commands() {
		if sub.Name() == "status" {
			assert.NotNil(t, sub.Flags().Lookup("json"))
			return
		}
	}
	t.Fatal("tailscale status subcommand not found")
}
