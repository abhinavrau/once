package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/pkg/stdcopy"
)

// ResticImage is pinned by digest, not tag alone: a silent upstream retag
// must not change restic's behaviour under a repo Once cannot re-create.
const ResticImage = "restic/restic:0.19.1@sha256:136600b6ff6843d61d355f7f71f460a166429f35de6fd11b568fece3c9a4d510"

const (
	resticStagingMount = "/staging"
	resticRepoMount    = "/repo"
	resticOffsiteMount = "/offsite"
	resticCacheMount   = "/root/.cache/restic"
)

// The tag prefixes every Backup Run snapshot carries. restic keeps no other
// per-snapshot metadata, and a bare-metal restore has to read the app name and
// the image ref before it has an archive to open - so they travel as tags.
// Tags play no part in retention: forget groups by host,paths.
const (
	snapshotAppTag   = "app:"
	snapshotImageTag = "image:"
)

// restic exit codes the engine drives on (restic >= 0.17).
const (
	resticExitNoRepository  = 10
	resticExitLockFailure   = 11
	resticExitWrongPassword = 12
)

// resticRepo addresses one restic repository for a single command.
type resticRepo struct {
	// name is RepoNameLocal or RepoNameOffsite: what results are recorded
	// under, and what status lines and per-repo errors call this repo. Never
	// the URL - that can carry a credential.
	name string
	// repository is RESTIC_REPOSITORY as seen inside the container.
	repository string
	// displayName is the repository as the user configured it. It differs
	// from repository whenever the repo is bind-mounted, and it is what logs
	// and errors must name: "/offsite" means nothing to the person reading.
	displayName string
	password    string
	// env carries backend credentials, splatted into the container
	// environment - secrets travel by environment, never argv (ADR-0013).
	env map[string]string
	// localRepoDir, when set, is a host directory bind-mounted read-write at
	// /repo: the local Backup Repo. It rides along on offsite commands too,
	// so init can copy its chunker params.
	localRepoDir string
	// offsiteRepoDir, when set, is a host directory bind-mounted read-write
	// at /offsite: an offsite repository that is a filesystem path - a second
	// disk or a network mount - rather than a backend URL.
	offsiteRepoDir string
	// stagingDir is the host Staging Directory, bind-mounted at /staging:
	// Once writes the archive, restic only reads it.
	stagingDir string
	// stagingWritable makes that mount read-write. Only a restore needs it -
	// it is restic that writes the archive back out - and backing up under a
	// read-only mount is the guarantee that a backup can never alter the file
	// it is preserving.
	stagingWritable bool
	// initArgs overrides the plain "init" ensureRepo runs when the repo does
	// not exist yet; empty means []string{"init"}.
	initArgs []string
}

type resticResult struct {
	exitCode int
	stdout   string
	stderr   string
}

// failure summarizes a failed command for error messages: the exit code and
// the tail of stderr, which is where restic explains itself.
func (r resticResult) failure() string {
	msg := strings.TrimSpace(r.stderr)
	if len(msg) > 300 {
		msg = msg[len(msg)-300:]
	}
	if msg == "" {
		msg = strings.TrimSpace(r.stdout)
	}
	return fmt.Sprintf("exit code %d: %s", r.exitCode, msg)
}

type resticSummary struct {
	MessageType         string `json:"message_type"`
	SnapshotID          string `json:"snapshot_id"`
	TotalBytesProcessed int64  `json:"total_bytes_processed"`
	NumErrors           int    `json:"num_errors"`
	SuggestRepairIndex  bool   `json:"suggest_repair_index"`
	SuggestPrune        bool   `json:"suggest_prune"`
}

// LatestOffsiteSnapshot reports when the app's newest snapshot landed in the
// Offsite Repo. This is a query and never state: State is a cache that dies
// with the install - and a rebuilt host reading a repo full of snapshots is
// exactly the case this signal exists for, so a state-backed answer would
// announce "never backed up" to the user who needs it most. A zero time means
// no offsite copy of this app exists yet - including when no offsite
// repository is configured at all.
func (n *Namespace) LatestOffsiteSnapshot(ctx context.Context, appName string) (time.Time, error) {
	cfg := n.BackupRepoSettings()
	if cfg.OffsiteURL == "" {
		return time.Time{}, nil
	}
	return n.latestSnapshot(ctx, offsiteResticRepo(cfg), appName)
}

