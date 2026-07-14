package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const statusJSON = `{
	"Version": "1.86.0",
	"BackendState": "Running",
	"CurrentTailnet": {
		"Name": "example.com",
		"MagicDNSSuffix": "tail1234.ts.net",
		"MagicDNSEnabled": true
	},
	"MagicDNSSuffix": "tail1234.ts.net",
	"Self": {"HostName": "once-tailscale", "DNSName": "once-tailscale.tail1234.ts.net."},
	"Peer": {
		"nodekey:aaa": {"HostName": "Grafana-Box", "DNSName": "grafana.tail1234.ts.net."},
		"nodekey:bbb": {"HostName": "Laptop", "DNSName": ""}
	}
}`

func TestFakeTailscaleDaemonMagicDNSSuffix(t *testing.T) {
	suffix := func(statusJSON string) string {
		t.Helper()
		daemon := &FakeTailscaleDaemon{StatusJSON: statusJSON}
		s, err := daemon.MagicDNSSuffix(t.Context())
		require.NoError(t, err)
		return s
	}

	assert.Equal(t, "tail1234.ts.net", suffix(statusJSON))

	// Older daemons only report the deprecated top-level alias.
	assert.Equal(t, "tail1234.ts.net", suffix(`{"MagicDNSSuffix": "tail1234.ts.net."}`))

	// A daemon that hasn't joined a tailnet yet reports no suffix.
	assert.Equal(t, "", suffix(`{"BackendState": "NeedsLogin"}`))
}

func TestFakeTailscaleDaemonMagicDNSSuffixErrors(t *testing.T) {
	daemon := &FakeTailscaleDaemon{StatusJSON: "not json"}
	_, err := daemon.MagicDNSSuffix(t.Context())
	require.ErrorContains(t, err, "parsing tailscale status")

	daemon = &FakeTailscaleDaemon{StatusErr: assert.AnError}
	_, err = daemon.MagicDNSSuffix(t.Context())
	require.ErrorIs(t, err, assert.AnError)
}

func TestFakeTailscaleDaemonMachineHostnames(t *testing.T) {
	daemon := &FakeTailscaleDaemon{StatusJSON: statusJSON}

	// The DNSName's first label wins over HostName ("grafana", not
	// "Grafana-Box"); a node without a DNSName falls back to its lowercased
	// HostName.
	names, err := daemon.MachineHostnames(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"grafana", "laptop", "once-tailscale"}, names)

	daemon = &FakeTailscaleDaemon{StatusJSON: `{"BackendState": "NeedsLogin"}`}
	names, err = daemon.MachineHostnames(t.Context())
	require.NoError(t, err)
	assert.Empty(t, names)

	daemon = &FakeTailscaleDaemon{StatusJSON: "not json"}
	_, err = daemon.MachineHostnames(t.Context())
	require.ErrorContains(t, err, "parsing tailscale status")

	daemon = &FakeTailscaleDaemon{StatusErr: assert.AnError}
	_, err = daemon.MachineHostnames(t.Context())
	require.ErrorIs(t, err, assert.AnError)
}

// serveJSON matches the ServeConfig shape `tailscale serve status --json`
// emits, per docktail's parsing of the same output.
const serveJSON = `{
	"TCP": {"443": {"HTTPS": true}},
	"Web": {
		"once.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:80"}}}
	},
	"AllowFunnel": {"once.tail1234.ts.net:443": true},
	"Services": {
		"svc:writebook": {
			"TCP": {"443": {"HTTPS": true}},
			"Web": {"writebook.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.5:80"}}}}
		},
		"svc:campfire": {
			"TCP": {"443": {"HTTPS": true}},
			"Web": {"campfire.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.6:80"}}}}
		}
	}
}`

func TestFakeTailscaleDaemonServeStatus(t *testing.T) {
	daemon := &FakeTailscaleDaemon{ServeJSON: serveJSON}

	status, err := daemon.ServeStatus(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"campfire", "writebook"}, status.Services)
	assert.True(t, status.AllowFunnel["once.tail1234.ts.net:443"])
	assert.False(t, status.AllowFunnel["other.tail1234.ts.net:443"])
}

