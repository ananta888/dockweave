package sshbroker

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrDenied is returned whenever policy denies issuance. Callers must not use
// it to distinguish "unknown grant" from "expired" (fail closed).
var ErrDenied = errors.New("ssh issuance denied")

// Grant authorizes one agent to reach one destination. All fields are matched
// exactly (case-insensitive host/user); no implicit wildcard.
type Grant struct {
	Agent    string   `json:"agent"`
	Host     string   `json:"host"`
	User     string   `json:"user"`
	Repo     string   `json:"repo,omitempty"`
	Commands []string `json:"commands,omitempty"`
	// TTL is a duration string (e.g. "15m"); empty means the broker default.
	TTL string `json:"ttl,omitempty"`
}

// DefaultTTL is the certificate lifetime when a grant does not specify one.
const DefaultTTL = 15 * time.Minute

// MaxTTL bounds every certificate, regardless of grant TTL.
const MaxTTL = 24 * time.Hour

// Policy is a persisted, DENY-by-default grant set.
type Policy struct {
	mu     sync.RWMutex
	grants map[string]Grant // key: agent|host|user (lowercased host/user)
}

// NewPolicy returns an empty policy.
func NewPolicy() *Policy {
	return &Policy{grants: map[string]Grant{}}
}

func grantKey(agent, host, user string) string {
	return strings.ToLower(agent + "|" + host + "|" + user)
}

// Add inserts or replaces a grant.
func (p *Policy) Add(g Grant) error {
	if g.Agent == "" || g.Host == "" || g.User == "" {
		return errors.New("sshbroker: agent, host and user are required")
	}
	if g.TTL != "" {
		if _, err := time.ParseDuration(g.TTL); err != nil {
			return fmt.Errorf("sshbroker: invalid ttl %q: %w", g.TTL, err)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.grants[grantKey(g.Agent, g.Host, g.User)] = g
	return nil
}

// Remove deletes a grant.
func (p *Policy) Remove(agent, host, user string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.grants[grantKey(agent, host, user)]
	delete(p.grants, grantKey(agent, host, user))
	return ok
}

// List returns grants, optionally filtered by agent.
func (p *Policy) List(agent string) []Grant {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Grant, 0, len(p.grants))
	for _, g := range p.grants {
		if agent == "" || g.Agent == agent {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Agent == out[j].Agent {
			if out[i].Host == out[j].Host {
				return out[i].User < out[j].User
			}
			return out[i].Host < out[j].Host
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}

// Match authorizes an agent to reach host/user and returns the matching grant.
// There is no implicit allow.
func (p *Policy) Match(agent, host, user string) (Grant, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	g, ok := p.grants[grantKey(agent, host, user)]
	return g, ok
}