// Private

// runRestic runs one restic command in its own container: create, start,
// stream logs, wait, remove. One container per command keeps the exit code on
// the ContainerWait seam (the whole init protocol is exit-code driven) and
// lets each command address a different repo with its own environment.
//
// onLine, when set, sees each stdout line as it arrives rather than after the
// container exits - which is what makes an upload's `--json` status stream
// renderable while a person is still waiting on it. nil is the normal case.
func (n *Namespace) runRestic(ctx context.Context, repo resticRepo, onLine func(string), args ...string) (resticResult, error) {
	if err := n.ensureResticImage(ctx); err != nil {
		return resticResult{}, err
	}

	id, err := ContainerRandomID()
	if err != nil {
		return resticResult{}, fmt.Errorf("generating container id: %w", err)
	}
	containerName := fmt.Sprintf("%s-restic-%s", n.name, id)

	env := []string{
		"RESTIC_REPOSITORY=" + repo.repository,
		"RESTIC_PASSWORD=" + repo.password,
	}
	for k, v := range repo.env {
		env = append(env, k+"="+v)
	}

	mounts := []mount.Mount{
		{Type: mount.TypeVolume, Source: n.resticCacheVolume(), Target: resticCacheMount},
	}
	// Only commands that carry an archive mount the Staging Directory. A
	// question about a repo must not require one to exist: on a fresh host
	// nothing has created it, and Docker refuses a missing bind source with an
	// error naming a path the user never typed.
	if repo.stagingDir != "" {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: repo.stagingDir, Target: resticStagingMount, ReadOnly: !repo.stagingWritable})
	}
	if repo.localRepoDir != "" {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: repo.localRepoDir, Target: resticRepoMount})
	}
	if repo.offsiteRepoDir != "" {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: repo.offsiteRepoDir, Target: resticOffsiteMount})
	}

	resp, err := n.client.ContainerCreate(ctx,
		&container.Config{
			Image: ResticImage,
			Cmd:   args,
			Env:   env,
			// Root, permanently: a repo created by root is root-owned 0700,
			// so a later non-root run would fail - a one-way door.
			User: "0:0",
		},
		&container.HostConfig{
			Mounts:    mounts,
			LogConfig: ContainerLogConfig(),
		},
		nil, nil, containerName,
	)
	if err != nil {
		return resticResult{}, fmt.Errorf("creating restic container: %w", err)
	}
	defer func() {
		removeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := n.client.ContainerRemove(removeCtx, resp.ID, container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
			slog.Error("Failed to remove restic container", "container", containerName, "error", err)
		}
	}()

	if err := n.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return resticResult{}, fmt.Errorf("starting restic container: %w", err)
	}

	// Following the logs first is what makes them live; the stream ends when
	// the container exits, so the wait below returns immediately after and the
	// exit code still comes off ContainerWait.
	stdout, stderr, err := n.streamContainerLogs(ctx, resp.ID, onLine)
	if err != nil {
		return resticResult{}, err
	}

	waitCh, errCh := n.client.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	var exitCode int
	select {
	case status := <-waitCh:
		exitCode = int(status.StatusCode)
	case err := <-errCh:
		return resticResult{}, fmt.Errorf("waiting for restic: %w", err)
	}

	return resticResult{exitCode: exitCode, stdout: stdout, stderr: stderr}, nil
}

// runResticRetryLock wraps runRestic with the evidence-triggered stale-lock
// recovery: on a lock failure, unlock --remove-all against that repo, log at
// warn, and retry the command once. Never scheduled or unconditional - a
// shared offsite repo may hold another host's live lock.
func (n *Namespace) runResticRetryLock(ctx context.Context, repo resticRepo, onLine func(string), args ...string) (resticResult, error) {
	return retryOnStaleLock(func(args ...string) (resticResult, error) {
		// The unlock leg stays silent: onLine is for the command's own
		// progress, not recovery chatter.
		stream := onLine
		if len(args) > 0 && args[0] == "unlock" {
			stream = nil
		}
		return n.runRestic(ctx, repo, stream, args...)
	}, repo.displayName, args...)
}

