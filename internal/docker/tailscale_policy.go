package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/tailscale/hujson"
)

// tailnetPolicyPath is the Tailnet Policy endpoint the Runtime Credential's
// policy_file:read scope unlocks (ADR-0017); its /validate sub-path runs ACL
// tests against the live policy and validates a candidate document.
const tailnetPolicyPath = "/tailnet/-/acl"

// The sections Once merges into the Tailnet Policy once a credential exists
// (ADR-0017): tagOwners stays manual, phase one's job. autoApprovers.services
// exists only under the Services topology (ADR-0015).
const (
	policySectionGrants    = "grants"
	policySectionApprovers = "autoApprovers.services"
	policySectionNodeAttrs = "nodeAttrs"
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

// NodesPolicySnippet returns the HuJSON snippet the Nodes topology needs in
// the tailnet policy: just tagOwners (so the OAuth client may create devices
// with the tag) and the funnel nodeAttr. Unlike Services, nodes needs no
// autoApprovers.services (there are no Services) and no grants entry: a
// tagged node is an ordinary tailnet device, reachable under the tailnet's
// existing default/ACL rules like any other, not a mechanism with its own
// approval and access-grant step.
func NodesPolicySnippet(tag string) string {
	tags := policyTags(tag)
	var owners strings.Builder
	for _, t := range tags {
		fmt.Fprintf(&owners, "\t\t%s: %s,\n", mustJSON(t), mustJSON(tagOwnerValue))
	}
	return fmt.Sprintf(`{
	"tagOwners": {
%s	},
	"nodeAttrs": [
		%s,
	],
}`, owners.String(), mustJSON(requiredNodeAttr(tags)))
}

// PolicyReview is the outcome of reading the Tailnet Policy with the Runtime
// Credential and computing the Merged Policy (ADR-0017): the user's own
// document with Once's missing sections inserted, plus the names of what was
// inserted and the policy version (ETag) the review was computed against.
type PolicyReview struct {
	// Merged is the full Merged Policy - the paste payload. Untouched lines
	// are the user's own, byte for byte; only Once's additions are new.
	Merged string
	// Added names the sections that were inserted, in policy order; empty
	// means the policy already carries everything Once needs.
	Added []string
	// ETag is the policy version Merged was computed from, re-checked
	// immediately before install so an edit in between is reported rather
	// than ignored.
	ETag string
	// Fallback, when non-empty, says why the policy could not be read at all
	// (the policy_file:read scope is missing or revoked). The flow then
	// degrades to the static PolicySnippet path: the scope is an enhancement,
	// never a precondition (ADR-0017).
	Fallback string
}

// PolicyRefusedError is enable's refusal outcome (ADR-0017): the Tailnet
// Policy is missing sections without which the install does not work, so
// nothing was installed. It carries the review so each frontend can hand the
// user the Merged Policy to apply before re-running - the CLI prints it, the
// TUI copies it.
type PolicyRefusedError struct {
	Review PolicyReview
}

func (e *PolicyRefusedError) Error() string {
	var sections, breaks []string
	if slices.Contains(e.Review.Added, policySectionGrants) {
		sections = append(sections, "the 443 grant")
		breaks = append(breaks, "without the grant, tailnet members cannot reach the apps on port 443")
	}
	if slices.Contains(e.Review.Added, policySectionApprovers) {
		sections = append(sections, policySectionApprovers)
		breaks = append(breaks, "without autoApprovers.services, every Service waits for manual approval and enable times out")
	}
	return fmt.Sprintf("the tailnet policy is missing %s: %s; apply the Merged Policy, then re-run - nothing was installed",
		strings.Join(sections, " and "), strings.Join(breaks, ", and "))
}

// ErrPolicyChangedDuringEnable is enable's stale outcome (ADR-0017): the
// Tailnet Policy was edited between the merge and the install, so the
// reviewed merge no longer describes it and installing on top would ignore
// someone else's edit.
var ErrPolicyChangedDuringEnable = errors.New("the tailnet policy was edited while enable was running, so it no longer matches the reviewed merge; nothing was installed - re-run `once tailscale enable` against the current policy")

// reviewTailnetPolicy is the production merge behind `once tailscale enable`
// (ADR-0017): read the Tailnet Policy, compute the Merged Policy for the
// given tag and topology, and report which sections were added. A merged
// document is validated through POST /acl/validate before it is returned, so
// no frontend ever shows the user an unappliable policy. The 443 grant is
// checked by an ACL test rather than by matching text, since an equivalent
// grant may be spelled differently; nodeAttrs and autoApprovers are not
// access rules, so they check the parsed document.
func reviewTailnetPolicy(ctx context.Context, api tailscaleAPI, tag string, services bool) (PolicyReview, error) {
	policy, etag, fallback, err := readTailnetPolicy(ctx, api)
	if err != nil {
		return PolicyReview{}, err
	}
	if fallback != "" {
		return PolicyReview{Fallback: fallback}, nil
	}

	merged, added, err := mergeTailnetPolicy(ctx, api, policy, policyTags(tag), services)
	if err != nil {
		return PolicyReview{}, err
	}
	if len(added) > 0 {
		if err := validateMergedPolicy(ctx, api, merged); err != nil {
			return PolicyReview{}, err
		}
	}
	return PolicyReview{Merged: merged, Added: added, ETag: etag}, nil
}

// Private

// blocksInstall reports whether a missing section means the install does not
// work: a missing 443 grant or autoApprovers.services refuses the enable,
// because validation that cannot refuse is decoration (ADR-0017). A missing
// funnel nodeAttr never blocks - it breaks only Funnel, which warning() names.
func (r PolicyReview) blocksInstall() bool {
	return slices.Contains(r.Added, policySectionGrants) || slices.Contains(r.Added, policySectionApprovers)
}

// warning is the one line a proceeding enable surfaces about the policy, or
// "": the funnel nodeAttr warn (only Tailscale Funnel breaks, opt-in and
// temporary, so enable proceeds), or the fallback's why when the policy could
// not be read at all.
func (r PolicyReview) warning() string {
	switch {
	case r.Fallback != "":
		return r.Fallback + "; check the sections `once tailscale policy` prints by hand"
	case !r.blocksInstall() && slices.Contains(r.Added, policySectionNodeAttrs):
		return "the tailnet policy is missing the funnel nodeAttr; only Tailscale Funnel breaks without it - apply the nodeAttrs section from the Merged Policy before running `once tailscale funnel enable`"
	}
	return ""
}

// Helpers

// requireUnchangedPolicy re-checks the ETag captured when the Merged Policy
// was generated, immediately before install, so a policy edited by someone
// else in between is reported rather than ignored (ADR-0017). A read that
// newly degrades to the fallback (the scope was revoked mid-flight) has
// nothing to compare and proceeds: the scope is an enhancement, never a
// precondition.
func requireUnchangedPolicy(ctx context.Context, api tailscaleAPI, etag string) error {
	_, current, fallback, err := readTailnetPolicy(ctx, api)
	if err != nil {
		return err
	}
	if fallback == "" && current != etag {
		return ErrPolicyChangedDuringEnable
	}
	return nil
}

// readTailnetPolicy fetches the Tailnet Policy as HuJSON - the Accept header
// asks for the user's document verbatim, comments and all, not the API's JSON
// rendering of it - and captures the ETag that guards the install. A 403
// means the policy_file:read scope is missing or revoked, which is a fallback
// to the static snippet path rather than a failure.
func readTailnetPolicy(ctx context.Context, api tailscaleAPI) (policy, etag, fallback string, err error) {
	header := http.Header{"Accept": []string{"application/hujson"}}
	status, respHeader, body, err := api.CallWithHeaders(ctx, http.MethodGet, tailnetPolicyPath, header, nil)
	if err != nil {
		return "", "", "", err
	}
	switch {
	case status == http.StatusForbidden:
		return "", "", fmt.Sprintf("the OAuth client is missing the policy_file:read scope, so the tailnet policy cannot be read and merged (HTTP %d): %s", status, errorMessage(body)), nil
	case status != http.StatusOK:
		return "", "", "", fmt.Errorf("reading the tailnet policy (HTTP %d): %s", status, errorMessage(body))
	}
	return string(body), respHeader.Get("ETag"), "", nil
}

// mergeTailnetPolicy inserts whichever of Once's sections the policy is
// missing and reports which were added, in policy order. Sections already
// present - the 443 grant proven by ACL test, the rest by the parsed
// document - are left exactly as the user wrote them, never duplicated or
// rewritten.
func mergeTailnetPolicy(ctx context.Context, api tailscaleAPI, policy string, tags []string, services bool) (merged string, added []string, err error) {
	v, err := hujson.Parse([]byte(policy))
	if err != nil {
		return "", nil, fmt.Errorf("parsing the tailnet policy: %w", err)
	}
	root, ok := v.Value.(*hujson.Object)
	if !ok {
		return "", nil, fmt.Errorf("the tailnet policy is not a HuJSON object")
	}
	doc, err := policyDocument(v)
	if err != nil {
		return "", nil, err
	}
	editor := newPolicyEditor(root)

	granted, err := tailnetGrantsMemberAccess(ctx, api, tags)
	if err != nil {
		return "", nil, err
	}
	if !granted {
		if err := editor.appendArrayEntry("grants", grantEntry(tags)); err != nil {
			return "", nil, err
		}
		added = append(added, policySectionGrants)
	}

	if services {
		if missing := missingApproverTags(doc, tags); len(missing) > 0 {
			if err := editor.insertApprovers(missing, tags); err != nil {
				return "", nil, err
			}
			added = append(added, policySectionApprovers)
		}
	}

	if !hasFunnelNodeAttr(doc, tags) {
		if err := editor.appendArrayEntry("nodeAttrs", nodeAttrEntry(tags)); err != nil {
			return "", nil, err
		}
		added = append(added, policySectionNodeAttrs)
	}

	return v.String(), added, nil
}

// aclTest is the Tailscale ACL test shape POST /acl/validate runs against the
// tailnet's current policy when the request body is a JSON array.
type aclTest struct {
	Src    string   `json:"src"`
	Accept []string `json:"accept"`
}

// tailnetGrantsMemberAccess runs the ACL test ADR-0017 specifies for the 443
// grant - can every tailnet member reach the tagged apps on 443 - against the
// live policy, so an equivalent grant spelled differently still counts as
// present. The endpoint answers 200 either way; a message in the body is the
// test failing.
func tailnetGrantsMemberAccess(ctx context.Context, api tailscaleAPI, tags []string) (bool, error) {
	accept := make([]string, len(tags))
	for i, tag := range tags {
		accept[i] = tag + ":443"
	}
	payload, err := json.Marshal([]aclTest{{Src: "autogroup:member", Accept: accept}})
	if err != nil {
		return false, err
	}

	status, body, err := api.Call(ctx, http.MethodPost, tailnetPolicyPath+"/validate", payload)
	if err != nil {
		return false, err
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("testing the tailnet policy's 443 grant (HTTP %d): %s", status, errorMessage(body))
	}
	return validationMessage(body) == "", nil
}

// validateMergedPolicy proves the Merged Policy would be accepted before any
// frontend shows it (ADR-0017), sending the HuJSON document itself.
func validateMergedPolicy(ctx context.Context, api tailscaleAPI, merged string) error {
	header := http.Header{"Content-Type": []string{"application/hujson"}}
	status, _, body, err := api.CallWithHeaders(ctx, http.MethodPost, tailnetPolicyPath+"/validate", header, []byte(merged))
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("validating the merged tailnet policy (HTTP %d): %s", status, errorMessage(body))
	}
	if msg := validationMessage(body); msg != "" {
		return fmt.Errorf("the merged tailnet policy failed validation: %s", msg)
	}
	return nil
}

