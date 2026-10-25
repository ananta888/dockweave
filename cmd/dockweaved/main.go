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
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ananta888/dockweave/internal/agents"
	"github.com/ananta888/dockweave/internal/api"
	"github.com/ananta888/dockweave/internal/config"
	"github.com/ananta888/dockweave/internal/configio"
	"github.com/ananta888/dockweave/internal/dockerclient"
	"github.com/ananta888/dockweave/internal/knowledge"
	"github.com/ananta888/dockweave/internal/memory"
	"github.com/ananta888/dockweave/internal/model"
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

	memoryMgr, err := memory.New(cfg.Root, func(memoryID string) string {
		// Decoupled memory stores live under <root>/memories/<id> (review-009).
		return filepath.Join(cfg.Root, "memories", memoryID)
	})
	if err != nil {
		return err
	}

	agentSvc := &agents.Service{
		Store: store, Mounts: mountMgr, Docker: docker, Memory: memoryMgr,
		DefaultUID: cfg.AgentUID, DefaultGID: cfg.AgentGID,
		AllowRootContainer: cfg.AllowRootContainer,
		KnowledgeDir:       cfg.KnowledgeDir,
	}
	// Shared knowledge store: ensure it exists and carries its usage guide.
	if cfg.KnowledgeDir != "" {
		if err := knowledge.Ensure(cfg.KnowledgeDir); err != nil {
			log.Printf("knowledge store: %v", err)
		}
	}
	wsSvc := &workspaces.Service{Store: store, Mounts: mountMgr}
	authPolicy := api.NewAuthPolicy(cfg.AdminUIDs, cfg.MonitorUIDs)
	// Agent removal revokes the agent's grants through the workspace service.
	agentSvc.Grants = wsSvc

	// Migrate legacy per-agent memory dirs to decoupled memory identities.
	if err := agentSvc.EnsureMemoryIdentities(context.Background()); err != nil {
		log.Printf("memory identity migration: %v", err)
	}

	lookup := &containerLookup{store: store, docker: docker}
	dockerProv := monitor.NewDockerProvider(docker, lookup)
	procfsProv := monitor.NewProcfsProvider(func() map[string]string {
		return workspaceRoots(store)
	})
	provs := []monitor.Provider{dockerProv, procfsProv}
	// Optional Tetragon eBPF provider (JSONL export stream).
	if tetraPath := os.Getenv("DOCKWEAVE_TETRAGON_EVENTS"); tetraPath != "" {
		tetra := monitor.NewTetragonProvider(tetraPath, func() map[string]string {
			return workspaceRoots(store)
		})
		tetra.PIDResolver = func(ctx context.Context) map[int]string {
			return monitor.BuildPIDMap(lookup, docker)
		}
		provs = append(provs, tetra)
		log.Printf("tetragon provider enabled (events: %s)", tetraPath)
	}
	mon := monitor.New(provs...)
	mon.DetectorRoots(func() map[string]string { return workspaceRoots(store) })

	// Startup recovery: refresh container states, re-prepare trees, reconcile mounts.
	sup := &supervisor.Supervisor{Store: store, Mounts: mountMgr, Docker: docker}
	recCtx, recCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	if err := sup.Recover(recCtx); err != nil {
		log.Printf("recovery incomplete: %v", err)
	}
	// Converge interrupted grant lifecycles (mounting/revoking/unmount_failed).
	wsSvc.ReconcileGrants(recCtx)
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
	// Root + group access; the group is configurable (SOCKET_GROUP,
	// default "dockweave") so non-root admins can connect. The role policy
	// still gates what a connected peer may do.
	if err := os.Chmod(cfg.SocketPath, 0o660); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}
	if err := applySocketGroup(cfg.SocketPath, cfg.SocketGroup); err != nil {
		log.Printf("socket group: %v (socket stays root:root)", err)
	}

	apiSrv := api.NewServer(agentSvc, wsSvc, mon, store)
	apiSrv.ConfigIO = &configio.Service{
		Store:      store,
		Agents:     agentSvc,
		Workspaces: wsSvc,
		DaemonConfig: map[string]string{
			"KNOWLEDGE_DIR":       cfg.KnowledgeDir,
			"AGENT_UID":           fmt.Sprint(cfg.AgentUID),
			"AGENT_GID":           fmt.Sprint(cfg.AgentGID),
			"ALLOW_ROOT_CONTAINER": fmt.Sprint(cfg.AllowRootContainer),
			"SOCKET_GROUP":        cfg.SocketGroup,
			"ALLOWED_ROOTS":       strings.Join(cfg.AllowedRoots, ":"),
		},
	}
	handler := api.PeerAuthMiddleware(authPolicy, apiSrv)
	srv := &http.Server{Handler: handler, ConnContext: api.PeerConnContext}

	// Terminal stream server (interactive PTY sessions; separate raw socket).
	termSock := filepath.Join(filepath.Dir(cfg.SocketPath), "dockweaved.term.sock")
	termSrv := api.NewTermServer(api.TermConfig{
		SocketPath: termSock,
		Docker:     docker,
		Auth:       authPolicy,
		FindContainer: func(ctx context.Context, idOrName string) (string, error) {
			a, err := store.GetAgent(ctx, idOrName)
			if err != nil {
				return "", err
			}
			cid, err := docker.FindByAgent(ctx, a.ID)
			if err != nil || cid == "" {
				return "", err
			}
			st, err := docker.State(ctx, cid)
			if err == nil && st != "running" {
				return "", fmt.Errorf("agent %q not running", a.Name)
			}
			return cid, nil
		},
	})
	if err := termSrv.Listen(); err != nil {
		return err
	}
	if err := applySocketGroup(termSock, cfg.SocketGroup); err != nil {
		log.Printf("term socket group: %v (socket stays root:root)", err)
	}
	go func() {
		if err := termSrv.Serve(); err != nil {
			log.Printf("term server: %v", err)
		}
	}()
	defer termSrv.Close()

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

// Agents implements monitor.AgentLookupExtended.
func (l *containerLookup) Agents(ctx context.Context) ([]*model.Agent, error) {
	return l.store.ListAgents(ctx)
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

// applySocketGroup hands a 0660 unix socket to the configured admin group
// (SOCKET_GROUP, default "dockweave"). With an empty group the socket stays
// root:root; a missing group is reported so operators create it explicitly.
// Access is still gated by the UID→role policy on top of socket mode.
func applySocketGroup(path, group string) error {
	if group == "" {
		return nil
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		return fmt.Errorf("group %q not found (create it: groupadd %s): %w", group, group, err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return fmt.Errorf("group %q has non-numeric gid %q: %w", group, g.Gid, err)
	}
	if err := os.Chown(path, -1, gid); err != nil {
		return fmt.Errorf("chown %s to group %s: %w", path, group, err)
	}
	return nil
}
