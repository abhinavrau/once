# Design: the App Profile seam - catalog columns to app settings

Status: done
Labels: wayfinder:grilling
Blocked by: 01 (resolved)

## Question

How do per-app non-defaults (data path, health path, URL env var) flow from the
catalog into a deployed Application, and where do they live afterwards?

Locked already: they become new columns on `catalog.App`, copied into
`ApplicationSettings` at deploy so they persist in the `once` label, and settable
by flag for arbitrary images. This ticket specifies the details:

- Exact field names and types on `catalog.App` and `ApplicationSettings`, and the
  glossary term for the concept (proposed: **App Profile**).
- What the defaults are, and how "unset" is represented so existing apps keep
  today's behaviour. `AppVolumeMountTargets` is currently two paths
  (`/storage` + `/rails/storage`) mounted from one volume - does a custom data
  path replace both, or add a third mount?
- `DefaultApplicationSettings` is documented as the single source of settings
  defaults (application_settings.go:71-76). Does the catalog override it, or
  feed it?
- Which call sites stop reading the package-level constants: `volumeMounts`
  (application.go:536), the backup copy source
  (`AppVolumeMountTargets[0]`, application_backup.go:215-223), `verifyHTTP`'s
  `HealthCheckPath` (application.go:490-513), and `deployArgs`
  (proxy.go:221-233), which must now pass `--health-check-path` - ticket 01
  confirmed the pinned image accepts it.
- Two more call sites ticket 01 surfaced. `internal/userstats/scraper.go:28,68`
  filters health-check hits out of user stats by comparing against `/up`, so it
  needs a service-to-path lookup or Vaultwarden's `/alive` probes get counted as
  real user traffic. `internal/background/admin.go:68` serves `/up` for Once's
  own admin server and must **not** be parameterised.
- The backup/restore asymmetry ticket 01 found: backup copies from
  `AppVolumeMountTargets[0]`, but `populateVolume` ignores the const entirely and
  mounts the volume at a hardcoded `/data` (application_backup.go:228-249),
  relying on the archive's `data/` tar prefix. That it happens to coincide with
  Vaultwarden's data path is luck, not design. Specify both sides together.
- `URLEnvVar`: when the app's URL changes (host change, exposure change), the env
  var must be refilled. Which path does that - `ApplySettings`? What happens if
  the user also set the same key by hand via `--env`?
- The label round-trip: these fields must survive backup and restore, since
  restore recreates the app from `once.application.json`. Confirm against the
  label codec's preservation doctrine (ADR-0008).
- The Vaultwarden catalog entry itself: name, alias, image ref, data path
  `/data`, health path `/alive`, URL env var `DOMAIN`. Note the app-name
  derivation problem - `NameFromImageRef` on the image ref yields `server`, so
  the catalog entry must carry the name.
- Whether existing apps need a backfill, and what the migration story is for an
  app deployed before these fields existed.

## Resolution

### The concept: App Profile

**App Profile** - the small set of per-app deployment facts that Once cannot
derive from the image: what the app is called, where it keeps its Application
Data, what path it answers a health check on, and which environment variable it
wants its own URL in. Empty means "the behaviour Once had before App Profiles
existed".

Glossary entry goes in CONTEXT.md under Core, next to **Application**.

### Shape and ownership

`AppProfile` lives in `internal/docker` (next to `ApplicationSettings`).
`internal/catalog` imports `docker`; verified there is no cycle, since
`internal/docker` does not import `internal/catalog`.

```go
type AppProfile struct {
	BaseName   string `json:"baseName,omitempty"`
	DataPath   string `json:"dataPath,omitempty"`
	HealthPath string `json:"healthPath,omitempty"`
	URLEnvVar  string `json:"urlEnvVar,omitempty"`
}

// Resolved accessors - the zero value means Once's built-in behaviour. Each
// accessor is named for what it returns, so none collides with its raw field.
func (p AppProfile) AppName(imageRef string) string // BaseName, else NameFromImageRef(imageRef)
func (p AppProfile) MountTargets() []string         // {DataPath}, else DefaultAppVolumeMountTargets
func (p AppProfile) HealthCheckPath() string        // HealthPath, else DefaultHealthCheckPath
```

