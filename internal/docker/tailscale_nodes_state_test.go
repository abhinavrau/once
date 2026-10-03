package docker

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLinkLegacyNodeStateLinksTheProviderDirToDefault(t *testing.T) {
	client := newFakeStateClient("/data/default")

	require.NoError(t, linkLegacyNodeState(context.Background(), client, "tsdproxy-id", "once"))

	require.Len(t, client.copies, 1)
	assert.Equal(t, "tsdproxy-id", client.copies[0].containerID)
	assert.Equal(t, "/data", client.copies[0].dstPath)
	require.Len(t, client.copies[0].entries, 1)
	entry := client.copies[0].entries[0]
	assert.Equal(t, "once", entry.Name)
	assert.Equal(t, byte(tar.TypeSymlink), entry.Typeflag)
	assert.Equal(t, "default", entry.Linkname, "relative, so it resolves inside the volume wherever it is mounted")
}

func TestLinkLegacyNodeStateLeavesAnExistingProviderDirAlone(t *testing.T) {
	client := newFakeStateClient("/data/default", "/data/once")

	require.NoError(t, linkLegacyNodeState(context.Background(), client, "tsdproxy-id", "once"))

	assert.Empty(t, client.copies)
}

func TestLinkLegacyNodeStateHasNothingToLinkOnAFreshVolume(t *testing.T) {
	client := newFakeStateClient()

	require.NoError(t, linkLegacyNodeState(context.Background(), client, "tsdproxy-id", "once"))

	assert.Empty(t, client.copies)
}

// TestLinkLegacyNodeStateNeverLinksDefaultToItself covers a namespace named
// "default", whose provider already is the legacy state directory.
func TestLinkLegacyNodeStateNeverLinksDefaultToItself(t *testing.T) {
	client := newFakeStateClient("/data/default")

	require.NoError(t, linkLegacyNodeState(context.Background(), client, "tsdproxy-id", "default"))

	assert.Empty(t, client.copies)
}

// TestLinkLegacyNodeStateRefusesToGuessOnAnUnreadableVolume: starting TSDProxy
// without the link would re-join every node under a deduped "-1" name, so a
// stat that fails for any reason but absence stops the boot instead.
func TestLinkLegacyNodeStateRefusesToGuessOnAnUnreadableVolume(t *testing.T) {
	client := newFakeStateClient("/data/default")
	client.statErr = errors.New("daemon unavailable")

	err := linkLegacyNodeState(context.Background(), client, "tsdproxy-id", "once")

	require.ErrorContains(t, err, "daemon unavailable")
	assert.Empty(t, client.copies)
}

// Helpers

type fakeStateClient struct {
	paths   map[string]bool
	statErr error
	copies  []stateCopy
}

type stateCopy struct {
	containerID string
	dstPath     string
	entries     []*tar.Header
}

func newFakeStateClient(paths ...string) *fakeStateClient {
	c := &fakeStateClient{paths: map[string]bool{}}
	for _, p := range paths {
		c.paths[p] = true
	}
	return c
}

func (c *fakeStateClient) ContainerStatPath(_ context.Context, _, path string) (container.PathStat, error) {
	if c.statErr != nil {
		return container.PathStat{}, c.statErr
	}
	if !c.paths[path] {
		return container.PathStat{}, errdefs.ErrNotFound
	}
	return container.PathStat{Name: path}, nil
}

func (c *fakeStateClient) CopyToContainer(_ context.Context, containerID, dstPath string, content io.Reader, _ container.CopyToContainerOptions) error {
	copied := stateCopy{containerID: containerID, dstPath: dstPath}
	tr := tar.NewReader(content)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		copied.entries = append(copied.entries, header)
	}
	c.copies = append(c.copies, copied)
	return nil
}
