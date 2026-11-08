// dockweave-broker is the unprivileged credential broker. It is deliberately
// separate from the root daemon: it holds decrypted provider credentials in
// memory and never performs mount operations or Docker API calls.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ananta888/dockweave/internal/credentialbroker"
	"github.com/ananta888/dockweave/internal/credentialvault"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "dockweave-broker: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	home, _ := os.UserHomeDir()
	defaultDir := filepath.Join(home, ".local", "share", "dockweave", "broker")
	var (
		dataDir       = flag.String("data-dir", envOr("DOCKWEAVE_BROKER_DATA_DIR", defaultDir), "broker data directory")
		proxySocket   = flag.String("proxy-socket", envOr("DOCKWEAVE_BROKER_SOCKET", ""), "agent-facing unix socket (default <data-dir>/broker.sock)")
		adminSocket   = flag.String("admin-socket", envOr("DOCKWEAVE_BROKER_ADMIN_SOCKET", ""), "operator unix socket (default <data-dir>/admin.sock)")
		initVault     = flag.Bool("init", false, "create a new vault (requires DOCKWEAVE_VAULT_PASSPHRASE)")
		adminUIDs     = flag.String("admin-uids", envOr("DOCKWEAVE_BROKER_ADMIN_UIDS", ""), "comma-separated admin UIDs (default: process euid)")
		listenAddr    = flag.String("listen", envOr("DOCKWEAVE_BROKER_LISTEN", ""), "optional TCP listen address for the proxy, e.g. 172.30.0.1:8787 (unix socket remains enabled)")
		providersFile = flag.String("providers-file", envOr("DOCKWEAVE_BROKER_PROVIDERS_FILE", ""), "optional JSON file overriding provider adapters")
		allowInsecure = flag.Bool("allow-insecure-http", false, "allow http:// provider base URLs (local demo only)")
	)
	flag.Parse()

	if *proxySocket == "" {
		*proxySocket = filepath.Join(*dataDir, "broker.sock")
	}
	if *adminSocket == "" {
		*adminSocket = filepath.Join(*dataDir, "admin.sock")
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	pass := os.Getenv("DOCKWEAVE_VAULT_PASSPHRASE")
	if pass == "" {
		return errors.New("DOCKWEAVE_VAULT_PASSPHRASE is required (never pass secrets on the command line)")
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	vaultPath := filepath.Join(*dataDir, "vault.json")
	var vault *credentialvault.Store
	var err error
	if *initVault {
		vault, err = credentialvault.Create(vaultPath, pass)
	} else {
		vault, err = credentialvault.Open(vaultPath, pass)
	}
	if err != nil {
		return err
	}
	defer vault.Close()

	policy, err := credentialbroker.OpenPolicy(filepath.Join(*dataDir, "policy.json"))
	if err != nil {
		return err
	}
	sessions, err := credentialbroker.OpenSessions(filepath.Join(*dataDir, "sessions.json"))
	if err != nil {
		return err
	}
	budget, err := credentialbroker.OpenBudget(filepath.Join(*dataDir, "budget.json"))
	if err != nil {
		return err
	}
	providers := credentialbroker.DefaultProviders()
	if *providersFile != "" {
		overrides, err := loadProviders(*providersFile)
		if err != nil {
			return err
		}
		for name, p := range overrides {
			providers[name] = p
		}
	}
	logger.Info("vault unlocked", "vault", vaultPath, "providers", credentialbroker.SortedProviders(providers))

	broker, err := credentialbroker.NewServer(credentialbroker.Config{
		Vault: vault, Policy: policy, Sessions: sessions, Budget: budget,
		Providers: providers, Logger: logger, AllowInsecureHTTP: *allowInsecure,
	})
	if err != nil {
		return err
	}
	adminCfg := credentialbroker.AdminConfig{
		Vault: vault, Policy: policy, Sessions: sessions, Budget: budget,
		Broker: broker, Providers: providers,
	}
	if *adminUIDs != "" {
		for _, part := range strings.Split(*adminUIDs, ",") {
			uid, err := strconv.ParseUint(strings.TrimSpace(part), 10, 32)
			if err != nil {
				return fmt.Errorf("invalid admin uid %q", part)
			}
			adminCfg.AllowedUIDs = append(adminCfg.AllowedUIDs, uint32(uid))
		}
	}
	admin, err := credentialbroker.NewAdminHandler(adminCfg)
	if err != nil {
		return err
	}

	proxyLn, err := listenUnix(*proxySocket)
	if err != nil {
		return fmt.Errorf("listen proxy socket: %w", err)
	}
	defer proxyLn.Close()
	adminLn, err := listenUnix(*adminSocket)
	if err != nil {
		return fmt.Errorf("listen admin socket: %w", err)
	}
	defer adminLn.Close()
	_ = os.Chmod(*proxySocket, 0o600)
	_ = os.Chmod(*adminSocket, 0o600)

	proxySrv := &http.Server{Handler: broker, ReadHeaderTimeout: 10 * time.Second}
	adminSrv := &http.Server{
		Handler:           admin,
		ReadHeaderTimeout: 10 * time.Second,
		ConnContext:       credentialbroker.PeerConnContext,
	}
	errCh := make(chan error, 3)
	go func() { errCh <- proxySrv.Serve(proxyLn) }()
	go func() { errCh <- adminSrv.Serve(adminLn) }()
	if *listenAddr != "" {
		tcpLn, err := net.Listen("tcp", *listenAddr)
		if err != nil {
			return fmt.Errorf("listen tcp %s: %w", *listenAddr, err)
		}
		defer tcpLn.Close()
		go func() { errCh <- proxySrv.Serve(tcpLn) }()
		logger.Warn("broker proxy exposed on TCP; restrict with firewall/routing", "listen", *listenAddr)
	}
	logger.Info("broker listening", "proxy_socket", *proxySocket, "admin_socket", *adminSocket)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case s := <-sig:
		logger.Info("shutting down", "signal", s.String())
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = proxySrv.Shutdown(ctx)
	_ = adminSrv.Shutdown(ctx)
	return nil
}

func loadProviders(path string) (map[string]credentialbroker.Provider, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read providers file: %w", err)
	}
	var out map[string]credentialbroker.Provider
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("parse providers file: %w", err)
	}
	return out, nil
}

func listenUnix(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to replace non-socket %s", path)
		}
		_ = os.Remove(path)
	}
	return net.Listen("unix", path)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
