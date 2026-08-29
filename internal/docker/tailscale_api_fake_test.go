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

// The paths the fake's own tests use. They are deliberately not any flow's real
// path: the paths that must match what docktail sends are asserted by the flow
// tests, next to the flows that build them.
const (
	fakeSvcPath     = "/tailnet/-/services/svc:x"
	fakeDevicesPath = fakeSvcPath + "/devices"
)

// authenticateMethod is the sentinel "method" Authenticate calls are canned,
// recorded, and error-injected under - it carries no method or path of its
// own, unlike Call, but reuses the same apiRequest bookkeeping so it is a call
// like any other rather than a bespoke case.
const authenticateMethod = "AUTHENTICATE"

// apiRequest identifies a Tailscale API request by the two things the seam
// carries with it: the method and the path.
type apiRequest struct {
	method string
	path   string
}

// String renders the request the way an unexpected-call failure names it:
// "METHOD /path", or just the method when there is no path, as with
// Authenticate.
func (r apiRequest) String() string {
	if r.path == "" {
		return r.method
	}
	return r.method + " " + r.path
}

// fakeAPICall is one call a flow made through the tailscaleAPI seam. It keeps
// the payload bytes, so the assertions that Once's requests stay
// byte-compatible with docktail's need no HTTP server to observe them, and
// the request headers when the flow sent any.
type fakeAPICall struct {
	apiRequest
	payload string
	header  http.Header
}

// fakeAPIReply is the canned outcome of one request: either a status, body,
// and response headers, which the production code parses exactly as it parses
// the real API's, or an injected transport failure.
type fakeAPIReply struct {
	status int
	body   string
	header http.Header
	err    error
}

// fakeAPIReporter is the part of *testing.T the fake needs: Errorf to fail the
// test on an unexpected call, and Helper so that failure is reported against
// the flow that made the call rather than against the fake. Taking an interface
// also lets the fake's own tests observe an unexpected call being reported
// instead of failing on it.
type fakeAPIReporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// recordingReporter stands in for *testing.T where a test needs to see what the
// fake reported rather than fail on it.
type recordingReporter struct {
	errors []string
}

func (r *recordingReporter) Helper() {}

func (r *recordingReporter) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

// fakeTailscaleAPI is the tailscaleAPI seam (ADR-0011) for every flow test that
// reaches the Tailscale SaaS API, in the mould of FakeTailnetReader: canned
// JSON per expected request, so the flows' parsing cannot drift from the
// production paths, and recorded calls so their request shapes can be asserted.
// Token exchange and bearer mechanics live in the HTTP adapter and its own
// tests, so they are absent here. Only registered requests are answered: an
// unregistered one fails the test rather than being read as an API error.
type fakeTailscaleAPI struct {
	t fakeAPIReporter

	replies map[apiRequest]fakeAPIReply
	queued  map[apiRequest][]fakeAPIReply
	calls   []fakeAPICall
}

func newFakeTailscaleAPI(t fakeAPIReporter) *fakeTailscaleAPI {
	return &fakeTailscaleAPI{t: t, replies: map[apiRequest]fakeAPIReply{}, queued: map[apiRequest][]fakeAPIReply{}}
}

func (f *fakeTailscaleAPI) Call(ctx context.Context, method, path string, payload []byte) (int, []byte, error) {
	status, _, body, err := f.CallWithHeaders(ctx, method, path, nil, payload)
	return status, body, err
}

func (f *fakeTailscaleAPI) CallWithHeaders(ctx context.Context, method, path string, header http.Header, payload []byte) (int, http.Header, []byte, error) {
	reply, err := f.record(apiRequest{method, path}, string(payload), header)
	if err != nil {
		return 0, nil, nil, err
	}
	if reply.err != nil {
		return 0, nil, nil, reply.err
	}
	return reply.status, reply.header, []byte(reply.body), nil
}

func (f *fakeTailscaleAPI) Authenticate(ctx context.Context) error {
	reply, err := f.record(apiRequest{authenticateMethod, ""}, "", nil)
	if err != nil {
		return err
	}
	return reply.err
}

// Private

// record appends request to the call log with payload and headers, and
// returns the canned reply for it - the bookkeeping every operation the seam
// offers shares, so a call fails the same way whether it carries a method and
// path or, like Authenticate, neither. Queued one-shot replies are consumed
// before the persistent one, so a flow that hits the same endpoint twice with
// different meanings (the policy flow's two /acl/validate posts) can be
// canned in order.
func (f *fakeTailscaleAPI) record(request apiRequest, payload string, header http.Header) (fakeAPIReply, error) {
	f.calls = append(f.calls, fakeAPICall{apiRequest: request, payload: payload, header: header})

	if queue := f.queued[request]; len(queue) > 0 {
		reply := queue[0]
		f.queued[request] = queue[1:]
		return reply, nil
	}
	reply, ok := f.replies[request]
	if !ok {
		f.t.Helper()
		err := fmt.Errorf("unexpected Tailscale API call %s", request)
		f.t.Errorf("%s", err)
		return fakeAPIReply{}, err
	}
	return reply, nil
}

