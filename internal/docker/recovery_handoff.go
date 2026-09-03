package docker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/basecamp/once/internal/handoff"
)

// The temporary route that publishes the recovery handoff page. The cert lands
// in the proxy's config volume, the same place the state file already goes, so
// no new mount is needed; both files are removed when the screen closes.
const (
	recoveryServiceName = "once-recovery"
	recoveryCertPath    = stateFileDir + "/once-recovery-cert.pem"
	recoveryKeyPath     = stateFileDir + "/once-recovery-key.pem"
)

// adminServiceName is the Tailscale Service once-admin is published under, and
// so the host of its tailnet URL.
const adminServiceName = "once-admin"

// RecoveryHandoffPath is the session file the custody flow writes and the
// daemon's admin server reads. It sits beside the admin socket, in the runtime
// directory the daemon already owns.
func RecoveryHandoffPath() string {
	return filepath.Join(adminRuntimeDir(), "once-recovery-handoff.json")
}

// LANAddress is the address a phone on the same network can reach this host at:
// the source address of the default route, resolved by a UDP dial that sends no
// packet. Taking the kernel's own routing choice is what excludes docker0, br-*
// and veth interfaces - none of them carries a default route - so there is no
// interface-picking heuristic to get wrong.
//
// The scan is the offline fallback, where there is no default route to read.
func LANAddress() (string, error) {
	if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		defer conn.Close()
		if address, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			return address.IP.String(), nil
		}
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("listing network interfaces: %w", err)
	}

	candidates := make([]lanInterface, 0, len(interfaces))
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		candidates = append(candidates, lanInterface{Name: iface.Name, Flags: iface.Flags, Addresses: addresses})
	}
	return lanAddressFrom(candidates)
}

// RecoveryHandoff is a live phone handoff: the page is reachable at URL until
// Close tears it down. Closing is not optional - it is what removes the
// temporary route, the certificate and the session, and the TTL is only the
// backstop for a process that died before it could.
type RecoveryHandoff struct {
	url   string
	store *handoff.Store
	proxy *Proxy
	admin *Admin
	// routed records that a temporary proxy service and certificate exist. The
	// tailnet path publishes neither, so it has nothing to take down.
	routed bool
	// bootedAdmin records that once-admin was started for this handoff alone,
	// on an install that had none, so it should not outlive it.
	bootedAdmin bool
}

// StartRecoveryHandoff publishes the Recovery Kit at a one-time HTTPS URL a
// phone on the same network can open.
//
// Both routes end at the same page on the daemon's admin server. Over the
// tailnet, once-admin is already published with a real certificate, so there is
// nothing to mint and no warning to click through. Otherwise the page is served
// on the host's LAN address behind a certificate Once signs itself, which does
// warn - accepted deliberately, and the screen says so before the user scans.
func (n *Namespace) StartRecoveryHandoff(ctx context.Context, kit handoff.Kit) (*RecoveryHandoff, error) {
	// The daemon serves the page; without it the QR would lead to nothing.
	if err := n.Admin().RequireDaemon(); err != nil {
		return nil, fmt.Errorf("the phone handoff needs the background daemon, which serves the page: %w", err)
	}

	origin, routed, err := n.recoveryOrigin(ctx)
	if err != nil {
		return nil, err
	}

	store := handoff.NewStore(RecoveryHandoffPath())
	token, err := store.Create(kit, handoff.TTL)
	if err != nil {
		return nil, err
	}

	live := &RecoveryHandoff{
		url:    origin + handoff.PagePath(token),
		store:  store,
		proxy:  n.Proxy(),
		admin:  n.Admin(),
		routed: routed,
	}

	if routed {
		// The LAN route targets once-admin, and only the Tailscale lifecycle
		// boots it - so a plain install has none, which is exactly the install
		// this route exists for. Borrow it when it is there, start one when it
		// is not, and remember which so Close leaves the install as it was.
		live.bootedAdmin = !live.admin.Exists(ctx)
		if err := live.admin.Boot(ctx); err != nil {
			_ = live.Close(ctx)
			return nil, fmt.Errorf("booting once-admin to serve the handoff page: %w", err)
		}

		if err := live.publishRoute(ctx, strings.TrimPrefix(origin, "https://")); err != nil {
			_ = live.Close(ctx)
			return nil, err
		}
	}
	return live, nil
}

func (h *RecoveryHandoff) URL() string { return h.url }

