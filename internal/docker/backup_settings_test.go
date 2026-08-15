package docker

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateRepoPassword(t *testing.T) {
	p1, err := GenerateRepoPassword()
	require.NoError(t, err)
	p2, err := GenerateRepoPassword()
	require.NoError(t, err)

	// 32 chars of base32 (A-Z 2-7) is 160 bits from crypto/rand.
	assert.Regexp(t, regexp.MustCompile(`^[A-Z2-7]{32}$`), p1)
	assert.NotEqual(t, p1, p2)
}

func TestBackupRepoSettingsStagingDir(t *testing.T) {
	s := BackupRepoSettings{LocalPath: "/var/lib/once/backups"}
	// A suffix, never a child: nothing Once writes can be mistaken for repo
	// content, and init never sees a non-empty directory.
	assert.Equal(t, "/var/lib/once/backups.staging", s.StagingDir())
}

func TestProxySettingsBackupsLabelRoundTrip(t *testing.T) {
	settings := ProxySettings{
		HTTPPort:  80,
		HTTPSPort: 443,
		Backups: BackupRepoSettings{
			Enabled:   true,
			LocalPath: "/var/lib/once/backups",
			Password:  "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567",
		},
	}

	decoded, err := UnmarshalProxySettings(settings.Marshal())
	require.NoError(t, err)
	assert.Equal(t, settings.Backups, decoded.Backups)
}

func TestProxySettingsWithoutBackupsOmitsKey(t *testing.T) {
	settings := ProxySettings{HTTPPort: 80}
	assert.NotContains(t, settings.Marshal(), "backups")
}
