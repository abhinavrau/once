package docker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// policyServer stands in for the Tailscale policy API: token exchange, GET
// /acl with an ETag, POST /acl/validate, and POST /acl guarded by If-Match.
// Requests are recorded so tests can assert the exact request shapes. On a
// configured 412 it swaps in the conflict policy/etag, simulating a
// concurrent edit that a re-fetch then observes.
type policyServer struct {
	srv *httptest.Server

	policy           string
	etag             string
	validateResponse string
	postStatuses     []int

	conflictPolicy string
	conflictETag   string

	gets           int
	getAccepts     []string
	validateBodies []string
	postBodies     []string
	postIfMatches  []string
}

func newPolicyServer(t *testing.T, policy string) *policyServer {
	t.Helper()
	p := &policyServer{policy: policy, etag: "etag-1"}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			assert.Equal(t, "Bearer tskey-api-policy", r.Header.Get("Authorization"))
		}
		switch {
		case r.URL.Path == "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tskey-api-policy"})
		case r.Method == http.MethodGet && r.URL.Path == "/tailnet/-/acl":
			p.gets++
			p.getAccepts = append(p.getAccepts, r.Header.Get("Accept"))
			w.Header().Set("ETag", p.etag)
			_, _ = w.Write([]byte(p.policy))
		case r.Method == http.MethodPost && r.URL.Path == "/tailnet/-/acl/validate":
			body, _ := io.ReadAll(r.Body)
			p.validateBodies = append(p.validateBodies, string(body))
			assert.Equal(t, "application/hujson", r.Header.Get("Content-Type"))
			_, _ = w.Write([]byte(p.validateResponse))
		case r.Method == http.MethodPost && r.URL.Path == "/tailnet/-/acl":
			body, _ := io.ReadAll(r.Body)
			p.postBodies = append(p.postBodies, string(body))
			p.postIfMatches = append(p.postIfMatches, r.Header.Get("If-Match"))
			assert.Equal(t, "application/hujson", r.Header.Get("Content-Type"))
			status := http.StatusOK
			if len(p.postStatuses) > 0 {
				status, p.postStatuses = p.postStatuses[0], p.postStatuses[1:]
			}
			if status == http.StatusPreconditionFailed {
				p.policy, p.etag = p.conflictPolicy, p.conflictETag
			}
			w.WriteHeader(status)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func planAgainst(t *testing.T, p *policyServer) *PolicyUpdate {
	t.Helper()
	update, err := planPolicyUpdate(context.Background(), p.srv.URL, "id", "secret", "tag:once")
	require.NoError(t, err)
	return update
}

const commentedPolicy = `{
	// ACLs let you allow everything for now.
	"acls": [
		{"action": "accept", "src": ["*"], "dst": ["*:*"]}, // allow all
	],
	/* hand-maintained tag */
	"tagOwners": {
		"tag:mine": ["alice@example.com"],
	},
}`

func TestPolicyUpdatePreservesCommentsAndPatchesOnlyRequiredSections(t *testing.T) {
	p := newPolicyServer(t, commentedPolicy)

	update := planAgainst(t, p)
	require.True(t, update.Changed())
	require.NoError(t, update.Apply(context.Background()))

	require.Len(t, p.postBodies, 1)
	written := p.postBodies[0]

	// Everything already in the policy survives byte-for-byte, comments included.
	assert.Contains(t, written, "// ACLs let you allow everything for now.")
	assert.Contains(t, written, "/* hand-maintained tag */")
	assert.Contains(t, written, `{"action": "accept", "src": ["*"], "dst": ["*:*"]}, // allow all`)
	assert.Contains(t, written, `"tag:mine": ["alice@example.com"],`)

	// The four sections are present with the expected entries.
	assert.Contains(t, written, `"tag:once": ["autogroup:admin"]`)
	assert.Contains(t, written, `"autoApprovers"`)
	assert.Contains(t, written, `"tag:once": ["tag:once"]`)
	assert.Contains(t, written, `{"src":["autogroup:member"],"dst":["tag:once"],"ip":["443"]}`)
	assert.Contains(t, written, `{"target":["tag:once"],"attr":["funnel"]}`)

	// The result is itself a policy the patch recognizes as complete.
	p2 := newPolicyServer(t, written)
	assert.False(t, planAgainst(t, p2).Changed())
}

func TestPolicyUpdateRequestShapes(t *testing.T) {
	p := newPolicyServer(t, commentedPolicy)

	update := planAgainst(t, p)
	require.NoError(t, update.Apply(context.Background()))

	// GET asks for raw HuJSON so comments round-trip.
	require.Equal(t, 1, p.gets)
	assert.Equal(t, []string{"application/hujson"}, p.getAccepts)

	// Validation runs against exactly the policy that is then written.
	require.Len(t, p.validateBodies, 1)
	assert.Equal(t, p.postBodies[0], p.validateBodies[0])

	// The write carries the GET's ETag as a quoted If-Match, never unconditionally.
	assert.Equal(t, []string{`"etag-1"`}, p.postIfMatches)
}

