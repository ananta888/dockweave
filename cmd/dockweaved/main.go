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
	"github.com/ananta888/dockweave/internal/agentsession"
	"github.com/ananta888/dockweave/internal/api"
	"github.com/ananta888/dockweave/internal/brokerclient"
	"github.com/ananta888/dockweave/internal/config"
	"github.com/ananta888/dockweave/internal/configio"
	"github.com/ananta888/dockweave/internal/dispatch"
	"github.com/ananta888/dockweave/internal/dockerclient"
	"github.com/ananta888/dockweave/internal/egress"
	"github.com/ananta888/dockweave/internal/gateexec"
	"github.com/ananta888/dockweave/internal/knowledge"
	"github.com/ananta888/dockweave/internal/memory"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/monitor"
	"github.com/ananta888/dockweave/internal/mounts"
	"github.com/ananta888/dockweave/internal/nativeagent"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/productivity"
	"github.com/ananta888/dockweave/internal/skillcatalog"
	"github.com/ananta888/dockweave/internal/supervisor"
	"github.com/ananta888/dockweave/internal/ticketing"
	tnative "github.com/ananta888/dockweave/internal/ticketing/native"
	"github.com/ananta888/dockweave/internal/ticketing/authz"
	pcticket "github.com/ananta888/dockweave/internal/ticketing/paperclip"
	"github.com/ananta888/dockweave/internal/ticketing/tsync"
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
	cfg, err := config.Load(cfgPath)
	if err != nil {
		if !os.IsNotExist(errors.Unwrap(err)) {
			return err
		}
		log.Printf("config %s not found, using defaults", cfgPath)
	}

	if os.Geteuid() != 0 && cfg.MountdSocket == "" {
		return errors.New("dockweaved must run as root or with MOUNTD_SOCKET (privileged mount helper); refusing to start")
	}

	mountMgr, err := mounts.New(mounts.Config{
		Root: cfg.Root, AllowedRoots: cfg.AllowedRoots, MountdSocket: cfg.MountdSocket,
	})
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
	// Optional host-side nftables enforcement for broker-only agents
	// (egress-003). Off by default; fail-closed when enabled.
	if cfg.EgressNftables {
		agentSvc.Egress = egress.New(egress.Config{BrokerPort: agents.BrokerProfilePort})
	}
	// Shared knowledge store: ensure it exists and carries its usage guide.
	if cfg.KnowledgeDir != "" {
		if err := knowledge.Ensure(cfg.KnowledgeDir); err != nil {
			log.Printf("knowledge store: %v", err)
		}
	}
	wsSvc := &workspaces.Service{Store: store, Mounts: mountMgr}
	authPolicy := api.NewAuthPolicy(cfg.AdminUIDs, cfg.MonitorUIDs).WithGateways(cfg.GatewayUIDs)
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
	apiSrv.AllowedRoots = cfg.AllowedRoots
	apiSrv.Root = cfg.Root
	apiSrv.KnowledgeDir = cfg.KnowledgeDir
	sessions := agentsession.New(agentsession.Config{
		Docker: docker,
		FindContainer: func(ctx context.Context, idOrName string) (string, error) {
			return lookup.ContainerID(ctx, idOrName)
		},
	})
	apiSrv.Sessions = sessions
	native := nativeagent.New(docker, func(ctx context.Context, idOrName string) (string, error) {
		return lookup.ContainerID(ctx, idOrName)
	})
	native.TypePrompt = sessions.TypePrompt
	native.PressEnter = sessions.PressEnter
	apiSrv.Native = native
	if skills, err := skillcatalog.New(filepath.Join(cfg.Root, "skills")); err != nil {
		log.Printf("skill catalog disabled: %v", err)
	} else {
		apiSrv.Skills = skills
	}
	if cfg.BrokerAdminSocket != "" {
		apiSrv.Broker = brokerclient.New(cfg.BrokerAdminSocket)
	}
	var usage gateexec.UsageChecker
	if apiSrv.Broker != nil {
		usage = apiSrv.Broker
	}
	apiSrv.Gates = &gateexec.Runner{
		Docker:       docker,
		Store:        store,
		Broker:       usage,
		BaseDir:      filepath.Join(cfg.Root, "gates"),
		AllowedRoots: cfg.AllowedRoots,
	}
	openTicketing(apiSrv, cfg, store, agentSvc)
	apiSrv.ConfigIO = &configio.Service{
		Store:      store,
		Agents:     agentSvc,
		Workspaces: wsSvc,
		DaemonConfig: map[string]string{
			"KNOWLEDGE_DIR":        cfg.KnowledgeDir,
			"AGENT_UID":            fmt.Sprint(cfg.AgentUID),
			"AGENT_GID":            fmt.Sprint(cfg.AgentGID),
			"ALLOW_ROOT_CONTAINER": fmt.Sprint(cfg.AllowRootContainer),
			"SOCKET_GROUP":         cfg.SocketGroup,
			"ALLOWED_ROOTS":        strings.Join(cfg.AllowedRoots, ":"),
		},
	}
	// Deterministic collector: presence-based agent runtime and broker deltas.
	collectCtx, collectCancel := context.WithCancel(context.Background())
	defer collectCancel()
	collector := &productivity.Collector{Store: store, Broker: apiSrv.Broker, Interval: time.Minute}
	go collector.Run(collectCtx)
	startDispatcher(collectCtx, apiSrv, cfg, agentSvc)

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

