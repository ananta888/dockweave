package gatesync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func gateConnector(name, contract string, enabled bool) Connector {
	return Connector{ID: "conn-" + slug(name), Kind: "gate", Name: name, Enabled: enabled, Contract: contract}
}

func TestDeriveBuildsGatesFromConnectors(t *testing.T) {
	doc := Document{Connectors: []Connector{
		gateConnector("Build", `{"command":"npm run build","cwd":".","timeout_sec":120,"review_required":true}`, true),
		gateConnector("Human", `{"human_approval_required":true,"expression":"budget < 5"}`, true),
		gateConnector("Disabled", `{"command":"echo no"}`, false),
		{ID: "conn-http", Kind: "http", Name: "API", Enabled: true, Contract: `{"command":"echo no"}`},
		gateConnector("Broken", `{not json`, true),
	}}

	cfg, warnings := Derive(doc)

	if len(cfg.Gates) != 1 {
		t.Fatalf("gates = %d, want 1 (%+v)", len(cfg.Gates), cfg.Gates)
	}
	g := cfg.Gates[0]
	if g.ID != "build" || g.Command != "npm run build" || g.When != "after_run" || g.TimeoutSec != 120 || g.Cwd != "." {
		t.Fatalf("unexpected gate: %+v", g)
	}
	if !cfg.Review.Required || cfg.Review.IssueMatch != "review" {
		t.Fatalf("review = %+v, want required with default issue_match", cfg.Review)
	}
	if !cfg.HumanApproval.Required {
		t.Fatal("human approval not derived from the second gate")
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want two (broken contract + descriptive-only expression)", warnings)
	}
	combined := strings.Join(warnings, "\n")
	if !strings.Contains(combined, "Broken") || !strings.Contains(combined, "Human") {
		t.Fatalf("warnings = %v, want a broken-contract and a descriptive-expression warning", warnings)
	}
}

func TestSyncWritesConfigAndClearsOwnFile(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "project")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := Document{
		Paperclip:  &Settings{Workspace: workspace, SyncGates: boolPtr(true)},
		Connectors: []Connector{gateConnector("Build", `{"command":"npm run build"}`, true)},
	}
	raw, _ := json.Marshal(doc)

	res := Sync(raw, []string{root})
	if res.Error != "" || !res.Written {
		t.Fatalf("sync failed: %+v", res)
	}
	path := filepath.Join(workspace, ".paperclip", "gates.json")
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(saved, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.GeneratedBy != GeneratedBy || len(cfg.Gates) != 1 {
		t.Fatalf("unexpected written config: %+v", cfg)
	}

	// Removing every graph gate must clear our own file instead of leaving a
	// stale build gate behind.
	doc.Connectors = nil
	raw, _ = json.Marshal(doc)
	res = Sync(raw, []string{root})
	if !res.Written {
		t.Fatalf("expected the previously generated file to be rewritten empty: %+v", res)
	}
	saved, _ = os.ReadFile(path)
	if err := json.Unmarshal(saved, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Gates) != 0 {
		t.Fatalf("stale gates remain: %+v", cfg.Gates)
	}
}

