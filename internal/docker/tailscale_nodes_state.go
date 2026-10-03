package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
)

// legacyNodesProvider is the provider every TSDProxy Once booted before
// ADR-0018 used, and so the directory on the data volume holding the tsnet
// state of every node it joined.
const legacyNodesProvider = "default"

// nodeStateClient is the slice of the Docker client linkLegacyNodeState needs.
type nodeStateClient interface {
	ContainerStatPath(ctx context.Context, containerID, path string) (container.PathStat, error)
	CopyToContainer(ctx context.Context, containerID, dstPath string, content io.Reader, options container.CopyToContainerOptions) error
}

// linkLegacyNodeState carries the nodes an install joined before ADR-0018
// over to its namespace's provider, which TSDProxy keeps under
// /data/<provider>/<node>: without them every node would join afresh, and
// Tailscale would dedupe its name to "<name>-1" next to its own old device.
// It runs against the created, not yet started, TSDProxy container (Docker
// mounts its volumes for the archive API), so nothing writes state meanwhile.
// The provider directory becomes a relative symlink to the legacy one rather
// than a copy or a move: TSDProxy's image has no shell to move with, and one
// symlink is created whole or not at all, so an interrupted boot cannot leave
// half the state behind. An existing provider directory means the link, or a
// fresh install's own state, is already there, so it is left alone.
func linkLegacyNodeState(ctx context.Context, client nodeStateClient, containerID, provider string) error {
	if provider == legacyNodesProvider {
		return nil
	}
	providerDir := path.Join(nodesDataVolumeTarget, provider)
	legacyDir := path.Join(nodesDataVolumeTarget, legacyNodesProvider)

	exists, err := statExists(ctx, client, containerID, providerDir)
	if err != nil || exists {
		return err
	}
	exists, err = statExists(ctx, client, containerID, legacyDir)
	if err != nil || !exists {
		return err
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	header := &tar.Header{Name: provider, Typeflag: tar.TypeSymlink, Linkname: legacyNodesProvider, Mode: 0o777}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("writing tar header: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar writer: %w", err)
	}
	if err := client.CopyToContainer(ctx, containerID, nodesDataVolumeTarget, &buf, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("linking %s to the tailnet node state in %s: %w", providerDir, legacyDir, err)
	}
	return nil
}

// Helpers

func statExists(ctx context.Context, client nodeStateClient, containerID, p string) (bool, error) {
	_, err := client.ContainerStatPath(ctx, containerID, p)
	switch {
	case err == nil:
		return true, nil
	case errdefs.IsNotFound(err):
		return false, nil
	default:
		return false, fmt.Errorf("checking %s in the tsdproxy data volume: %w", p, err)
	}
}
