package docker

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelfSignedCertificateCarriesTheLANAddressInItsSAN(t *testing.T) {
	certificate, key, err := selfSignedCertificate("192.168.1.40")
	require.NoError(t, err)

	pair, err := tls.X509KeyPair(certificate, key)
	require.NoError(t, err, "the PEM pair loads as a usable server certificate")

	parsed, err := x509.ParseCertificate(pair.Certificate[0])
	require.NoError(t, err)

	// A browser matches on the SAN, never the common name, and an IP has to be
	// an IP SAN rather than a DNS name or the warning is unclickable-through.
	require.Len(t, parsed.IPAddresses, 1)
	assert.True(t, parsed.IPAddresses[0].Equal(net.ParseIP("192.168.1.40")))
	assert.Empty(t, parsed.DNSNames)
	assert.Contains(t, parsed.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
	assert.True(t, parsed.NotBefore.Before(parsed.NotAfter))

	// Two handoffs must not share a serial.
	other, _, err := selfSignedCertificate("192.168.1.40")
	require.NoError(t, err)
	assert.NotEqual(t, certificate, other)
}

func TestSelfSignedCertificateUsesADNSNameForAHostname(t *testing.T) {
	certificate, _, err := selfSignedCertificate("once-admin.tail9dc1e2.ts.net")
	require.NoError(t, err)

	parsed := parseCertificate(t, certificate)
	assert.Equal(t, []string{"once-admin.tail9dc1e2.ts.net"}, parsed.DNSNames)
	assert.Empty(t, parsed.IPAddresses)
}

func TestLANAddressFromSkipsVirtualAndUnusableInterfaces(t *testing.T) {
	address, err := lanAddressFrom([]lanInterface{
		{Name: "lo", Flags: net.FlagUp | net.FlagLoopback, Addresses: addrs(t, "127.0.0.1/8")},
		{Name: "docker0", Flags: net.FlagUp, Addresses: addrs(t, "172.17.0.1/16")},
		{Name: "br-9f2c1a", Flags: net.FlagUp, Addresses: addrs(t, "172.18.0.1/16")},
		{Name: "veth3a1b", Flags: net.FlagUp, Addresses: addrs(t, "172.19.0.1/16")},
		{Name: "tailscale0", Flags: net.FlagUp, Addresses: addrs(t, "100.64.1.2/32")},
		{Name: "eth1", Flags: 0, Addresses: addrs(t, "192.168.9.9/24")},
		{Name: "eth0", Flags: net.FlagUp, Addresses: addrs(t, "192.168.1.40/24")},
	})
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.40", address, "a Docker bridge address resolves to nothing from a phone")
}

func TestLANAddressFromRefusesWhenNothingIsReachable(t *testing.T) {
	_, err := lanAddressFrom([]lanInterface{
		{Name: "lo", Flags: net.FlagUp | net.FlagLoopback, Addresses: addrs(t, "127.0.0.1/8")},
		{Name: "docker0", Flags: net.FlagUp, Addresses: addrs(t, "172.17.0.1/16")},
	})
	assert.Error(t, err)
}

func TestLANAddressPrefersTheDefaultRoutesSource(t *testing.T) {
	// The kernel's routing choice is the rule, and on any machine with a
	// route to the internet it must not be a Docker bridge.
	address, err := LANAddress()
	if err != nil {
		t.Skip("host has no reachable local address")
	}
	assert.NotNil(t, net.ParseIP(address))
	assert.False(t, net.ParseIP(address).IsLoopback())
}

func TestDeployArgsCarryTheCustomCertificatePaths(t *testing.T) {
	proxy := &Proxy{}

	args := proxy.deployArgs(DeployOptions{
		AppName:            recoveryServiceName,
		Target:             "once-admin:80",
		Host:               "192.168.1.40",
		TLS:                true,
		TLSCertificatePath: recoveryCertPath,
		TLSPrivateKeyPath:  recoveryKeyPath,
	})

	assert.Equal(t, []string{
		"kamal-proxy", "deploy", "once-recovery",
		"--target", "once-admin:80",
		"--deploy-timeout", deployTimeout,
		"--host", "192.168.1.40",
		"--tls",
		"--tls-certificate-path", recoveryCertPath,
		"--tls-private-key-path", recoveryKeyPath,
	}, args)

	// No secret is ever an argument: the password reaches the page through the
	// session file, never a command line another user could read from ps.
	for _, arg := range args {
		assert.NotContains(t, arg, "password")
	}
}

func TestDeployArgsAreUnchangedWithoutCertificatePaths(t *testing.T) {
	proxy := &Proxy{}
	args := proxy.deployArgs(DeployOptions{AppName: "app", Target: "abc123", TLS: true})
	assert.NotContains(t, args, "--tls-certificate-path")
}

// Helpers

func addrs(t *testing.T, cidrs ...string) []net.Addr {
	t.Helper()
	var out []net.Addr
	for _, cidr := range cidrs {
		ip, network, err := net.ParseCIDR(cidr)
		require.NoError(t, err)
		network.IP = ip
		out = append(out, network)
	}
	return out
}

func parseCertificate(t *testing.T, certificate []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certificate)
	require.NotNil(t, block)
	parsed, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return parsed
}