// validationMessage extracts the failure a POST /acl/validate response
// carries: the endpoint answers 200 whether or not the policy or its tests
// passed, with a message naming the failure when one did.
func validationMessage(body []byte) string {
	var resp struct {
		Message string `json:"message"`
	}
	if len(bytes.TrimSpace(body)) == 0 || json.Unmarshal(body, &resp) != nil {
		return ""
	}
	return resp.Message
}

// policyDocument renders the parsed policy as plain JSON for the presence
// checks that read it structurally.
func policyDocument(v hujson.Value) (map[string]any, error) {
	std := v.Clone()
	std.Standardize()
	var doc map[string]any
	if err := json.Unmarshal(std.Pack(), &doc); err != nil {
		return nil, fmt.Errorf("decoding the tailnet policy: %w", err)
	}
	return doc, nil
}

// hasFunnelNodeAttr reports whether every tag already holds the funnel
// attribute - through any entry, not just one shaped exactly like Once's, so
// an existing broader entry is never duplicated.
func hasFunnelNodeAttr(doc map[string]any, tags []string) bool {
	attrs, ok := doc["nodeAttrs"].([]any)
	if !ok {
		return false
	}
	for _, tag := range tags {
		covered := slices.ContainsFunc(attrs, func(entry any) bool {
			m, ok := entry.(map[string]any)
			return ok && listContainsAny(m["target"], tag, "*") && listContainsAny(m["attr"], "funnel")
		})
		if !covered {
			return false
		}
	}
	return true
}

