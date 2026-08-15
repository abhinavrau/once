package docker

import (
	"crypto/rand"
	"encoding/base32"
)

// DefaultBackupLocalPath is where the local Backup Repo lives unless the user
// points it elsewhere (typically at a second disk).
const DefaultBackupLocalPath = "/var/lib/once/backups"

// BackupRepoSettings configures the install-wide Backup Repo. It rides the
// Proxy's once label as a top-level key (ADR-0014): unknownFields walks only
// the top level of the decoded type, so an older binary rewriting the proxy
// label carries the whole struct - password included - through labelExtra
// instead of destroying it.
//
// Enabled is explicit rather than inferred from LocalPath, so disabling
// backups does not mean erasing the configuration. A non-empty OffsiteURL is
// the offsite switch; no separate flag exists.
type BackupRepoSettings struct {
	Enabled    bool              `json:"enabled,omitempty"`
	LocalPath  string            `json:"localPath,omitempty"`
	OffsiteURL string            `json:"offsiteUrl,omitempty"`
	OffsiteEnv map[string]string `json:"offsiteEnv,omitempty"`
	Password   string            `json:"password,omitempty"`
}

// Configured reports whether a Backup Repo has been set up, whether or not
// backups are currently enabled.
func (s BackupRepoSettings) Configured() bool {
	return s.LocalPath != "" && s.Password != ""
}

// StagingDir is where Staging Files are written: a suffix on the repo path -
// same device as the repo the user chose, but never inside it, so nothing
// Once stages can be mistaken for repo content.
func (s BackupRepoSettings) StagingDir() string {
	return s.LocalPath + ".staging"
}

// GenerateRepoPassword returns the restic repository password: 32 base32
// characters (A-Z 2-7), 160 bits from crypto/rand. Generated at configure
// time - the one moment a human is present - shown once, and cached in the
// proxy label so unattended runs work. Lose it, lose the backups, by design.
func GenerateRepoPassword() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.EncodeToString(b), nil
}
