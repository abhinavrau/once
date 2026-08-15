package docker

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppProfileZeroValueResolvesDefaults(t *testing.T) {
	var p AppProfile

	assert.Equal(t, "once-campfire", p.AppName("ghcr.io/basecamp/once-campfire"))
	assert.Equal(t, []string{"/storage", "/rails/storage"}, p.MountTargets())
	assert.Equal(t, "/up", p.HealthCheckPath())
}

func TestAppProfileResolvedAccessors(t *testing.T) {
	p := AppProfile{
		BaseName:   "vaultwarden",
		DataPath:   "/data",
		HealthPath: "/alive",
		URLEnvVar:  "DOMAIN",
	}

	assert.Equal(t, "vaultwarden", p.AppName("vaultwarden/server"))
	assert.Equal(t, []string{"/data"}, p.MountTargets(), "a custom data path replaces both default targets")
	assert.Equal(t, "/alive", p.HealthCheckPath())
}

func TestApplicationSettingsOldLabelDecodesZeroProfile(t *testing.T) {
	label := `{"name":"chat","image":"ghcr.io/basecamp/once-campfire","host":"chat.localhost","env":null,"smtp":{},"resources":{},"autoUpdate":true,"backup":{},"disableTLS":false}`

	settings, err := UnmarshalApplicationSettings(label)
	require.NoError(t, err)

	assert.Equal(t, AppProfile{}, settings.Profile)
	assert.Equal(t, []string{"/storage", "/rails/storage"}, settings.Profile.MountTargets())
	assert.Equal(t, "/up", settings.Profile.HealthCheckPath())
}

func TestApplicationSettingsProfileLabelRoundTrip(t *testing.T) {
	settings := DefaultApplicationSettings()
	settings.Image = "vaultwarden/server"
	settings.Host = "vault.localhost"
	settings.Profile = AppProfile{
		BaseName:   "vaultwarden",
		DataPath:   "/data",
		HealthPath: "/alive",
		URLEnvVar:  "DOMAIN",
	}

	decoded, err := UnmarshalApplicationSettings(settings.Marshal())
	require.NoError(t, err)

	assert.Equal(t, settings.Profile, decoded.Profile)
	assert.True(t, settings.Equal(decoded))
}

func TestApplicationSettingsEqualComparesProfile(t *testing.T) {
	a := DefaultApplicationSettings()
	a.Image = "vaultwarden/server"
	b := a
	b.Profile.HealthPath = "/alive"

	assert.False(t, a.Equal(b))
}

func TestBuildEnvFillsURLEnvVar(t *testing.T) {
	settings := DefaultApplicationSettings()
	settings.Profile.URLEnvVar = "DOMAIN"

	env := settings.BuildEnv(ApplicationVolumeSettings{}, "https://vault.example.com")
	assert.Contains(t, env, "DOMAIN=https://vault.example.com")
}

func TestBuildEnvUserEnvWinsOverURLEnvVar(t *testing.T) {
	settings := DefaultApplicationSettings()
	settings.Profile.URLEnvVar = "DOMAIN"
	settings.EnvVars = map[string]string{"DOMAIN": "https://custom.example.com"}

	env := settings.BuildEnv(ApplicationVolumeSettings{}, "https://vault.example.com")

	// Docker resolves duplicate env keys last-wins, so the user's explicit
	// value must come after the Profile-computed one.
	computed := slices.Index(env, "DOMAIN=https://vault.example.com")
	explicit := slices.Index(env, "DOMAIN=https://custom.example.com")
	require.GreaterOrEqual(t, computed, 0)
	require.GreaterOrEqual(t, explicit, 0)
	assert.Greater(t, explicit, computed)
}

func TestBuildEnvSkipsEmptyURL(t *testing.T) {
	settings := DefaultApplicationSettings()
	settings.Profile.URLEnvVar = "DOMAIN"

	for _, e := range settings.BuildEnv(ApplicationVolumeSettings{}, "") {
		assert.False(t, strings.HasPrefix(e, "DOMAIN="), "unexpected %q", e)
	}
}

func TestApplicationSettingsValidateProfile(t *testing.T) {
	valid := func() ApplicationSettings {
		s := DefaultApplicationSettings()
		s.Image = "vaultwarden/server"
		return s
	}

	s := valid()
	s.Profile = AppProfile{DataPath: "/data", HealthPath: "/alive", URLEnvVar: "DOMAIN"}
	assert.NoError(t, s.Validate())

	s = valid()
	s.Profile.DataPath = "data"
	assert.ErrorIs(t, s.Validate(), ErrDataPathInvalid)

	s = valid()
	s.Profile.DataPath = "/"
	assert.ErrorIs(t, s.Validate(), ErrDataPathInvalid)

	s = valid()
	s.Profile.HealthPath = "alive"
	assert.ErrorIs(t, s.Validate(), ErrHealthPathInvalid)

	s = valid()
	s.Profile.URLEnvVar = "DOMAIN=x"
	assert.ErrorIs(t, s.Validate(), ErrURLEnvVarInvalid)
}
