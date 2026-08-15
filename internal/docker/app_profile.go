package docker

// AppProfile is the small set of per-app deployment facts Once cannot derive
// from the image: what the app is called, where it keeps its Application Data,
// what path it answers a health check on, and which environment variable it
// wants its own URL in. The zero value means the behaviour Once had before App
// Profiles existed, which is what keeps apps deployed before this field was
// added working without a backfill.
type AppProfile struct {
	BaseName   string `json:"baseName,omitempty"`
	DataPath   string `json:"dataPath,omitempty"`
	HealthPath string `json:"healthPath,omitempty"`
	URLEnvVar  string `json:"urlEnvVar,omitempty"`
}

// AppName resolves the base name apps are named (and hosts derived) from.
func (p AppProfile) AppName(imageRef string) string {
	if p.BaseName != "" {
		return p.BaseName
	}
	return NameFromImageRef(imageRef)
}

// MountTargets resolves where the Application Data Volume is mounted inside
// the container. A custom data path replaces both default targets; it never
// adds a third mount. The first entry is the backup copy source.
func (p AppProfile) MountTargets() []string {
	if p.DataPath != "" {
		return []string{p.DataPath}
	}
	return DefaultAppVolumeMountTargets
}

// HealthCheckPath resolves the path the app answers health checks on, named
// to match kamal-proxy's --health-check-path flag.
func (p AppProfile) HealthCheckPath() string {
	if p.HealthPath != "" {
		return p.HealthPath
	}
	return DefaultHealthCheckPath
}