// missingApproverTags returns the tags with no autoApprovers.services entry.
// An existing entry counts whatever its value: a key the user wrote is never
// overwritten (ADR-0017).
func missingApproverTags(doc map[string]any, tags []string) []string {
	approvers, _ := doc["autoApprovers"].(map[string]any)
	services, _ := approvers["services"].(map[string]any)
	var missing []string
	for _, tag := range tags {
		if _, ok := services[tag]; !ok {
			missing = append(missing, tag)
		}
	}
	return missing
}

func listContainsAny(list any, wanted ...string) bool {
	items, ok := list.([]any)
	if !ok {
		return false
	}
	return slices.ContainsFunc(items, func(item any) bool {
		s, ok := item.(string)
		return ok && slices.Contains(wanted, s)
	})
}

// grantEntry and nodeAttrEntry are the one-line section entries the merge
// inserts, spelled with the same field order and spacing the policy docs use.
func grantEntry(tags []string) string {
	g := requiredGrant(tags)
	return fmt.Sprintf(`{"src": %s, "dst": %s, "ip": %s}`, mustJSON(g.Src), mustJSON(g.Dst), mustJSON(g.IP))
}

func nodeAttrEntry(tags []string) string {
	a := requiredNodeAttr(tags)
	return fmt.Sprintf(`{"target": %s, "attr": %s}`, mustJSON(a.Target), mustJSON(a.Attr))
}

