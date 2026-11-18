// Package oidc implements OpenID Connect sign-in for the DockWeave web
// gateway, structured around a small domain model (SOLID / DDD): pure domain
// types, an application service that orchestrates the login flow, a provider
// port for the IdP, and a session repository port with an encrypted-cookie
// adapter.
package oidc

import (
	"errors"
	"time"
)

// Role is a DockWeave capability derived from IdP claims.
type Role string

const (
	RoleAdmin   Role = "admin"
	RoleMonitor Role = "monitor"
)

// Principal is the minimal identity the gateway keeps. Only iss + sub (and the
// mapped roles) are retained; profile claims are never copied into sessions or
// audit data.
type Principal struct {
	Issuer  string   `json:"iss"`
	Subject string   `json:"sub"`
	Roles   []string `json:"roles,omitempty"`
}

// HasRole reports whether the principal carries the given role.
func (p Principal) HasRole(r Role) bool {
	for _, got := range p.Roles {
		if Role(got) == r {
			return true
		}
	}
	return false
}

// Session is the authenticated browser session.
type Session struct {
	ID        string    `json:"id"`
	Principal Principal `json:"principal"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Expired reports whether the session has lapsed.
func (s Session) Expired(now time.Time) bool { return now.After(s.ExpiresAt) }

// Domain errors.
var (
	ErrNoSession      = errors.New("no session")
	ErrInvalidSession = errors.New("invalid session")
	ErrInvalidState   = errors.New("invalid state")
	ErrTokenExchange  = errors.New("token exchange failed")
	ErrInvalidID      = errors.New("invalid id token")
)

// Config configures the OIDC connection.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	// RolesClaim is the id_token claim that carries the role list
	// (e.g. "groups", "roles").
	RolesClaim string
	// AdminGroups / MonitorGroups map claim values to DockWeave roles.
	AdminGroups   []string
	MonitorGroups []string
	// SessionTTL is how long a browser session stays valid.
	SessionTTL time.Duration
	// SessionKey encrypts the session cookie (32 bytes).
	SessionKey [32]byte
}