// Close removes everything the handoff put in place. It reports the first
// failure but always attempts every step: a leftover route outlives the screen,
// so a half-done teardown is worse than a noisy one.
func (h *RecoveryHandoff) Close(ctx context.Context) error {
	var firstErr error
	fail := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	fail(h.store.Clear())
	if h.routed {
		fail(h.proxy.Remove(ctx, recoveryServiceName))
		fail(h.proxy.Exec(ctx, []string{"rm", "-f", recoveryCertPath, recoveryKeyPath}))
	}
	if h.bootedAdmin {
		h.bootedAdmin = false
		fail(h.admin.Destroy(ctx))
	}
	return firstErr
}

// Private

// recoveryOrigin picks the origin the phone will reach the page on, and reports
// whether it needs a temporary proxy route to get there.
func (n *Namespace) recoveryOrigin(ctx context.Context) (string, bool, error) {
	if url := n.adminTailnetURL(ctx); url != "" {
		return url, false, nil
	}

	address, err := LANAddress()
	if err != nil {
		return "", false, err
	}
	return "https://" + address, true, nil
}

// adminTailnetURL is once-admin's own tailnet origin, which already carries a
// certificate the phone will trust, or "" when Tailscale is not enabled or the
// tailnet name is not known yet. Either way the LAN route is the fallback, so
// none of this is worth failing over.
func (n *Namespace) adminTailnetURL(ctx context.Context) string {
	if enabled, err := n.Tailscale().Enabled(ctx); err != nil || !enabled {
		return ""
	}
	suffix, err := n.Tailscale().DomainSuffix(ctx)
	if err != nil || suffix == "" {
		return ""
	}
	// once-admin is a node like any other, so it too can hold a deduped name
	// (once-admin-1); handing the phone the composed one sends it somewhere
	// that does not resolve.
	fqdns, _ := n.Tailscale().NodeFQDNs(ctx)
	return TailscaleSettings{DomainSuffix: suffix}.TailnetURLFor(adminServiceName, fqdns)
}

// publishRoute mints a certificate for the address the phone dials, copies it
// into the Proxy, and registers a temporary kamal-proxy service pointing at
// once-admin. The service is host-scoped to that address, so it cannot shadow
// any app's route. kamal-proxy redirects HTTP to HTTPS by default, which is
// what keeps the page off plain HTTP: a browser there gets no secure context,
// and suppresses the very save prompt this page exists to trigger.
func (h *RecoveryHandoff) publishRoute(ctx context.Context, address string) error {
	certificate, key, err := selfSignedCertificate(address)
	if err != nil {
		return err
	}

	files := map[string][]byte{
		filepath.Base(recoveryCertPath): certificate,
		filepath.Base(recoveryKeyPath):  key,
	}
	if err := h.proxy.copyFiles(ctx, stateFileDir, files); err != nil {
		return fmt.Errorf("copying the handoff certificate into the proxy: %w", err)
	}

	if err := h.proxy.Deploy(ctx, DeployOptions{
		AppName:            recoveryServiceName,
		Target:             h.proxy.namespace.name + "-admin:80",
		Host:               address,
		TLS:                true,
		TLSCertificatePath: recoveryCertPath,
		TLSPrivateKeyPath:  recoveryKeyPath,
	}); err != nil {
		return fmt.Errorf("publishing the handoff page: %w", err)
	}
	return nil
}

// Helpers

type lanInterface struct {
	Name      string
	Flags     net.Flags
	Addresses []net.Addr
}

// virtualInterfacePrefixes are the interfaces that carry an address a phone can
// never reach: Docker's own bridges and the veth ends of container pairs.
var virtualInterfacePrefixes = []string{"docker", "br-", "veth", "tailscale"}

func lanAddressFrom(interfaces []lanInterface) (string, error) {
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if slices.ContainsFunc(virtualInterfacePrefixes, func(prefix string) bool {
			return strings.HasPrefix(iface.Name, prefix)
		}) {
			continue
		}
		for _, address := range iface.Addresses {
			network, ok := address.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := network.IP.To4(); ip != nil && ip.IsPrivate() {
				return ip.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no local network address found to serve the handoff page on")
}

// selfSignedCertificate mints a short-lived certificate for the address the
// phone dials. The address goes in the SAN list - modern browsers ignore the
// common name entirely - and an IP address has to be an IP SAN, not a DNS one.
func selfSignedCertificate(address string) (certificate, key []byte, err error) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating the handoff key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generating the handoff serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: address},
		// Backdated so a phone whose clock runs slow still accepts it, and
		// short-lived because the handoff it serves lasts minutes.
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(address); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{address}
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &private.PublicKey, private)
	if err != nil {
		return nil, nil, fmt.Errorf("signing the handoff certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(private)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding the handoff key: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		nil
}
