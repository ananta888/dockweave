// Package credentialbroker implements DockWeave's credential broker: an
// HTTP gateway that keeps provider credentials outside agent containers and
// injects them only for authorized, policy-checked requests.
package credentialbroker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ErrDenied is returned whenever policy denies a request. Callers must not
// use it to distinguish "not found" from "not allowed" in responses.
var ErrDenied = errors.New("credential access denied")

// Limits are per-grant abuse and cost controls. Zero values disable the
// corresponding limit except MaxRequestsPerMinute and MaxConcurrent, where
// zero means "use broker defaults" (see DefaultLimits).
type Limits struct {
	MaxRequestsPerMinute int     `json:"max_requests_per_minute,omitempty"`
	MaxConcurrent        int     `json:"max_concurrent,omitempty"`
	MaxRequestsPerDay    int     `json:"max_requests_per_day,omitempty"`
	MaxRequestsPerMonth  int     `json:"max_requests_per_month,omitempty"`
	MaxTokensPerDay      int64   `json:"max_tokens_per_day,omitempty"`
	MaxTokensPerMonth    int64   `json:"max_tokens_per_month,omitempty"`
	MaxCostUSDPerDay     float64 `json:"max_cost_usd_per_day,omitempty"`
	MaxCostUSDPerMonth   float64 `json:"max_cost_usd_per_month,omitempty"`
	PromptUSDPer1K       float64 `json:"prompt_usd_per_1k,omitempty"`
	CompletionUSDPer1K   float64 `json:"completion_usd_per_1k,omitempty"`
}

// DefaultLimits returns conservative defaults for a grant that omits limits.
func DefaultLimits() Limits {
	return Limits{MaxRequestsPerMinute: 60, MaxConcurrent: 4}
}

// Grant authorizes one agent to use one credential for one provider.
// Models restricts LLM model identifiers; ["*"] allows any model and an empty
// list denies every request that carries a model field.
type Grant struct {
	ID         string     `json:"id"`
	Agent      string     `json:"agent"`
	Credential string     `json:"credential"`
	Provider   string     `json:"provider"`
	Models     []string   `json:"models,omitempty"`
	Limits     Limits     `json:"limits"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// Expired reports whether the grant is past its expiry.
func (g Grant) Expired(now time.Time) bool {
	return g.ExpiresAt != nil && now.After(*g.ExpiresAt)
}

// AllowsModel applies the model allowlist. A request without a model (for
// non-LLM APIs) is not model-checked; a request with a model is denied unless
// explicitly allowed.
func AllowsModel(model string) func([]string) bool {
	return func(models []string) bool {
		if model == "" {
			return true
		}
		for _, m := range models {
			if m == "*" || m == model {
				return true
			}
		}
		return false
	}
}

type policyFile struct {
	Version int     `json:"version"`
	Grants  []Grant `json:"grants"`
}

// Policy is a persisted, DENY-by-default grant set. It is separate from the
// root daemon's filesystem grants: credential permissions are managed by the
// operator through the broker's admin socket.
type Policy struct {
	mu     sync.RWMutex
	path   string
	grants map[string]Grant
}

// OpenPolicy loads grants from path. A missing file is an empty policy.
func OpenPolicy(path string) (*Policy, error) {
	p := &Policy{path: path, grants: map[string]Grant{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return p, nil
		}
		return nil, fmt.Errorf("read policy: %w", err)
	}
	var f policyFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	for _, g := range f.Grants {
		p.grants[g.ID] = g
	}
	return p, nil
}

func (p *Policy) saveLocked() error {
	list := make([]Grant, 0, len(p.grants))
	for _, g := range p.grants {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Agent == list[j].Agent {
			return list[i].Credential < list[j].Credential
		}
		return list[i].Agent < list[j].Agent
	})
	b, err := json.MarshalIndent(policyFile{Version: 1, Grants: list}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(p.path, b, 0o600)
}

// Add creates or replaces a grant. An empty ID is generated.
func (p *Policy) Add(g Grant) (Grant, error) {
	if g.Agent == "" || g.Credential == "" || g.Provider == "" {
		return Grant{}, errors.New("agent, credential and provider are required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().UTC()
	if g.ID == "" {
		g.ID = "cg_" + randomHex(12)
		g.CreatedAt = now
	} else if old, ok := p.grants[g.ID]; ok {
		g.CreatedAt = old.CreatedAt
	} else {
		g.CreatedAt = now
	}
	g.UpdatedAt = now
	if g.Limits.MaxRequestsPerMinute == 0 {
		g.Limits.MaxRequestsPerMinute = DefaultLimits().MaxRequestsPerMinute
	}
	if g.Limits.MaxConcurrent == 0 {
		g.Limits.MaxConcurrent = DefaultLimits().MaxConcurrent
	}
	p.grants[g.ID] = g
	if err := p.saveLocked(); err != nil {
		return Grant{}, err
	}
	return g, nil
}

// Remove deletes a grant by ID or by agent+credential.
func (p *Policy) Remove(idOrAgent, credential string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if credential == "" {
		if _, ok := p.grants[idOrAgent]; !ok {
			return errors.New("grant not found")
		}
		delete(p.grants, idOrAgent)
	} else {
		found := false
		for id, g := range p.grants {
			if g.Agent == idOrAgent && g.Credential == credential {
				delete(p.grants, id)
				found = true
			}
		}
		if !found {
			return errors.New("grant not found")
		}
	}
	return p.saveLocked()
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
			return out[i].Credential < out[j].Credential
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}

// Get looks up a grant by ID.
func (p *Policy) Get(id string) (Grant, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	g, ok := p.grants[id]
	return g, ok
}

// Match authorizes an agent to use provider/model. It returns the first
// matching, unexpired grant or ErrDenied. There is no implicit allow.
func (p *Policy) Match(agent, provider, model string, now time.Time) (Grant, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, g := range p.grants {
		if g.Agent != agent || g.Provider != provider {
			continue
		}
		if g.Expired(now) {
			continue
		}
		if !AllowsModel(model)(g.Models) {
			continue
		}
		return g, nil
	}
	return Grant{}, ErrDenied
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
