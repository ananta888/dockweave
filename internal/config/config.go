// Package config loads daemon configuration from file and environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds daemon settings.
type Config struct {
	Root         string   // state root, default /var/lib/dockweave
	SocketPath   string   // unix socket, default /run/dockweave/dockweaved.sock
	DBPath       string   // sqlite path, default <root>/dockweave.db
	AllowedRoots []string // additional allowed mount source roots
}

// Default returns the default configuration.
func Default() Config {
	return Config{
		Root:       "/var/lib/dockweave",
		SocketPath: "/run/dockweave/dockweaved.sock",
		DBPath:     "/var/lib/dockweave/dockweave.db",
		AllowedRoots: []string{
			"/srv", "/home", "/opt/projects", "/var/lib/dockweave/shared",
		},
	}
}

// Load reads a simple KEY=VALUE config file (if it exists) and applies
// environment overrides (DOCKWEAVE_*).
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read config %q: %w", path, err)
		}
		applyPairs(&cfg, strings.Split(string(b), "\n"))
	}
	// Environment overrides.
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "DOCKWEAVE_") {
			env = append(env, strings.TrimPrefix(e, "DOCKWEAVE_"))
		}
	}
	applyPairs(&cfg, env)
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.Root, "dockweave.db")
	}
	return cfg, nil
}

func applyPairs(cfg *Config, lines []string) {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "ROOT":
			cfg.Root = v
		case "SOCKET_PATH":
			cfg.SocketPath = v
		case "DB_PATH":
			cfg.DBPath = v
		case "ALLOWED_ROOTS":
			cfg.AllowedRoots = strings.Split(v, ":")
		}
	}
}
