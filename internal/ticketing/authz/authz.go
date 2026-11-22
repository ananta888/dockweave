// Package authz maps IdP groups to roles and roles to permissions for humans
// acting through the web gateway (ADR 0003 D-6a). The mapping lives in a
// configuration file, never in code; the daemon enforces it.
package authz

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Permission is one action a human may perform.
type Permission string

const (
	TicketRead        Permission = "ticket.read"
	TicketCreate      Permission = "ticket.create"
	TicketEdit        Permission = "ticket.edit"
	TicketComment     Permission = "ticket.comment"
	TicketAssign      Permission = "ticket.assign"
	TicketApprove     Permission = "ticket.approve"
	TicketDelete      Permission = "ticket.delete"
	GateOverride      Permission = "gate.override"
	AgentWake         Permission = "agent.wake"
	TerminalView      Permission = "terminal.view"
	TerminalTakeover  Permission = "terminal.takeover"
	ProviderConfigure Permission = "provider.configure"
	SyncRun           Permission = "sync.run"
)

// All lists every known permission.
var All = []Permission{TicketRead, TicketCreate, TicketEdit, TicketComment, TicketAssign, TicketApprove, TicketDelete,
	GateOverride, AgentWake, TerminalView, TerminalTakeover, ProviderConfigure, SyncRun}

func known(p Permission) bool {
	for _, k := range All {
		if k == p {
			return true
		}
	}
	return false
}

// Config is the file format of ticket-roles.json.
type Config struct {
	// Groups maps an IdP group to roles.
	Groups map[string][]string `json:"groups"`
	// Roles maps a role to permissions; "*" grants all.
	Roles map[string][]string `json:"roles"`
}

// Default is used when no file exists: the groups of the bundled Keycloak
// realm. Admin may do everything, monitor only read; other groups nothing.
func Default() Config {
	return Config{
		Groups: map[string][]string{"dockweave-admin": {"admin"}, "dockweave-monitor": {"reader"}},
		Roles: map[string][]string{
			"admin":    {"*"},
			"approver": {"ticket.read", "ticket.comment", "ticket.approve", "ticket.assign", "agent.wake", "terminal.view"},
			"author":   {"ticket.read", "ticket.create", "ticket.edit", "ticket.comment", "terminal.view"},
			"reader":   {"ticket.read"},
		},
	}
}

// Policy is a validated, immutable mapping.
type Policy struct {
	groups map[string][]string
	roles  map[string]map[Permission]bool
}

// Compile validates a config: unknown permissions and roles referenced by a
// group without a definition are errors (a typo must not silently deny or
// grant).
func Compile(c Config) (*Policy, error) {
	p := &Policy{groups: map[string][]string{}, roles: map[string]map[Permission]bool{}}
	for role, perms := range c.Roles {
		if strings.TrimSpace(role) == "" {
			return nil, errors.New("empty role name")
		}
		set := map[Permission]bool{}
		for _, raw := range perms {
			if raw == "*" {
				for _, k := range All {
					set[k] = true
				}
				continue
			}
			perm := Permission(raw)
			if !known(perm) {
				return nil, fmt.Errorf("role %q: unknown permission %q", role, raw)
			}
			set[perm] = true
		}
		p.roles[role] = set
	}
	for group, roles := range c.Groups {
		for _, r := range roles {
			if _, ok := p.roles[r]; !ok {
				return nil, fmt.Errorf("group %q: undefined role %q", group, r)
			}
		}
		p.groups[group] = append([]string(nil), roles...)
	}
	return p, nil
}

// ReadOnly grants every human ticket.read and nothing else (fallback when
// the configuration is invalid).
func ReadOnly() *Policy {
	return &Policy{groups: map[string][]string{"*": {"reader"}}, roles: map[string]map[Permission]bool{"reader": {TicketRead: true}}}
}

// Grant is the evaluated access of one human.
type Grant struct {
	Roles       []string     `json:"roles"`
	Permissions []Permission `json:"permissions"`
	set         map[Permission]bool
}

// Has reports whether the grant contains perm.
func (g Grant) Has(perm Permission) bool { return g.set[perm] }

// For evaluates the groups of a human. Unknown groups grant nothing.
func (p *Policy) For(groups []string) Grant {
	roleSet := map[string]bool{}
	for _, r := range p.groups["*"] {
		roleSet[r] = true
	}
	for _, g := range groups {
		for _, r := range p.groups[g] {
			roleSet[r] = true
		}
	}
	g := Grant{set: map[Permission]bool{}}
	for r := range roleSet {
		g.Roles = append(g.Roles, r)
		for perm := range p.roles[r] {
			g.set[perm] = true
		}
	}
	for perm := range g.set {
		g.Permissions = append(g.Permissions, perm)
	}
	sort.Strings(g.Roles)
	sort.Slice(g.Permissions, func(i, j int) bool { return g.Permissions[i] < g.Permissions[j] })
	return g
}

// Store loads the policy from a file and reloads it when the file changes
// (checked at most every CheckEvery). A missing file means Default(); an
// invalid file means ReadOnly() and Error() says why.
type Store struct {
	Path       string
	CheckEvery time.Duration
	// OnReload is called after a successful (re)load or a failure.
	OnReload func(err error)

	mu      sync.Mutex
	policy  *Policy
	err     error
	modTime time.Time
	size    int64
	checked time.Time
	loaded  bool
}

// Policy returns the current policy, reloading if the file changed.
func (s *Store) Policy() *Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	every := s.CheckEvery
	if every == 0 {
		every = 2 * time.Second
	}
	if s.loaded && time.Since(s.checked) < every {
		return s.policy
	}
	s.checked = time.Now()
	st, statErr := os.Stat(s.Path)
	if s.loaded {
		switch {
		case statErr != nil && s.modTime.IsZero():
			return s.policy // still no file
		case statErr == nil && st.ModTime().Equal(s.modTime) && st.Size() == s.size:
			return s.policy
		}
	}
	s.loaded = true
	if errors.Is(statErr, os.ErrNotExist) {
		pol, _ := Compile(Default())
		s.policy, s.err, s.modTime, s.size = pol, nil, time.Time{}, 0
		s.notify(nil)
		return s.policy
	}
	if statErr == nil {
		s.modTime, s.size = st.ModTime(), st.Size()
	}
	pol, err := load(s.Path)
	if err != nil {
		s.policy, s.err = ReadOnly(), fmt.Errorf("ticket roles %s: %w (humans are read-only until fixed)", s.Path, err)
	} else {
		s.policy, s.err = pol, nil
	}
	s.notify(s.err)
	return s.policy
}

func (s *Store) notify(err error) {
	if s.OnReload != nil {
		s.OnReload(err)
	}
}

// Error is the current configuration error, if any.
func (s *Store) Error() error {
	s.Policy()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func load(path string) (*Policy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	return Compile(c)
}
