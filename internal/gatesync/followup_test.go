package gatesync

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNoopSyncPreservesGateBytesAndDriftSidecar(t *testing.T) {
	ws := t.TempDir()
	raw := pipelineDoc(t, ws, Connector{ID: "g1", Kind: "gate", Enabled: true, Contract: `{"command":"true"}`})
	if got := Sync(raw, []string{ws}); !got.Written {
		t.Fatalf("initial sync: %+v", got)
	}
	path := filepath.Join(ws, gatesRelPath)
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(old, &cfg); err != nil {
		t.Fatal(err)
	}
	// Make a new timestamp distinguishable without sleeps or clock assumptions.
	cfg.GeneratedAt = "2000-01-01T00:00:00Z"
	old, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	old = append(old, '\n')
	if err := os.WriteFile(path, old, 0o644); err != nil {
		t.Fatal(err)
	}
	sidePath := filepath.Join(ws, ".paperclip", ".gates.dockweave.json")
	side := []byte(fmt.Sprintf("{\"hash\":\"%x\",\"generatedAt\":%q}\n", sha256.Sum256(old), cfg.GeneratedAt))
	if err := os.WriteFile(sidePath, side, 0o644); err != nil {
		t.Fatal(err)
	}
	got := Sync(raw, []string{ws})
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	currentSide, err := os.ReadFile(sidePath)
	if err != nil {
		t.Fatal(err)
	}
	if got.Error != "" || got.Written || !bytes.Equal(old, current) || !bytes.Equal(side, currentSide) {
		t.Fatalf("no-op rewrote evidence: %+v", got)
	}
}

func TestMalformedContractCannotClearAnyWorkspace(t *testing.T) {
	for _, contract := range []string{"{", "null", "[]"} {
		t.Run(contract, func(t *testing.T) {
			ws := t.TempDir()
			valid := Connector{ID: "valid", Kind: "gate", Enabled: true, AgentID: "a", TargetAgentID: "b", Contract: `{"command":"true"}`}
			before := pipelineDoc(t, ws, valid)
			if got := Sync(before, []string{ws}); !got.Written {
				t.Fatal(got)
			}
			old, err := os.ReadFile(filepath.Join(ws, gatesRelPath))
			if err != nil {
				t.Fatal(err)
			}
			invalid := valid
			invalid.ID = "bad"
			invalid.Contract = contract
			raw := pipelineDoc(t, ws, valid, invalid)
			results, _ := SyncAll(raw, before, []string{ws})
			if len(results) != 1 || results[0].Error == "" {
				t.Fatalf("invalid contract accepted: %+v", results)
			}
			if edges, _, err := DerivePipeline(raw, []string{ws}); err == nil || len(edges) != 0 {
				t.Fatalf("partial edge replacement: %v, %v", edges, err)
			}
			now, err := os.ReadFile(filepath.Join(ws, gatesRelPath))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(old, now) {
				t.Fatal("invalid contract changed previously protected workspace")
			}
		})
	}
}

func TestPipelineBindingsUseCanonicalDeduplicatedGateIdentity(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	alias := filepath.Join(root, "alias")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ws, alias); err != nil {
		t.Fatal(err)
	}
	first := Connector{ID: "g1", Kind: "gate", Enabled: true, TargetAgentID: "b", Contract: `{"gate_id":"same","command":"true"}`}
	second := first
	second.ID = "g2"
	second.Contract = fmt.Sprintf(`{"gate_id":"same","command":"false","workspace":%q}`, alias)
	raw := pipelineDoc(t, ws, first, second)
	results, _ := SyncAll(raw, nil, []string{root})
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("sync: %+v", results)
	}
	edges, bindings, _, err := DerivePipelineBindings(raw, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 || bindings["g1"] == bindings["g2"] {
		t.Fatalf("ambiguous bindings: %+v %+v", edges, bindings)
	}
	cfg := readConfig(t, ws)
	for _, edge := range edges {
		if edge.Workspace != ws || !hasGateID(cfg.Gates, edge.GateID) {
			t.Fatalf("edge has no corresponding generated gate: %+v", edge)
		}
	}
}

func TestSyncRepairsDriftAndPreservesManifestScope(t *testing.T) {
	ws := t.TempDir()
	raw := pipelineDoc(t, ws, Connector{ID: "g", Kind: "gate", Enabled: true, Contract: `{"command":"true","deny_new_dependencies":true,"dependency_manifests":["ui/web/package.json"]}`})
	if got := Sync(raw, []string{ws}); !got.Written {
		t.Fatal(got)
	}
	path := filepath.Join(ws, gatesRelPath)
	if err := os.WriteFile(path, []byte(`{"generatedBy":"dockweave-graph","gates":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Sync(raw, []string{ws}); got.Error != "" || !got.Written {
		t.Fatal(got)
	}
	cfg := readConfig(t, ws)
	if len(cfg.Gates) != 1 || len(cfg.DependencyManifests) != 1 || cfg.DependencyManifests[0] != "ui/web/package.json" {
		t.Fatalf("restored config: %+v", cfg)
	}
}
