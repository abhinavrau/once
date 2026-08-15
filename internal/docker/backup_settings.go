package docker

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"path/filepath"
	"strings"
)

// DefaultBackupLocalPath is where the local Backup Repo lives unless the user
// points it elsewhere (typically at a second disk).
const DefaultBackupLocalPath = "/var/lib/once/backups"

// The names per-repo results are recorded under - in State.LastCheck, and in
// the log lines and status output that report on one repo rather than one app.
const (
	RepoNameLocal   = "local"
	RepoNameOffsite = "offsite"
)

// ErrOffsiteURLInvalid rejects the offsite repositories restic would resolve
// to a path Once cannot bind-mount. Such a repo would be initialized inside
// the restic container and thrown away with it, while every command reported
// success - a backup that looks identical to a working one and holds nothing.
var ErrOffsiteURLInvalid = errors.New("offsite repository must be an absolute path or a restic backend URL such as s3:s3.amazonaws.com/bucket/once")

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

// Private

// offsiteDir is the host directory an offsite repository lives in when it is
// a filesystem path - a second disk or a network mount - rather than a
// backend URL. Empty for a URL, which restic reaches over the network and
// Once never touches.
func (s BackupRepoSettings) offsiteDir() string {
	return offsiteDirFromURL(s.OffsiteURL)
}

// ValidateOffsiteURL accepts an empty URL (the offsite leg is off), a
// filesystem path Once can mount, or anything naming a restic backend by
// scheme. See ErrOffsiteURLInvalid for what is left.
func ValidateOffsiteURL(url string) error {
	if url == "" || offsiteDirFromURL(url) != "" {
		return nil
	}
	if scheme, _, found := strings.Cut(url, ":"); found && scheme != "" && scheme != "local" && !strings.ContainsAny(scheme, `/\.`) {
		return nil
	}
	return ErrOffsiteURLInvalid
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

// FormatRepoPassword groups the password in 4s for reading and typing; the
// separators are cosmetic and NormalizeRepoPassword strips them on entry.
func FormatRepoPassword(p string) string {
	var groups []string
	for i := 0; i < len(p); i += 4 {
		groups = append(groups, p[i:min(i+4, len(p))])
	}
	return strings.Join(groups, "-")
}

// NormalizeRepoPassword reverses what display formatting and phone keyboards
// do to a typed-back password: case, dashes, and spaces never count.
func NormalizeRepoPassword(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.NewReplacer("-", "", " ", "").Replace(s)
}

// Helpers

// offsiteDirFromURL returns the host directory behind a filesystem-path
// offsite repository, honouring restic's optional local: prefix, and "" for
// anything reached over the network.
func offsiteDirFromURL(url string) string {
	path := strings.TrimPrefix(url, "local:")
	if filepath.IsAbs(path) {
		return path
	}
	return ""
}
