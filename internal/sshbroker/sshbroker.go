package sshbroker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// Broker issues destination-bound certificates under a CA and a
// DENY-by-default policy. It is the safe entry point: policy is always
// enforced before any key material is created.
type Broker struct {
	CA     *CA
	Policy *Policy
}

// New builds a Broker.
func New(ca *CA, policy *Policy) *Broker {
	return &Broker{CA: ca, Policy: policy}
}

// Issue authorizes the request against policy and, on success, returns a
// short-lived certificate + ephemeral key. No key material is produced for a
// denied request.
func (b *Broker) Issue(agent, host, user string, ttl time.Duration) (*IssuedCert, error) {
	if b.CA == nil || b.Policy == nil {
		return nil, errors.New("sshbroker: broker not configured (ca or policy missing)")
	}
	grant, ok := b.Policy.Match(agent, host, user)
	if !ok {
		return nil, fmt.Errorf("%w: no grant for %s@%s (agent %s)", ErrDenied, user, host, agent)
	}
	eff := ttl
	if eff <= 0 && grant.TTL != "" {
		if d, err := time.ParseDuration(grant.TTL); err == nil {
			eff = d
		}
	}
	if eff <= 0 {
		eff = DefaultTTL
	}
	return b.CA.Issue(IssueRequest{Agent: agent, Host: host, User: user, TTL: eff})
}

// policyFile is the on-disk shape of a policy.
type policyFile struct {
	Version int     `json:"version"`
	Grants  []Grant `json:"grants"`
}

// SavePolicy writes the policy as JSON (0600).
func SavePolicy(path string, p *Policy) error {
	b, err := json.MarshalIndent(policyFile{Version: 1, Grants: p.List("")}, "", "  ")
	if err != nil {
		return err
	}
	return writeFile0600(path, b)
}

// LoadPolicy reads a policy file. A missing file is an empty policy.
func LoadPolicy(path string) (*Policy, error) {
	p := NewPolicy()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return p, nil
		}
		return nil, err
	}
	var f policyFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse policy %q: %w", path, err)
	}
	for _, g := range f.Grants {
		if err := p.Add(g); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func writeFile0600(path string, data []byte) error {
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
