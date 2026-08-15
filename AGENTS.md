# AGENTS.md

## Global agent instructions

- When reporting information to me, be extremely concise and sacrifice grammar for the sake of concision.
- Never use the em dash "—". Use plain "-" instead
- When writing commit messages, NEVER auto-add your agent name as co-author
- Never manually modify CHANGELOG.md files or any files that are marked as auto-generated.
- When making technical decisions, do not give much weight to development cost. Instead, prefer quality, simplicity, robustness and long term maintainability. This makes sure you find the real problem so your fix will actually solve it.
- When end-to-end testing a product, be picky about UI you see and be obsessed with pixel perfection.
- If something clearly looks off, even if it is not directly related to what you are doing, try to get it fixed along the way.
- Apply that same high standard of engineering excellence: lint, test failures, and test flakiness. If you see one, even if it is not caused by what you are working on right now, still get it fixed.

- Use `gh-axi` for GitHub instead of `gh` 
- `chrome-devtools-axi` for browser automation.

## Project Overview

Once is a CLI/TUI tool for installing and managing web applications from Docker images. It's designed to make self-hosting as easy as possible.

Once uses a proxy server (github.com/basecamp/kamal-proxy) to route traffic to the application containers, which allows it to provide zero-downtime restarts and upgrades, automatic SSL, and multiple applications running on a single server. A single instance of Once may deploy one proxy container, along with multiple application containers. Host-based routing is used inside the proxy to route traffic to the correct application container.

## Design and Concepts

### Container Naming

All containers are namespaced (default namespace: "once"):
- Proxy: `{namespace}-proxy`
- Apps: `{namespace}-app-{appName}-{randomID}` (`randomID` is a random suffix, not a truncated container ID)

This allows us to easily identify the app containers, but still allows us to
boot a second app container without naming collisions when we want to deploy a
new version without downtime.

### Data and State

Once deals primarily with two classes of state:

- Application data: this is stored in Docker volumes to provide persistent
storage between versions of the app container. Once provides one volume for
each app, which it will mount into two locations (`/storage` and
`/rails/storage`) to match typical app conventions. Once may provide backup and
restore features on the contents of those volumes. But it does not otherwise
touch the volume contents, or have any opinions about what should be in there
-- this data is entirely for each app's own use.

- Configuration: this keeps track of the applications and settings that have
been set up using Once itself. For example, the list of applications that are
deployed, the hostname and TLS settings for each, any custom port settings for
the proxy, and so on. Once stores this information as JSON strings in an `once`
label on the containers and volumes, so that everything is stored within the
Docker state. Application settings go with the app container; proxy settings go
with the proxy container; volume settings (like encryption keys) go with the
volume.

## Build Commands

```bash
make build       # Build binary to bin/ (CGO disabled)
make test        # Run unit tests (internal/ packages)
make integration # Run integration tests (requires Docker)
make lint        # Run golangci-lint
```

## Testing notes

- The integration suite runs under a total `go test` timeout, set explicitly to
  25 minutes in the Makefile: a healthy run now takes about 10 minutes of real
  Docker work, and go's 10-minute default had begun reporting a passing suite as
  a timeout panic. A single hung test still consumes the whole budget and still
  reports as a timeout panic, masking the real culprit. When this happens, read
  the goroutine dump for the goroutine stuck in the test body (e.g.
  `appDriver.waitUntil`), not the alarm or the background `scraper.go`
  stats-stream goroutines that always appear.
- Run the suite under `flock /tmp/once-integration.lock` whenever another agent
  may share the Docker daemon. Two concurrent runs tear down each other's
  containers, which surfaces as "network not found" or a proxy that vanishes
  mid-test - and looks like flakiness rather than a collision.
- TUI forms are integration-tested in `integration/ui_test.go` by clicking named
  targets - `field:<key>` for a field, `submit`/`action`/`cancel` for the buttons -
  so adding, removing, or reordering a field does not break the drivers. The
  driver's click helper asserts the target is registered before sending, so
  renaming a key without updating its driver fails the test immediately, naming
  the target, instead of silently no-opping. The driver's message pump fails the
  test the moment a form emits `ui.FormSubmitRejectedMsg`, naming the field key
  and reason, so a rejected submit is an immediate assertion rather than a
  `waitUntil` timeout.
- Integration tests pull images from Docker Hub even when they are cached locally
  (`ImagePull` always contacts the registry). If tests hang or fail with
  "failed to fetch anonymous token", "context deadline exceeded" on
  `images/create`, or a `read tcp [<ipv6>]... connection timed out`, the host's
  IPv6 route to Docker Hub is flapping - an environment problem, not test
  flakiness. Confirm with a plain `docker pull hello-world` outside the suite;
  it will fail the same way. Wait for the route to recover (it usually does
  within minutes) and re-run, or force dockerd to IPv4 if it keeps recurring.
  Don't burn time bisecting the tests for this failure signature.
- Tailscale SaaS paths (the OAuth probe against `api.tailscale.com`) can't run in
  CI - the integration harness only exercises the headscale control seam, which
  skips OAuth. Cover SaaS-only logic with httptest unit tests, and keep any request
  that must match docktail's real API call byte-compatible with it.
  docktail reference docs: https://docktail.org/llms.txt (index,
  https://docktail.org/llms-full.txt for full text) - consult these before
  making claims about docktail env vars or behavior.

## Coding Style

- Don't use excessive comments. Try to make the code speak for itself; only resort to adding comments where the meaning or intention may otherwise be unclear, or a subtle detail may be missed.
- Organize imports in sections: stdlib imports, 3rd-party imports, and project imports. Each section should be sorted alphabetically, and the sections should be separated from each other by 1 blank line.
- Where a struct type has both public and private methods, arrange the public methods first, then add a comment line of `// Private` and put the private methods below that comment. In the public section, put any constructor methods first, followed by simple accessor methods, followed by the rest.
- Private functions that are not methods of a type should go last in the file, and should be separated with a `// Helpers` comment.
- Prefer modern Go constructs where possible (for example, ranging over numbers is better than a for loop). If the LSP suggests there is an option to modernize something you should generally do so.
- Write tests to cover changes where possible, but don't go overboard trying to cover every single case. Prefer using helper functions (including functions defined locally inside the test) over table-driven tests, except in cases where the latter would be more readable.
- When writing tests, use github.com/stretchr/testify's assert and require packages, to make test conditions more readable. Don't add descriptions to the assertions unless they would add meaningful context over what the default failure message would be).
- Consider opportunities to refactor large methods into smaller pieces, and spot opportunities where it's worth extracting functionality into a new type. But do not go overboard with this.


## Agent skills

### Issue tracker

Local markdown file-based issue tracker. See [issue-tracker.md](docs/agents/issue-tracker.md).

### Triage labels

State roles mapped to issue metadata labels. See [triage-labels.md](docs/agents/triage-labels.md).

### Domain docs

Single-context repository layout. See [domain.md](docs/agents/domain.md).
