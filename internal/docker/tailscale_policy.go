package docker

import (
	"encoding/json"
	"fmt"
	"strings"
)

// tagOwnerValue is who may apply Once's tags: tailnet admins.
var tagOwnerValue = []string{"autogroup:admin"}

// policyGrant and policyNodeAttr are structs rather than maps so the JSON
// field order matches the policy syntax docs.
type policyGrant struct {
	Src []string `json:"src"`
	Dst []string `json:"dst"`
	IP  []string `json:"ip"`
}

type policyNodeAttr struct {
	Target []string `json:"target"`
	Attr   []string `json:"attr"`
}

// PolicySnippet returns the HuJSON snippet of the four sections Once needs in
// the tailnet policy: tagOwners (also a precondition for creating the tagged
// OAuth client in the admin console), autoApprovers.services, a grant for the
// tagged Services, and the funnel nodeAttr. The user merges it by hand; the
// enable-time credential probe is the enforcement.
func PolicySnippet(tag string) string {
	tags := policyTags(tag)
	var owners, approvers strings.Builder
	for _, t := range tags {
		fmt.Fprintf(&owners, "\t\t%s: %s,\n", mustJSON(t), mustJSON(tagOwnerValue))
		fmt.Fprintf(&approvers, "\t\t\t%s: %s,\n", mustJSON(t), mustJSON(tags))
	}
	return fmt.Sprintf(`{
	"tagOwners": {
%s	},
	"autoApprovers": {
		"services": {
%s		},
	},
	"grants": [
		%s,
	],
	"nodeAttrs": [
		%s,
	],
}`, owners.String(), approvers.String(), mustJSON(requiredGrant(tags)), mustJSON(requiredNodeAttr(tags)))
}

// Helpers

// requiredGrant grants every tailnet member access to the tagged Services,
// which serve on 443. The dst is the tag, not svc: names: a tag assigned to a
// Service selects it in grants (kb/1552), and docktail stamps the tags on
// every Service definition it creates - so one grant covers apps deployed
// later, which per-svc: entries could not.
func requiredGrant(tags []string) policyGrant {
	return policyGrant{Src: []string{"autogroup:member"}, Dst: tags, IP: []string{"443"}}
}

// requiredNodeAttr grants the funnel attribute to the tagged node advertising
// the Services, needed for `once tailscale funnel enable`.
func requiredNodeAttr(tags []string) policyNodeAttr {
	return policyNodeAttr{Target: tags, Attr: []string{"funnel"}}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// policyTags splits the (normalized) comma-separated tag setting into the tag
// list the policy sections are built from.
func policyTags(tag string) []string {
	return strings.Split((TailscaleSettings{Tag: tag}).normalizeTag(), ",")
}
