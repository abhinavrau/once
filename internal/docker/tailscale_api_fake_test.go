package docker

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAPICall is one call a flow made through the tailscaleAPI seam. It keeps
// the payload bytes, so the assertions that Once's requests stay
// byte-compatible with docktail's need no HTTP server to observe them.
type fakeAPICall struct {
	method  string
	path    string
	payload string
}

// fakeAPIReply is the canned outcome of one method-and-path: either a status
// and body, which the production code parses exactly as it parses the real
// API's, or an injected transport failure.
type fakeAPIReply struct {
	status int
	body   string
	err    error
}

// fakeAPIReporter is the part of *testing.T the fake needs: Errorf to fail the
// test on an unexpected call, and Helper so that failure is reported against
// the flow that made the call rather than against the fake. Taking an
// interface also lets the fake's own tests observe an unexpected call being
// reported instead of failing on it.
type fakeAPIReporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// fakeTailscaleAPI is the tailscaleAPI seam (ADR-0011) for every flow test that
// reaches the Tailscale SaaS API, in the mould of FakeTailscaleDaemon: canned
// JSON per expected request, so the flows' parsing cannot drift from the
// production paths, and recorded calls so their request shapes can be
// asserted. Token exchange and bearer mechanics live in the HTTP adapter and
// its own tests, so they are absent here. Only registered requests are
// answered: an unregistered one fails the test rather than being read as an
// API error.
type fakeTailscaleAPI struct {
	t fakeAPIReporter

	replies map[string]fakeAPIReply
	calls   []fakeAPICall
}

func newFakeTailscaleAPI(t fakeAPIReporter) *fakeTailscaleAPI {
	return &fakeTailscaleAPI{t: t, replies: map[string]fakeAPIReply{}}
}

func (f *fakeTailscaleAPI) Call(ctx context.Context, method, path string, payload []byte) (int, []byte, error) {
	f.calls = append(f.calls, fakeAPICall{method: method, path: path, payload: string(payload)})

	reply, ok := f.replies[fakeAPIKey(method, path)]
	if !ok {
		f.t.Helper()
		err := fmt.Errorf("unexpected Tailscale API call %s %s", method, path)
		f.t.Errorf("%s", err)
		return 0, nil, err
	}
	if reply.err != nil {
		return 0, nil, reply.err
	}
	return reply.status, []byte(reply.body), nil
}

// Private

// reply cans the status and body this method-and-path answers with, for as
// many times as the flow asks.
func (f *fakeTailscaleAPI) reply(method, path string, status int, body string) {
	f.replies[fakeAPIKey(method, path)] = fakeAPIReply{status: status, body: body}
}

// fail injects a transport failure for this method-and-path, as the adapter
// reports one when it cannot reach the API or its credentials are rejected.
func (f *fakeTailscaleAPI) fail(method, path string, err error) {
	f.replies[fakeAPIKey(method, path)] = fakeAPIReply{err: err}
}

// requested reports whether the flow made this call.
func (f *fakeTailscaleAPI) requested(method, path string) bool {
	return slices.ContainsFunc(f.calls, func(call fakeAPICall) bool {
		return call.method == method && call.path == path
	})
}

// payload returns the bytes the flow last sent to this method-and-path.
func (f *fakeTailscaleAPI) payload(method, path string) string {
	for _, call := range slices.Backward(f.calls) {
		if call.method == method && call.path == path {
			return call.payload
		}
	}
	return ""
}

// paths returns the paths the flow called with this method, in order.
func (f *fakeTailscaleAPI) paths(method string) []string {
	var paths []string
	for _, call := range f.calls {
		if call.method == method {
			paths = append(paths, call.path)
		}
	}
	return paths
}

const (
	fakeSvcPath     = "/tailnet/-/services/svc:x"
	fakeDevicesPath = "/tailnet/-/devices"
)

func TestFakeTailscaleAPIRecordsCallsAndCansReplies(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodPut, fakeSvcPath, http.StatusOK, `{}`)

	status, body, err := api.Call(context.Background(), http.MethodPut, fakeSvcPath, []byte(`{"name":"svc:x"}`))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, `{}`, string(body))

	assert.True(t, api.requested(http.MethodPut, fakeSvcPath))
	assert.False(t, api.requested(http.MethodDelete, fakeSvcPath))
	assert.Equal(t, `{"name":"svc:x"}`, api.payload(http.MethodPut, fakeSvcPath))
	assert.Equal(t, []string{fakeSvcPath}, api.paths(http.MethodPut))
	assert.Empty(t, api.paths(http.MethodGet))
}

func TestFakeTailscaleAPIRepeatsACannedReply(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, fakeDevicesPath, http.StatusOK, `{"devices":[]}`)

	for range 3 {
		status, body, err := api.Call(context.Background(), http.MethodGet, fakeDevicesPath, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, `{"devices":[]}`, string(body))
	}
	assert.Len(t, api.paths(http.MethodGet), 3, "a polling flow must keep getting the canned reply")
}

func TestFakeTailscaleAPIRecordsThePayloadOfEachCall(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodPut, fakeSvcPath, http.StatusOK, `{}`)

	_, _, err := api.Call(context.Background(), http.MethodPut, fakeSvcPath, []byte(`{"comment":"first"}`))
	require.NoError(t, err)
	_, _, err = api.Call(context.Background(), http.MethodPut, fakeSvcPath, []byte(`{"comment":"second"}`))
	require.NoError(t, err)

	assert.Equal(t, `{"comment":"second"}`, api.payload(http.MethodPut, fakeSvcPath))
	assert.Empty(t, api.payload(http.MethodDelete, fakeSvcPath), "an uncalled request has no payload")
}

func TestFakeTailscaleAPIInjectsTransportErrors(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.fail(http.MethodGet, fakeDevicesPath, assert.AnError)

	status, body, err := api.Call(context.Background(), http.MethodGet, fakeDevicesPath, nil)
	require.ErrorIs(t, err, assert.AnError)
	assert.Zero(t, status)
	assert.Empty(t, body)
}

func TestFakeTailscaleAPIUnexpectedCallFailsTheTest(t *testing.T) {
	reporter := &recordingReporter{}
	api := newFakeTailscaleAPI(reporter)

	_, _, err := api.Call(context.Background(), http.MethodGet, fakeDevicesPath, nil)
	require.Error(t, err, "an uncanned call must not read as a successful API response")
	require.Len(t, reporter.errors, 1)
	assert.Contains(t, reporter.errors[0], "unexpected Tailscale API call GET "+fakeDevicesPath)
	assert.True(t, api.requested(http.MethodGet, fakeDevicesPath), "an unexpected call is still recorded")
}

func TestFakeTailscaleAPIAnswersOnlyTheRegisteredMethodAndPath(t *testing.T) {
	reporter := &recordingReporter{}
	api := newFakeTailscaleAPI(reporter)
	api.reply(http.MethodGet, fakeSvcPath, http.StatusOK, `{}`)

	_, _, err := api.Call(context.Background(), http.MethodDelete, fakeSvcPath, nil)
	require.Error(t, err, "the same path under another method is still unexpected")
	_, _, err = api.Call(context.Background(), http.MethodGet, fakeSvcPath+"/devices", nil)
	require.Error(t, err, "a longer path is not a prefix match")
	assert.Len(t, reporter.errors, 2)
}

// Helpers

func fakeAPIKey(method, path string) string {
	return method + " " + path
}

// recordingReporter stands in for *testing.T where a test needs to see what
// the fake reported rather than fail on it.
type recordingReporter struct {
	errors []string
}

func (r *recordingReporter) Helper() {}

func (r *recordingReporter) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}