// ensureRepo probes the repo with cat config and initializes it on exit 10.
// init's exit 1 is never treated as "already exists" - it also covers
// permission and backend errors.
//
// Both steps are network round trips against a backend, and both can take
// minutes on a fresh remote repo, so each announces itself: with no output of
// their own to stream, a status line is the only progress they can offer.
func (n *Namespace) ensureRepo(ctx context.Context, repo resticRepo, progress DeployProgressCallback) error {
	reportWaiting(progress, "checking "+repo.name)
	res, err := n.runResticRetryLock(ctx, repo, nil, "cat", "config")
	if err != nil {
		return err
	}

	switch res.exitCode {
	case 0:
		return nil
	case resticExitNoRepository:
		initArgs := repo.initArgs
		if len(initArgs) == 0 {
			initArgs = []string{"init"}
		}
		reportWaiting(progress, "creating "+repo.name)
		initRes, err := n.runRestic(ctx, repo, nil, initArgs...)
		if err != nil {
			return err
		}
		if initRes.exitCode != 0 {
			return fmt.Errorf("restic init failed: %s", initRes.failure())
		}
		return nil
	case resticExitWrongPassword:
		return fmt.Errorf("wrong password for restic repository %s - is it aimed at a repository Once did not create?", repo.displayName)
	default:
		return fmt.Errorf("restic cat config failed: %s", res.failure())
	}
}

// latestSnapshot runs the staleness query. Deliberately not
// runResticRetryLock: the query takes no lock, so it cannot fail on one, and
// asking a question must never be a reason to remove another host's lock from
// a shared repo.
func (n *Namespace) latestSnapshot(ctx context.Context, repo resticRepo, appName string) (time.Time, error) {
	res, err := n.runRestic(ctx, repo, nil, resticLatestSnapshotArgs(n.name, appName)...)
	if err != nil {
		return time.Time{}, err
	}
	if res.exitCode != 0 {
		return time.Time{}, fmt.Errorf("restic snapshots failed: %s", res.failure())
	}
	return parseLatestSnapshotTime(res.stdout)
}

func (n *Namespace) resticCacheVolume() string {
	return n.name + "-restic-cache"
}

func (n *Namespace) removeResticCacheVolume(ctx context.Context) error {
	if err := n.client.VolumeRemove(ctx, n.resticCacheVolume(), true); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("removing restic cache volume: %w", err)
	}
	return nil
}

func (n *Namespace) ensureResticImage(ctx context.Context) error {
	if _, err := n.client.ImageInspect(ctx, ResticImage); err == nil {
		return nil
	}
	reader, err := n.client.ImagePull(ctx, ResticImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling restic image: %w", err)
	}
	defer reader.Close()
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

// streamContainerLogs follows the container's output until it exits,
// accumulating both streams and, when onLine is set, handing each stdout line
// to it as it arrives.
func (n *Namespace) streamContainerLogs(ctx context.Context, containerID string, onLine func(string)) (string, string, error) {
	logs, err := n.client.ContainerLogs(ctx, containerID, container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true})
	if err != nil {
		return "", "", fmt.Errorf("reading restic logs: %w", err)
	}
	defer logs.Close()

	var stdout, stderr strings.Builder
	var out io.Writer = &stdout
	if onLine != nil {
		out = io.MultiWriter(&stdout, &lineWriter{fn: onLine})
	}
	if _, err := stdcopy.StdCopy(out, &stderr, logs); err != nil {
		return "", "", fmt.Errorf("demuxing restic logs: %w", err)
	}
	return stdout.String(), stderr.String(), nil
}

// Helpers

// reportWaiting announces a step with no output of its own. nil is the
// legitimate headless adapter (ADR-0012) and stays free.
func reportWaiting(progress DeployProgressCallback, status string) {
	if progress != nil {
		progress(DeployProgress{Stage: DeployStageWaiting, Status: status})
	}
}

