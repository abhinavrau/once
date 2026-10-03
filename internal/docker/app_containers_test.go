package docker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActiveContainer(t *testing.T) {
	settings := appSettings("blog")
	old := appSummary("aaaaaaaaaaaa-old", "once-app-blog-a1b2c3", 100, "running", settings)
	newer := appSummary("bbbbbbbbbbbb-new", "once-app-blog-d4e5f6", 200, "running", settings)

	t.Run("the container the Proxy routes to", func(t *testing.T) {
		assert.Equal(t, newer, activeContainer([]container.Summary{old, newer}, "bbbbbbbbbbbb"))
		assert.Equal(t, old, activeContainer([]container.Summary{newer, old}, "aaaaaaaaaaaa"))
	})

	t.Run("the oldest when the Proxy routes to none of them", func(t *testing.T) {
		stopped := old
		stopped.State = "exited"
		assert.Equal(t, stopped, activeContainer([]container.Summary{newer, stopped}, ""))
		assert.Equal(t, stopped, activeContainer([]container.Summary{newer, stopped}, "cccccccccccc"))
	})
}

func TestActiveContainersReadsTheProxyOnlyWhenAmbiguous(t *testing.T) {
	settings := appSettings("blog")
	lone := map[string][]container.Summary{
		"blog": {appSummary("aaaaaaaaaaaa", "once-app-blog-a1b2c3", 100, "running", settings)},
	}
	routes := &fakeRouteTargets{err: errors.New("proxy down")}

	active, err := activeContainers(context.Background(), lone, routes)
	require.NoError(t, err)
	assert.Equal(t, "aaaaaaaaaaaa", active["blog"].ID)
	assert.Zero(t, routes.calls)
}

func TestStrayContainers(t *testing.T) {
	settings := appSettings("blog")
	now := time.Unix(100_000, 0)
	settled := now.Add(-strayContainerGrace - time.Minute).Unix()
	recent := now.Add(-time.Minute).Unix()

	t.Run("a lone container has no strays", func(t *testing.T) {
		only := appSummary("id-old", "once-app-blog-a1b2c3", settled, "running", settings)
		assert.Empty(t, strayContainers([]container.Summary{only}, only, now))
	})

	t.Run("every container but the active one is a stray", func(t *testing.T) {
		active := appSummary("id-old", "once-app-blog-a1b2c3", settled-60, "running", settings)
		stray := appSummary("id-new", "once-app-blog-d4e5f6", settled, "restarting", settings)

		assert.Equal(t, []container.Summary{stray}, strayContainers([]container.Summary{stray, active}, active, now))
	})

	t.Run("an app with a recently created container may have a deploy in flight", func(t *testing.T) {
		active := appSummary("id-old", "once-app-blog-a1b2c3", settled, "running", settings)
		inFlight := appSummary("id-new", "once-app-blog-d4e5f6", recent, "running", settings)

		assert.Empty(t, strayContainers([]container.Summary{active, inFlight}, active, now))
	})
}

type fakeRouteTargets struct {
	targets map[string]string
	err     error
	calls   int
}

func (f *fakeRouteTargets) RouteTargets(ctx context.Context) (map[string]string, error) {
	f.calls++
	return f.targets, f.err
}
