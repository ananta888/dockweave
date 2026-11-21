package gatesync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ananta888/dockweave/internal/model"
)

// DerivePipeline derives the gate -> agent pipeline edges from the graph
// document (ADR 0002). Every enabled gate connector with a targetAgentId
// yields one edge from its agent (upstream) to the target (downstream). The
// gate id and workspace resolve exactly like DeriveAll, so the edge points at
// the gate that ends up in that workspace's gates.json; the workspace is
// canonicalised against roots.
//
// Edges are fail-closed: a gate without a command (it never runs) still
// produces an edge, so the downstream agent stays blocked with
// "unknown_gate" instead of silently running unchecked, and a warning
// explains why. Only a self edge (target == upstream) is dropped, because it
// would deadlock the agent that has to fix its own gate.
func DerivePipeline(raw []byte, roots []string) ([]model.PipelineEdge, []string, error) {
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, fmt.Errorf("graph document is not JSON: %w", err)
	}
	_, refs, _ := deriveGroups(doc)

	var (
		edges    []model.PipelineEdge
		warnings []string
	)
	seen := map[string]bool{}
	for _, c := range doc.Connectors {
		if !c.Enabled || c.Kind != "gate" {
			continue
		}
		target := strings.TrimSpace(c.TargetAgentID)
		if target == "" {
			continue
		}
		ref, ok := refs[c.ID]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("gate %q: invalid contract; the proceed edge to %q is not enforced", c.Name, target))
			continue
		}
		upstream := strings.TrimSpace(c.AgentID)
		if upstream != "" && upstream == target {
			warnings = append(warnings, fmt.Sprintf("gate %q: target agent %q is the gate's own agent; self edges would deadlock and are ignored", c.Name, target))
			continue
		}
		gateID := ref.ID
		if gateID == "" {
			// No command: the gate never runs. Keep the edge with the id the
			// gate would get, so admission reports unknown_gate (blocked).
			gateID = gateID0(c, ref.Contract)
			warnings = append(warnings, fmt.Sprintf("gate %q has a target agent but no command; %q stays blocked until the gate runs", c.Name, target))
		}
		ws := strings.TrimSpace(ref.Workspace)
		if ws == "" {
			warnings = append(warnings, fmt.Sprintf("gate %q has a target agent but no workspace; %q stays blocked", c.Name, target))
		} else if canonical, err := validateWorkspace(ws, roots); err != nil {
			warnings = append(warnings, fmt.Sprintf("gate %q: workspace %q: %v; %q stays blocked", c.Name, ws, err, target))
		} else {
			ws = canonical
		}
		ct := ref.Contract
		tAct, fAct, tReason, fReason := ResolveRouting(ct.OnTrue, ct.OnFalse, ct.ReasonTrue, ct.ReasonFalse)
		e := model.PipelineEdge{
			GateID:            gateID,
			UpstreamAgentID:   upstream,
			DownstreamAgentID: target,
			Workspace:         ws,
			OnTrue:            tAct.String(),
			OnFalse:           fAct.String(),
			ReasonTrue:        tReason,
			ReasonFalse:       fReason,
			MaxAgeSec:         max(ct.MaxAgeSec, 0),
		}
		e.ID = edgeID(e)
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		edges = append(edges, e)
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	warnings = append(warnings, pipelineCycles(edges)...)
	return edges, warnings, nil
}

// gateID0 is the id a command-less gate would get (no de-duplication).
func gateID0(c Connector, ct Contract) string { return gateID(c, ct, 0) }

// edgeID is stable for (workspace, gate, downstream) so repeated pushes of the
// same graph produce the same ids.
func edgeID(e model.PipelineEdge) string {
	sum := sha256.Sum256([]byte(e.Workspace + "\x00" + e.GateID + "\x00" + e.DownstreamAgentID))
	return "pe-" + hex.EncodeToString(sum[:8])
}

// pipelineCycles reports agent cycles (A -> gate -> B -> gate -> A). A cycle
// can deadlock: neither agent may work before the other's gate passed.
func pipelineCycles(edges []model.PipelineEdge) []string {
	next := map[string][]string{}
	for _, e := range edges {
		if e.UpstreamAgentID != "" {
			next[e.UpstreamAgentID] = append(next[e.UpstreamAgentID], e.DownstreamAgentID)
		}
	}
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var (
		out   []string
		stack []string
		seen  = map[string]bool{}
	)
	var visit func(n string)
	visit = func(n string) {
		color[n] = grey
		stack = append(stack, n)
		for _, m := range next[n] {
			switch color[m] {
			case white:
				visit(m)
			case grey:
				i := len(stack) - 1
				for i >= 0 && stack[i] != m {
					i--
				}
				cycle := append(append([]string{}, stack[i:]...), m)
				key := canonicalCycle(cycle[:len(cycle)-1])
				if !seen[key] {
					seen[key] = true
					out = append(out, fmt.Sprintf("pipeline cycle %s: these agents block each other until one gate passes", strings.Join(cycle, " -> ")))
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
	}
	nodes := make([]string, 0, len(next))
	for n := range next {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	for _, n := range nodes {
		if color[n] == white {
			visit(n)
		}
	}
	return out
}

func canonicalCycle(c []string) string {
	s := append([]string{}, c...)
	sort.Strings(s)
	return strings.Join(s, ",")
}