// policyEditor inserts Once's sections into a parsed Tailnet Policy without
// reformatting anything the user wrote - the resolution of the ADR-0017
// amendment's contradiction. hujson.Patch+Format (the prototype's approach)
// repaints the whole document in hujson's house style, so the paste payload
// would no longer be "the user's document plus additions"; direct edits on
// hujson's syntax tree leave every untouched line byte-for-byte intact, which
// also keeps the diff add-only and a GitOps-managed policy's VCS diff
// minimal. Insertions copy the document's own indentation unit and
// trailing-comma style, sniffed from what is already there.
type policyEditor struct {
	root     *hujson.Object
	unit     string // one indentation level, sniffed from the root's members
	trailing bool   // whether the document ends lists with a trailing comma
}

func newPolicyEditor(root *hujson.Object) *policyEditor {
	e := &policyEditor{root: root, unit: "\t", trailing: true}
	if n := len(root.Members); n > 0 {
		if indent := lineIndent(root.Members[0].Name.BeforeExtra); indent != "" {
			e.unit = indent
		}
		e.trailing = root.Members[n-1].Value.AfterExtra != nil
	}
	return e
}

// appendArrayEntry appends entry to the named top-level array section,
// creating the section when the policy has none.
func (e *policyEditor) appendArrayEntry(name, entry string) error {
	member := e.member(e.root, name)
	if member == nil {
		member = e.appendMember(e.root, name, "[]", 1)
	}
	arr, ok := member.Value.(*hujson.Array)
	if !ok {
		return fmt.Errorf("the tailnet policy's %q section is not an array", name)
	}
	e.appendElement(arr, entry, 2)
	return nil
}

