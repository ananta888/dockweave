// dockweaved is the privileged DockWeave daemon. It owns the registry,
// performs all mount operations and serves the v1 API on a unix socket.
//
// It must run as root with CAP_SYS_ADMIN (mount operations). It refuses to
// start without the required capabilities or on non-Linux platforms.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/ananta888/dockweave/internal/agents"
	"github.com/ananta888/dockweave/internal/api"
	"github.com/ananta888/dockweave/internal/config"
	"github.com/ananta888/dockweave/internal/dockerclient"
	"github.com/ananta888/dockweave/internal/monitor"
	"github.com/ananta888/dockweave/internal/mounts"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/supervisor"
	"github.com/ananta888/dockweave/internal/workspaces"
)

func main() {
	cfgPath := flag.String("config", "/etc/dockweave/dockweaved.conf", "config file")
	flag.Parse()
	if err := run(*cfgPath); err != nil {
		log.Fatalf("dockweaved: %v", err)
	}
}

func run(cfgPath string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("dockweaved requires Linux (mount propagation, /proc); got %s", runtime.GOOS)
	}
	if os.Geteuid() != 0 {
		return errors.New("dockweaved must run as root (mount operations require CAP_SYS_ADMIN); refusing to start")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		if !os.IsNotExist(errors.Unwrap(err)) {
			return err
		}
		log.Printf("config %s not found, using defaults", cfgPath)
	}

	mountMgr, err := mounts.New(mounts.Config{Root: cfg.Root, AllowedRoots: cfg.AllowedRoots})
	if err != nil {
		return err
	}
	// Capability detection: prove mount+propagation control before serving.
	if err := mountMgr.PropagationProbe(); err != nil {
		return fmt.Errorf("capability check failed: %w", err)
	}

	store, err := persistence.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	docker, err := dockerclient.New()
	if err != nil {
		return err
	}
	defer docker.Close()
	pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = docker.Ping(pingCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("docker engine unavailable: %w", err)
	}

	// Register known workspace paths as allowed mount sources.
	if wss, err := store.ListWorkspaces(context.Background()); err == nil {
		for _, w := range wss {
			mountMgr.AddAllowedRoot(w.HostPath)
		}
	}

	agentSvc := &agents.Service{Store: store, Mounts: mountMgr, Docker: docker}
	wsSvc := &workspaces.Service{Store: store, Mounts: mountMgr}

	lookup := &containerLookup{store: store, docker: docker}
	dockerProv := monitor.NewDockerProvider(docker, lookup)
	procfsProv := monitor.NewProcfsProvider(func() map[string]string {
		return workspaceRoots(store)
	})
	mon := monitor.New(dockerProv, procfsProv)
	mon.DetectorRoots(func() map[string]string { return workspaceRoots(store) })

	// Startup recovery: refresh container states, re-prepare trees, reconcile mounts.
	sup := &supervisor.Supervisor{Store: store, Mounts: mountMgr, Docker: docker}
	recCtx, recCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	if err := sup.Recover(recCtx); err != nil {
		log.Printf("recovery incomplete: %v", err)
	}
	recCancel()

	// Unix socket with peer auth.
	if err := os.MkdirAll(filepath.Dir(cfg.SocketPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(cfg.SocketPath)
	ln, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen %q: %w", cfg.SocketPath, err)
	}
	// Root + group access; the group may be tightened by the installer.
	if err := os.Chmod(cfg.SocketPath, 0o660); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}

	handler := api.PeerAuthMiddleware(api.NewServer(agentSvc, wsSvc, mon, store))
	srv := &http.Server{Handler: handler, ConnContext: api.PeerConnContext}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("dockweaved listening on %s", cfg.SocketPath)
		errCh <- srv.Serve(ln)
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case s := <-sig:
		log.Printf("received %v, shutting down", s)
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	ctx, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	return srv.Shutdown(ctx)
}

type containerLookup struct {
	store  *persistence.Store
	docker *dockerclient.Client
}

func (l *containerLookup) ContainerID(ctx context.Context, agentID string) (string, error) {
	a, err := l.store.GetAgent(ctx, agentID)
	if err != nil {
		return "", err
	}
	if a.ContainerID != "" {
		return a.ContainerID, nil
	}
	return l.docker.FindByAgent(ctx, a.ID)
}

func workspaceRoots(store *persistence.Store) map[string]string {
	out := map[string]string{}
	agents, err := store.ListAgents(context.Background())
	if err != nil {
		return out
	}
	for _, a := range agents {
		// Host path of the agent's workspace tree: <root>/agents/<id>/workspace.
		// Root is derivable from DB path's directory.
		out[a.ID] = filepath.Join(filepath.Dir(storePath(store)), "agents", a.ID, "workspace")
	}
	return out
}

func storePath(store *persistence.Store) string {
	// The DB lives at <root>/dockweave.db; DSN parsing would be overkill here,
	// so the store exposes its path via a small accessor contract.
	return store.Path()
}
