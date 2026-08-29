package main

// Canned Tailnet Policy fixtures. Phase one looks the same for all of them -
// Once cannot read the policy yet - but every later phase (tickets 02-04)
// reads the selected one, so the set covers the states those phases need:
// merge, warn, refuse, and nothing-to-add.

type scenario struct {
	name   string
	note   string
	policy string
}

var scenarios = []scenario{
	{
		name: "Fresh tailnet",
		note: "no tagOwners section - paste the block",
		policy: `{
	// Default Tailnet Policy for a fresh tailnet.
	"acls": [
		// Allow all connections.
		{"action": "accept", "src": ["*"], "dst": ["*:*"]},
	],
	"ssh": [
		{
			"action": "check",
			"src":    ["autogroup:member"],
			"dst":    ["autogroup:self"],
			"users":  ["autogroup:nonroot", "root"],
		},
	],
}`,
	},
	{
		name: "Existing tagOwners",
		note: "tag:once missing - add the line",
		policy: `{
	"tagOwners": {
		// The CI runners, managed by the platform team.
		"tag:ci": ["group:platform"],
		"tag:monitoring": ["autogroup:admin"],
	},
	"acls": [
		{"action": "accept", "src": ["*"], "dst": ["*:*"]},
	],
}`,
	},
	{
		name: "Everything already present",
		note: "all four sections - nothing to add",
		policy: `{
	"tagOwners": {
		"tag:once": ["autogroup:admin"],
	},
	"grants": [
		{"src": ["autogroup:member"], "dst": ["tag:once"], "ip": ["443"]},
	],
	"autoApprovers": {
		"services": {"tag:once": ["tag:once"]},
	},
	"nodeAttrs": [
		{"target": ["tag:once"], "attr": ["funnel"]},
	],
}`,
	},
	{
		name: "Missing funnel nodeAttr",
		note: "funnel attr missing - warn, proceed",
		policy: `{
	"tagOwners": {
		"tag:once": ["autogroup:admin"],
	},
	"grants": [
		{"src": ["autogroup:member"], "dst": ["tag:once"], "ip": ["443"]},
	],
	"autoApprovers": {
		"services": {"tag:once": ["tag:once"]},
	},
}`,
	},
	{
		name: "Missing 443 grant",
		note: "no 443 grant - refuse",
		policy: `{
	"tagOwners": {
		"tag:once": ["autogroup:admin"],
	},
	"acls": [
		// Broad accept, but no grant reaches tag:once on 443.
		{"action": "accept", "src": ["group:eng"], "dst": ["tag:ci:*"]},
	],
}`,
	},
}