// insertApprovers adds an autoApprovers.services entry for each missing tag,
// creating the enclosing objects only as far as they do not already exist.
func (e *policyEditor) insertApprovers(missing, tags []string) error {
	approvers := e.member(e.root, "autoApprovers")
	if approvers == nil {
		approvers = e.appendMember(e.root, "autoApprovers", "{}", 1)
	}
	approversObj, ok := approvers.Value.(*hujson.Object)
	if !ok {
		return fmt.Errorf(`the tailnet policy's "autoApprovers" section is not an object`)
	}

	services := e.member(approversObj, "services")
	if services == nil {
		services = e.appendMember(approversObj, "services", "{}", 2)
	}
	servicesObj, ok := services.Value.(*hujson.Object)
	if !ok {
		return fmt.Errorf(`the tailnet policy's "autoApprovers.services" section is not an object`)
	}

	for _, tag := range missing {
		e.appendMember(servicesObj, tag, string(mustJSON(tags)), 3)
	}
	return nil
}

// member finds the named member's value in obj, or nil when absent.
func (e *policyEditor) member(obj *hujson.Object, name string) *hujson.Value {
	quoted := string(mustJSON(name))
	for i := range obj.Members {
		if lit, ok := obj.Members[i].Name.Value.(hujson.Literal); ok && string(lit) == quoted {
			return &obj.Members[i].Value
		}
	}
	return nil
}

// appendMember appends a member on its own line at depth, and returns the
// member's value for further edits.
func (e *policyEditor) appendMember(obj *hujson.Object, name, valueText string, depth int) *hujson.Value {
	m := hujson.ObjectMember{
		Name:  hujson.Value{BeforeExtra: e.lineBreak(depth), Value: hujson.Literal(mustJSON(name))},
		Value: hujson.Value{BeforeExtra: hujson.Extra(" "), Value: mustParseValue(valueText)},
	}
	if e.trailing {
		m.Value.AfterExtra = hujson.Extra{}
	}
	obj.Members = append(obj.Members, m)
	e.closeAcrossLines(&obj.AfterExtra, depth-1)
	return &obj.Members[len(obj.Members)-1].Value
}

// appendElement appends an array element on its own line, indented like its
// siblings (or at depth when the array has none).
func (e *policyEditor) appendElement(arr *hujson.Array, entryText string, depth int) {
	indent := e.lineBreak(depth)
	trailing := e.trailing
	if n := len(arr.Elements); n > 0 {
		if sniffed := lineIndent(arr.Elements[n-1].BeforeExtra); sniffed != "" {
			indent = hujson.Extra("\n" + sniffed)
		}
		trailing = arr.Elements[n-1].AfterExtra != nil
	}
	elem := hujson.ArrayElement{BeforeExtra: indent, Value: mustParseValue(entryText)}
	if trailing {
		elem.AfterExtra = hujson.Extra{}
	}
	arr.Elements = append(arr.Elements, elem)
	e.closeAcrossLines(&arr.AfterExtra, depth-1)
}

func (e *policyEditor) lineBreak(depth int) hujson.Extra {
	return hujson.Extra("\n" + strings.Repeat(e.unit, depth))
}

// closeAcrossLines puts a composite's closing bracket on its own line when an
// insertion just went multi-line inside what used to close inline.
func (e *policyEditor) closeAcrossLines(extra *hujson.Extra, depth int) {
	if !bytes.Contains(*extra, []byte("\n")) {
		*extra = hujson.Extra("\n" + strings.Repeat(e.unit, depth))
	}
}

// lineIndent is the whitespace after the last newline in extra - the
// indentation of whatever follows it - or "" when extra holds no newline or
// ends mid-comment.
func lineIndent(extra hujson.Extra) string {
	i := bytes.LastIndexByte(extra, '\n')
	if i < 0 {
		return ""
	}
	indent := string(extra[i+1:])
	if strings.Trim(indent, " \t") != "" {
		return ""
	}
	return indent
}

// mustParseValue parses one of Once's own HuJSON literals, whose validity is
// the package's to guarantee - like mustJSON.
func mustParseValue(text string) hujson.ValueTrimmed {
	v, err := hujson.Parse([]byte(text))
	if err != nil {
		panic(err)
	}
	return v.Value
}

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
