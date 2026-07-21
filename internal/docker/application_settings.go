package docker

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
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

// removedSettingsFields are label fields older versions of Once wrote that no
// longer exist. They are shed on rewrite, unlike unknown fields from newer
// versions, which are preserved.
var removedSettingsFields = []string{"tailscaleExcluded"}

type ApplicationSettings struct {
	Name       string             `json:"name"`
	Image      string             `json:"image"`
	Host       string             `json:"host"`
	Exposure   Exposure           `json:"exposure,omitempty"`
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

	// extra preserves label fields written by a newer version of Once, so a
	// binary that predates a field doesn't destroy it when it rewrites the
	// label on a container roll (as older daemons once did to "exposure").
	extra map[string]json.RawMessage
}

func UnmarshalApplicationSettings(s string) (ApplicationSettings, error) {
	var settings ApplicationSettings
	err := json.Unmarshal([]byte(s), &settings)
	if err != nil {
		return settings, err
	}
	settings.extra = unknownFields([]byte(s), reflect.TypeOf(settings))
	for _, removed := range removedSettingsFields {
		delete(settings.extra, removed)
	}
	// Backfill apps deployed before the exposure field existed; the derived
	// value is persisted to the label the next time the container is recreated.
	if settings.Exposure == "" {
		settings.Exposure = ExposureForHost(settings.Host)
	}
	return settings, nil
}

func (s ApplicationSettings) Marshal() string {
	b, _ := json.Marshal(s)
	if len(s.extra) == 0 {
		return string(b)
	}
	var merged map[string]json.RawMessage
	if json.Unmarshal(b, &merged) != nil {
		return string(b)
	}
	for k, v := range s.extra {
		if _, known := merged[k]; !known {
			merged[k] = v
		}
	}
	b, _ = json.Marshal(merged)
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
	return s.Exposure == ExposurePublic && !s.DisableTLS
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
	if s.Exposure != other.Exposure {
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

// unknownFields returns the JSON object fields of data that no exported field
// of struct type t claims via its json tag - the fields a newer version of
// Once wrote that this binary doesn't model.
func unknownFields(data []byte, t reflect.Type) map[string]json.RawMessage {
	var all map[string]json.RawMessage
	if json.Unmarshal(data, &all) != nil {
		return nil
	}
	for _, f := range reflect.VisibleFields(t) {
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == "" {
			tag = f.Name
		}
		delete(all, tag)
	}
	if len(all) == 0 {
		return nil
	}
	return all
}

func funnelExpiryEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