All-string fields make the struct comparable, so `ApplicationSettings.Equal`
gains one line (`s.Profile != other.Profile`), matching the existing
`SMTPSettings` / `ContainerResources` precedent. One nested field is added:

```go
Profile AppProfile `json:"profile,omitempty"`
```

The two package-level constants become explicit defaults and stop being read
directly: `HealthCheckPath` -> `DefaultHealthCheckPath`, `AppVolumeMountTargets`
-> `DefaultAppVolumeMountTargets`. The field/method name split (`HealthPath` the
raw value, `HealthCheckPath()` the resolved one) keeps the resolved name
matching kamal-proxy's `--health-check-path`.

### Data path replaces the mount targets

A custom `DataPath` **replaces** both default targets; it does not add a third.
Three mount points expose one directory three times, so Vaultwarden would also
see its own data under `/storage` and `/rails/storage`, and Rails-shaped paths
would leak into every non-Rails app. `DataPath` is a single string rather than a
list, so the two-path Rails default stays an internal detail that catalog
authors never see.

### Defaults: the zero value is the default

`DefaultApplicationSettings` leaves `Profile` zero and remains the single source
of settings defaults - it is neither overridden by nor fed from the catalog. The
*interpretation* of zero lives in the accessors above. Layering at deploy is:

    DefaultApplicationSettings() -> catalog entry's Profile -> flags

**This is why no backfill is needed.** An `ApplicationSettings` label written
before App Profiles existed has no `profile` key, decodes to the zero value, and
therefore behaves exactly as it does today. (Round 2 confirms the migration
story for the arbitrary-image case.)

### The catalog seam

`Resolve` cannot return a bare image ref any more, because a string cannot carry
a Profile. It returns the entry:

```go
func Resolve(s string) App   // unmatched argument -> App{ImageRef: s}
```

Three call sites change: `deploy.go:34`, `install.go:79`,
`install_image_form.go:27`. One function, not two - a second lookup function
would be a second way to ask the same question.

A fourth site is not a `Resolve` caller but drops the entry just as effectively:
`install_app_list.go:41` emits `InstallAppSelectedMsg{ImageRef: app.ImageRef}`, a
bare string, so a Vaultwarden installed from the TUI menu would arrive with no
Profile. The message carries the `catalog.App` instead. A reverse lookup by image
ref is the alternative and it breaks the moment two entries share an image.

### The App Name comes from the Profile

`NameFromImageRef("vaultwarden/server")` is `server`, so Vaultwarden would deploy
as the Application `server` at `server.localhost`. The catalog cannot correct this
from the CLI layer: `ns.Deploy` (`namespace.go:239`) and `ns.Restore`
(`namespace.go:456`) both *overwrite* `settings.Name` with
`UniqueName(NameFromImageRef(settings.Image))`, so a caller-set name is discarded
and a restore repeats the error.

Hence the `BaseName` field. `Deploy` and `Restore` call
`UniqueName(settings.Profile.AppName(settings.Image))`, and the Host derivation at
`deploy.go:45` calls the same accessor - otherwise the App Name and the Host
disagree. Because `BaseName` rides in the `once` label, a bare-metal restore
recovers the right name with no extra plumbing.

**Every catalog entry sets `BaseName` from its `Alias`, which also fixes an
existing wart.** Measured behaviour today:

| Image ref | App Name today | App Name after |
|---|---|---|
| `ghcr.io/basecamp/once-campfire` | `once-campfire` | `campfire` |
| `ghcr.io/basecamp/fizzy` | `fizzy` | `fizzy` |
| `ghcr.io/basecamp/writebook` | `writebook` | `writebook` |
| `vaultwarden/server` | `server` | `vaultwarden` |

