package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseExposure(t *testing.T) {
	e, err := ParseExposure("local")
	require.NoError(t, err)
	assert.Equal(t, ExposureLocal, e)

	e, err = ParseExposure("tailnet")
	require.NoError(t, err)
	assert.Equal(t, ExposureTailnet, e)

	e, err = ParseExposure("public")
	require.NoError(t, err)
	assert.Equal(t, ExposurePublic, e)

	_, err = ParseExposure("")
	assert.ErrorContains(t, err, "invalid exposure")

	_, err = ParseExposure("everywhere")
	assert.ErrorContains(t, err, "invalid exposure")
}

func TestExposureForHost(t *testing.T) {
	assert.Equal(t, ExposureLocal, ExposureForHost("app.localhost"))
	assert.Equal(t, ExposureLocal, ExposureForHost("localhost"))
	assert.Equal(t, ExposureLocal, ExposureForHost(""))
	assert.Equal(t, ExposurePublic, ExposureForHost("app.example.com"))
}

func TestResolveHostLocal(t *testing.T) {
	host, err := ResolveHost(ExposureLocal, "", "campfire")
	require.NoError(t, err)
	assert.Equal(t, "campfire.localhost", host)

	host, err = ResolveHost(ExposureLocal, "writebook", "campfire")
	require.NoError(t, err)
	assert.Equal(t, "writebook.localhost", host)

	host, err = ResolveHost(ExposureLocal, "writebook.localhost", "campfire")
	require.NoError(t, err)
	assert.Equal(t, "writebook.localhost", host)

	host, err = ResolveHost(ExposureLocal, "localhost", "campfire")
	require.NoError(t, err)
	assert.Equal(t, "localhost", host)

	// A real FQDN is refused rather than mangled into fqdn.localhost.
	_, err = ResolveHost(ExposureLocal, "chat.example.com", "campfire")
	assert.ErrorIs(t, err, ErrHostNotLocal)
}

func TestResolveHostTailnet(t *testing.T) {
	// The host is the Tailscale Service name, derived from the app name when
	// not given.
	host, err := ResolveHost(ExposureTailnet, "", "campfire")
	require.NoError(t, err)
	assert.Equal(t, "campfire", host)

	host, err = ResolveHost(ExposureTailnet, "writebook", "campfire")
	require.NoError(t, err)
	assert.Equal(t, "writebook", host)

	// A service name must be a Magic DNS label; dotted or invalid names are refused.
	_, err = ResolveHost(ExposureTailnet, "chat.example.com", "campfire")
	assert.ErrorContains(t, err, "not a valid DNS label")

	_, err = ResolveHost(ExposureTailnet, "My_App", "campfire")
	assert.ErrorContains(t, err, "not a valid DNS label")
}

func TestResolveHostPublicRequiresFQDN(t *testing.T) {
	_, err := ResolveHost(ExposurePublic, "", "campfire")
	assert.ErrorIs(t, err, ErrPublicHostRequired)

	_, err = ResolveHost(ExposurePublic, "writebook", "campfire")
	assert.ErrorIs(t, err, ErrPublicHostRequired)

	_, err = ResolveHost(ExposurePublic, "writebook.localhost", "campfire")
	assert.ErrorIs(t, err, ErrPublicHostRequired)

	host, err := ResolveHost(ExposurePublic, "chat.example.com", "campfire")
	require.NoError(t, err)
	assert.Equal(t, "chat.example.com", host)
}
