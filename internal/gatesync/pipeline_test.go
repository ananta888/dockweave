package gatesync

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseAction(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"allow", "allow", true},
		{" DENY ", "deny", true},
		{"rework", "rework", true},
		{"wake:agent-b", "wake:agent-b", true},
		{"WAKE: agent_b.2", "wake:agent_b.2", true},
		{"wake:", "", false},
		{"wake:bad agent", "", false},
		{"continue", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		a, ok := ParseAction(c.in)
		if ok != c.ok || (ok && a.String() != c.want) {
			t.Fatalf("ParseAction(%q) = %v,%v; want %q,%v", c.in, a, ok, c.want, c.ok)
		}
	}
}

func TestResolveRoutingDefaultsAndLegacyText(t *testing.T) {
	ta, fa, tr, fr := ResolveRouting("", "", "", "")
	if ta.String() != "allow" || fa.String() != "deny" || tr != "" || fr != "" {
		t.Fatalf("defaults = %v %v %q %q", ta, fa, tr, fr)
	}
	ta, fa, tr, fr = ResolveRouting("tests green", "rework", "", "fix the tests")
	if ta.String() != "allow" || tr != "tests green" {
		t.Fatalf("legacy on_true must become the reason with default action: %v %q", ta, tr)
	}
	if fa.String() != "rework" || fr != "fix the tests" {
		t.Fatalf("on_false = %v %q", fa, fr)
	}
	// An explicit reason wins over legacy text.
	_, _, tr, _ = ResolveRouting("legacy", "", "explicit", "")
	if tr != "explicit" {
		t.Fatalf("reason = %q, want explicit", tr)
	}
}

func TestDeriveWarnsAboutLegacyRoutingText(t *testing.T) {
	doc := Document{Connectors: []Connector{
		{ID: "c1", Kind: "gate", Name: "Tests", Enabled: true,
			Contract: `{"command":"npm test","on_true":"continue","on_false":"wake:bad agent"}`},
	}}
	cfg, warnings := Derive(doc)
	if len(cfg.Gates) != 1 || cfg.Gates[0].OnTrue != "continue" {
		t.Fatalf("legacy value must still be carried to gates.json: %+v", cfg.Gates)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "on_true") || !strings.Contains(joined, "invalid wake target") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func pipelineDoc(t *testing.T, ws string, conns ...Connector) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"connectors": conns,
		"paperclip":  map[string]any{"workspace": ws, "syncGates": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDerivePipelineBuildsEdges(t *testing.T) {
	ws := t.TempDir()
	raw := pipelineDoc(t, ws,
		Connector{ID: "g1", Kind: "gate", Name: "Tests", Enabled: true, AgentID: "a", TargetAgentID: "b",
			Contract: `{"gate_id":"tests","command":"npm test","on_false":"rework","reason_false":"fix tests","max_age_sec":600}`},
		Connector{ID: "g2", Kind: "gate", Name: "Lint", Enabled: true, AgentID: "a",
			Contract: `{"command":"npm run lint"}`}, // no target: no edge
		Connector{ID: "g3", Kind: "gate", Name: "Off", Enabled: false, AgentID: "a", TargetAgentID: "c",
			Contract: `{"command":"true"}`}, // disabled: no edge
	)
	edges, warnings, err := DerivePipeline(raw, []string{ws})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if len(edges) != 1 {
		t.Fatalf("edges = %+v", edges)
	}
	e := edges[0]
	if e.GateID != "tests" || e.UpstreamAgentID != "a" || e.DownstreamAgentID != "b" || e.Workspace == "" ||
		e.OnTrue != "allow" || e.OnFalse != "rework" || e.ReasonFalse != "fix tests" || e.MaxAgeSec != 600 || e.ID == "" {
		t.Fatalf("edge = %+v", e)
	}
	again, _, _ := DerivePipeline(raw, []string{ws})
	if again[0].ID != e.ID {
		t.Fatal("edge id must be stable across derivations")
	}
}

func TestDerivePipelineGateIDMatchesGatesJSON(t *testing.T) {
	ws := t.TempDir()
	raw := pipelineDoc(t, ws,
		Connector{ID: "g1", Kind: "gate", Name: "Tests", Enabled: true, AgentID: "a", Contract: `{"command":"x"}`},
		Connector{ID: "g2", Kind: "gate", Name: "Tests", Enabled: true, AgentID: "a", TargetAgentID: "b", Contract: `{"command":"y"}`},
	)
	edges, _, _ := DerivePipeline(raw, []string{ws})
	var doc Document
	_ = json.Unmarshal(raw, &doc)
	cfg, _ := Derive(doc)
	if len(edges) != 1 || len(cfg.Gates) != 2 || edges[0].GateID != cfg.Gates[1].ID || edges[0].GateID != "tests-2" {
		t.Fatalf("edge gate id %+v must match de-duplicated gates.json id %+v", edges, cfg.Gates)
	}
}

func TestDerivePipelineFailClosedAndWarnings(t *testing.T) {
	ws := t.TempDir()
	raw := pipelineDoc(t, ws,
		// No command: edge kept (blocked as unknown_gate) + warning.
		Connector{ID: "g1", Kind: "gate", Name: "Review", Enabled: true, AgentID: "a", TargetAgentID: "b",
			Contract: `{"review_required":true}`},
		// Self edge: dropped + warning.
		Connector{ID: "g2", Kind: "gate", Name: "Self", Enabled: true, AgentID: "a", TargetAgentID: "a",
			Contract: `{"command":"true"}`},
		// Cycle b -> a.
		Connector{ID: "g3", Kind: "gate", Name: "Back", Enabled: true, AgentID: "b", TargetAgentID: "a",
			Contract: `{"command":"true"}`},
		// Workspace outside the roots: edge kept with the raw path + warning.
		Connector{ID: "g4", Kind: "gate", Name: "Elsewhere", Enabled: true, AgentID: "c", TargetAgentID: "d",
			Contract: `{"command":"true","workspace":"/definitely/not/here"}`},
	)
	edges, warnings, err := DerivePipeline(raw, []string{ws})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 3 {
		t.Fatalf("edges = %+v, want 3 (no-command, back, elsewhere)", edges)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"no command", "self edges", "pipeline cycle", "/definitely/not/here"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing warning %q in %v", want, warnings)
		}
	}
	for _, e := range edges {
		if e.DownstreamAgentID == "b" && e.GateID != "review" {
			t.Fatalf("command-less gate id = %q, want review", e.GateID)
		}
	}
}

func TestDerivePipelineRejectsInvalidJSON(t *testing.T) {
	if _, _, err := DerivePipeline([]byte("{"), nil); err == nil {
		t.Fatal("expected error")
	}
}
