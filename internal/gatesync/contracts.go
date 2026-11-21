package gatesync

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ContractError identifies the enabled connector that prevented a complete sync.
type ContractError struct {
	ConnectorID   string
	TargetAgentID string
	Err           error
}

func (e *ContractError) Error() string {
	return fmt.Sprintf("connector %q to agent %q: invalid gate contract: %v", e.ConnectorID, e.TargetAgentID, e.Err)
}
func (e *ContractError) Unwrap() error { return e.Err }

// canonicalDocument shares workspace identity and ID allocation between files
// and pipeline edges. Invalid contracts abort the whole replacement.
func canonicalDocument(doc Document, roots []string) (Document, error) {
	doc.Connectors = append([]Connector(nil), doc.Connectors...)
	if doc.Paperclip != nil {
		pc := *doc.Paperclip
		if ws, err := validateWorkspace(pc.Workspace, roots); err == nil {
			pc.Workspace = ws
		}
		doc.Paperclip = &pc
	}
	seen := map[string]bool{}
	for i, c := range doc.Connectors {
		if !c.Enabled || c.Kind != "gate" {
			continue
		}
		if c.ID == "" || seen[c.ID] {
			return Document{}, &ContractError{c.ID, c.TargetAgentID, fmt.Errorf("connector id must be nonempty and unique")}
		}
		seen[c.ID] = true
		raw := strings.TrimSpace(c.Contract)
		if raw == "" {
			raw = "{}"
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			return Document{}, &ContractError{c.ID, c.TargetAgentID, err}
		}
		if fields == nil {
			return Document{}, &ContractError{c.ID, c.TargetAgentID, fmt.Errorf("contract must be an object")}
		}
		var ct Contract
		if err := json.Unmarshal([]byte(raw), &ct); err != nil {
			return Document{}, &ContractError{c.ID, c.TargetAgentID, err}
		}
		if ct.Workspace != "" {
			if ws, err := validateWorkspace(ct.Workspace, roots); err == nil {
				value, err := json.Marshal(ws)
				if err != nil {
					return Document{}, fmt.Errorf("encode workspace: %w", err)
				}
				fields["workspace"] = value
			}
		}
		data, err := json.Marshal(fields)
		if err != nil {
			return Document{}, fmt.Errorf("encode contract: %w", err)
		}
		doc.Connectors[i].Contract = string(data)
	}
	return doc, nil
}