// reply cans the status and body this request answers with, for as many times
// as the flow asks.
func (f *fakeTailscaleAPI) reply(method, path string, status int, body string) {
	f.replies[apiRequest{method, path}] = fakeAPIReply{status: status, body: body}
}

// replyWithHeaders is reply with response headers, for the requests whose
// answer the flow reads from a header (the policy read's ETag).
func (f *fakeTailscaleAPI) replyWithHeaders(method, path string, status int, body string, header http.Header) {
	f.replies[apiRequest{method, path}] = fakeAPIReply{status: status, body: body, header: header}
}

// replyOnce queues a one-shot reply, consumed before the persistent reply in
// the order queued.
func (f *fakeTailscaleAPI) replyOnce(method, path string, status int, body string) {
	request := apiRequest{method, path}
	f.queued[request] = append(f.queued[request], fakeAPIReply{status: status, body: body})
}

// fail injects a transport failure for this request, as the adapter reports one
// when it cannot reach the API or its credentials are rejected.
func (f *fakeTailscaleAPI) fail(method, path string, err error) {
	f.replies[apiRequest{method, path}] = fakeAPIReply{err: err}
}

func (f *fakeTailscaleAPI) callCount() int {
	return len(f.calls)
}

func (f *fakeTailscaleAPI) requested(method, path string) bool {
	want := apiRequest{method, path}
	return slices.ContainsFunc(f.calls, func(call fakeAPICall) bool { return call.apiRequest == want })
}

// payload returns the bytes the flow last sent to this request; whether it was
// sent at all is requested's question, not this one's.
func (f *fakeTailscaleAPI) payload(method, path string) string {
	want := apiRequest{method, path}
	for _, call := range slices.Backward(f.calls) {
		if call.apiRequest == want {
			return call.payload
		}
	}
	return ""
}

// payloads returns every payload the flow sent to this request, in order -
// for the endpoints one flow hits more than once.
func (f *fakeTailscaleAPI) payloads(method, path string) []string {
	want := apiRequest{method, path}
	var payloads []string
	for _, call := range f.calls {
		if call.apiRequest == want {
			payloads = append(payloads, call.payload)
		}
	}
	return payloads
}

// requestHeader returns the headers the flow last sent to this request, nil
// when it sent none.
func (f *fakeTailscaleAPI) requestHeader(method, path string) http.Header {
	want := apiRequest{method, path}
	for _, call := range slices.Backward(f.calls) {
		if call.apiRequest == want {
			return call.header
		}
	}
	return nil
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

func TestFakeTailscaleAPIRecordsThePayloadOfEachCall(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodPut, fakeSvcPath, http.StatusOK, `{}`)

	_, _, err := api.Call(context.Background(), http.MethodPut, fakeSvcPath, []byte(`{"comment":"first"}`))
	require.NoError(t, err)
	_, _, err = api.Call(context.Background(), http.MethodPut, fakeSvcPath, []byte(`{"comment":"second"}`))
	require.NoError(t, err)

	assert.Equal(t, 2, api.callCount(), "a canned reply answers as often as the flow asks")
	assert.Equal(t, `{"comment":"second"}`, api.payload(http.MethodPut, fakeSvcPath))
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

func TestFakeTailscaleAPIAuthenticateCansAndRecordsCalls(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(authenticateMethod, "", 0, "")

	require.NoError(t, api.Authenticate(context.Background()))
	require.NoError(t, api.Authenticate(context.Background()))

	assert.True(t, api.requested(authenticateMethod, ""))
	assert.Equal(t, 2, api.callCount(), "a canned reply answers as often as the flow asks")
}

func TestFakeTailscaleAPIAuthenticateInjectsErrors(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.fail(authenticateMethod, "", ErrOAuthCredentialsRejected)

	require.ErrorIs(t, api.Authenticate(context.Background()), ErrOAuthCredentialsRejected)
}

func TestFakeTailscaleAPIAuthenticateUnexpectedCallFailsTheTest(t *testing.T) {
	reporter := &recordingReporter{}
	api := newFakeTailscaleAPI(reporter)

	err := api.Authenticate(context.Background())
	require.Error(t, err, "an uncanned Authenticate must not read as proven credentials")
	require.Len(t, reporter.errors, 1)
	assert.Contains(t, reporter.errors[0], "unexpected Tailscale API call "+authenticateMethod)
	assert.True(t, api.requested(authenticateMethod, ""), "an unexpected call is still recorded")
}

func TestFakeTailscaleAPIAnswersOnlyTheRegisteredMethodAndPath(t *testing.T) {
	reporter := &recordingReporter{}
	api := newFakeTailscaleAPI(reporter)
	api.reply(http.MethodGet, fakeSvcPath, http.StatusOK, `{}`)

	_, _, err := api.Call(context.Background(), http.MethodDelete, fakeSvcPath, nil)
	require.Error(t, err, "the same path under another method is still unexpected")
	_, _, err = api.Call(context.Background(), http.MethodGet, fakeDevicesPath, nil)
	require.Error(t, err, "a longer path is not a prefix match")
	assert.Len(t, reporter.errors, 2)
}
