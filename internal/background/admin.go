package background

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/handoff"
)

// AdminServer is the daemon's Admin Web App HTTP server. It listens exclusively
// on a Unix domain socket (no TCP port) and the once-admin nginx container
// proxies the tailnet to it. Until the Admin Web App PRD lands it serves only a
// placeholder/health endpoint.
type AdminServer struct {
	socketPath string
}

func NewAdminServer() *AdminServer {
	return &AdminServer{socketPath: docker.AdminSocketPath()}
}

// Run serves on the socket in the directory once-admin bind-mounts until the
// context is cancelled, removing the socket on the way out.
func (a *AdminServer) Run(ctx context.Context) error {
	// The directory already exists when dockerd created it for a once-admin
	// that started first, and it must be kept rather than replaced: the
	// container's mount holds that very directory.
	if err := os.MkdirAll(filepath.Dir(a.socketPath), 0o755); err != nil {
		return fmt.Errorf("creating once-admin socket directory: %w", err)
	}

	// Clear a socket left behind by an unclean shutdown, or an empty directory
	// left in its place, so Listen can rebind.
	_ = os.Remove(a.socketPath)

	ln, err := net.Listen("unix", a.socketPath)
	if err != nil {
		return fmt.Errorf("listening on once-admin socket: %w", err)
	}

	// ponytail: world-rw socket - nginx workers in the once-admin container run
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

	// The one-time page that hands the Repo Password to a phone. The session
	// it serves is created by the custody flow, which runs in the CLI or TUI
	// process, and reaches the daemon through an owner-only file: these routes
	// answer 410 until a handoff is actually live.
	handoff.Mount(mux, handoff.NewStore(docker.RecoveryHandoffPath()))

	return mux
}
