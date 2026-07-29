package command

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/once/internal/docker"
)

type settingsFlags struct {
	host         string
	expose       string
	disableTLS   bool
	env          []string
	smtpServer   string
	smtpPort     string
	smtpUsername string
	smtpPassword string
	smtpFrom     string
	cpus         int
	memory       int
	autoUpdate   bool
	backupPath   string
	autoBackup   bool
}

func (f *settingsFlags) register(cmd *cobra.Command) {
	defaults := docker.DefaultApplicationSettings()

	cmd.Flags().StringVar(&f.host, "host", defaults.Host, "hostname for the application")
	cmd.Flags().StringVar(&f.expose, "expose", string(defaults.Exposure), "where the application is reachable: local, tailnet, or public")
	cmd.Flags().BoolVar(&f.disableTLS, "disable-tls", defaults.DisableTLS, "disable TLS for this application")
	cmd.Flags().StringArrayVar(&f.env, "env", nil, "environment variable in KEY=VALUE format (can be repeated)")
	cmd.Flags().StringVar(&f.smtpServer, "smtp-server", defaults.SMTP.Server, "SMTP server address")
	cmd.Flags().StringVar(&f.smtpPort, "smtp-port", defaults.SMTP.Port, "SMTP server port")
	cmd.Flags().StringVar(&f.smtpUsername, "smtp-username", defaults.SMTP.Username, "SMTP username")
	cmd.Flags().StringVar(&f.smtpPassword, "smtp-password", defaults.SMTP.Password, "SMTP password")
	cmd.Flags().StringVar(&f.smtpFrom, "smtp-from", defaults.SMTP.From, "SMTP from address")
	cmd.Flags().IntVar(&f.cpus, "cpus", defaults.Resources.CPUs, "CPU limit for the container")
	cmd.Flags().IntVar(&f.memory, "memory", defaults.Resources.MemoryMB, "memory limit in MB for the container")
	cmd.Flags().BoolVar(&f.autoUpdate, "auto-update", defaults.AutoUpdate, "automatically update the application")
	cmd.Flags().StringVar(&f.backupPath, "backup-path", defaults.Backup.Path, "path for backups")
	cmd.Flags().BoolVar(&f.autoBackup, "auto-backup", defaults.Backup.AutoBackup, "enable automatic backups")

	// Removed flag, kept registered (hidden) so it fails with a clean-break
	// error naming its replacement instead of cobra's "unknown flag".
	cmd.Flags().Bool("tailscale", false, "")
	_ = cmd.Flags().MarkHidden("tailscale")
}

// checkRemoved fails a command that was invoked with a removed flag, naming
// the replacement.
func (f *settingsFlags) checkRemoved(cmd *cobra.Command) error {
	if cmd.Flags().Changed("tailscale") {
		return errors.New("the --tailscale flag has been removed: use --expose tailnet instead")
	}
	return nil
}

func (f *settingsFlags) buildSettings(image, host string, exposure docker.Exposure) (docker.ApplicationSettings, error) {
	envVars, err := f.parseEnvVars()
	if err != nil {
		return docker.ApplicationSettings{}, err
	}

	if f.backupPath != "" && !filepath.IsAbs(f.backupPath) {
		return docker.ApplicationSettings{}, docker.ErrBackupPathRelative
	}

	// Start from the defaults so fields without a flag keep their default value.
	s := docker.DefaultApplicationSettings()
	s.Image = image
	s.Host = host
	s.Exposure = exposure
	s.DisableTLS = f.disableTLS
	s.EnvVars = envVars
	s.SMTP = docker.SMTPSettings{
		Server:   f.smtpServer,
		Port:     f.smtpPort,
		Username: f.smtpUsername,
		Password: f.smtpPassword,
		From:     f.smtpFrom,
	}
	s.Resources = docker.ContainerResources{
		CPUs:     f.cpus,
		MemoryMB: f.memory,
	}
	s.AutoUpdate = f.autoUpdate
	s.Backup = docker.BackupSettings{
		Path:       f.backupPath,
		AutoBackup: f.autoBackup,
	}

	return s, nil
}

func (f *settingsFlags) applyChanges(cmd *cobra.Command, existing docker.ApplicationSettings, image string) (docker.ApplicationSettings, error) {
	s := existing

	s.Image = image

	if cmd.Flags().Changed("host") {
		s.Host = f.host
	}
	if cmd.Flags().Changed("disable-tls") {
		s.DisableTLS = f.disableTLS
	}
	if cmd.Flags().Changed("env") {
		envVars, err := f.parseEnvVars()
		if err != nil {
			return s, err
		}
		s.EnvVars = envVars
	}
	if cmd.Flags().Changed("smtp-server") {
		s.SMTP.Server = f.smtpServer
	}
	if cmd.Flags().Changed("smtp-port") {
		s.SMTP.Port = f.smtpPort
	}
	if cmd.Flags().Changed("smtp-username") {
		s.SMTP.Username = f.smtpUsername
	}
	if cmd.Flags().Changed("smtp-password") {
		s.SMTP.Password = f.smtpPassword
	}
	if cmd.Flags().Changed("smtp-from") {
		s.SMTP.From = f.smtpFrom
	}
	if cmd.Flags().Changed("cpus") {
		s.Resources.CPUs = f.cpus
	}
	if cmd.Flags().Changed("memory") {
		s.Resources.MemoryMB = f.memory
	}
	if cmd.Flags().Changed("auto-update") {
		s.AutoUpdate = f.autoUpdate
	}
	if cmd.Flags().Changed("expose") {
		exposure, err := docker.ParseExposure(f.expose)
		if err != nil {
			return s, err
		}
		// The old exposure's host shape doesn't carry over (e.g. app.localhost
		// is no tailnet service name); unless a host was given, re-derive it.
		if exposure != s.Exposure && !cmd.Flags().Changed("host") {
			s.Host = ""
		}
		s.Exposure = exposure
	}
	if cmd.Flags().Changed("backup-path") {
		if f.backupPath != "" && !filepath.IsAbs(f.backupPath) {
			return s, docker.ErrBackupPathRelative
		}
		s.Backup.Path = f.backupPath
	}
	if cmd.Flags().Changed("auto-backup") {
		s.Backup.AutoBackup = f.autoBackup
	}

	host, err := docker.ResolveHost(s.Exposure, s.Host, docker.NameFromImageRef(s.Image))
	if err != nil {
		return s, err
	}
	s.Host = host

	if err := s.Validate(); err != nil {
		return s, err
	}

	return s, nil
}

func (f *settingsFlags) parseEnvVars() (map[string]string, error) {
	if f.env == nil {
		return nil, nil
	}

	envVars := make(map[string]string, len(f.env))
	for _, e := range f.env {
		key, value, ok := strings.Cut(e, "=")
		if !ok {
			return nil, fmt.Errorf("invalid environment variable %q: must be in KEY=VALUE format", e)
		}
		if key == "" {
			return nil, fmt.Errorf("invalid environment variable %q: key must not be empty", e)
		}
		envVars[key] = value
	}

	return envVars, nil
}
