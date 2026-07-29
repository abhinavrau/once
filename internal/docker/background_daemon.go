package docker

import (
	"fmt"

	"github.com/basecamp/once/internal/service"
)

const backgroundServiceSuffix = "-background"

// BackgroundServiceName is the system-service name the namespace's background
// daemon runs under - the one `once background install` registers.
func BackgroundServiceName(namespace string) string {
	return namespace + backgroundServiceSuffix
}

// Helpers

// requireBackgroundDaemon fails unless the namespace's background daemon is
// installed and running. It must ask the service manager, not probe the admin
// socket: the socket server restarts with backoff independently of the loop
// that tears Funnels down at expiry, so a socket probe would refuse
// legitimate Funnels.
func requireBackgroundDaemon(namespace string) error {
	svc, err := service.New()
	if err != nil {
		return err
	}
	name := BackgroundServiceName(namespace)
	if !svc.IsInstalled(name) || !svc.IsRunning(name) {
		return fmt.Errorf("the background service is not running; run `once background install` before enabling a Funnel so it can be torn down automatically on expiry")
	}
	return nil
}