func TestFakeTailscaleDaemonServeStatusEmpty(t *testing.T) {
	daemon := &FakeTailscaleDaemon{ServeJSON: `{}`}

	status, err := daemon.ServeStatus(t.Context())
	require.NoError(t, err)
	assert.Empty(t, status.Services)
	assert.Empty(t, status.AllowFunnel)
}

func TestFakeTailscaleDaemonServeStatusErrors(t *testing.T) {
	daemon := &FakeTailscaleDaemon{ServeJSON: "not json"}
	_, err := daemon.ServeStatus(t.Context())
	require.ErrorContains(t, err, "parsing tailscale serve status")

	daemon = &FakeTailscaleDaemon{ServeErr: assert.AnError}
	_, err = daemon.ServeStatus(t.Context())
	require.ErrorIs(t, err, assert.AnError)
}

func TestCLITailscaleDaemon(t *testing.T) {
	daemon := func(res ExecResult, err error) *cliTailscaleDaemon {
		return &cliTailscaleDaemon{run: func(ctx context.Context, args ...string) (ExecResult, error) {
			return res, err
		}}
	}

	t.Run("parses command output", func(t *testing.T) {
		suffix, err := daemon(ExecResult{Stdout: statusJSON}, nil).MagicDNSSuffix(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "tail1234.ts.net", suffix)

		status, err := daemon(ExecResult{Stdout: serveJSON}, nil).ServeStatus(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []string{"campfire", "writebook"}, status.Services)
	})

	t.Run("strips warnings printed before the JSON", func(t *testing.T) {
		out := "Warning: client version \"1.86.0\" != tailscaled server version \"1.88.0\"\n" + statusJSON
		suffix, err := daemon(ExecResult{Stdout: out}, nil).MagicDNSSuffix(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "tail1234.ts.net", suffix)
	})

	t.Run("empty serve config exits nonzero but is not an error", func(t *testing.T) {
		res := ExecResult{Stderr: "No serve config found\n", ExitCode: 1}
		status, err := daemon(res, nil).ServeStatus(t.Context())
		require.NoError(t, err)
		assert.Empty(t, status.Services)
		assert.Empty(t, status.AllowFunnel)
	})

	t.Run("command failures surface stderr", func(t *testing.T) {
		res := ExecResult{Stderr: "failed to connect to local tailscaled\n", ExitCode: 1}
		_, err := daemon(res, nil).MagicDNSSuffix(t.Context())
		require.ErrorContains(t, err, "failed to connect to local tailscaled")

		_, err = daemon(res, nil).ServeStatus(t.Context())
		require.ErrorContains(t, err, "failed to connect to local tailscaled")

		// A failure that merely contains "does not exist" is still a failure,
		// not an empty serve config.
		res = ExecResult{Stderr: "socket /var/run/tailscale/tailscaled.sock does not exist\n", ExitCode: 1}
		_, err = daemon(res, nil).ServeStatus(t.Context())
		require.ErrorContains(t, err, "tailscaled.sock does not exist")
	})

	t.Run("runner errors propagate", func(t *testing.T) {
		_, err := daemon(ExecResult{}, assert.AnError).MagicDNSSuffix(t.Context())
		require.ErrorIs(t, err, assert.AnError)

		_, err = daemon(ExecResult{}, assert.AnError).ServeStatus(t.Context())
		require.ErrorIs(t, err, assert.AnError)
	})
}

func TestRunHostTailscale(t *testing.T) {
	stub := func(script string) {
		t.Helper()
		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "tailscale"), []byte("#!/bin/sh\n"+script), 0o755)
		require.NoError(t, err)
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	stub(`echo '{"MagicDNSSuffix":"tail1234.ts.net"}'`)
	res, err := runHostTailscale(t.Context(), "status", "--json")
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.JSONEq(t, `{"MagicDNSSuffix":"tail1234.ts.net"}`, res.Stdout)

	stub(`echo 'No serve config found' >&2; exit 1`)
	res, err = runHostTailscale(t.Context(), "serve", "status", "--json")
	require.NoError(t, err)
	assert.Equal(t, 1, res.ExitCode)
	assert.Equal(t, "No serve config found\n", res.Stderr)

	t.Setenv("PATH", t.TempDir())
	_, err = runHostTailscale(t.Context(), "status", "--json")
	require.ErrorContains(t, err, "running tailscale CLI")
}
