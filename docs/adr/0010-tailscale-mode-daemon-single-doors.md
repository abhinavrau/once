# Tailscale mode, daemon, and control seam resolve through single doors

The mode-selection rule of [ADR-0007](0007-sidecar-default-tailscale-mode.md)
(explicit mode wins, empty keeps a stored mode, fresh enables default to the
sidecar) existed twice - once in the resolver, once inline in the enable
lifecycle - synchronized by a comment; the stored-settings-to-daemon binding
was copy-pasted across five read paths; and the hidden headscale control seam
was read at eight scattered branch points, two of them in the frontends, each
re-implementing the same credential suppression. Every one of these is a
provider-shaped concept: multiple VPN providers beyond Tailscale are coming,
and a provider swap would have to find and change every copy.

We decided each concept gets exactly one function and callers receive the
result instead of re-deriving it: mode resolution happens once per enable (the
resolver also reports whether the enable is fresh, so the "--mode host" notice
is computed where warnings assemble and the enable lifecycle returns only an
error); read paths bind settings and daemon through one session function; and
one accessor is the only reader of the control-seam env vars, with credential
suppression absorbed into the enable workflow so the seam never leaks outside
the Docker package. A consequence of unifying on the resolver: a corrupt or
legacy label with no stored mode now self-heals to the sidecar by rebuilding
the topology on any re-enable, not only on credential changes.

We deliberately did not build a provider abstraction. One provider means the
seam is hypothetical, and a pass-through interface today would be guessed
wrong. The constraint this ADR records instead is that mode resolution, daemon
binding, session resolution, and the control-seam accessor must each remain a
single function - those four doors are where a real provider seam opens later,
and scattering any of them again paints it shut.
