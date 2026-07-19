package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/tailscale/hujson"
)

// policyWriteAttempts bounds the fetch-patch-validate-write loop retried when
// a concurrent policy edit fails the If-Match condition (HTTP 412).
const policyWriteAttempts = 3

// tagOwnerValue is who may apply Once's tags: tailnet admins.
var tagOwnerValue = []string{"autogroup:admin"}

// pointerEscaper escapes a member name for use in an RFC 6901 JSON pointer.
var pointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

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

// PolicyUpdate is a planned change to the tailnet policy file: the current
// policy as fetched, the version with Once's required sections patched in
// (comment- and format-preserving, via Tailscale's HuJSON library), and the
// ETag guarding the write against concurrent edits. The policy_file-scoped
// token lives only in memory for one plan/apply cycle and is never stored.
type PolicyUpdate struct {
	baseURL string
	token   string
	tags    []string
	current []byte
	patched []byte
	etag    string
}

// PlanPolicyUpdate exchanges the policy_file-scoped OAuth credentials for a
// token, fetches the tailnet policy, and computes the patched version with the
// four sections Once needs (tagOwners, autoApprovers.services, a grant for the
// tagged Services, and the funnel nodeAttr).
func PlanPolicyUpdate(ctx context.Context, clientID, clientSecret, tag string) (*PolicyUpdate, error) {
	return planPolicyUpdate(ctx, tailscaleAPIBaseURL, clientID, clientSecret, tag)
}

// Changed reports whether the policy is missing any required entry.
func (u *PolicyUpdate) Changed() bool {
	return u.patched != nil
}

// Diff returns a unified diff of the current policy against the patched one,
// for user confirmation before Apply.
func (u *PolicyUpdate) Diff() string {
	diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(u.current)),
		B:        difflib.SplitLines(string(u.patched)),
		FromFile: "tailnet policy (current)",
		ToFile:   "tailnet policy (proposed)",
		Context:  3,
	})
	return diff
}

// Apply validates the patched policy server-side, then writes it conditioned
// on the fetch's ETag so a concurrent edit is never clobbered. A 412 conflict
// triggers a fresh fetch-and-patch, retried a bounded number of times.
func (u *PolicyUpdate) Apply(ctx context.Context) error {
	for range policyWriteAttempts {
		if !u.Changed() {
			return nil
		}
		if err := u.validate(ctx); err != nil {
			return err
		}
		status, body, _, err := u.request(ctx, http.MethodPost, "/tailnet/-/acl", u.patched, `"`+u.etag+`"`)
		if err != nil {
			return err
		}
		switch status {
		case http.StatusOK:
			return nil
		case http.StatusPreconditionFailed:
			if err := u.plan(ctx); err != nil {
				return err
			}
		default:
			return fmt.Errorf("Tailscale rejected the policy write (HTTP %d): %s", status, errorMessage(body))
		}
	}
	return fmt.Errorf("giving up after %d concurrent edits to the tailnet policy; re-run enable, or apply the snippet manually", policyWriteAttempts)
}

// Consent is one seam with two renderings (the CLI prompt and the TUI
// overlay), so the copy they share lives here with the policy logic.
const (
	PolicyConsentIntro        = "Once needs four sections in your tailnet policy file: tagOwners, autoApprovers.services, a grant for the tagged Services, and the funnel nodeAttr."
	PolicyGitOpsWarning       = "Warning: if this tailnet's policy is managed via GitOps, the next pipeline sync will silently revert this write - decline and commit the snippet to your policy repo instead."
	PolicyRevokeFailureNotice = "Could not delete the one-shot policy OAuth client; revoke it in the admin console (Trust credentials)"
)

// PolicySnippet returns the HuJSON snippet of the four sections Once needs,
// for manual application when the API write is declined or fails. Its entries
// are built from the same values as the API patch, so a pasted snippet makes a
// later re-run of the patch a no-op.
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

// Private

// plan fetches the current policy and computes the patched version, leaving
// patched nil when every required entry is already present.
func (u *PolicyUpdate) plan(ctx context.Context) error {
	status, body, etag, err := u.request(ctx, http.MethodGet, "/tailnet/-/acl", nil, "")
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("fetching the tailnet policy (HTTP %d): %s; check the credential has the policy_file scope", status, errorMessage(body))
	}
	u.current, u.etag, u.patched = body, etag, nil

	v, err := hujson.Parse(body)
	if err != nil {
		return fmt.Errorf("parsing the tailnet policy: %w", err)
	}
	changed, err := addRequiredPolicySections(&v, u.tags)
	if err != nil {
		return err
	}
	if changed {
		u.patched = v.Pack()
	}
	return nil
}

