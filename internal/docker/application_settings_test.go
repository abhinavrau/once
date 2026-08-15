package docker

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFunnelExpired(t *testing.T) {
	now := time.Now()

	assert.False(t, ApplicationSettings{}.FunnelExpired(now))

	future := now.Add(time.Minute)
	assert.False(t, ApplicationSettings{FunnelExpiresAt: &future}.FunnelExpired(now))

	past := now.Add(-time.Minute)
	assert.True(t, ApplicationSettings{FunnelExpiresAt: &past}.FunnelExpired(now))
}

func TestBuildEnvWithSMTP(t *testing.T) {
	settings := ApplicationSettings{
		SMTP: SMTPSettings{
			Server:   "smtp.example.com",
			Port:     "587",
			Username: "user@example.com",
			Password: "secret",
			From:     "noreply@example.com",
		},
	}

	env := settings.BuildEnv(ApplicationVolumeSettings{SecretKeyBase: "test-secret-key"}, "")

	assert.Contains(t, env, "SMTP_ADDRESS=smtp.example.com")
	assert.Contains(t, env, "SMTP_PORT=587")
	assert.Contains(t, env, "SMTP_USERNAME=user@example.com")
	assert.Contains(t, env, "SMTP_PASSWORD=secret")
	assert.Contains(t, env, "MAILER_FROM_ADDRESS=noreply@example.com")
}

func TestBuildEnvWithCPULimit(t *testing.T) {
	settings := ApplicationSettings{Resources: ContainerResources{CPUs: 4}}

	env := settings.BuildEnv(ApplicationVolumeSettings{SecretKeyBase: "test-secret-key"}, "")

	assert.Contains(t, env, "NUM_CPUS=4")
}

func TestBuildEnvWithoutCPULimit(t *testing.T) {
	settings := ApplicationSettings{}

	env := settings.BuildEnv(ApplicationVolumeSettings{SecretKeyBase: "test-secret-key"}, "")

	assert.NotContains(t, env, "NUM_CPUS=0")
}

func TestBuildEnvWithoutSMTP(t *testing.T) {
	settings := ApplicationSettings{}

	env := settings.BuildEnv(ApplicationVolumeSettings{SecretKeyBase: "test-secret-key"}, "")

	for _, e := range env {
		assert.NotContains(t, e, "SMTP_")
	}
}

func TestContainerResourcesEqualDiffers(t *testing.T) {
	base := ApplicationSettings{Name: "app", Resources: ContainerResources{CPUs: 1, MemoryMB: 512}}

	differentCPUs := ApplicationSettings{Name: "app", Resources: ContainerResources{CPUs: 2, MemoryMB: 512}}
	assert.False(t, base.Equal(differentCPUs))

	differentMemory := ApplicationSettings{Name: "app", Resources: ContainerResources{CPUs: 1, MemoryMB: 1024}}
	assert.False(t, base.Equal(differentMemory))

	zeroResources := ApplicationSettings{Name: "app"}
	assert.False(t, base.Equal(zeroResources))
}

func TestContainerResourcesMarshalRoundTrip(t *testing.T) {
	original := ApplicationSettings{
		Name:      "app",
		Image:     "img:latest",
		Exposure:  ExposureLocal,
		Resources: ContainerResources{CPUs: 2, MemoryMB: 512},
	}
	restored, err := UnmarshalApplicationSettings(original.Marshal())
	require.NoError(t, err)
	assert.Equal(t, 2, restored.Resources.CPUs)
	assert.Equal(t, 512, restored.Resources.MemoryMB)
	assert.True(t, original.Equal(restored))
}

func TestAutoUpdateEqualDiffers(t *testing.T) {
	base := ApplicationSettings{Name: "app", AutoUpdate: false}
	different := ApplicationSettings{Name: "app", AutoUpdate: true}
	assert.False(t, base.Equal(different))
}

func TestBackupSettingsEqualDiffers(t *testing.T) {
	base := ApplicationSettings{Name: "app", Backup: BackupSettings{AutoBackup: true}}

	differentAutoBackup := ApplicationSettings{Name: "app", Backup: BackupSettings{AutoBackup: false}}
	assert.False(t, base.Equal(differentAutoBackup))
}

func TestExposureBackfilledFromHostOnUnmarshal(t *testing.T) {
	local, err := UnmarshalApplicationSettings(`{"name":"app","host":"app.localhost"}`)
	require.NoError(t, err)
	assert.Equal(t, ExposureLocal, local.Exposure)

	public, err := UnmarshalApplicationSettings(`{"name":"app","host":"app.example.com"}`)
	require.NoError(t, err)
	assert.Equal(t, ExposurePublic, public.Exposure)

	// An explicitly stored exposure wins over the hostname shape.
	stored, err := UnmarshalApplicationSettings(`{"name":"app","host":"app.example.com","exposure":"local"}`)
	require.NoError(t, err)
	assert.Equal(t, ExposureLocal, stored.Exposure)
}

func TestExposureMarshalRoundTrip(t *testing.T) {
	original := ApplicationSettings{Name: "app", Image: "img:latest", Host: "app.localhost", Exposure: ExposureLocal}
	assert.Contains(t, original.Marshal(), `"exposure":"local"`)

	restored, err := UnmarshalApplicationSettings(original.Marshal())
	require.NoError(t, err)
	assert.Equal(t, ExposureLocal, restored.Exposure)
	assert.True(t, original.Equal(restored))
}

