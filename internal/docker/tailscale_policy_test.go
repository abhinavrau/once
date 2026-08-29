package docker

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tailscale/hujson"
)

const tailnetPolicyValidatePath = tailnetPolicyPath + "/validate"

// aclTestFailedBody is how POST /acl/validate reports a failing ACL test: the
// status stays 200 and the failure rides in the body's message.
const aclTestFailedBody = `{"message":"test(s) failed"}`

const freshTailnetPolicy = `{
	// Default Tailnet Policy for a fresh tailnet.
	"acls": [
		// Allow all connections.
		{"action": "accept", "src": ["*"], "dst": ["*:*"]},
	],
}`

const completeTailnetPolicy = `{
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
}`

// reviewPolicyFixture cans a full review conversation: the policy read with
// an ETag, the 443-grant ACL test (passing or failing), and a passing
// validation for whatever merge follows.
func reviewPolicyFixture(t *testing.T, policy string, grantPresent bool) *fakeTailscaleAPI {
	t.Helper()
	api := newFakeTailscaleAPI(t)
	header := http.Header{}
	header.Set("ETag", `"v1"`)
	api.replyWithHeaders(http.MethodGet, tailnetPolicyPath, http.StatusOK, policy, header)
	grantReply := aclTestFailedBody
	if grantPresent {
		grantReply = ""
	}
	api.replyOnce(http.MethodPost, tailnetPolicyValidatePath, http.StatusOK, grantReply)
	api.reply(http.MethodPost, tailnetPolicyValidatePath, http.StatusOK, "")
	return api
}

func TestReviewMergesEveryMissingSectionIntoAFreshPolicy(t *testing.T) {
	api := reviewPolicyFixture(t, freshTailnetPolicy, false)

	review, err := reviewTailnetPolicy(context.Background(), api, "tag:once", true)
	require.NoError(t, err)

	assert.Equal(t, []string{"grants", "autoApprovers.services", "nodeAttrs"}, review.Added)
	assert.Equal(t, `"v1"`, review.ETag)
	assert.Empty(t, review.Fallback)

	_, err = hujson.Parse([]byte(review.Merged))
	require.NoError(t, err, "the Merged Policy must stay valid HuJSON")
	assert.Contains(t, review.Merged, "Default Tailnet Policy", "comments must survive the merge")
	assert.Contains(t, review.Merged, `{"src": ["autogroup:member"], "dst": ["tag:once"], "ip": ["443"]}`)
	assert.Contains(t, review.Merged, `"services": {`)
	assert.Contains(t, review.Merged, `{"target": ["tag:once"], "attr": ["funnel"]}`)

	assertKeepsEveryLine(t, freshTailnetPolicy, review.Merged)
}

func TestReviewReadsThePolicyAsHuJSONAndValidatesTheMergeAsHuJSON(t *testing.T) {
	api := reviewPolicyFixture(t, freshTailnetPolicy, false)

	review, err := reviewTailnetPolicy(context.Background(), api, "tag:once", true)
	require.NoError(t, err)

	assert.Equal(t, "application/hujson", api.requestHeader(http.MethodGet, tailnetPolicyPath).Get("Accept"),
		"the read must ask for the user's document verbatim, not the API's JSON rendering")
	assert.Equal(t, "application/hujson", api.requestHeader(http.MethodPost, tailnetPolicyValidatePath).Get("Content-Type"))
	assert.Equal(t, review.Merged, api.payload(http.MethodPost, tailnetPolicyValidatePath),
		"exactly the document shown to the user is what gets validated")
}

func TestReviewGrantPresenceIsAnACLTestNotTextMatching(t *testing.T) {
	// No literal 443 grant appears anywhere, but the canned ACL test passes -
	// an equivalent rule spelled differently - so no grant may be added.
	api := reviewPolicyFixture(t, freshTailnetPolicy, true)

	review, err := reviewTailnetPolicy(context.Background(), api, "tag:once", true)
	require.NoError(t, err)

	assert.Equal(t, []string{"autoApprovers.services", "nodeAttrs"}, review.Added)
	tests := api.payloads(http.MethodPost, tailnetPolicyValidatePath)
	require.NotEmpty(t, tests)
	assert.Equal(t, `[{"src":"autogroup:member","accept":["tag:once:443"]}]`, tests[0])
}

func TestReviewNothingToAddSkipsTheMergedValidation(t *testing.T) {
	api := reviewPolicyFixture(t, completeTailnetPolicy, true)

	review, err := reviewTailnetPolicy(context.Background(), api, "tag:once", true)
	require.NoError(t, err)

	assert.Empty(t, review.Added)
	assert.Equal(t, completeTailnetPolicy, review.Merged, "present sections are never duplicated or rewritten")
	assert.Equal(t, 2, api.callCount(), "nothing to add means nothing to validate: just the read and the grant test")
}

