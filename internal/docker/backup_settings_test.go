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

func TestFormatAndNormalizeRepoPassword(t *testing.T) {
	password := "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	formatted := FormatRepoPassword(password)
	assert.Equal(t, "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567", formatted)

	// The gate strips what display formatting and phone keyboards introduce:
	// dashes, spaces, and case.
	assert.Equal(t, password, NormalizeRepoPassword(formatted))
	assert.Equal(t, password, NormalizeRepoPassword("  abcd-efgh ijkl MNOP qrst uvwx yz23 4567 "))
}

func TestBackupRepoSettingsStagingDir(t *testing.T) {
	s := BackupRepoSettings{LocalPath: "/var/lib/once/backups"}
	// A suffix, never a child: nothing Once writes can be mistaken for repo
	// content, and init never sees a non-empty directory.
	assert.Equal(t, "/var/lib/once/backups.staging", s.StagingDir())
}

// An offsite repository that is a filesystem path - a second disk, an NFS
// mount, or the second directory the suite uses in place of cloud credentials
// - has to be bind-mounted, so it is named here and nowhere else. A backend
// URL has no host directory.
func TestBackupRepoSettingsOffsiteDir(t *testing.T) {
	assert.Equal(t, "/mnt/usb/once", BackupRepoSettings{OffsiteURL: "/mnt/usb/once"}.offsiteDir())
	assert.Equal(t, "/mnt/usb/once", BackupRepoSettings{OffsiteURL: "local:/mnt/usb/once"}.offsiteDir())
	assert.Empty(t, BackupRepoSettings{OffsiteURL: "s3:s3.example.com/bucket/once"}.offsiteDir())
	assert.Empty(t, BackupRepoSettings{}.offsiteDir())
}

// restic reads a bare relative path as its local backend, so an unmounted one
// would be "backed up" into the restic container's writable layer and vanish
// with it - a backup that reports success and holds nothing. Refuse it at the
// one seam both frontends pass through.
func TestValidateOffsiteURL(t *testing.T) {
	for _, url := range []string{"", "/mnt/usb/once", "local:/mnt/usb/once", "s3:s3.example.com/bucket/once", "b2:bucket:once", "sftp:user@host:/srv/once", "rest:http://host:8000/"} {
		assert.NoError(t, ValidateOffsiteURL(url), url)
	}
	for _, url := range []string{"backups/offsite", "once", "local:relative/path", "./offsite"} {
		assert.ErrorIs(t, ValidateOffsiteURL(url), ErrOffsiteURLInvalid, url)
	}
}

// A repository named on the command line takes the same addresses, except
// that empty is a mistake there rather than the offsite leg's off switch.
func TestValidateRepoURL(t *testing.T) {
	for _, url := range []string{"/mnt/usb/once", "local:/mnt/usb/once", "s3:s3.example.com/bucket/once", "rest:http://host:8000/"} {
		assert.NoError(t, ValidateRepoURL(url), url)
	}
	for _, url := range []string{"", "./backups", "backups", "local:relative/path"} {
		assert.ErrorIs(t, ValidateRepoURL(url), ErrRepoURLInvalid, url)
	}
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

// olderProxySettings is Once as it was before install-wide backup settings
// existed: the same label, read and rewritten by a binary that does not model
// the key at all.
type olderProxySettings struct {
	labelExtra
	HTTPPort  int `json:"httpPort,omitempty"`
	HTTPSPort int `json:"httpsPort,omitempty"`
}

// partialProxySettings is the counterfactual ADR-0014 rejects: a binary that
// models the backups object but not everything inside it, which is what any
// nesting under a key an older binary already knows would look like.
type partialProxySettings struct {
	labelExtra
	HTTPPort int `json:"httpPort,omitempty"`
	Backups  struct {
		Enabled bool `json:"enabled,omitempty"`
	} `json:"backups,omitempty"`
}

// The whole reason the settings ride a top-level key (ADR-0014): an older
// binary rewriting the proxy label - a status read is enough to make it - must
// carry the Repo Password through rather than destroy the only copy of it.
func TestOlderBinaryRewritePreservesBackupSettings(t *testing.T) {
	current := ProxySettings{
		HTTPPort: 80,
		Backups: BackupRepoSettings{
			Enabled:    true,
			LocalPath:  "/var/lib/once/backups",
			OffsiteURL: "s3:s3.example.com/bucket/once",
			Password:   "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567",
		},
	}

	older, err := decodeLabel[olderProxySettings](current.Marshal())
	require.NoError(t, err)
	older.HTTPSPort = 8443

	decoded, err := UnmarshalProxySettings(encodeLabel[olderProxySettings](older))
	require.NoError(t, err)
	assert.Equal(t, current.Backups, decoded.Backups)
	assert.Equal(t, 8443, decoded.HTTPSPort, "the older binary's own edit still lands")

	// Nested one level deeper the carry cannot save it: unknown fields are
	// captured at the top level only, so anything inside a modelled object is
	// gone the moment an older binary writes the label back.
	partial, err := decodeLabel[partialProxySettings](current.Marshal())
	require.NoError(t, err)
	nested, err := UnmarshalProxySettings(encodeLabel[partialProxySettings](partial))
	require.NoError(t, err)
	assert.Empty(t, nested.Backups.Password)
}

func TestProxySettingsWithoutBackupsOmitsKey(t *testing.T) {
	settings := ProxySettings{HTTPPort: 80}
	assert.NotContains(t, settings.Marshal(), "backups")
}