// retryOnStaleLock is the stale-lock policy, separated from the container that
// carries it out: run the command, and only on the evidence of a lock failure
// clear the repo's locks and run it once more. The recovery is never scheduled
// and never unconditional, because a shared offsite repo may hold another
// host's live lock - so "once, after a lock error" is the whole of the rule.
func retryOnStaleLock(run func(args ...string) (resticResult, error), repoName string, args ...string) (resticResult, error) {
	res, err := run(args...)
	if err != nil || res.exitCode != resticExitLockFailure {
		return res, err
	}

	slog.Warn("restic repository is locked; removing stale locks and retrying once", "repository", repoName)
	if unlockRes, unlockErr := run("unlock", "--remove-all"); unlockErr != nil {
		return res, unlockErr
	} else if unlockRes.exitCode != 0 {
		return res, fmt.Errorf("restic unlock failed: %s", unlockRes.failure())
	}

	return run(args...)
}

// configuredResticRepos lists the repos a Backup Run addresses, in order. A
// non-empty OffsiteURL is the offsite switch; there is no separate flag.
func configuredResticRepos(cfg BackupRepoSettings) []resticRepo {
	repos := []resticRepo{localResticRepo(cfg)}
	if cfg.OffsiteURL != "" {
		repos = append(repos, offsiteResticRepo(cfg))
	}
	return repos
}

// localResticRepo addresses the local Backup Repo on disk.
func localResticRepo(cfg BackupRepoSettings) resticRepo {
	return resticRepo{
		name:         RepoNameLocal,
		repository:   resticRepoMount,
		displayName:  cfg.LocalPath,
		password:     cfg.Password,
		localRepoDir: cfg.LocalPath,
		stagingDir:   cfg.StagingDir(),
	}
}

// offsiteResticRepo addresses the Offsite Repo by URL, with the backend
// credentials splatted into the environment. The local repo rides along
// mounted at /repo, and the RESTIC_FROM_* variables point at it, so that
// init runs with --copy-chunker-params: matching chunker params keep a later
// `copy` a one-shot backfill instead of a full re-chunk. Both are inert on
// every other command.
func offsiteResticRepo(cfg BackupRepoSettings) resticRepo {
	env := make(map[string]string, len(cfg.OffsiteEnv)+2)
	maps.Copy(env, cfg.OffsiteEnv)
	env["RESTIC_FROM_REPOSITORY"] = resticRepoMount
	env["RESTIC_FROM_PASSWORD"] = cfg.Password
	repo := resticRepo{
		name:         RepoNameOffsite,
		repository:   cfg.OffsiteURL,
		displayName:  cfg.OffsiteURL,
		password:     cfg.Password,
		env:          env,
		localRepoDir: cfg.LocalPath,
		stagingDir:   cfg.StagingDir(),
		initArgs:     []string{"init", "--copy-chunker-params"},
	}
	// A filesystem-path offsite repository - a second disk, a network mount -
	// is bind-mounted and addressed by its in-container path. Left unmounted,
	// restic would init it inside the container and lose it on removal.
	if dir := cfg.offsiteDir(); dir != "" {
		repo.offsiteRepoDir = dir
		repo.repository = resticOffsiteMount
	}
	return repo
}

// resticProbeBackupArgs builds the validation probe's backup invocation: the
// real --host and a fixed path, like the real run, so repeated failed probes
// land in one host,paths retention group.
func resticProbeBackupArgs(namespace string) []string {
	return []string{
		"backup", "--json",
		"--host", namespace,
		resticStagingMount + "/" + backupProbeFile,
	}
}

// resticBackupArgs builds the backup invocation. Both --host and the staging
// path are constants of the namespace and app name: forget groups by
// host,paths, so any variation would mint a new retention group per run and
// retention would silently keep everything forever. Tags carry what a restore
// must read before it has an archive to open, and are not part of the
// grouping. The app tag is also what the staleness query
// (`snapshots --latest 1 --tag app:<name>`) selects on.
func resticBackupArgs(namespace, appName, image string) []string {
	args := []string{
		"backup", "--json",
		"--host", namespace,
		"--tag", snapshotAppTag + appName,
	}
	if image != "" {
		args = append(args, "--tag", snapshotImageTag+image)
	}
	return append(args, resticStagingMount+"/"+appName+".tar")
}