func TestReviewNodesTopologyDoesNotAddServiceApprovers(t *testing.T) {
	api := reviewPolicyFixture(t, freshTailnetPolicy, false)

	review, err := reviewTailnetPolicy(context.Background(), api, "tag:once", false)
	require.NoError(t, err)

	assert.Equal(t, []string{"grants", "nodeAttrs"}, review.Added, "Nodes has no Tailscale Services to approve (ADR-0015)")
	assert.NotContains(t, review.Merged, "autoApprovers")
}

func TestReviewAddsOnlyTheMissingSections(t *testing.T) {
	missingFunnel := `{
	"tagOwners": {
		"tag:once": ["autogroup:admin"],
	},
	"grants": [
		{"src": ["autogroup:member"], "dst": ["tag:once"], "ip": ["443"]},
	],
	"autoApprovers": {
		// Auto-approve advertised services.
		"services": {"tag:once": ["tag:once"]},
	},
}`
	api := reviewPolicyFixture(t, missingFunnel, true)

	review, err := reviewTailnetPolicy(context.Background(), api, "tag:once", true)
	require.NoError(t, err)

	assert.Equal(t, []string{"nodeAttrs"}, review.Added)
	assert.Equal(t, 1, strings.Count(review.Merged, `"funnel"`))
	assert.Contains(t, review.Merged, `"services": {"tag:once": ["tag:once"]},`, "a present section is left exactly as written")
}

func TestReviewAddsApproverEntriesOnlyForMissingTags(t *testing.T) {
	partial := `{
	"autoApprovers": {
		"services": {
			"tag:once": ["group:platform"],
		},
	},
}`
	api := reviewPolicyFixture(t, partial, true)

	review, err := reviewTailnetPolicy(context.Background(), api, "tag:once,tag:extra", true)
	require.NoError(t, err)

	assert.Contains(t, review.Added, "autoApprovers.services")
	assert.Contains(t, review.Merged, `"tag:once": ["group:platform"],`, "an existing key is never overwritten")
	assert.Contains(t, review.Merged, `"tag:extra": ["tag:once","tag:extra"],`)
}

func TestReviewFollowsTheDocumentsOwnIndentationAndCommaStyle(t *testing.T) {
	fourSpaces := `{
    "acls": [
        {"action": "accept", "src": ["*"], "dst": ["*:*"]}
    ]
}`
	api := reviewPolicyFixture(t, fourSpaces, false)

	review, err := reviewTailnetPolicy(context.Background(), api, "tag:once", false)
	require.NoError(t, err)

	assert.Contains(t, review.Merged, "\n    \"grants\": [\n        {\"src\":")
	assert.Contains(t, review.Merged, "\n    \"nodeAttrs\": [\n        {\"target\":")
	assert.NotContains(t, review.Merged, "\t")
	assert.NotContains(t, review.Merged, ",\n    ]", "a document without trailing commas gains none")
}

func TestReviewFallsBackWhenThePolicyReadScopeIsMissing(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, tailnetPolicyPath, http.StatusForbidden, `{"message":"insufficient permissions"}`)

	review, err := reviewTailnetPolicy(context.Background(), api, "tag:once", true)
	require.NoError(t, err, "a missing scope degrades, it does not fail")

	assert.Contains(t, review.Fallback, "policy_file:read")
	assert.Contains(t, review.Fallback, "insufficient permissions")
	assert.Empty(t, review.Added)
	assert.Equal(t, 1, api.callCount(), "nothing else runs without a readable policy")
}

func TestReviewReportsAMergedPolicyFailingValidation(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	header := http.Header{}
	header.Set("ETag", `"v1"`)
	api.replyWithHeaders(http.MethodGet, tailnetPolicyPath, http.StatusOK, freshTailnetPolicy, header)
	api.replyOnce(http.MethodPost, tailnetPolicyValidatePath, http.StatusOK, aclTestFailedBody)
	api.reply(http.MethodPost, tailnetPolicyValidatePath, http.StatusOK, `{"message":"invalid policy"}`)

	_, err := reviewTailnetPolicy(context.Background(), api, "tag:once", true)
	require.ErrorContains(t, err, "invalid policy")
}

func TestPolicyOutcomesProceedWarnAndRefuse(t *testing.T) {
	nothingToAdd := PolicyReview{}
	assert.False(t, nothingToAdd.blocksInstall())
	assert.Empty(t, nothingToAdd.warning())

	funnelOnly := PolicyReview{Added: []string{policySectionNodeAttrs}}
	assert.False(t, funnelOnly.blocksInstall(), "a missing funnel nodeAttr breaks only Funnel, so enable proceeds")
	assert.Contains(t, funnelOnly.warning(), "only Tailscale Funnel breaks", "the warning must name Funnel as the only casualty")

	missingGrant := PolicyReview{Added: []string{policySectionGrants, policySectionNodeAttrs}}
	assert.True(t, missingGrant.blocksInstall())
	assert.Empty(t, missingGrant.warning(), "a refusal is not also a warning")

	missingApprovers := PolicyReview{Added: []string{policySectionApprovers}}
	assert.True(t, missingApprovers.blocksInstall())

	fallback := PolicyReview{Fallback: "the OAuth client is missing the policy_file:read scope"}
	assert.False(t, fallback.blocksInstall())
	assert.Contains(t, fallback.warning(), "policy_file:read")
	assert.Contains(t, fallback.warning(), "once tailscale policy", "the degraded path must point at the static snippet")
}

