package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
// emits, per docktail's parsing of the same output. The node's funnel-enabled
// endpoint proxies to the same backend as svc:writebook, which is how the
// funnel is attributed to that app.
const serveJSON = `{
	"TCP": {"443": {"HTTPS": true}},
	"Web": {
		"once.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.5:80"}}}
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
	assert.Equal(t, map[string]string{"writebook": "once.tail1234.ts.net:443"}, status.Funnels)
}

func TestFakeTailscaleDaemonServeStatusFunnels(t *testing.T) {
	funnels := func(serveJSON string) map[string]string {
		t.Helper()
		daemon := &FakeTailscaleDaemon{ServeJSON: serveJSON}
		status, err := daemon.ServeStatus(t.Context())
		require.NoError(t, err)
		return status.Funnels
	}

	writebook := `"Services": {"svc:writebook": {
		"Web": {"writebook.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.5:80"}}}}
	}}`

	// A serve-only endpoint (funnel disabled or absent) funnels nothing.
	assert.Empty(t, funnels(`{
		"Web": {"once.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.5:80"}}}},
		"AllowFunnel": {"once.tail1234.ts.net:443": false},` + writebook + `}`))
	assert.Empty(t, funnels(`{
		"Web": {"once.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.5:80"}}}},` + writebook + `}`))

	// A funnel flag with no matching web handler (config still settling)
	// attributes nothing either.
	assert.Empty(t, funnels(`{"AllowFunnel": {"once.tail1234.ts.net:443": true},` + writebook + `}`))

	// A live funnel whose backend belongs to no advertised service is not
	// attributed to any app: it may be another node's, or a stale entry.
	assert.Empty(t, funnels(`{
		"Web": {"once.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.9:80"}}}},
		"AllowFunnel": {"once.tail1234.ts.net:443": true},` + writebook + `}`))

	// The two sides are programmed from separate labels, so the join is on the
	// backend address: a scheme that differs between them still attributes.
	assert.Equal(t, map[string]string{"writebook": "once.tail1234.ts.net:443"}, funnels(`{
		"Web": {"once.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "https://10.0.0.5:80"}}}},
		"AllowFunnel": {"once.tail1234.ts.net:443": true},` + writebook + `}`))

	// Several endpoints funnelling one backend read the same on every poll.
	assert.Equal(t, map[string]string{"writebook": "once.tail1234.ts.net:443"}, funnels(`{
		"Web": {
			"once.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.5:80"}}},
			"once.tail1234.ts.net:8443": {"Handlers": {"/": {"Proxy": "http://10.0.0.5:80"}}}
		},
		"AllowFunnel": {
			"once.tail1234.ts.net:443": true,
			"once.tail1234.ts.net:8443": true
		},` + writebook + `}`))

	// Two apps funnelled at once are keyed apart by name, each with the public
	// endpoint it serves on.
	assert.Equal(t, map[string]string{
		"writebook": "once.tail1234.ts.net:8443",
		"campfire":  "once.tail1234.ts.net:443",
	}, funnels(`{
		"Web": {
			"once.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.6:80"}}},
			"once.tail1234.ts.net:8443": {"Handlers": {"/": {"Proxy": "http://10.0.0.5:80"}}}
		},
		"AllowFunnel": {
			"once.tail1234.ts.net:443": true,
			"once.tail1234.ts.net:8443": true
		},
		"Services": {
			"svc:writebook": {
				"Web": {"writebook.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.5:80"}}}}
			},
			"svc:campfire": {
				"Web": {"campfire.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.6:80"}}}}
			}
		}
	}`))
}

func TestFakeTailscaleDaemonServeStatusEmpty(t *testing.T) {
	daemon := &FakeTailscaleDaemon{ServeJSON: `{}`}

	status, err := daemon.ServeStatus(t.Context())
	require.NoError(t, err)
	assert.Empty(t, status.Services)
	assert.Empty(t, status.Funnels)
}

func TestFakeTailscaleDaemonServeStatusErrors(t *testing.T) {
	daemon := &FakeTailscaleDaemon{ServeJSON: "not json"}
	_, err := daemon.ServeStatus(t.Context())
	require.ErrorContains(t, err, "parsing tailscale serve status")

	daemon = &FakeTailscaleDaemon{ServeErr: assert.AnError}
	_, err = daemon.ServeStatus(t.Context())
	require.ErrorIs(t, err, assert.AnError)
}

func TestFakeTailscaleDaemonServeEnablementURL(t *testing.T) {
	url, err := (&FakeTailscaleDaemon{ProbeOutput: serveDisabledOutput}).ServeEnablementURL(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "https://login.tailscale.com/f/serve?node=nodeIDabc123CNTRL", url)

	url, err = (&FakeTailscaleDaemon{}).ServeEnablementURL(t.Context())
	require.NoError(t, err)
	assert.Empty(t, url)

	_, err = (&FakeTailscaleDaemon{ProbeErr: assert.AnError}).ServeEnablementURL(t.Context())
	require.ErrorIs(t, err, assert.AnError)
}

// serveDisabledOutput is what `tailscale serve --https=443 <target>` prints on
// a tailnet that has never enabled Serve, before it blocks polling for someone
// to click the link.
const serveDisabledOutput = `
Tailscale Serve is not enabled on your tailnet.
To enable, visit:

         https://login.tailscale.com/f/serve?node=nodeIDabc123CNTRL

