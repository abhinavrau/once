# Read the Tailnet Policy, Never Write It

ADR-0006 left the user to merge Once's Policy Snippet into the Tailnet Policy by
hand. That removed the policy write, and with it any help with the merge - which
is the highest-friction, most error-prone step in enabling Tailscale, because
HuJSON has to be merged into a document the user already owns. It also left
three of the four sections Once needs unverified: the 443 grant is checked
nowhere (a Services install reports an Application "approved" while its Host
refuses connections), `autoApprovers.services` is caught only after a full Boot,
and the `funnel` nodeAttr only at `funnel enable`, as a 15 second timeout with a
guessed cause.

We decided the Runtime Credential carries `policy_file:read` (and its required
companion `devices:posture_attributes:read`). Once reads the Tailnet Policy,
generates a Merged Policy - the user's own document with Once's missing sections
added - validates it through `POST /tailnet/-/acl/validate` before showing it,
and displays the difference. **The user still applies it. Once never writes the
Tailnet Policy.**

The user declares `tagOwners` by hand, before the credential exists. This is not
a concession we can design away: the admin console will not issue a tagged
credential until the tag is already in `tagOwners`, so no credential-driven flow
can run before it. Once therefore automates only the sections it can reach once
a credential exists - `grants`, `nodeAttrs`, and `autoApprovers` - which are
also the fiddly ones, being arrays and nested maps that must merge into
possibly-existing entries. `tagOwners` is a single map key and the section
nobody gets wrong.

Once needs no check that `tagOwners` was applied. A working tagged credential is
the proof, because the console could not have issued one otherwise.

## Considered options

- **A separate untagged policy-read credential**, letting Once handle every
  section including `tagOwners`. Rejected: it reintroduces the second
  admin-console round trip that ADR-0006 deleted as the biggest friction point,
  and it breaks the invariant that the Runtime Credential is the only Tailscale
  credential that ever exists.
- **`policy_file` write**, letting Once apply the policy itself. Rejected: write
  is the catastrophic scope, since a stolen credential could then grant itself
  anything, and it would clobber GitOps-managed policies. Read plus a diff wins
  the friction back without either.
- **No new scope**, keeping today's static Policy Snippet. Rejected once the
  goal was understood as "merge this correctly into my policy" rather than "tell
  me which sections I need".

## Consequences

- The damage from a stolen Runtime Credential grows, deliberately. The
  credential already mints tagged, preauthorized auth keys (tailnet presence)
  and holds `devices:core` write (full inventory, and deletion of any device),
  so policy read adds reconnaissance, not privilege: group email addresses, SSH
  rules, host-to-IP aliases, subnet route CIDRs, and per-device OS versions.
  Read-only cannot grant access. `CONTEXT.md` already records that Once does not
  protect this credential at rest.
- `devices:core:read` is not a new cost. Both topologies already hold
  `devices:core` write, which covers it.
- The scope is an enhancement, never a precondition. When it is absent or
  revoked, the flow degrades to today's static Policy Snippet and says why.
- The flow is two commands, not a wizard. `once tailscale policy` stays phase
  one: it needs neither Docker nor a credential, and prints the `tagOwners` the
  user declares by hand. `once tailscale enable` does the read, merge, and
  validate, then either installs or stops with the Merged Policy and "apply
  this, then re-run". Nothing prompts, so a script and a terminal behave the
  same, and ADR-0004's surviving constraint - enable never blocks on a prompt
  without a flag equivalent - still holds.
- A missing section blocks or warns according to what it breaks. A missing 443
  grant or `autoApprovers.services` means the install does not work, so enable
  refuses. A missing `funnel` nodeAttr only breaks Tailscale Funnel, which is
  opt-in and temporary, so enable warns and proceeds. Validation that cannot
  refuse is decoration.
- Phase one shows two shapes of the `tagOwners` entry: a whole block, and a
  single line to add to an existing section. Once cannot yet read the policy, so
  it cannot know which applies. Pasting a second `tagOwners` block into a policy
  that already has one is the common failure, and the console's duplicate-key
  error does not name it.
- Because Once never writes, the paste is the critical path, so copying is a
  first-class control rather than a hidden keybinding. The TUI keeps ctrl+p and
  adds a visible, clickable copy control; both go through `tea.SetClipboard`,
  which already reaches the user's clipboard over SSH via OSC 52 and needs no
  new dependency. The control registers a named click target, so the form
  drivers assert it like any other.
- Presence in the policy text is not coverage. The 443 grant is decided by an
  ACL test (`autogroup:member` to the tag on 443) rather than by matching text,
  since an equivalent grant may be spelled differently. `nodeAttrs` and
  `autoApprovers` are not access rules, so they fall back to a check against the
  parsed document.
- An existing key is never overwritten. A `tagOwners` or `autoApprovers` entry
  that is already present is left exactly as the user wrote it.
- `GET /acl` returns an ETag. It is captured when the Merged Policy is generated
  and re-checked before install, so a policy edited by someone else in between
  is reported rather than silently reverted.
- Comments and formatting survive. `github.com/tailscale/hujson` is already a
  direct dependency, and its `Patch` preserves comments around inserted values,
  so the Merged Policy is the user's document plus additions, not a reformatted
  replacement.
- The scope list is duplicated by hand in both `SetupIntro` constants, the
  `--client-id` flag help, and the per-scope error strings, while
  `TopologyFacts.OAuthScopes` has no production consumer. They all change
  together, or that field becomes the single source.