// agentDirectory resolves DockWeave agent names/ids for ticket adapters.
type agentDirectory struct{ agents *agents.Service }

func (d agentDirectory) Resolve(ctx context.Context, nameOrID string) (string, bool) {
	a, err := d.agents.Get(ctx, nameOrID)
	if err != nil {
		return "", false
	}
	return a.ID, true
}

// openTicketing wires the configured ticket providers (ADR 0003 D-3).
// Ticketing is opt-in; an open error is reported through the API instead of
// falling back to another provider.
func openTicketing(apiSrv *api.Server, cfg config.Config, store *persistence.Store, agentSvc *agents.Service) {
	tnative.Register(store)
	pcticket.Register(agentDirectory{agentSvc})
	// Permissions of forwarded humans; reloaded when the file changes, every
	// (re)load or failure is audited (D-6a).
	apiSrv.TicketRoles = &authz.Store{Path: cfg.TicketRolesFile, OnReload: func(err error) {
		typ, msg := model.EventType("ticket.roles_loaded"), "ticket roles loaded from "+cfg.TicketRolesFile
		if err != nil {
			typ, msg = model.EventType("ticket.roles_invalid"), err.Error()
			log.Printf("ticketing: %v", err)
		}
		_ = store.AppendEvent(context.Background(), &model.Event{ID: model.NewID("evt"), Type: typ, Actor: "dockweaved", Message: msg, Timestamp: time.Now().UTC()})
	}}
	_ = apiSrv.TicketRoles.Policy()
	if cfg.TicketProvider == "" {
		log.Printf("ticketing: not configured (TICKET_PROVIDER)")
		return
	}
	sel, err := ticketing.Open(ticketing.Config{Primary: cfg.TicketProvider, Mirror: cfg.TicketMirror, Options: map[string]string{
		"paperclip.url": cfg.PaperclipURL, "paperclip.company": cfg.PaperclipCompany, "paperclip.token_file": cfg.PaperclipTokenFile,
	}})
	if err != nil {
		apiSrv.TicketsError = err.Error()
		log.Printf("ticketing: %v", err)
		return
	}
	// Every mutation, whoever triggers it, lands in the audit chain (D-5);
	// with a mirror the same events feed the live sync outbox (D-8).
	audit := api.NewTicketAudit(store)
	var pub ticketing.Publisher = audit
	var syncer *tsync.Syncer
	if sel.Mirror != nil {
		syncer = &tsync.Syncer{Links: store, Marks: store, Audit: audit}
		pub = ticketing.Publishers(audit, syncer)
	}
	sel.Primary = ticketing.Observe(sel.Primary, pub)
	if sel.Mirror != nil {
		sel.Mirror = ticketing.Observe(sel.Mirror, pub)
		syncer.Primary, syncer.Mirror = sel.Primary, sel.Mirror
		apiSrv.Sync = syncer
	}
	apiSrv.Tickets = sel
	mirror := "none"
	if sel.Mirror != nil {
		mirror = sel.Mirror.Name()
	}
	log.Printf("ticketing: primary=%s mirror=%s", sel.Primary.Name(), mirror)
}

// startDispatcher wires the ticket worker loop (ADR 0003 D-4/D-9). The
// native provider gets the daemon's own heartbeat scheduler; Paperclip keeps
// its heartbeats and calls `dockweave ticket run <agent>`.
func startDispatcher(ctx context.Context, apiSrv *api.Server, cfg config.Config, agentSvc *agents.Service) {
	if apiSrv.Tickets == nil {
		return
	}
	primary := apiSrv.Tickets.Primary
	interval := time.Duration(cfg.DispatchHeartbeatSec) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	sched := &dispatch.Scheduler{
		Interval:    interval,
		Concurrency: cfg.DispatchConcurrency,
		Logf:        log.Printf,
		Agents: func(ctx context.Context) ([]string, error) {
			list, err := agentSvc.List(ctx)
			if err != nil {
				return nil, err
			}
			var ids []string
			for _, a := range list {
				if a.State == model.AgentRunning {
					ids = append(ids, a.ID)
				}
			}
			return ids, nil
		},
	}
	d := apiSrv.NewDispatcher(api.DispatchSettings{
		DefaultWorkspace: cfg.DispatchDefaultWorkspace,
		Reviewers:        cfg.TicketReviewers,
		PromptTimeout:    time.Duration(cfg.DispatchPromptTimeoutSec) * time.Second,
	}, primary, api.AgentWaker{Server: apiSrv, Provider: primary, Scheduler: sched})
	sched.D = d
	apiSrv.Dispatcher = d
	if apiSrv.Sync != nil {
		go apiSrv.Sync.Run(ctx)
		log.Printf("ticketing: live sync %s <-> %s every %s", primary.Name(), apiSrv.Tickets.Mirror.Name(), apiSrv.Sync.Interval)
	}
	if primary.Name() == "native" {
		go sched.Run(ctx)
		log.Printf("dispatch: native heartbeat every %s", sched.Interval)
	}
}
