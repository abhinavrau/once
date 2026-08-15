package docker

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBackupReadsPlainTar(t *testing.T) {
	var buf bytes.Buffer
	writeTestBackupArchive(t, &buf)

	assertParsesTestBackup(t, &buf)
}

func TestParseBackupReadsGzippedTar(t *testing.T) {
	// Archives written before the gzip drop exist on users' disks; without the
	// magic-byte sniff every one of them becomes unrestorable on upgrade.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	writeTestBackupArchive(t, gw)
	require.NoError(t, gw.Close())

	assertParsesTestBackup(t, &buf)
}

func TestParseBackupRejectsGarbage(t *testing.T) {
	n := &Namespace{}
	_, _, _, err := n.parseBackup(bytes.NewReader([]byte("not an archive")))
	assert.ErrorIs(t, err, ErrInvalidBackup)
}

// Helpers

func writeTestBackupArchive(t *testing.T, w io.Writer) {
	t.Helper()

	settings := DefaultApplicationSettings()
	settings.Name = "vault"
	settings.Image = "vaultwarden/server"
	settings.Host = "vault.localhost"
	settings.Profile = AppProfile{BaseName: "vaultwarden", DataPath: "/data", HealthPath: "/alive", URLEnvVar: "DOMAIN"}

	vol := ApplicationVolumeSettings{SecretKeyBase: "skb"}

	tw := tar.NewWriter(w)
	require.NoError(t, writeTarEntry(tw, backupAppSettingsEntry, []byte(settings.Marshal())))
	require.NoError(t, writeTarEntry(tw, backupVolSettingsEntry, []byte(vol.Marshal())))
	require.NoError(t, writeTarEntry(tw, "data/file.txt", []byte("hello")))
	require.NoError(t, tw.Close())
}

func assertParsesTestBackup(t *testing.T, r io.Reader) {
	t.Helper()

	n := &Namespace{}
	appSettings, volSettings, volumeData, err := n.parseBackup(r)
	require.NoError(t, err)

	assert.Equal(t, "vault", appSettings.Name)
	assert.Equal(t, "vaultwarden/server", appSettings.Image)
	// The Profile rides in once.application.json, so it survives the round trip.
	assert.Equal(t, AppProfile{BaseName: "vaultwarden", DataPath: "/data", HealthPath: "/alive", URLEnvVar: "DOMAIN"}, appSettings.Profile)
	assert.Equal(t, "skb", volSettings.SecretKeyBase)
	assert.NotEmpty(t, volumeData)
}