func TestMarshalPreservesUnknownFields(t *testing.T) {
	// A label written by a newer version of Once carries fields this binary's
	// struct doesn't have (as "exposure" once was to older binaries). Rolling
	// the container must write them back, not destroy them.
	label := `{"name":"app","image":"img:latest","host":"app.localhost","futureSetting":{"nested":true},"futureFlag":"on"}`

	settings, err := UnmarshalApplicationSettings(label)
	require.NoError(t, err)

	settings.AutoUpdate = true
	out := settings.Marshal()
	assert.Contains(t, out, `"futureSetting":{"nested":true}`)
	assert.Contains(t, out, `"futureFlag":"on"`)
	assert.Contains(t, out, `"autoUpdate":true`)

	// The preserved fields survive a second round trip unchanged.
	again, err := UnmarshalApplicationSettings(out)
	require.NoError(t, err)
	assert.Contains(t, again.Marshal(), `"futureSetting":{"nested":true}`)

	// Known fields never duplicate or leak into the preserved set.
	assert.Equal(t, 1, strings.Count(out, `"name"`))
}

func TestExposureEqualDiffers(t *testing.T) {
	base := ApplicationSettings{Name: "app", Exposure: ExposureLocal}
	assert.False(t, base.Equal(ApplicationSettings{Name: "app", Exposure: ExposurePublic}))
}

func TestTLSEnabledFollowsExposure(t *testing.T) {
	assert.False(t, ApplicationSettings{Host: "app.example.com", Exposure: ExposureLocal}.TLSEnabled())
	assert.True(t, ApplicationSettings{Host: "app.example.com", Exposure: ExposurePublic}.TLSEnabled())
	assert.False(t, ApplicationSettings{Host: "app.example.com", Exposure: ExposurePublic, DisableTLS: true}.TLSEnabled())
}

func TestUnmarshalIgnoresRemovedTailscaleExcluded(t *testing.T) {
	// Labels written before the exposure rework may still carry the removed
	// tailscaleExcluded boolean; exposure is the only reachability state now.
	restored, err := UnmarshalApplicationSettings(`{"name":"app","host":"app.localhost","tailscaleExcluded":true}`)
	require.NoError(t, err)
	assert.Equal(t, ExposureLocal, restored.Exposure)
	assert.NotContains(t, restored.Marshal(), "tailscaleExcluded")
}

func TestFunnelExpiresAtEqualDiffers(t *testing.T) {
	t1 := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)

	base := ApplicationSettings{Name: "app", FunnelExpiresAt: &t1}
	assert.True(t, base.Equal(ApplicationSettings{Name: "app", FunnelExpiresAt: &t1}))
	assert.False(t, base.Equal(ApplicationSettings{Name: "app", FunnelExpiresAt: &t2}))
	assert.False(t, base.Equal(ApplicationSettings{Name: "app"}))
	assert.True(t, ApplicationSettings{Name: "app"}.Equal(ApplicationSettings{Name: "app"}))
}

func TestBuildEnvWithVAPIDKeys(t *testing.T) {
	settings := ApplicationSettings{}

	vol := ApplicationVolumeSettings{
		SecretKeyBase:   "test-secret-key",
		VAPIDPublicKey:  "test-vapid-public",
		VAPIDPrivateKey: "test-vapid-private",
	}
	env := settings.BuildEnv(vol, "")

	assert.Contains(t, env, "VAPID_PUBLIC_KEY=test-vapid-public")
	assert.Contains(t, env, "VAPID_PRIVATE_KEY=test-vapid-private")
}

func TestBuildEnvWithEnvVars(t *testing.T) {
	settings := ApplicationSettings{
		EnvVars: map[string]string{
			"DB_HOST": "postgres.local",
			"DB_NAME": "mydb",
		},
	}

	env := settings.BuildEnv(ApplicationVolumeSettings{SecretKeyBase: "test-secret-key"}, "")

	assert.Contains(t, env, "DB_HOST=postgres.local")
	assert.Contains(t, env, "DB_NAME=mydb")
}

func TestEnvVarsMarshalRoundTrip(t *testing.T) {
	original := ApplicationSettings{
		Name:     "app",
		Image:    "img:latest",
		Exposure: ExposureLocal,
		EnvVars: map[string]string{
			"FOO": "bar",
			"BAZ": "qux",
		},
	}
	restored, err := UnmarshalApplicationSettings(original.Marshal())
	require.NoError(t, err)
	assert.Equal(t, "bar", restored.EnvVars["FOO"])
	assert.Equal(t, "qux", restored.EnvVars["BAZ"])
	assert.True(t, original.Equal(restored))
}

func TestEnvVarsEqualDiffers(t *testing.T) {
	base := ApplicationSettings{Name: "app", EnvVars: map[string]string{"A": "1"}}

	different := ApplicationSettings{Name: "app", EnvVars: map[string]string{"A": "2"}}
	assert.False(t, base.Equal(different))

	extra := ApplicationSettings{Name: "app", EnvVars: map[string]string{"A": "1", "B": "2"}}
	assert.False(t, base.Equal(extra))

	none := ApplicationSettings{Name: "app"}
	assert.False(t, base.Equal(none))
}

func TestAutoUpdateAndBackupMarshalRoundTrip(t *testing.T) {
	original := ApplicationSettings{
		Name:       "app",
		Image:      "img:latest",
		Exposure:   ExposureLocal,
		AutoUpdate: true,
		Backup:     BackupSettings{AutoBackup: true},
	}
	restored, err := UnmarshalApplicationSettings(original.Marshal())
	require.NoError(t, err)
	assert.True(t, restored.AutoUpdate)
	assert.True(t, restored.Backup.AutoBackup)
	assert.True(t, original.Equal(restored))
}
