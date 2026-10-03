package background

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/userstats"
	"github.com/basecamp/once/internal/version"
)

const CheckInterval = 5 * time.Minute

// Bounds for restarting the admin socket server after it dies. A transient boot
// failure self-heals quickly; persistent failures back off so the daemon never
// busy-loops.
const (
	adminRestartMinBackoff = 250 * time.Millisecond
	adminRestartMaxBackoff = 30 * time.Second
)

type Runner struct {
	namespace string
}

func NewRunner(namespace string) *Runner {
	return &Runner{
		namespace: namespace,
	}
}

func (r *Runner) Run(ctx context.Context) error {
	slog.Info("Starting background runner", "namespace", r.namespace, "check_interval", CheckInterval,
		"version", version.Version, "self_update", selfUpdateEnabled(version.Version))

	scraper := userstats.NewScraper(r.namespace)
	go scraper.Run(ctx)

	go superviseAdminServer(ctx, func(ctx context.Context) error {
		return NewAdminServer().Run(ctx)
	})

	if ctx.Err() == nil {
		r.removeStrayContainersAtStart(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			slog.Info("Shutting down")
			return nil
		default:
		}

		timer := time.NewTimer(r.nextWake(ctx))
		select {
		case <-ctx.Done():
			timer.Stop()
			slog.Info("Shutting down")
			return nil
		case <-timer.C:
			if updated, err := r.check(ctx); err != nil {
				slog.Error("Check failed", "error", err)
			} else if updated {
				return nil
			}
		}
	}
}

// Private

func (r *Runner) check(ctx context.Context) (bool, error) {
	ns, err := docker.RestoreNamespace(ctx, r.namespace)
	if err != nil {
		return false, fmt.Errorf("restoring namespace: %w", err)
	}

	state, err := ns.LoadState(ctx)
	if err != nil {
		return false, fmt.Errorf("loading state: %w", err)
	}

	r.removeStrayContainers(ctx, ns)

	if r.checkSelfUpdate(ctx, ns, state) {
		return true, nil
	}

	// Backups run before auto-update: when both fall due on the same tick,
	// the newest snapshot must be of the known-good version, not of whatever
	// the new image did to the app.
	r.runBackups(ctx, ns, state)

	for _, app := range ns.Applications() {
		if !app.Running {
			continue
		}

		r.checkUpdate(ctx, app, state)
		r.checkFunnelExpiry(ctx, app)
	}

	return false, nil
}

// nextWake returns how long to sleep before the next check: the regular
// interval, shortened to the soonest pending Funnel expiry so a forgotten
// Funnel closes on time. Falls back to the regular interval if state can't be
// loaded — the next tick will retry. An already-passed expiry yields a zero
// wait, so a Funnel that expired while the daemon was down is torn down on the
// next check.
func (r *Runner) nextWake(ctx context.Context) time.Duration {
	ns, err := docker.RestoreNamespace(ctx, r.namespace)
	if err != nil {
		return CheckInterval
	}

	var expiries []time.Time
	for _, app := range ns.Applications() {
		if app.Running && app.Settings.FunnelExpiresAt != nil {
			expiries = append(expiries, *app.Settings.FunnelExpiresAt)
		}
	}

	return wakeInterval(time.Now(), expiries, CheckInterval)
}

// removeStrayContainersAtStart cleans up after a Deploy the previous daemon
// was killed in the middle of. Every check sweeps again, catching strays
// still too young to remove at start.
func (r *Runner) removeStrayContainersAtStart(ctx context.Context) {
	ns, err := docker.RestoreNamespace(ctx, r.namespace)
	if err != nil {
		slog.Error("Stray container sweep failed", "error", err)
		return
	}
	r.removeStrayContainers(ctx, ns)
}

func (r *Runner) removeStrayContainers(ctx context.Context, ns *docker.Namespace) {
	if err := ns.RemoveStrayContainers(ctx, time.Now()); err != nil {
		slog.Error("Stray container sweep failed", "error", err)
	}
}