func TestPolicyUpdateNoopWhenSnippetAlreadyApplied(t *testing.T) {
	// A user who pasted the printed snippet verbatim must get a no-op re-run.
	p := newPolicyServer(t, PolicySnippet("tag:once"))

	update := planAgainst(t, p)
	assert.False(t, update.Changed())
	require.NoError(t, update.Apply(context.Background()))
	assert.Empty(t, p.validateBodies)
	assert.Empty(t, p.postBodies)
}

func TestPolicyUpdatePatchesOnlyMissingEntries(t *testing.T) {
	// tagOwners already defines the tag and grants already has an unrelated
	// entry: only the missing pieces are added, nothing is replaced.
	p := newPolicyServer(t, `{
	"tagOwners": {"tag:once": ["bob@example.com"]},
	"grants": [
		{"src": ["group:eng"], "dst": ["tag:prod"], "ip": ["22"]},
	],
}`)

	update := planAgainst(t, p)
	require.True(t, update.Changed())
	require.NoError(t, update.Apply(context.Background()))

	written := p.postBodies[0]
	assert.Contains(t, written, `"tagOwners": {"tag:once": ["bob@example.com"]}`,
		"an existing tagOwners entry must not be replaced")
	assert.Contains(t, written, `{"src": ["group:eng"], "dst": ["tag:prod"], "ip": ["22"]},`)
	assert.Contains(t, written, `{"src":["autogroup:member"],"dst":["tag:once"],"ip":["443"]}`)
	assert.Contains(t, written, `"tag:once": ["tag:once"]`)
	assert.Contains(t, written, `{"target":["tag:once"],"attr":["funnel"]}`)
}

func TestPolicyUpdateRetriesOnETagConflict(t *testing.T) {
	p := newPolicyServer(t, commentedPolicy)
	p.postStatuses = []int{http.StatusPreconditionFailed, http.StatusOK}
	p.conflictPolicy = strings.Replace(commentedPolicy, "hand-maintained", "concurrently edited", 1)
	p.conflictETag = "etag-2"

	update := planAgainst(t, p)
	require.NoError(t, update.Apply(context.Background()))

	// The conflict forces a fresh fetch, re-patch, re-validate, and a write
	// conditioned on the new ETag - the concurrent edit is never clobbered.
	assert.Equal(t, 2, p.gets)
	require.Len(t, p.postBodies, 2)
	assert.Equal(t, []string{`"etag-1"`, `"etag-2"`}, p.postIfMatches)
	assert.Contains(t, p.postBodies[1], "concurrently edited")
	assert.Equal(t, p.postBodies, p.validateBodies)
}

func TestPolicyUpdateGivesUpAfterBoundedConflictRetries(t *testing.T) {
	p := newPolicyServer(t, commentedPolicy)
	p.postStatuses = []int{
		http.StatusPreconditionFailed,
		http.StatusPreconditionFailed,
		http.StatusPreconditionFailed,
	}
	p.conflictPolicy = commentedPolicy
	p.conflictETag = "etag-x"

	update := planAgainst(t, p)
	err := update.Apply(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "concurrent")
	assert.Len(t, p.postBodies, 3)
}

func TestPolicyUpdateValidationFailureBlocksWrite(t *testing.T) {
	p := newPolicyServer(t, commentedPolicy)
	p.validateResponse = `{"message":"line 12: invalid grant"}`

	update := planAgainst(t, p)
	err := update.Apply(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 12: invalid grant")
	assert.Empty(t, p.postBodies, "an invalid policy must never be written")
}

func TestPolicyUpdateRejectedWriteSurfacesError(t *testing.T) {
	p := newPolicyServer(t, commentedPolicy)
	p.postStatuses = []int{http.StatusBadRequest}

	update := planAgainst(t, p)
	err := update.Apply(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
	assert.Len(t, p.postBodies, 1, "a 400 is not retried")
}

func TestPolicyUpdateDiffShowsAdditionsOnly(t *testing.T) {
	p := newPolicyServer(t, commentedPolicy)

	diff := planAgainst(t, p).Diff()
	assert.Contains(t, diff, "tailnet policy (current)")
	assert.Contains(t, diff, "tailnet policy (proposed)")
	assert.Contains(t, diff, "tag:once")

	// A line the patch appends to shows as modified, but its original content
	// must reappear on the replacement side - nothing is ever removed.
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			assert.Contains(t, diff, "+"+line[1:],
				"removed line must reappear (with additions) on the + side: %s", line)
		}
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
