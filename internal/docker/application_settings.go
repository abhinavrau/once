package docker

import (
	"encoding/json"
	"strconv"
	"time"
)

type SMTPSettings struct {
	Server   string `json:"server,omitempty"`
	Port     string `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	From     string `json:"from,omitempty"`
}

func (s SMTPSettings) BuildEnv() []string {
	if s.Server == "" {
		return nil
	}
	return []string{
		"SMTP_ADDRESS=" + s.Server,
		"SMTP_PORT=" + s.Port,
		"SMTP_USERNAME=" + s.Username,
		"SMTP_PASSWORD=" + s.Password,
		"MAILER_FROM_ADDRESS=" + s.From,
	}
}

type ContainerResources struct {
	CPUs     int `json:"cpus,omitempty"`
	MemoryMB int `json:"memoryMB,omitempty"`
}

type BackupSettings struct {
	Path       string `json:"path,omitempty"`
	AutoBackup bool   `json:"autoBackup,omitempty"`
}

type ApplicationSettings struct {
	Name       string             `json:"name"`
	Image      string             `json:"image"`
	Host       string             `json:"host"`
	DisableTLS bool               `json:"disableTLS"`
	EnvVars    map[string]string  `json:"env"`
	SMTP       SMTPSettings       `json:"smtp"`
	Resources  ContainerResources `json:"resources"`
	AutoUpdate bool               `json:"autoUpdate"`
	Backup     BackupSettings     `json:"backup"`
	// FunnelExpiresAt, when set, marks the app as publicly exposed via Tailscale
	// Funnel until this time. Written atomically with the tailscale_funnel label
	// option in the same container recreation, so the two never drift apart.
	FunnelExpiresAt *time.Time `json:"funnelExpiresAt,omitempty"`
}

func UnmarshalApplicationSettings(s string) (ApplicationSettings, error) {
	var settings ApplicationSettings
	err := json.Unmarshal([]byte(s), &settings)
	return settings, err
}

func (s ApplicationSettings) Marshal() string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (s ApplicationSettings) Validate() error {
	if s.Image == "" {
		return ErrImageRequired
	}
	if s.Backup.AutoBackup && s.Backup.Path == "" {
		return ErrAutoBackupWithoutPath
	}
	return nil
}

func (s ApplicationSettings) TLSEnabled() bool {
	return s.Host != "" && !s.DisableTLS && !IsLocalhost(s.Host)
}

func (s ApplicationSettings) FunnelEnabled() bool {
	return s.FunnelExpiresAt != nil
}

// FunnelExpired reports whether a set Funnel expiry has passed, so the daemon
// can tear it down. False when no Funnel is active.
func (s ApplicationSettings) FunnelExpired(now time.Time) bool {
	return s.FunnelExpiresAt != nil && !s.FunnelExpiresAt.After(now)
}

func (s ApplicationSettings) Equal(other ApplicationSettings) bool {
	if s.Name != other.Name || s.Image != other.Image || s.Host != other.Host || s.DisableTLS != other.DisableTLS {
		return false
	}
	if s.Resources != other.Resources {
		return false
	}
	if s.SMTP != other.SMTP {
		return false
	}
	if s.AutoUpdate != other.AutoUpdate {
		return false
	}
	if s.Backup != other.Backup {
		return false
	}
	if !funnelExpiryEqual(s.FunnelExpiresAt, other.FunnelExpiresAt) {
		return false
	}
	if len(s.EnvVars) != len(other.EnvVars) {
		return false
	}
	for k, v := range s.EnvVars {
		if other.EnvVars[k] != v {
			return false
		}
	}
	return true
}

func (s ApplicationSettings) BuildEnv(vol ApplicationVolumeSettings) []string {
	env := []string{
		"SECRET_KEY_BASE=" + vol.SecretKeyBase,
		"VAPID_PUBLIC_KEY=" + vol.VAPIDPublicKey,
		"VAPID_PRIVATE_KEY=" + vol.VAPIDPrivateKey,
	}

	if !s.TLSEnabled() {
		env = append(env, "DISABLE_SSL=true")
	}

	if s.Resources.CPUs > 0 {
		env = append(env, "NUM_CPUS="+strconv.Itoa(s.Resources.CPUs))
	}

	env = append(env, s.SMTP.BuildEnv()...)

	for k, v := range s.EnvVars {
		env = append(env, k+"="+v)
	}

	return env
}

// Helpers

func funnelExpiryEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