// resticRepoSnapshotsArgs asks a repo for everything it holds - the discovery
// listing. --no-lock keeps it a pure question: a repo shared with another host
// must not gain a lock because somebody listed it.
func resticRepoSnapshotsArgs() []string {
	return []string{"snapshots", "--json", "--no-lock"}
}

// resticRestoreArgs restores the Staging Directory subfolder of a snapshot
// into the Staging Directory: the snapshot holds exactly one file, the app's
// archive, at the fixed in-container path every Backup Run writes.
func resticRestoreArgs(snapshotID string) []string {
	return []string{"restore", snapshotID + ":" + resticStagingMount, "--target", resticStagingMount}
}

// resticLatestSnapshotArgs asks a repo for one app's newest snapshot - the
// staleness signal. It stays a pure question: --no-lock writes nothing at all
// to a repo that may be shared with another host, and --host scopes the
// answer to this install, which is what keeps two installs sharing one
// offsite repo from reading each other's freshness.
func resticLatestSnapshotArgs(namespace, appName string) []string {
	return []string{
		"snapshots", "--json", "--no-lock",
		"--latest", "1",
		"--host", namespace,
		"--tag", snapshotAppTag + appName,
	}
}

// resticForgetArgs applies the fixed retention policy in one untagged forget:
// the per-app staging paths put each app in its own group automatically.
func resticForgetArgs(namespace string) []string {
	return []string{
		"forget", "--prune", "--host", namespace,
		"--keep-daily", "7", "--keep-weekly", "4", "--keep-monthly", "6",
	}
}

// resticCheckArgs verifies repo metadata only - no --read-data, which would
// be unbounded against a large repo.
func resticCheckArgs() []string {
	return []string{"check", "--json"}
}

// backupDiskCheck fails when the staging device holds less than twice the
// archive it is about to hold - the app's last one when backing up, the
// snapshot's own when restoring. A zero baseline (a newly participating app, a
// snapshot from a restic too old to record its size) skips the check: there is
// nothing to size against.
func backupDiskCheck(freeBytes uint64, archiveBytes int64) error {
	if archiveBytes <= 0 {
		return nil
	}
	if freeBytes < 2*uint64(archiveBytes) {
		return fmt.Errorf("not enough free disk in the staging directory: %d bytes free, need %d (2x the archive)", freeBytes, 2*archiveBytes)
	}
	return nil
}

// parseResticSummary streams restic's --json output and returns the last
// summary line - ~10 status lines a second precede it.
func parseResticSummary(output string) (resticSummary, bool) {
	var summary resticSummary
	found := false
	for line := range strings.Lines(output) {
		var candidate resticSummary
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &candidate); err != nil {
			continue
		}
		if candidate.MessageType == "summary" {
			summary = candidate
			found = true
		}
	}
	return summary, found
}

// parseLatestSnapshotTime reads the newest snapshot time out of `restic
// snapshots --json`. An empty list is not an error: it means no copy of this
// app has landed in the repo yet.
func parseLatestSnapshotTime(output string) (time.Time, error) {
	var snapshots []struct {
		Time time.Time `json:"time"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &snapshots); err != nil {
		return time.Time{}, fmt.Errorf("parsing restic snapshots: %w", err)
	}

	var latest time.Time
	for _, snapshot := range snapshots {
		if snapshot.Time.After(latest) {
			latest = snapshot.Time
		}
	}
	return latest, nil
}

// resticCheckError derives the check outcome: check has no distinct exit code
// for corruption, so the summary's num_errors and repair suggestions decide.
func resticCheckError(exitCode int, stdout string) error {
	summary, ok := parseResticSummary(stdout)
	if exitCode != 0 {
		return fmt.Errorf("restic check failed with exit code %d", exitCode)
	}
	if !ok {
		return nil
	}
	if summary.NumErrors > 0 || summary.SuggestRepairIndex || summary.SuggestPrune {
		return fmt.Errorf("restic check found problems: %d errors (repair index: %t, prune: %t)", summary.NumErrors, summary.SuggestRepairIndex, summary.SuggestPrune)
	}
	return nil
}
