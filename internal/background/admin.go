package background

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/basecamp/once/internal/docker"
)

// AdminServer is the daemon's Admin Web App HTTP server. It listens exclusively
// on a Unix domain socket (no TCP port) and the once-admin nginx container
// proxies the tailnet to it. Until the Admin Web App PRD lands it serves only a
// placeholder/health endpoint.
type AdminServer struct {
	socketPath string
	confPath   string
}

func NewAdminServer() *AdminServer {
	return &AdminServer{
		socketPath: docker.AdminSocketPath(),
		confPath:   docker.AdminNginxConfPath(),
	}
}

// Run writes the nginx config the once-admin container mounts, then serves until
// the context is cancelled, removing the socket on the way out.
func (a *AdminServer) Run(ctx context.Context) error {
	if err := os.WriteFile(a.confPath, []byte(docker.AdminNginxConfig()), 0o644); err != nil {
		return fmt.Errorf("writing once-admin nginx config: %w", err)
	}

	// Clear a socket left behind by an unclean shutdown so Listen can rebind.
	_ = os.Remove(a.socketPath)

	ln, err := net.Listen("unix", a.socketPath)
	if err != nil {
		return fmt.Errorf("listening on once-admin socket: %w", err)
	}

	// ponytail: world-rw socket — nginx workers in the once-admin container run
	// as a different uid and must be able to connect to the bind-mounted socket.
	if err := os.Chmod(a.socketPath, 0o666); err != nil {
		return fmt.Errorf("chmod once-admin socket: %w", err)
	}

	srv := &http.Server{Handler: adminHandler()}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
		_ = os.Remove(a.socketPath)
	}()

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Helpers

func adminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/up", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("once-admin ok\n"))
	})
	return mux
}
