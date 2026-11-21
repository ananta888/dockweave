package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ananta888/dockweave/internal/gateexec"
)

// RF-014: a layout-only save must not touch the generated gate files, so a
// PASS on the workspace fingerprint stays valid; real gate changes and
// external tampering are still written / repaired.
func TestLayoutOnlySaveKeepsGateFilesAndFingerprint(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{browseRoots: []string{ws}}
	doc := func(rev int, command string, x float64) string {
		conn := map[string]any{"id": "g1", "kind": "gate", "name": "Tests", "enabled": true, "agentId": "a",
			"contract": `{"gate_id":"tests","command":"` + command + `"}`}
		b, _ := json.Marshal(map[string]any{"revision": rev, "connectors": []any{conn},
			"positions": map[string]any{"agent-a": map[string]float64{"x": x, "y": 10}},
			"paperclip": map[string]any{"workspace": ws, "syncGates": true}})
		return string(b)
	}
	gates := filepath.Join(ws, ".paperclip", "gates.json")
	sidecar := filepath.Join(ws, ".paperclip", ".gates.dockweave.json")
	read := func() (string, string) {
		g, err := os.ReadFile(gates)
		if err != nil {
			t.Fatal(err)
		}
		sc, err := os.ReadFile(sidecar)
		if err != nil {
			t.Fatal(err)
		}
		return string(g), string(sc)
	}
	fingerprint := func() string {
		fp, err := gateexec.Fingerprint(context.Background(), ws, gateexec.FingerprintLimits{})
		if err != nil {
			t.Fatal(err)
		}
		return fp
	}
	put := func(body string) {
		t.Helper()
		if rec := putGraphReq(s, body); rec.Code != http.StatusOK {
			t.Fatalf("put: %d %s", rec.Code, rec.Body)
		}
	}

	put(doc(0, "go test ./...", 10))
	g1, sc1 := read()
	fp1 := fingerprint()

	// Unchanged gates, moved node: byte-identical files, same fingerprint.
	put(doc(1, "go test ./...", 500))
	if g2, sc2 := read(); g2 != g1 || sc2 != sc1 {
		t.Fatal("layout-only save rewrote the generated gate files")
	}
	if fp := fingerprint(); fp != fp1 {
		t.Fatal("layout-only save changed the workspace fingerprint (PASS would turn stale)")
	}

	// A changed command is a real configuration change.
	put(doc(2, "go vet ./...", 500))
	g3, _ := read()
	if g3 == g1 || !strings.Contains(g3, "go vet ./...") || fingerprint() == fp1 {
		t.Fatal("changed gate command was not written")
	}

	// External tampering is repaired on the next save even without changes.
	if err := os.WriteFile(gates, []byte(`{"version":1,"gates":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	put(doc(3, "go vet ./...", 900))
	if g4, _ := read(); !strings.Contains(g4, "go vet ./...") {
		t.Fatalf("manipulated gate file not restored: %s", g4)
	}

	// Tampering that keeps the generatedBy marker is caught by the drift
	// sidecar, not skipped as "unchanged".
	g5, _ := read()
	if err := os.WriteFile(gates, []byte(strings.Replace(g5, "go vet ./...", "true", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	put(doc(4, "go vet ./...", 900))
	// Compare content, not bytes: a rewrite legitimately renews generatedAt.
	if g6, _ := read(); !strings.Contains(g6, `"go vet ./..."`) || strings.Contains(g6, `"command": "true"`) {
		t.Fatalf("drifted generated file not restored:\n%s", g6)
	}
}