func (r *Runner) checkFunnelExpiry(ctx context.Context, app *docker.Application) {
	if !app.Settings.FunnelExpired(time.Now()) {
		return
	}

	slog.Info("Closing expired funnel", "app", app.Settings.Name)

	if err := app.DisableFunnel(ctx); err != nil {
		slog.Error("Funnel teardown failed", "app", app.Settings.Name, "error", err)
	} else {
		slog.Info("Funnel closed", "app", app.Settings.Name)
	}
}

func (r *Runner) checkSelfUpdate(ctx context.Context, ns *docker.Namespace, state *docker.State) bool {
	if !selfUpdateEnabled(version.Version) {
		return false
	}

	if !state.SelfUpdateDue() {
		return false
	}

	slog.Info("Checking for once update")

	updated, err := version.NewUpdater().UpdateBinary()
	state.RecordSelfUpdate(err)
	if saveErr := ns.SaveState(ctx, state); saveErr != nil {
		slog.Error("Failed to save state after self-update check", "error", saveErr)
	}

	switch {
	case err != nil:
		slog.Error("Self-update failed", "error", err)
		return false
	case !updated:
		slog.Info("Once is up to date", "version", version.Version)
		return false
	default:
		slog.Info("Self-update complete, restarting")
		return true
	}
}

func (r *Runner) runBackups(ctx context.Context, ns *docker.Namespace, state *docker.State) {
	if err := ns.RunBackups(ctx, state); err != nil {
		slog.Error("Backup run failed", "error", err)
	}
}

func (r *Runner) checkUpdate(ctx context.Context, app *docker.Application, state *docker.State) {
	if !app.Settings.AutoUpdate {
		return
	}
	if !state.UpdateDue(app.Settings.Name) {
		return
	}

	slog.Info("Running auto-update", "app", app.Settings.Name)

	changed, err := app.Update(ctx, nil)
	if err != nil {
		slog.Error("Auto-update failed", "app", app.Settings.Name, "error", err)
	} else if changed {
		slog.Info("Auto-update completed", "app", app.Settings.Name)
	} else {
		slog.Info("Already up to date", "app", app.Settings.Name)
	}
}

// Helpers

// selfUpdateEnabled leaves locally-built binaries alone: replacing them with
// the upstream release would silently discard the developer's build.
func selfUpdateEnabled(currentVersion string) bool {
	return os.Getenv("ONCE_NO_SELF_UPDATE") == "" && version.IsRelease(currentVersion)
}

// superviseAdminServer runs the admin socket server, restarting it with bounded
// backoff whenever it returns while ctx is still live. Without this a transient
// failure (a stale socket, a listen race at boot) would silently kill the socket
// while the daemon stays "active", leaving enable's precondition to wrongly
// report the daemon down. Returns when ctx is cancelled.
func superviseAdminServer(ctx context.Context, run func(context.Context) error) {
	backoff := adminRestartMinBackoff
	for {
		start := time.Now()
		err := run(ctx)
		if ctx.Err() != nil {
			return
		}
		// A server that stayed up well past the cap was healthy, not boot-looping;
		// reset so an isolated later failure self-heals fast instead of inheriting
		// the backoff from old, unrelated failures.
		if time.Since(start) > adminRestartMaxBackoff {
			backoff = adminRestartMinBackoff
		}
		slog.Error("Admin socket server stopped; restarting", "error", err, "backoff", backoff)

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(backoff*2, adminRestartMaxBackoff)
	}
}

// wakeInterval returns interval shortened to the soonest future expiry, clamped
// to zero so a past expiry wakes the daemon immediately.
func wakeInterval(now time.Time, expiries []time.Time, interval time.Duration) time.Duration {
	wake := interval
	for _, e := range expiries {
		if d := e.Sub(now); d < wake {
			wake = d
		}
	}
	return max(wake, 0)
}
