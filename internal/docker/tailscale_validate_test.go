package docker

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The probe's request paths, spelled out rather than derived from the
// production constants: they must match what Docktail sends, including the "-"
// tailnet segment, so a change to either side has to be made deliberately here.
const (
	tsKeysPath      = "/tailnet/-/keys"
	probeKeyPath    = "/tailnet/-/keys/k123"
	probeSvcPath    = "/tailnet/-/services/svc:once-probe"
	probeDevsPath   = "/tailnet/-/services/svc:once-probe/devices"
	notPermittedMsg = `{"message":"calling actor does not have enough permissions"}`

	// sidecarKeyPayload is the auth-key request Once sends for tag:once:
	// reusable (a restart-always container must be able to re-auth after dying
	// mid-login), non-ephemeral (the sidecar keeps its node state in a volume),
	// preauthorized, tagged, described as the sidecar. The probe and the real
	// sidecar mint must send exactly these bytes.
	sidecarKeyPayload = `{"capabilities":{"devices":{"create":{"reusable":true,"ephemeral":false,"preauthorized":true,"tags":["tag:once"]}}},"description":"once-tailscale"}`
)

// probeAPI returns a seam that answers every step of the credential probe
// with success; each test re-cans the one step it exercises.
func probeAPI(t *testing.T) *fakeTailscaleAPI {
	t.Helper()
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodPost, tsKeysPath, http.StatusOK, `{"id":"k123","key":"tskey-auth-xyz"}`)
	api.reply(http.MethodDelete, probeKeyPath, http.StatusOK, "")
	api.reply(http.MethodGet, probeSvcPath, http.StatusNotFound, "")
	api.reply(http.MethodPut, probeSvcPath, http.StatusOK, `{}`)
	api.reply(http.MethodGet, probeDevsPath, http.StatusOK, `{"hosts":[]}`)
	api.reply(http.MethodDelete, probeSvcPath, http.StatusOK, "")
	return api
}

func validateAgainst(api *fakeTailscaleAPI, tag string) error {
	return validateOAuthCredentials(context.Background(), api,
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: tag})
}

func TestValidateOAuthCredentialsSuccess(t *testing.T) {
	api := probeAPI(t)

	require.NoError(t, validateAgainst(api, "tag:once"))

	assert.Equal(t, sidecarKeyPayload, api.payload(http.MethodPost, tsKeysPath))
	assert.True(t, api.requested(http.MethodDelete, probeKeyPath), "the probe key must be cleaned up")

	// The Service create must be byte-compatible with Docktail's putService
	// payload (json.Marshal of a map: alphabetical keys).
	assert.Equal(t,
		`{"comment":"Once credential probe - safe to delete","name":"svc:once-probe","ports":["tcp:443"],"tags":["tag:once"]}`,
		api.payload(http.MethodPut, probeSvcPath))
	assert.True(t, api.requested(http.MethodGet, probeDevsPath))
	assert.True(t, api.requested(http.MethodDelete, probeSvcPath), "probe Service must be cleaned up")
}

func TestValidateOAuthCredentialsUsesNormalizedTag(t *testing.T) {
	api := probeAPI(t)

	// EnableTailscale normalizes before probing; a bare "once" must reach the
	// key and Service payloads as "tag:once", not the misleading bare name.
	settings := TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "once,tag:admin"}
	settings.Tag = settings.normalizeTag()
	err := validateOAuthCredentials(context.Background(), api, settings)
	require.NoError(t, err)

	assert.Contains(t, api.payload(http.MethodPost, tsKeysPath), `"tags":["tag:once","tag:admin"]`)
	assert.Contains(t, api.payload(http.MethodPut, probeSvcPath), `"tags":["tag:once","tag:admin"]`)
}

func TestValidateOAuthCredentialsRejectsBadCredentials(t *testing.T) {
	api := probeAPI(t)
	// Rejected credentials surface from the adapter on the probe's first call,
	// wherever that call goes; the wording is the adapter's, tested there.
	rejected := errors.New("Tailscale rejected the OAuth credentials (401 Unauthorized)")
	api.fail(http.MethodPost, tsKeysPath, rejected)

	err := validateAgainst(api, "tag:once")
	require.ErrorIs(t, err, rejected)
	assert.Len(t, api.calls, 1, "a failed first call must abort the probe, not mint or probe further")
}

func TestValidateOAuthCredentialsMissingAuthKeysScope(t *testing.T) {
	api := probeAPI(t)
	api.reply(http.MethodPost, tsKeysPath, http.StatusForbidden, notPermittedMsg)

	err := validateAgainst(api, "tag:once")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth_keys")
	assert.Contains(t, err.Error(), "does not have enough permissions")
	assert.False(t, api.requested(http.MethodPut, probeSvcPath), "the Service probe must not run after a scope failure")
}

