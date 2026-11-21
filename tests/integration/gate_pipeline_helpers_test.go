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

const gateTestImage = "node:22-bookworm"

// gateRunner builds a sandbox gate runner against the local Docker engine.
// It skips (never fails) when Docker or the runner image is unavailable.
// Gate runs only start unprivileged, network-less containers; they do not
// touch the mount table.
func gateRunner(t *testing.T) (*gateexec.Runner, *persistence.Store, string) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "image", "inspect", gateTestImage).Run(); err != nil {
		t.Skipf("%s image not available", gateTestImage)
	}
	dc, err := dockerclient.New()
	if err != nil {
		t.Skipf("docker client: %v", err)
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
