//go:build integration
// +build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ananta888/dockweave/internal/dockerclient"
	"github.com/ananta888/dockweave/internal/gateexec"
	"github.com/ananta888/dockweave/internal/persistence"
)

// gateTestImage runs gates and egress probes. scripts/test-integration.sh
// passes a digest-pinned reference; the tag is the fallback for direct runs.
var gateTestImage = func() string {
	if v := os.Getenv("DOCKWEAVE_GATE_TEST_IMAGE"); v != "" {
		return v
	}
	return "node:22-bookworm"
}()

func gatePrerequisite(t *testing.T, reason string) {
	t.Helper()
	if os.Getenv("DOCKWEAVE_INTEGRATION_REQUIRED") == "1" {
		t.Fatal(reason)
	}
	t.Skip(reason)
}

// gateRunner builds a sandbox gate runner against the local Docker engine.
// It skips (never fails) when Docker or the runner image is unavailable.
// Gate runs only start unprivileged, network-less containers; they do not
// touch the mount table.
func gateRunner(t *testing.T) (*gateexec.Runner, *persistence.Store, string) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		gatePrerequisite(t, "docker not available")
	}
	if err := exec.Command("docker", "image", "inspect", gateTestImage).Run(); err != nil {
		gatePrerequisite(t, gateTestImage+" image not available")
	}
	dc, err := dockerclient.New()
	if err != nil {
		gatePrerequisite(t, "docker client: "+err.Error())
	}
	store, err := persistence.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	root := t.TempDir()
	r := &gateexec.Runner{
		Docker: dc, Store: store, BaseDir: t.TempDir(), DefaultImage: gateTestImage,
		AllowedRoots: []string{root},
	}
	return r, store, root
}

// workspaceWithGates creates a workspace below root with the given gates.json.
func workspaceWithGates(t *testing.T, root string, cfg map[string]any, files map[string]string) string {
	t.Helper()
	ws, err := os.MkdirTemp(root, "ws-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".paperclip"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(ws, ".paperclip", "gates.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		p := filepath.Join(ws, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	canon, _ := filepath.EvalSymlinks(ws)
	return canon
}

var bg = context.Background()

// requireHostFirewall guards tests that change nftables rules. The
// integration runner shares the host network namespace, so these tests run
// only where that is acceptable: a disposable VM such as CI
// (DOCKWEAVE_INTEGRATION_HOST_FIREWALL=1, set by scripts/test-integration.sh
// when CI=true). There, a missing nft binary is a failure, not a skip.
func requireHostFirewall(t *testing.T) {
	t.Helper()
	if os.Getenv("DOCKWEAVE_INTEGRATION_HOST_FIREWALL") != "1" {
		t.Skip("changes the host firewall; runs only in a disposable VM (DOCKWEAVE_INTEGRATION_HOST_FIREWALL=1, set in CI)")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		gatePrerequisite(t, "nft binary not available")
	}
}