Campfire alone disagrees with its own alias today, and it deploys to
`once-campfire.localhost`. The change reaches new deploys only - an existing
Application keeps the name recorded in its `once` label - so the cost is
documentation and habit, and the gain is that the alias a user types is the name
they get, for every catalog app.

### Flags

Three flags registered in `settingsFlags`, so `once deploy` and `once update`
both get them, exactly as every other setting does:

    --data-path  --health-check-path  --url-env-var

All three are changeable on `update`. `--data-path` looked as though it had to be
immutable, but that is wrong: the Application Data Volume's root holds the
*contents* of the data directory, so a new mount target shows the app the same
files at a new path. Nothing is hidden and nothing is lost.

No flag is offered for `BaseName`. The App Name already has its own derivation
rule and `--host` already covers what a user wants to change.

### Validation

The three path and name values are user input that reaches a Docker mount
specification, a probe URL, and a process environment. `ApplicationSettings.Validate`
rejects them, so the CLI and the TUI are both covered by one rule, as
`ErrAutoBackupWithoutPath` is today:

- `DataPath` must be absolute, and must not be `/`. A relative mount target fails
  obscurely inside Docker, and `/` shadows the container's root filesystem.
- `HealthPath` must begin with `/`. Without it the probe URL silently becomes
  `https://hostalive`.
- `URLEnvVar` must not contain `=`, which would corrupt the environment list.

### The Vaultwarden catalog entry

```go
{
	Name:     "Vaultwarden",
	Alias:    "vaultwarden",
	ImageRef: "vaultwarden/server",
	Profile: docker.AppProfile{
		BaseName:   "vaultwarden",
		DataPath:   "/data",
		HealthPath: "/alive",
		URLEnvVar:  "DOMAIN",
	},
}
```

The image ref carries no tag, matching the three existing entries. `AutoUpdate`
stays on by default, as the map locked during charting. `/alive` is
unauthenticated and checks the database connection, so it is a true readiness
signal rather than a liveness stub.

### The URL environment variable is computed, never stored

Only `URLEnvVar` (the *name*) persists. The *value* is computed at container
creation, because `BuildEnv` is always called with the Application in hand
(`application.go:414`, `application_backup.go:281`).

`BuildEnv` takes the URL as a parameter and appends `URLEnvVar=<url>` **before**
the `s.EnvVars` loop. A user's explicit `--env DOMAIN=...` therefore wins with
no extra code, exactly as SMTP env vars are overridable today.

No refill path and no `ApplySettings` change are needed: Host, Exposure and
DisableTLS are all compared by `Equal`, so any change to them already rolls the
container, and the roll recomputes the environment from scratch.

**Tailnet apps use the tailnet URL.** `Application.URL()` returns `""` under
Tailnet exposure, which would leave `DOMAIN` empty and silently disable
Vaultwarden's WebAuthn. `deployWithVolume` already calls
`a.namespace.Tailscale().Enabled(ctx)` on the line above `BuildEnv`, so it asks
the same seam for `DomainSuffix(ctx)` and passes
`TailscaleSettings.TailnetURL(host)` instead. `TailnetURL` is pure string
concatenation (`tailscale.go:160`); the suffix is learned at
`once tailscale enable`, long before any deploy, so there is no chicken-and-egg
with the Tailscale Service advertisement. The error is ignored, never fatal, as
`dashboard.go:410` already does - a live daemon read here must not fail a Deploy.

**Warn when the URL cannot be resolved.** The tailnet URL narrows the empty-URL
window but does not close it: `DomainSuffix` returns `""` before the daemon joins
the tailnet, and it is persisted lazily, so nothing re-rolls the app when the
suffix later becomes known. Rare-and-silent is worse than rare-and-visible, so
Deploy emits a warning when the Profile declares a `URLEnvVar` and the resolved
URL is empty. The channel exists: `ns.Deploy` returns warnings and `deploy.go:61`
prints them.

