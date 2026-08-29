package main

import (
	"fmt"
	"strings"
)

// Canned Tailnet Policy fixtures. Phase one looks the same for all of them -
// Once cannot read the policy yet - but every later phase (tickets 02-04)
// reads the selected one, so the set covers the states those phases need:
// merge, warn, refuse, and nothing-to-add.

// outcome is the canned "Validate Policy & Install" end state a fixture
// demonstrates (ticket 03). The prototype has no API to validate against, so
// the switcher decides: proceed simulates a correct paste, warn and refuse
// simulate applying the policy without the missing sections, stale simulates
// an edit by someone else between merge and validate.
type outcome int

const (
	outcomeProceed outcome = iota
	outcomeWarn
	outcomeRefuse
	outcomeStale
)

type scenario struct {
	name    string
	note    string
	policy  string
	outcome outcome
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
		name:    "Missing funnel nodeAttr",
		note:    "funnel attr missing - warn, proceed",
		outcome: outcomeWarn,
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
		name:    "Large GitOps policy",
		note:    "200+ lines - scrolling diff, then stale",
		policy:  largeGitOpsPolicy(),
		outcome: outcomeStale,
	},
	{
		name:    "Missing 443 grant",
		note:    "no 443 grant - refuse",
		outcome: outcomeRefuse,
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

// largeGitOpsPolicy builds the 200+ line fixture ticket 04 needs: a policy
// big enough that the diff must scroll, with comments that have to survive.
func largeGitOpsPolicy() string {
	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString("\t// Managed by GitOps - tailnet-policy repo, deploy on merge.\n")
	b.WriteString("\t// Ask #infra before editing groups or grants.\n")
	b.WriteString("\t\"groups\": {\n")
	for _, team := range []string{"platform", "backend", "frontend", "data", "sre", "security", "support", "contractors"} {
		fmt.Fprintf(&b, "\t\t\"group:%s\": [\"%s-lead@example.com\", \"%s-2@example.com\", \"%s-3@example.com\"],\n", team, team, team, team)
	}
	b.WriteString("\t},\n\n")
	b.WriteString("\t\"hosts\": {\n")
	for i := 1; i <= 95; i++ {
		fmt.Fprintf(&b, "\t\t\"prod-%02d\": \"10.0.4.%d\",\n", i, i)
	}
	for i := 1; i <= 35; i++ {
		fmt.Fprintf(&b, "\t\t\"staging-%02d\": \"10.0.8.%d\",\n", i, i)
	}
	b.WriteString("\t},\n\n")
	b.WriteString("\t\"tagOwners\": {\n")
	b.WriteString("\t\t// Server tags are owned by the teams that run them.\n")
	for _, tag := range []string{"ci", "monitoring", "backup", "k8s", "bastion", "once"} {
		owner := "group:platform"
		if tag == "once" {
			owner = "autogroup:admin"
		}
		fmt.Fprintf(&b, "\t\t\"tag:%s\": [\"%s\"],\n", tag, owner)
	}
	b.WriteString("\t},\n\n")
	b.WriteString("\t\"acls\": [\n")
	b.WriteString("\t\t// Wide-open for admins, per-team below.\n")
	b.WriteString("\t\t{\"action\": \"accept\", \"src\": [\"autogroup:admin\"], \"dst\": [\"*:*\"]},\n")
	for _, team := range []string{"platform", "backend", "frontend", "data", "sre", "security", "support"} {
		fmt.Fprintf(&b, "\t\t// %s reaches its own boxes and the bastion.\n", team)
		fmt.Fprintf(&b, "\t\t{\"action\": \"accept\", \"src\": [\"group:%s\"], \"dst\": [\"tag:bastion:22\"]},\n", team)
		fmt.Fprintf(&b, "\t\t{\"action\": \"accept\", \"src\": [\"group:%s\"], \"dst\": [\"tag:k8s:443\"]},\n", team)
	}
	b.WriteString("\t],\n\n")
	b.WriteString("\t\"ssh\": [\n")
	b.WriteString("\t\t// Check mode everywhere; root only via the bastion.\n")
	b.WriteString("\t\t{\"action\": \"check\", \"src\": [\"autogroup:member\"], \"dst\": [\"autogroup:self\"], \"users\": [\"autogroup:nonroot\"]},\n")
	b.WriteString("\t\t{\"action\": \"check\", \"src\": [\"group:sre\"], \"dst\": [\"tag:bastion\"], \"users\": [\"root\"]},\n")
	b.WriteString("\t],\n\n")
	b.WriteString("\t\"tests\": [\n")
	for _, team := range []string{"platform", "backend", "frontend", "data", "sre"} {
		fmt.Fprintf(&b, "\t\t{\"src\": \"group:%s\", \"accept\": [\"tag:bastion:22\"]},\n", team)
		fmt.Fprintf(&b, "\t\t{\"src\": \"group:%s\", \"accept\": [\"tag:k8s:443\"]},\n", team)
	}
	b.WriteString("\t],\n")
	b.WriteString("}\n")
	return b.String()
}
