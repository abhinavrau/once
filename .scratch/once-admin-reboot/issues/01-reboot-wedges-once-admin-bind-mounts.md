Status: done

# A host reboot wedges once-admin and crash-loops the daemon's admin server

## Parent

- Observed live on a host after a reboot

## What's wrong

After a host reboot, `/var/run/once-admin.sock` and
`/var/run/once-admin-nginx.conf` were both empty, root-owned directories,
created at boot time (13:11:59). The background daemon then crash-looped:

```
ERROR msg="Admin socket server stopped; restarting" error="writing once-admin nginx config: open /var/run/once-admin-nginx.conf: is a directory"
```

and the once-admin container sat Exited (127).

## Root cause

`Admin.Boot` created once-admin with `RestartPolicy: always` and legacy
`HostConfig.Binds` of two host files: the daemon's socket and the nginx config
the daemon wrote. `/var/run` is tmpfs, so a reboot wipes both. dockerd restarts
once-admin before the daemon is up to recreate them, and a legacy bind
auto-creates a missing source as a directory. From then on the daemon can
neither write the config nor listen on the socket, and nothing removes the
directories.

A second, latent defect of the same design: a file bind pins the socket's
inode, so every daemon restart (which removes and re-creates the socket) left
once-admin proxying to a dead inode until it was recreated.

## Fix

- once-admin bind-mounts a dedicated socket directory,
  `/var/run/once-admin` -> `/run/once-admin` (ro), instead of individual files.
  The daemon listens on `/var/run/once-admin/admin.sock`, creating the
  directory if needed and never replacing it. A bind source dockerd auto-creates
  after a reboot is now exactly the directory the daemon wants, so once-admin
  starts first, answers 502, and serves as soon as the daemon's socket appears.
  Re-created sockets are visible through the directory mount.
  The legacy `Binds` form is kept deliberately: a `mount.TypeBind` Mount would
  refuse to start once-admin after a reboot instead.
- The nginx config is constant, so it no longer lives on the host: `create`
  copies it into the container's writable layer before start
  (`CopyToContainer`), where it survives restarts. nginx resolves the unix
  upstream per request, so startup ordering against the daemon doesn't matter.
- The daemon clears a stale socket, or an empty directory, at the socket path
  before listening (defensive: the new layout never makes one).

## Migration

- `Admin.Heal`, run by the daemon at startup (retried every 30s until it
  succeeds), recreates a once-admin whose `Binds` aren't the socket-directory
  mount, then removes the legacy `once-admin.sock` / `once-admin-nginx.conf`
  paths (files or empty dirs).
- `Admin.Boot` (Tailscale enable, recovery handoff) recreates an outdated
  once-admin the same way.
- A replacement once-admin is routed on the name its existing tailnet node
  holds, not refused as a deduped fresh join (which would delete it).

## Comments

Covered by `TestOnceAdminRecoversWhenTheDaemonComesUpAfterIt` (reboot replay:
daemon down, socket gone, once-admin restarted first, then daemon back) and
`TestOnceAdminHealMigratesLegacyFileBinds` (legacy container plus reboot
debris) in `integration/admin_test.go`, plus unit tests in
`internal/docker/admin_test.go` and `internal/background/admin_test.go`.
