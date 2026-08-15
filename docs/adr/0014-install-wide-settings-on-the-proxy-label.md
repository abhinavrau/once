# Install-Wide Settings on the Proxy Label

Once stores Configuration in `once` labels: app settings on the app container,
proxy settings on the proxy, volume settings on the volume. Backup Repo
settings are the first configuration that is **install-wide**: they belong to
no Application, and backups have no container of their own to carry them -
restic runs as a throwaway container per command, and the
existence-as-truth doctrine Tailscale uses does not apply because backups need
an explicit `Enabled` flag (there is nothing whose existence could mean
"enabled", and disabling must not erase the configuration).

We decided `BackupRepoSettings` lives on the **Proxy container's label**, as a
field on `ProxySettings` - which makes it a top-level `backups` key in the
label JSON. This stretches the rule "proxy settings go with the proxy" into
"install-wide settings ride with the Proxy": the Proxy is the one container
every install always has, created at Setup and alive for the install's whole
life, so it is the install's anchor for state that belongs to no other
component.

Top-level placement is load-bearing, not cosmetic. `unknownFields` walks only
the top level of the decoded type (label.go), so an older binary rewriting the
proxy label for a port change carries an unmodelled `backups` key through
`labelExtra` under the preservation doctrine (ADR-0008). Nested one level
deeper inside a modelled key it would be silently destroyed - taking the Repo
Password, and with it every backup, along.

Alternatives rejected: a dedicated settings container (a container that exists
only to carry a label is a config file with extra steps, and it would have to
be booted, health-checked, and survived); the Application Data Volume labels
(per-app, and this is not); a file on the host (contradicts "everything in
Docker state").

## Consequences

- Observed state stays off the label. Docker labels are immutable, so writing
  one recreates the container; a `lastRun` field on the Proxy would bounce
  every Application's traffic on every backup run. Mutable observations go to
  the state file inside the Proxy container (a cache a Proxy roll erases) or
  are queried from restic directly (truth).
- The Repo Password and the offsite backend credential values are at rest,
  plaintext, in the proxy label - recorded in ADR-0013's at-rest inventory,
  with the threat model stated there.
- Any future install-wide setting follows this pattern: a top-level key on
  the proxy label, through the Label Codec.
- Every path that recreates the Proxy must round-trip the label through the
  codec, since it now carries state whose loss is unrecoverable by design.