func TestPolicyRefusedErrorNamesEachSectionAndWhatItBreaks(t *testing.T) {
	err := &PolicyRefusedError{Review: PolicyReview{Added: []string{policySectionGrants, policySectionApprovers, policySectionNodeAttrs}}}

	msg := err.Error()
	assert.Contains(t, msg, "443 grant")
	assert.Contains(t, msg, "cannot reach the apps on port 443")
	assert.Contains(t, msg, "autoApprovers.services")
	assert.Contains(t, msg, "manual approval")
	assert.Contains(t, msg, "nothing was installed")
	assert.Contains(t, msg, "apply the Merged Policy, then re-run")

	grantOnly := &PolicyRefusedError{Review: PolicyReview{Added: []string{policySectionGrants}}}
	assert.NotContains(t, grantOnly.Error(), "autoApprovers", "only what is actually missing gets named")
}

func TestRequireUnchangedPolicyComparesTheCapturedETag(t *testing.T) {
	etagReply := func(etag string) *fakeTailscaleAPI {
		api := newFakeTailscaleAPI(t)
		header := http.Header{}
		header.Set("ETag", etag)
		api.replyWithHeaders(http.MethodGet, tailnetPolicyPath, http.StatusOK, "{}", header)
		return api
	}

	assert.NoError(t, requireUnchangedPolicy(context.Background(), etagReply(`"v1"`), `"v1"`))

	err := requireUnchangedPolicy(context.Background(), etagReply(`"v2"`), `"v1"`)
	require.ErrorIs(t, err, ErrPolicyChangedDuringEnable)
	assert.Contains(t, err.Error(), "nothing was installed")
}

func TestRequireUnchangedPolicyProceedsWhenTheScopeWasRevokedMidFlight(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, tailnetPolicyPath, http.StatusForbidden, `{"message":"insufficient permissions"}`)

	assert.NoError(t, requireUnchangedPolicy(context.Background(), api, `"v1"`),
		"the scope is an enhancement, never a precondition - no comparison, no stop")
}

// assertKeepsEveryLine asserts every line of the original policy survives in
// the merged one, byte for byte and in order - the merge only adds.
func assertKeepsEveryLine(t *testing.T, original, merged string) {
	t.Helper()
	remaining := strings.Split(merged, "\n")
	for _, line := range strings.Split(original, "\n") {
		idx := slices.Index(remaining, line)
		require.NotEqual(t, -1, idx, "line %q must survive the merge byte-for-byte", line)
		remaining = remaining[idx+1:]
	}
}

func TestPolicySnippetCoversAllSectionsPerTag(t *testing.T) {
	snippet := PolicySnippet("once,tag:extra")

	assert.Contains(t, snippet, `"tag:once": ["autogroup:admin"],`)
	assert.Contains(t, snippet, `"tag:extra": ["autogroup:admin"],`)
	assert.Contains(t, snippet, `"tag:once": ["tag:once","tag:extra"],`)
	assert.Contains(t, snippet, `{"src":["autogroup:member"],"dst":["tag:once","tag:extra"],"ip":["443"]},`)
	assert.Contains(t, snippet, `{"target":["tag:once","tag:extra"],"attr":["funnel"]},`)
}

func TestPolicySnippetIsValidHuJSON(t *testing.T) {
	// The snippet is pasted into the tailnet policy editor verbatim, so it must
	// parse as HuJSON.
	_, err := hujson.Parse([]byte(PolicySnippet("tag:once")))
	require.NoError(t, err)
}

func TestReviewCarriesTheOriginalPolicyForDiffing(t *testing.T) {
	api := reviewPolicyFixture(t, freshTailnetPolicy, false)

	review, err := reviewTailnetPolicy(context.Background(), api, "tag:once", true)
	require.NoError(t, err)

	assert.Equal(t, freshTailnetPolicy, review.Original, "the review must carry the policy as read, so frontends can diff Merged against it")
}

func TestTagOwnersPolicyShapesAgree(t *testing.T) {
	block := TagOwnersPolicyBlock("tag:once")
	assert.Contains(t, block, TagOwnersPolicyLines("tag:once"), "the line shape must be the block's own entry")

	_, err := hujson.Parse([]byte("{" + block + "}"))
	require.NoError(t, err, "the paste payload must be valid HuJSON in context")

	assert.Equal(t, "\"tag:once\": [\"autogroup:admin\"],\n\"tag:extra\": [\"autogroup:admin\"],",
		TagOwnersPolicyLines("tag:once,tag:extra"), "one line per tag")
}
