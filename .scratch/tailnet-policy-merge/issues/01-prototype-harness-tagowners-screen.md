# 01 - Prototype harness and the tagOwners declaration screen

**What to build:** A runnable throwaway program showing phase one of the new
Tailscale enable experience, where the user declares the `tagOwners` entry by
hand. This step cannot be automated away: the admin console will not issue a
tagged Runtime Credential until the tag is already in `tagOwners` (ADR-0017).

Canned Tailnet Policy fixtures only. No Docker, no Tailscale API, no install.
This ticket also carries the scenario switcher that every later phase selects
against.

Two shapes of the entry are shown, because Once cannot read the Tailnet Policy
yet and so cannot know which one applies. Getting this pair right is the point
of the screen: pasting a second `tagOwners` block into a policy that already has
one is the common failure, and the console's duplicate-key error does not name
it.

```
# no tagOwners section yet - paste the block
"tagOwners": {
    "tag:once": ["autogroup:admin"],
},

# already has one - add this line inside it
"tag:once": ["autogroup:admin"],
```

`internal/ui/custody.go` is the closest existing pattern: a multi-screen flow
that hands the user something to copy, with a clipboard payload beside a text
block. Lift it rather than invent a new shape.

**Blocked by:** None - can start immediately.

**Status:** ready-for-human

- [x] The prototype runs from a single command, and needs neither Docker nor a Tailscale credential.
- [x] Phase one shows both shapes of the `tagOwners` entry, labelled so it is obvious which one to use.
- [x] A visible copy control and ctrl+p both copy the `tagOwners` text, through one shared code path.
- [x] The screen links to the tailnet policy editor and names the success signal: the tag appears in the credential form's tag picker.
- [x] A skip path exists for a user whose tag already exists, with no verification step, since a working tagged credential is itself the proof.
- [x] A scenario switcher selects between canned Tailnet Policy fixtures, and later phases read the selected one.
- [x] The prototype sits outside production code paths and can be deleted in a single commit.

## Comments

Built on branch `prototype/tailnet-policy` (cmd/prototype-tailnet-policy/).
Run: `go run ./cmd/prototype-tailnet-policy`. Keyboard and mouse both work;
the copy control and ctrl+p share one path (`copyTagOwners`), payload is the
whole block. Five fixtures cover the states tickets 02-03 need; the 200-line
fixture for ticket 04 is deferred to that ticket. All three screens fit the
80x24 floor (phase one is exactly 24 rows). Status ready-for-human = react
to layout and words; tickets 02-04 continue on the same branch.
