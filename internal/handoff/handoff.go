// Package handoff serves the one-time page that hands the Repo Password to a
// user's phone, so the phone's password manager can save it from a real form
// field. The QR code the terminal draws encodes this page's URL, never the
// secret itself: a URL is the one payload stock phone scanners act on, and no
// password manager ingests a raw-text QR.
//
// The page is served by the daemon's admin server, while the custody flow that
// creates the session runs in the CLI/TUI process. A small owner-only file is
// what the two processes share: the flow writes a session, the daemon reads it
// per request, and either the Done button or the flow's teardown ends it.
package handoff

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// TTL bounds a session even if nothing tears it down: the URL lands in the
// phone browser's history, and possibly in browser sync.
const TTL = 10 * time.Minute

// pathPrefix is deliberately terse. It is part of the QR payload, and payload
// length decides the symbol version, which decides whether the code fits the
// 24-row terminal budget.
const pathPrefix = "/r/"

// tokenLength is the encoded length of tokenBytes of entropy. 80 bits is far
// more than a single-use, 10-minute, network-bound token needs, and base32
// keeps it to 16 characters with no padding and no URL-escaping.
const (
	tokenBytes  = 10
	tokenLength = 16
)

var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// PagePath is the URL path the token is served at. Callers join it to the
// origin the phone can reach.
func PagePath(token string) string { return pathPrefix + token }

// Detail is one line of the Recovery Kit as the page shows it: the facts a
// fresh host needs beyond the password. Values are never credentials - the
// credential line carries names only.
type Detail struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Kit is what the page displays. Hostname names the credential in the phone's
// password manager, so a user with several servers can tell them apart.
type Kit struct {
	Password string   `json:"password"`
	Hostname string   `json:"hostname"`
	Details  []Detail `json:"details"`
}

// Store is the session shared between the process running the custody flow and
// the daemon serving the page.
type Store struct {
	path string
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

// Create replaces any existing session with a fresh one and returns its token.
// Only one handoff can be live at a time, which is what makes teardown simple:
// there is exactly one thing to clear.
func (s *Store) Create(kit Kit, ttl time.Duration) (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("minting handoff token: %w", err)
	}
	token := tokenEncoding.EncodeToString(raw)

	if err := s.write(session{
		Token:     token,
		Kit:       kit,
		ExpiresAt: time.Now().Add(ttl),
	}); err != nil {
		return "", err
	}
	return token, nil
}

// Clear ends the handoff. It is the teardown backstop for a screen the user
// closed without pressing Done, and an absent session is already the goal.
func (s *Store) Clear() error {
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clearing handoff session: %w", err)
	}
	return nil
}

// Private

type session struct {
	Token     string    `json:"token"`
	Kit       Kit       `json:"kit"`
	ExpiresAt time.Time `json:"expiresAt"`
	Burned    bool      `json:"burned"`
}

// lookup resolves a token from a request. The token is compared in constant
// time: the ServeMux pattern matches any token, so this comparison is the whole
// of the check, and a timing side channel here would leak the token a character
// at a time.
func (s *Store) lookup(token string) (session, bool) {
	current, err := s.read()
	if err != nil {
		return session{}, false
	}
	if subtle.ConstantTimeCompare([]byte(current.Token), []byte(token)) != 1 {
		return session{}, false
	}
	if current.Burned || time.Now().After(current.ExpiresAt) {
		return session{}, false
	}
	return current, true
}

// burn marks the session spent. It records rather than deletes so a second
// visit to a burned link is answered with 410 rather than looking like a link
// that never existed.
func (s *Store) burn(token string) error {
	current, ok := s.lookup(token)
	if !ok {
		return errors.New("no live handoff session")
	}
	current.Burned = true
	return s.write(current)
}

func (s *Store) read() (session, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return session{}, err
	}
	var current session
	if err := json.Unmarshal(data, &current); err != nil {
		return session{}, err
	}
	return current, nil
}

// write publishes the session atomically, so the daemon never reads a
// half-written file, and owner-only, so the password is no more exposed at rest
// than it already is on the Proxy label.
func (s *Store) write(current session) error {
	data, err := json.Marshal(current)
	if err != nil {
		return fmt.Errorf("encoding handoff session: %w", err)
	}

	temp, err := os.CreateTemp(filepath.Dir(s.path), ".handoff-*")
	if err != nil {
		return fmt.Errorf("creating handoff session: %w", err)
	}
	defer os.Remove(temp.Name())

	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("securing handoff session: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("writing handoff session: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("writing handoff session: %w", err)
	}
	if err := os.Rename(temp.Name(), s.path); err != nil {
		return fmt.Errorf("publishing handoff session: %w", err)
	}
	return nil
}
