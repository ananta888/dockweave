package gatesync

import (
	"fmt"
	"regexp"
	"strings"
)

// Routing actions for on_true/on_false (ADR 0002, decision D-2).
//
//	allow          the downstream agent may work (default for on_true)
//	deny           the downstream agent stays blocked (default for on_false)
//	wake:<agent>   allow, and the wrapper wakes <agent> (a DockWeave agent id)
//	rework         stay blocked; the wrapper opens one rework issue for the
//	               upstream agent
//
// Free text that is not an action is a legacy reason (the pre-D-2 meaning of
// on_true/on_false). It keeps working as the verdict reason, the action falls
// back to the default and the graph sync reports a warning.
const (
	ActionAllow  = "allow"
	ActionDeny   = "deny"
	ActionWake   = "wake"
	ActionRework = "rework"
)

// Action is a parsed routing action.
type Action struct {
	Kind  string `json:"kind"`            // allow|deny|wake|rework
	Agent string `json:"agent,omitempty"` // wake target
}

// String renders the canonical form (e.g. "wake:agent-b").
func (a Action) String() string {
	if a.Kind == ActionWake {
		return ActionWake + ":" + a.Agent
	}
	return a.Kind
}

// Allows reports whether the action lets the downstream agent work.
func (a Action) Allows() bool { return a.Kind == ActionAllow || a.Kind == ActionWake }

var wakeAgentRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ParseAction parses an action value. ok is false for free text (a legacy
// reason) and for a malformed wake target; empty input is not an action.
func ParseAction(s string) (Action, bool) {
	v := strings.TrimSpace(s)
	switch strings.ToLower(v) {
	case ActionAllow:
		return Action{Kind: ActionAllow}, true
	case ActionDeny:
		return Action{Kind: ActionDeny}, true
	case ActionRework:
		return Action{Kind: ActionRework}, true
	}
	if len(v) > len(ActionWake)+1 && strings.EqualFold(v[:len(ActionWake)+1], ActionWake+":") {
		agent := strings.TrimSpace(v[len(ActionWake)+1:])
		if wakeAgentRe.MatchString(agent) {
			return Action{Kind: ActionWake, Agent: agent}, true
		}
	}
	return Action{}, false
}

// ResolveRouting turns the raw on_true/on_false/reason_* values into actions
// and reasons. A legacy free-text on_* value becomes the reason (unless an
// explicit reason is set) and the default action applies.
func ResolveRouting(onTrue, onFalse, reasonTrue, reasonFalse string) (tAct, fAct Action, tReason, fReason string) {
	tAct, tReason = resolveOne(onTrue, reasonTrue, Action{Kind: ActionAllow})
	fAct, fReason = resolveOne(onFalse, reasonFalse, Action{Kind: ActionDeny})
	// A failed gate never releases work: allow/wake on the FAIL branch would
	// only report a releasing action on a blocked edge (ADR 0002 D-2).
	if fAct.Allows() {
		fAct = Action{Kind: ActionDeny}
	}
	return
}

func resolveOne(raw, reason string, def Action) (Action, string) {
	reason = strings.TrimSpace(reason)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, reason
	}
	if a, ok := ParseAction(raw); ok {
		return a, reason
	}
	if reason == "" {
		reason = raw
	}
	return def, reason
}

// routingWarnings reports legacy or malformed on_true/on_false values.
func routingWarnings(name, onTrue, onFalse string) []string {
	var out []string
	for _, f := range []struct{ field, v string }{{"on_true", onTrue}, {"on_false", onFalse}} {
		v := strings.TrimSpace(f.v)
		if v == "" {
			continue
		}
		if a, ok := ParseAction(v); ok {
			if f.field == "on_false" && a.Allows() {
				out = append(out, fmt.Sprintf("gate %q: on_false %q cannot release work after a failed gate; treated as deny (use deny or rework)", name, v))
			}
			continue
		}
		if strings.HasPrefix(strings.ToLower(v), ActionWake+":") {
			out = append(out, fmt.Sprintf("gate %q: %s %q has an invalid wake target; treated as reason text", name, f.field, v))
			continue
		}
		out = append(out, fmt.Sprintf("gate %q: %s %q is not an action (allow|deny|wake:<agent>|rework); treated as reason text, move it to reason_%s",
			name, f.field, v, strings.TrimPrefix(f.field, "on_")))
	}
	return out
}
