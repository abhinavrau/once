package docker

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
)

// strayContainerGrace outlasts the longest a Deploy waits on its new
// container, so an Application with a container younger than this may still
// have a Deploy in flight.
const strayContainerGrace = 15 * time.Minute

// routeTargetReader reports, per Application name, the container its Proxy
// route targets.
type routeTargetReader interface {
	RouteTargets(ctx context.Context) (map[string]string, error)
}

// RemoveStrayContainers removes every App Container that is not its
// Application's active one: what a Deploy leaves behind when it is killed
// before promoting or cleaning up its new container. An Application with a
// container younger than strayContainerGrace is left for a later sweep, as a
// Deploy, in this process or another, may still be in flight.
func (n *Namespace) RemoveStrayContainers(ctx context.Context, now time.Time) error {
	byApp, err := n.appContainers(ctx)
	if err != nil {
		return err
	}
	active, err := activeContainers(ctx, byApp, n.proxy)
	if err != nil {
		return fmt.Errorf("reading proxy routes: %w", err)
	}

	var errs []error
	for appName, containers := range byApp {
		for _, c := range strayContainers(containers, active[appName], now) {
			slog.Info("Removing stray app container", "app", appName, "container", containerName(c), "state", c.State)
			if err := n.client.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
				errs = append(errs, fmt.Errorf("removing stray container %s: %w", containerName(c), err))
			}
		}
	}
	return errors.Join(errs...)
}

// Private

// appContainers lists every App Container in the namespace, grouped by the
// Application its name carries.
func (n *Namespace) appContainers(ctx context.Context) (map[string][]container.Summary, error) {
	containers, err := n.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	byApp := make(map[string][]container.Summary)
	for _, c := range containers {
		if appName := n.containerAppName(containerName(c)); appName != "" {
			byApp[appName] = append(byApp[appName], c)
		}
	}
	return byApp, nil
}

// Helpers

// activeContainers picks each Application's active App Container. The Proxy
// is read only when some Application has more than one; when that read
// fails, the error comes back alongside picks that fell back to the oldest.
func activeContainers(ctx context.Context, byApp map[string][]container.Summary, routes routeTargetReader) (map[string]container.Summary, error) {
	var targets map[string]string
	var err error
	for _, containers := range byApp {
		if len(containers) > 1 {
			targets, err = routes.RouteTargets(ctx)
			break
		}
	}

	active := make(map[string]container.Summary, len(byApp))
	for appName, containers := range byApp {
		active[appName] = activeContainer(containers, targets[appName])
	}
	return active, err
}

// activeContainer picks the container the Proxy routes to, which a Deploy
// switches only once its new container is healthy. With no route to any of
// them (an app the Proxy does not route, or an unreadable Proxy) it picks the
// oldest: a Deploy removes the older containers once it has promoted the
// new one, so a newer survivor is one it never finished.
func activeContainer(containers []container.Summary, routeTarget string) container.Summary {
	if routeTarget != "" {
		for _, c := range containers {
			if strings.HasPrefix(c.ID, routeTarget) {
				return c
			}
		}
	}
	return slices.MinFunc(containers, func(a, b container.Summary) int {
		return cmp.Or(cmp.Compare(a.Created, b.Created), strings.Compare(a.ID, b.ID))
	})
}

// strayContainers returns every container but the active one, or none while
// a container young enough to be a Deploy in flight exists.
func strayContainers(containers []container.Summary, active container.Summary, now time.Time) []container.Summary {
	for _, c := range containers {
		if now.Sub(time.Unix(c.Created, 0)) < strayContainerGrace {
			return nil
		}
	}
	return slices.DeleteFunc(slices.Clone(containers), func(c container.Summary) bool {
		return c.ID == active.ID
	})
}

func containerName(c container.Summary) string {
	if len(c.Names) == 0 {
		return ""
	}
	return strings.TrimPrefix(c.Names[0], "/")
}