// validate dry-runs the patched policy server-side. The endpoint returns 200
// even for an invalid policy, so the body's message is the real verdict.
func (u *PolicyUpdate) validate(ctx context.Context) error {
	status, body, _, err := u.request(ctx, http.MethodPost, "/tailnet/-/acl/validate", u.patched, "")
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("validating the patched tailnet policy (HTTP %d): %s", status, errorMessage(body))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var result struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decoding the policy validation response: %w", err)
	}
	if result.Message != "" {
		return fmt.Errorf("the patched tailnet policy failed validation: %s", result.Message)
	}
	return nil
}

// request performs one policy API call, returning the response's ETag
// (unquoted) alongside the status and body. Policy bodies travel as HuJSON in
// both directions so comments and formatting round-trip.
func (u *PolicyUpdate) request(ctx context.Context, method, path string, policy []byte, ifMatch string) (int, []byte, string, error) {
	var reqBody io.Reader
	if policy != nil {
		reqBody = bytes.NewReader(policy)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.baseURL+path, reqBody)
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+u.token)
	req.Header.Set("Accept", "application/hujson")
	if policy != nil {
		req.Header.Set("Content-Type", "application/hujson")
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, "", fmt.Errorf("contacting the Tailscale API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, "", fmt.Errorf("reading Tailscale API response: %w", err)
	}
	return resp.StatusCode, body, strings.Trim(resp.Header.Get("ETag"), `"`), nil
}

// Helpers