func TestValidateOAuthCredentialsRejectsUnownedTag(t *testing.T) {
	api := probeAPI(t)
	api.reply(http.MethodPost, tsKeysPath, http.StatusBadRequest, `{"message":"requested tags are invalid or not permitted"}`)

	err := validateAgainst(api, "tag:wrong")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTagNotInPolicy)
	assert.Contains(t, err.Error(), "tag:wrong")
	assert.Contains(t, err.Error(), "tagOwners")
	assert.Contains(t, err.Error(), "invalid or not permitted")
	assert.NotContains(t, err.Error(), "auth_keys", "an unowned tag must not read as a scope problem")
}

func TestValidateOAuthCredentialsServerErrorIsNotATagProblem(t *testing.T) {
	api := probeAPI(t)
	api.reply(http.MethodPost, tsKeysPath, http.StatusInternalServerError, "")

	err := validateAgainst(api, "tag:once")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrTagNotInPolicy, "a 5xx must not tell the user to edit the tailnet policy")
}

func TestValidateOAuthCredentialsMissingServicesScope(t *testing.T) {
	api := probeAPI(t)
	api.reply(http.MethodGet, probeSvcPath, http.StatusForbidden, notPermittedMsg)

	err := validateAgainst(api, "tag:once")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Services: Write")
	assert.True(t, api.requested(http.MethodDelete, probeKeyPath), "the auth key probe runs and cleans up first")
}

func TestValidateOAuthCredentialsRejectsUndefinedServiceTag(t *testing.T) {
	api := probeAPI(t)
	api.reply(http.MethodPut, probeSvcPath, http.StatusBadRequest, `{"message":"tag \"tag:once\" is invalid or not permitted"}`)

	err := validateAgainst(api, "tag:once")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTagNotInPolicy)
	assert.Contains(t, err.Error(), "tag:once")
	assert.Contains(t, err.Error(), "tagOwners")
	assert.False(t, api.requested(http.MethodDelete, probeSvcPath), "a rejected create leaves nothing to delete")
}

func TestValidateOAuthCredentialsMissingDevicesScope(t *testing.T) {
	api := probeAPI(t)
	api.reply(http.MethodGet, probeDevsPath, http.StatusForbidden, notPermittedMsg)

	err := validateAgainst(api, "tag:once")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Devices -> Core")
	assert.True(t, api.requested(http.MethodDelete, probeSvcPath), "probe Service must be cleaned up even when the devices check fails")
}

func TestValidateOAuthCredentialsRefusesForeignServiceName(t *testing.T) {
	api := probeAPI(t)
	api.reply(http.MethodGet, probeSvcPath, http.StatusOK, `{"name":"svc:once-probe","comment":"my real service"}`)

	err := validateAgainst(api, "tag:once")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.False(t, api.requested(http.MethodPut, probeSvcPath), "a foreign Service must never be overwritten")
	assert.False(t, api.requested(http.MethodDelete, probeSvcPath), "a foreign Service must never be deleted")
}

func TestValidateOAuthCredentialsReusesLeftoverProbeService(t *testing.T) {
	api := probeAPI(t)
	api.reply(http.MethodGet, probeSvcPath, http.StatusOK,
		`{"name":"svc:once-probe","comment":"Once credential probe - safe to delete"}`)

	// A leftover from an interrupted probe is ours: overwrite and delete it
	// rather than failing enable forever.
	require.NoError(t, validateAgainst(api, "tag:once"))
	assert.True(t, api.requested(http.MethodPut, probeSvcPath))
	assert.True(t, api.requested(http.MethodDelete, probeSvcPath))
}

func TestValidateOAuthCredentialsRequiresTag(t *testing.T) {
	// The seam cans nothing: any call would fail the test.
	err := validateOAuthCredentials(context.Background(), newFakeTailscaleAPI(t),
		TailscaleSettings{ClientID: "id", ClientSecret: "secret"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tag is required")
}

func TestValidateTailscaleCredentialsSkipsControlSeam(t *testing.T) {
	t.Setenv(envControlURL, "http://headscale:8080")
	t.Setenv(envAuthKey, "tskey-auth-abc")

	// No tag, no reachable API: validation must short-circuit on the control seam.
	err := validateTailscaleCredentials(context.Background(), TailscaleSettings{})
	require.NoError(t, err)
}

func TestMintSidecarAuthKeyMatchesProbeShape(t *testing.T) {
	api := probeAPI(t)

	key, err := mintSidecarAuthKey(context.Background(), api, TailscaleSettings{Tag: "tag:once"})
	require.NoError(t, err)
	assert.Equal(t, "tskey-auth-xyz", key)

	// Byte-for-byte the request the probe proved could be minted.
	assert.Equal(t, sidecarKeyPayload, api.payload(http.MethodPost, tsKeysPath))

	// The sidecar registers with this key; unlike the probe's, it is not deleted.
	assert.False(t, api.requested(http.MethodDelete, probeKeyPath))
}