func TestSyncLeavesHandMaintainedConfigUntouched(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(workspace, ".paperclip"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, ".paperclip", "gates.json")
	original := []byte(`{"version":1,"gates":[{"id":"manual","when":"after_run","command":"make test"}]}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	doc := Document{Paperclip: &Settings{Workspace: workspace, SyncGates: boolPtr(true)}}
	raw, _ := json.Marshal(doc)
	res := Sync(raw, []string{root})
	if !res.Skipped || res.Written {
		t.Fatalf("expected skip, got %+v", res)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatalf("hand-maintained file was modified:\n%s", after)
	}
}

func TestSyncMergesHandMaintainedGatesOnFirstWrite(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(workspace, ".paperclip"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, ".paperclip", "gates.json")
	manual := `{"version":1,"review":{"required":false},"human_approval":{"required":false},` +
		`"gates":[{"id":"tests","when":"after_run","command":"npm test","cwd":".","timeout_sec":60},` +
		`{"id":"build","when":"after_run","command":"make build","cwd":".","timeout_sec":60}]}`
	if err := os.WriteFile(path, []byte(manual), 0o644); err != nil {
		t.Fatal(err)
	}

	doc := Document{
		Paperclip:  &Settings{Workspace: workspace, SyncGates: boolPtr(true)},
		Connectors: []Connector{gateConnector("build", `{"command":"npm run build","review_required":true}`, true)},
	}
	raw, _ := json.Marshal(doc)
	res := Sync(raw, []string{root})
	if res.Error != "" || !res.Written {
		t.Fatalf("sync failed: %+v", res)
	}

	var cfg Config
	saved, _ := os.ReadFile(path)
	if err := json.Unmarshal(saved, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Gates) != 2 {
		t.Fatalf("expected graph gate + preserved manual gate, got %+v", cfg.Gates)
	}
	if !hasGateID(cfg.Gates, "tests") {
		t.Fatalf("manual tests gate was dropped: %+v", cfg.Gates)
	}
	for _, g := range cfg.Gates {
		if g.ID == "build" && g.Command != "npm run build" {
			t.Fatalf("graph gate did not win over the manual one: %+v", g)
		}
	}
	if !cfg.Review.Required {
		t.Fatal("review flag from the graph was not applied")
	}
	if cfg.GeneratedBy != GeneratedBy {
		t.Fatal("merged file was not marked as generated")
	}
}

func TestSyncRejectsWorkspaceOutsideRoots(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	doc := Document{
		Paperclip:  &Settings{Workspace: outside, SyncGates: boolPtr(true)},
		Connectors: []Connector{gateConnector("Build", `{"command":"true"}`, true)},
	}
	raw, _ := json.Marshal(doc)
	res := Sync(raw, []string{allowed})
	if res.Error == "" || res.Written {
		t.Fatalf("expected a rejected workspace, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(outside, ".paperclip", "gates.json")); !os.IsNotExist(err) {
		t.Fatal("file was written outside the allowed roots")
	}
}

func TestSyncDisabled(t *testing.T) {
	doc := Document{
		Paperclip:  &Settings{Workspace: "/tmp", SyncGates: boolPtr(false)},
		Connectors: []Connector{gateConnector("Build", `{"command":"true"}`, true)},
	}
	raw, _ := json.Marshal(doc)
	res := Sync(raw, []string{"/tmp"})
	if !res.Skipped || res.Written {
		t.Fatalf("expected skip, got %+v", res)
	}
}

func TestSyncAllFansOutPerGateWorkspace(t *testing.T) {
	root := t.TempDir()
	def := filepath.Join(root, "default")
	alt := filepath.Join(root, "alt")
	for _, d := range []string{def, alt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	doc := Document{
		Paperclip: &Settings{Workspace: def, SyncGates: boolPtr(true)},
		Connectors: []Connector{
			gateConnector("Build", `{"command":"make build"}`, true),
			{ID: "conn-alt", Kind: "gate", Name: "Lint", Enabled: true,
				Contract: `{"command":"make lint","workspace":"` + alt + `"}`},
		},
	}
	raw, _ := json.Marshal(doc)
	results, _ := SyncAll(raw, nil, []string{root})
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2: %+v", len(results), results)
	}
	defPath := filepath.Join(def, ".paperclip", "gates.json")
	altPath := filepath.Join(alt, ".paperclip", "gates.json")
	var defCfg, altCfg Config
	if b, err := os.ReadFile(defPath); err == nil {
		_ = json.Unmarshal(b, &defCfg)
	}
	if b, err := os.ReadFile(altPath); err == nil {
		_ = json.Unmarshal(b, &altCfg)
	}
	if len(defCfg.Gates) != 1 || defCfg.Gates[0].Command != "make build" {
		t.Fatalf("default workspace config = %+v", defCfg)
	}
	if len(altCfg.Gates) != 1 || altCfg.Gates[0].Command != "make lint" {
		t.Fatalf("alternate workspace config = %+v", altCfg)
	}
}

func TestDeriveUsesStableGateID(t *testing.T) {
	doc := Document{Connectors: []Connector{
		{ID: "conn-x", Kind: "gate", Name: "Renamed", Enabled: true,
			Contract: `{"gate_id":"stable-build","command":"make build"}`},
	}}
	cfg, _ := Derive(doc)
	if len(cfg.Gates) != 1 || cfg.Gates[0].ID != "stable-build" {
		t.Fatalf("gate id = %+v, want stable-build", cfg.Gates)
	}
}

func TestDeriveCarriesExpressionContract(t *testing.T) {
	doc := Document{Connectors: []Connector{
		{ID: "conn-g", Kind: "gate", Name: "Tests", Enabled: true,
			Contract: `{"command":"npm test","expression":"command == 0 && budget < 5","on_true":"continue","on_false":"rework"}`},
	}}
	cfg, _ := Derive(doc)
	if len(cfg.Gates) != 1 {
		t.Fatalf("gates = %d, want 1", len(cfg.Gates))
	}
	g := cfg.Gates[0]
	if g.Expression != "command == 0 && budget < 5" || g.OnTrue != "continue" || g.OnFalse != "rework" {
		t.Fatalf("expression contract not carried: %+v", g)
	}
}

func readConfig(t *testing.T, ws string) Config {
	t.Helper()
	var cfg Config
	b, err := os.ReadFile(filepath.Join(ws, ".paperclip", "gates.json"))
	if err != nil {
		t.Fatalf("read gates.json in %s: %v", ws, err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestSyncAllMergesSameCanonicalWorkspace(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "ws-link")
	if err := os.Symlink(ws, link); err != nil {
		t.Fatal(err)
	}
	doc := Document{
		Paperclip: &Settings{Workspace: ws, SyncGates: boolPtr(true)},
		Connectors: []Connector{
			gateConnector("Build", `{"command":"make build"}`, true),
			{ID: "c2", Kind: "gate", Name: "Lint", Enabled: true, Contract: `{"command":"make lint","workspace":"` + link + `"}`},
			{ID: "c3", Kind: "gate", Name: "Build", Enabled: true, Contract: `{"command":"make build2","workspace":"` + ws + `/."}`},
		},
	}
	raw, _ := json.Marshal(doc)
	results, _ := SyncAll(raw, nil, []string{root})
	if len(results) != 1 || !results[0].Written {
		t.Fatalf("results = %+v, want one written result", results)
	}
	cfg := readConfig(t, ws)
	var ids []string
	for _, g := range cfg.Gates {
		ids = append(ids, g.ID)
	}
	if len(ids) != 3 {
		t.Fatalf("merged gate ids = %v", ids)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate gate id %q in %v", id, ids)
		}
		seen[id] = true
	}
}

func TestSyncAllClearsWorkspaceDroppedSincePreviousSave(t *testing.T) {
	root := t.TempDir()
	def := filepath.Join(root, "default")
	alt := filepath.Join(root, "alt")
	hand := filepath.Join(root, "hand")
	for _, d := range []string{def, alt, hand} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A hand-maintained config that a previous graph pointed at but never
	// wrote (sync never owned it) must stay untouched.
	if err := os.MkdirAll(filepath.Join(hand, ".paperclip"), 0o755); err != nil {
		t.Fatal(err)
	}
	handCfg := `{"version":1,"gates":[{"id":"manual","when":"after_run","command":"true","cwd":".","timeout_sec":5}]}`
	if err := os.WriteFile(filepath.Join(hand, ".paperclip", "gates.json"), []byte(handCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := Document{
		Paperclip: &Settings{Workspace: def, SyncGates: boolPtr(true)},
		Connectors: []Connector{
			gateConnector("Build", `{"command":"make build"}`, true),
			{ID: "c2", Kind: "gate", Name: "Lint", Enabled: true, Contract: `{"command":"make lint","workspace":"` + alt + `"}`},
		},
	}
	prevRaw, _ := json.Marshal(prev)
	if _, _ = SyncAll(prevRaw, nil, []string{root}); len(readConfig(t, alt).Gates) != 1 {
		t.Fatal("alt workspace should have received the lint gate")
	}
	// The lint gate loses its override; a stale hand target appears in prev.
	prev.Connectors = append(prev.Connectors, Connector{ID: "c3", Kind: "gate", Name: "H", Enabled: true,
		Contract: `{"command":"x","workspace":"` + hand + `"}`})
	prevRaw, _ = json.Marshal(prev)
	next := Document{
		Paperclip:  &Settings{Workspace: def, SyncGates: boolPtr(true)},
		Connectors: []Connector{gateConnector("Build", `{"command":"make build"}`, true), gateConnector("Lint", `{"command":"make lint"}`, true)},
	}
	nextRaw, _ := json.Marshal(next)
	results, _ := SyncAll(nextRaw, prevRaw, []string{root})
	if got := readConfig(t, alt); len(got.Gates) != 0 || got.GeneratedBy != GeneratedBy {
		t.Fatalf("dropped workspace not cleared: %+v (results %+v)", got, results)
	}
	if got := readConfig(t, def); len(got.Gates) != 2 {
		t.Fatalf("default workspace gates = %+v", got.Gates)
	}
	if got := readConfig(t, hand); len(got.Gates) != 1 || got.Gates[0].ID != "manual" || got.GeneratedBy != "" {
		t.Fatalf("hand-maintained config was modified: %+v", got)
	}
}

func TestSyncAllWarnsAboutUnroutedGatesOnce(t *testing.T) {
	root := t.TempDir()
	alt := filepath.Join(root, "alt")
	other := filepath.Join(root, "other")
	for _, d := range []string{alt, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	doc := Document{
		Paperclip: &Settings{SyncGates: boolPtr(true)},
		Connectors: []Connector{
			gateConnector("Orphan", `{"command":"make orphan"}`, true),
			gateConnector("Descr", `{"expression":"command == 0"}`, true),
			{ID: "c2", Kind: "gate", Name: "Lint", Enabled: true, Contract: `{"command":"make lint","workspace":"` + alt + `"}`},
			{ID: "c3", Kind: "gate", Name: "Test", Enabled: true, Contract: `{"command":"make test","workspace":"` + other + `"}`},
		},
	}
	raw, _ := json.Marshal(doc)
	results, warnings := SyncAll(raw, nil, []string{root})
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	for _, r := range results {
		if len(r.Warnings) != 0 {
			t.Fatalf("document warnings must not be repeated per workspace: %+v", r)
		}
	}
	var unrouted, descr int
	for _, w := range warnings {
		if strings.Contains(w, "no target workspace") {
			unrouted++
		}
		if strings.Contains(w, "Descr") {
			descr++
		}
	}
	if unrouted != 1 || descr != 1 {
		t.Fatalf("warnings = %v", warnings)
	}
}