`

// serveEnabledOutput is what the probe's deliberately unservable target earns
// once Serve is enabled: the CLI rejects it and exits, having written nothing.
// The usage text it prints carries a tailscale.com link, which must not read as
// an enablement link.
const serveEnabledOutput = `error: failed apply web serve: must be a URL starting with one of the supported schemes: [http https https+insecure unix]

USAGE
  serve [flags] <target>

For more information, see https://tailscale.com/kb/1242/tailscale-serve
`

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
		assert.Empty(t, status.Funnels)
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

func TestCLITailscaleDaemonServeEnablementURL(t *testing.T) {
	type probeCall struct {
		timeout time.Duration
		args    []string
	}
	daemon := func(called *probeCall, res ExecResult, err error) *cliTailscaleDaemon {
		return &cliTailscaleDaemon{probe: func(ctx context.Context, timeout time.Duration, args ...string) (ExecResult, error) {
			*called = probeCall{timeout: timeout, args: args}
			return res, err
		}}
	}

	t.Run("reports the link the CLI printed", func(t *testing.T) {
		var called probeCall
		url, err := daemon(&called, ExecResult{Stdout: serveDisabledOutput, ExitCode: 143}, nil).ServeEnablementURL(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "https://login.tailscale.com/f/serve?node=nodeIDabc123CNTRL", url)

		// The probe must ask for an HTTPS serve (the type the enablement check
		// gates on) with a target the CLI rejects before writing any config, and
		// must be bounded so a blocked CLI cannot hold the enable open.
		assert.Equal(t, []string{"serve", "--bg", "--https=443", "once-serve-probe://invalid"}, called.args)
		assert.Equal(t, serveProbeTimeout, called.timeout)
	})

	t.Run("reads the link from stderr too", func(t *testing.T) {
		var called probeCall
		url, err := daemon(&called, ExecResult{Stderr: serveDisabledOutput}, nil).ServeEnablementURL(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "https://login.tailscale.com/f/serve?node=nodeIDabc123CNTRL", url)
	})

	t.Run("an enabled tailnet reports no link", func(t *testing.T) {
		var called probeCall
		url, err := daemon(&called, ExecResult{Stderr: serveEnabledOutput, ExitCode: 1}, nil).ServeEnablementURL(t.Context())
		require.NoError(t, err)
		assert.Empty(t, url)
	})

	t.Run("probe failures propagate", func(t *testing.T) {
		var called probeCall
		_, err := daemon(&called, ExecResult{}, assert.AnError).ServeEnablementURL(t.Context())
		require.ErrorIs(t, err, assert.AnError)
	})
}

func TestBoundedTailscaleCommand(t *testing.T) {
	assert.Equal(t, []string{"timeout", "5", "tailscale", "serve", "--bg"},
		boundedTailscaleCommand(5*time.Second, []string{"serve", "--bg"}))

	// busybox reads a 0 timeout as "no timeout", which would restore the hang
	// the probe exists to avoid.
	assert.Equal(t, []string{"timeout", "1", "tailscale", "serve"},
		boundedTailscaleCommand(100*time.Millisecond, []string{"serve"}))
}

func TestRunHostTailscale(t *testing.T) {
	stub := func(script string) { stubHostTailscale(t, script) }

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

// A host CLI blocked on the enablement link is the whole reason the probe
// exists: killing it at the timeout must still hand back what it printed, and
// must not wait on a child that outlives it.
func TestRunHostTailscaleBoundedKeepsOutputOfAKilledCommand(t *testing.T) {
	stubHostTailscale(t, "printf '%s' \""+serveDisabledOutput+"\"\nsleep 60\n")

	start := time.Now()
	res, err := runHostTailscaleBounded(t.Context(), 100*time.Millisecond, serveProbeArgs...)
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Equal(t, "https://login.tailscale.com/f/serve?node=nodeIDabc123CNTRL", parseServeEnablementURL(res.Stdout))
}

// Helpers

// stubHostTailscale puts a shell-script tailscale on PATH, so the host runner
// can be driven without a real CLI.
func stubHostTailscale(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tailscale"), []byte("#!/bin/sh\n"+script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