// planPolicyUpdate is PlanPolicyUpdate with the API base URL parameterised so
// tests can point it at an httptest server.
func planPolicyUpdate(ctx context.Context, baseURL, clientID, clientSecret, tag string) (*PolicyUpdate, error) {
	token, err := tailscaleOAuthToken(ctx, baseURL, clientID, clientSecret)
	if err != nil {
		return nil, err
	}
	u := &PolicyUpdate{baseURL: baseURL, token: token, tags: policyTags(tag)}
	if err := u.plan(ctx); err != nil {
		return nil, err
	}
	return u, nil
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

// addRequiredPolicySections mutates the parsed policy, adding each required
// entry the policy lacks: a tagOwners and an autoApprovers.services entry per
// tag, one grant, and one funnel nodeAttr. All edits are appends - existing
// content is never rewritten, so the rest of the file round-trips
// byte-for-byte, comments included. New entries copy the indentation and
// trailing-comma style of the sibling above them.
func addRequiredPolicySections(v *hujson.Value, tags []string) (bool, error) {
	if _, ok := v.Value.(*hujson.Object); !ok {
		return false, fmt.Errorf("the tailnet policy root is not an object")
	}
	changed := false

	owners, err := objectSection(v, "", "tagOwners", 1, &changed)
	if err != nil {
		return false, err
	}
	for _, t := range tags {
		if v.Find("/tagOwners/"+pointerEscaper.Replace(t)) == nil {
			appendMember(owners, t, parseValue(mustJSON(tagOwnerValue)), 2)
			changed = true
		}
	}

	missingApprovers := false
	for _, t := range tags {
		missingApprovers = missingApprovers || v.Find("/autoApprovers/services/"+pointerEscaper.Replace(t)) == nil
	}
	if missingApprovers {
		if _, err := objectSection(v, "", "autoApprovers", 1, &changed); err != nil {
			return false, err
		}
		services, err := objectSection(v, "/autoApprovers", "services", 2, &changed)
		if err != nil {
			return false, err
		}
		for _, t := range tags {
			if v.Find("/autoApprovers/services/"+pointerEscaper.Replace(t)) == nil {
				appendMember(services, t, parseValue(mustJSON(tags)), 3)
			}
		}
	}

	grantAdded, err := ensureElement(v, "grants", requiredGrant(tags))
	if err != nil {
		return false, err
	}
	attrAdded, err := ensureElement(v, "nodeAttrs", requiredNodeAttr(tags))
	if err != nil {
		return false, err
	}
	return changed || grantAdded || attrAdded, nil
}

// objectSection returns the object at parentPath/name, appending an empty one
// (closing brace indented at depth) when absent. parentPath must already be an
// object: "" for the root, or a path a previous objectSection call ensured.
func objectSection(v *hujson.Value, parentPath, name string, depth int, changed *bool) (*hujson.Object, error) {
	if sec := v.Find(parentPath + "/" + pointerEscaper.Replace(name)); sec != nil {
		obj, ok := sec.Value.(*hujson.Object)
		if !ok {
			return nil, fmt.Errorf("the tailnet policy's %q section is not an object", name)
		}
		return obj, nil
	}

	parent := v
	if parentPath != "" {
		parent = v.Find(parentPath)
	}
	obj := &hujson.Object{AfterExtra: hujson.Extra("\n" + strings.Repeat("\t", depth))}
	appendMember(parent.Value.(*hujson.Object), name, hujson.Value{Value: obj}, depth)
	*changed = true
	return obj, nil
}

// ensureElement appends entry to the named top-level array section unless an
// element semantically equal to it already exists, creating the section when
// absent.
// ponytail: exact-entry match only - a hand-written broader rule (say, a
// grant with extra dsts) is not recognized and Once appends its own entry
// alongside; harmless duplication, widen the match if it annoys.
func ensureElement(v *hujson.Value, name string, entry any) (bool, error) {
	section := v.Find("/" + name)
	if section == nil {
		arr := &hujson.Array{AfterExtra: hujson.Extra("\n\t")}
		appendMember(v.Value.(*hujson.Object), name, hujson.Value{Value: arr}, 1)
		appendElement(arr, parseValue(mustJSON(entry)), 2)
		return true, nil
	}

	arr, ok := section.Value.(*hujson.Array)
	if !ok {
		return false, fmt.Errorf("the tailnet policy's %q section is not an array", name)
	}
	standardized := section.Clone()
	standardized.Standardize()
	var elements []json.RawMessage
	if err := json.Unmarshal(standardized.Pack(), &elements); err != nil {
		return false, fmt.Errorf("decoding the tailnet policy's %q section: %w", name, err)
	}
	want := jsonShape(mustJSON(entry))
	for _, e := range elements {
		if reflect.DeepEqual(jsonShape(e), want) {
			return false, nil
		}
	}
	appendElement(arr, parseValue(mustJSON(entry)), 2)
	return true, nil
}

// appendMember appends "name": value to obj, mimicking the previous member's
// indentation and trailing-comma style (tab-indented with a trailing comma in
// an empty object).
func appendMember(obj *hujson.Object, name string, value hujson.Value, depth int) {
	indent := hujson.Extra("\n" + strings.Repeat("\t", depth))
	trailingComma := true
	if n := len(obj.Members); n > 0 {
		indent = siblingIndent(obj.Members[n-1].Name.BeforeExtra, depth)
		trailingComma = obj.Members[n-1].Value.AfterExtra != nil
	}
	value.BeforeExtra = hujson.Extra(" ")
	if trailingComma {
		value.AfterExtra = hujson.Extra{}
	}
	obj.Members = append(obj.Members, hujson.ObjectMember{
		Name:  hujson.Value{BeforeExtra: indent, Value: hujson.String(name)},
		Value: value,
	})
}

// appendElement appends value to arr on its own line, mimicking the previous
// element's indentation and trailing-comma style.
func appendElement(arr *hujson.Array, value hujson.Value, depth int) {
	indent := hujson.Extra("\n" + strings.Repeat("\t", depth))
	trailingComma := true
	if n := len(arr.Elements); n > 0 {
		indent = siblingIndent(arr.Elements[n-1].BeforeExtra, depth)
		trailingComma = arr.Elements[n-1].AfterExtra != nil
	}
	value.BeforeExtra = indent
	if trailingComma {
		value.AfterExtra = hujson.Extra{}
	}
	arr.Elements = append(arr.Elements, value)
}

// siblingIndent derives "\n" plus the sibling's own indentation from its
// leading extra, so appended entries match the file's indent style (tabs or
// spaces). Comments are never copied; a sibling without a clean indent (e.g.
// on a shared line) falls back to depth tabs.
func siblingIndent(prev hujson.Extra, depth int) hujson.Extra {
	s := string(prev)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		if indent := s[i+1:]; strings.Trim(indent, " \t") == "" {
			return hujson.Extra("\n" + indent)
		}
	}
	return hujson.Extra("\n" + strings.Repeat("\t", depth))
}

// parseValue parses trusted JSON (mustJSON output) into a hujson value.
func parseValue(b []byte) hujson.Value {
	v, err := hujson.Parse(b)
	if err != nil {
		panic(err)
	}
	return v
}

// jsonShape decodes raw JSON into the generic any representation, so two
// encodings of the same value compare equal regardless of formatting.
func jsonShape(raw []byte) any {
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
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