### Restore needs no change

The ticket asked to specify both sides of the backup/restore asymmetry together.
Having read both, **there is no asymmetry to fix.** Backup rewrites the source
directory's basename to `BackupDataDir` unconditionally
(`application_backup.go:221`), and restore mounts the volume at `/data` and
unpacks `data/` into it (`namespace.go:671-676`). The `/data` mountpoint is
paired with the archive layout, not with the app's data path, so it is already
correct for every data path - including Vaultwarden's, where the match is a
coincidence rather than a dependency.

Only backup's *source* path reads the Profile. `populateVolume` keeps its literal
`/data`, with a comment naming its true coupling to `BackupDataDir`, so nobody
changes one out of step with the other. Parameterising a constant that does not
vary buys nothing.

### User stats filter on source address, not path

`scraper.go:28,68` drops health-check hits by comparing `Path` to one constant,
which a per-app path breaks. Rather than build a service-to-path lookup that must
be kept in sync with every app's settings, the scraper filters the **Proxy's own
source address** - it already holds a Docker client with `ContainerInspect`. One
address test covers every health path, for every Application, permanently.

Blast radius is small either way: ticket 01 established that kamal-proxy stops
probing a single target once it is healthy, so the over-count is one distinct
address in hours when a deploy happened.

### Call sites

Read the Profile instead of the constant:

1. `application.go:497` - `verifyHTTP` -> `a.Settings.Profile.HealthCheckPath()`
2. `application.go:536` - `volumeMounts` -> `a.Settings.Profile.MountTargets()`
3. `application_backup.go:215,221` - backup source -> `MountTargets()[0]`
4. `proxy.go:52,221` - `DeployOptions` gains `HealthCheckPath`; `deployArgs`
   passes `--health-check-path` (always - kamal-proxy's own default is `/up`, so
   passing it explicitly is a no-op)
5. `application.go:475` - the `Proxy().Deploy` call supplies it
6. `application_settings.go:167` + `application.go:414` - `BuildEnv` gains the
   URL parameter
7. `scraper.go:28,68` - replaced by the source-address filter
8. `catalog.go` + `deploy.go:34`, `install.go:79`, `install_image_form.go:27` -
   `Resolve` returns `App`; every entry gains a `Profile`, with `BaseName` set
   from its `Alias`
9. `install_app_list.go:41` - `InstallAppSelectedMsg` carries `catalog.App`, not
   an image ref string
10. `namespace.go:239` and `namespace.go:456` - `UniqueName(NameFromImageRef(...))`
    -> `UniqueName(settings.Profile.AppName(settings.Image))`
11. `deploy.go:45` - Host derivation uses the same `AppName` accessor
12. `settings_flags.go` - three new flags, registered once for `deploy` and
    `update`, plus their `applyChanges` branches
13. `application_settings.go:95` - `Validate` gains the three Profile rules
14. `application_settings.go:131` - `Equal` compares `Profile`
15. `application_test.go:25,27,44,58,69,79` - update alongside

Deliberately **not** changed:

- `background/admin.go:68` - serves `/up` for Once's own admin server. Unrelated
  to app health; must stay a literal.
- `application_backup.go:228-249` - `populateVolume`'s `/data`, per above.

### Label round-trip

Automatic, no work. `Profile` is a modelled field on `ApplicationSettings`, which
already goes through the Label Codec, so ADR-0008's preservation doctrine covers
it: an older Once that does not model `profile` carries it through untouched
rather than shedding it. Backup writes `once.application.json` from
`Settings.Marshal()` and `Namespace.Restore` rebuilds from it, so the Profile
survives a bare-metal restore with no extra plumbing.

### Out of scope (recorded on the map)

Tailscale Funnel's dual origin. A funnelled Application is reachable at both its
tailnet URL and its per-node Funnel URL, and WebAuthn is origin-bound, so one
`DOMAIN` value cannot serve both. No App Profile field shape fixes one variable
against two origins.
