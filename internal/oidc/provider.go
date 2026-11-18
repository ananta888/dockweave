package oidc

import (
	"context"
	"fmt"
	"net/url"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Provider is the port the application service depends on. It hides the
// go-oidc/oauth2 specifics so the flow can be tested with a fake.
type Provider interface {
	AuthURL(state, nonce, codeChallenge string) (string, error)
	// Exchange verifies the id_token and binds it to the expected nonce.
	Exchange(ctx context.Context, code, codeVerifier, nonce string) (*Principal, error)
	EndSessionURL(state string) string
}

type idClaims struct {
	Issuer  string   `json:"iss"`
	Subject string   `json:"sub"`
	Groups  []string `json:"groups"`
	Roles   []string `json:"roles"`
}

// idpProvider is the real OpenID Connect adapter.
type idpProvider struct {
	provider   *oidc.Provider
	verifier   *oidc.IDTokenVerifier
	oauth      oauth2.Config
	cfg        Config
	endSession string
}

// NewProvider performs OIDC discovery and returns a ready provider.
func NewProvider(ctx context.Context, cfg Config) (Provider, error) {
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile"}
	}
	p, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	prov := &idpProvider{
		provider: p,
		verifier: p.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     p.Endpoint(),
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
		},
		cfg: cfg,
	}
	var disco struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&disco)
	prov.endSession = disco.EndSessionEndpoint
	return prov, nil
}

func (p *idpProvider) AuthURL(state, nonce, codeChallenge string) (string, error) {
	return p.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	), nil
}

func (p *idpProvider) Exchange(ctx context.Context, code, codeVerifier, nonce string) (*Principal, error) {
	tok, err := p.oauth.Exchange(ctx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenExchange, err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("%w: id_token missing", ErrTokenExchange)
	}
	idToken, err := p.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidID, err)
	}
	if nonce != "" && idToken.Nonce != nonce {
		return nil, fmt.Errorf("%w: nonce mismatch", ErrInvalidID)
	}
	var claims idClaims
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidID, err)
	}
	return &Principal{
		Issuer:  claims.Issuer,
		Subject: claims.Subject,
		Roles:   p.mapRoles(claims),
	}, nil
}

func (p *idpProvider) mapRoles(c idClaims) []string {
	raw := c.Groups
	if p.cfg.RolesClaim == "roles" {
		raw = c.Roles
	}
	in := func(v string, set []string) bool {
		for _, s := range set {
			if s == v {
				return true
			}
		}
		return false
	}
	var out []string
	for _, v := range raw {
		if in(v, p.cfg.AdminGroups) {
			out = append(out, string(RoleAdmin))
		}
		if in(v, p.cfg.MonitorGroups) {
			out = append(out, string(RoleMonitor))
		}
	}
	return dedupe(out)
}

func (p *idpProvider) EndSessionURL(state string) string {
	if p.endSession == "" {
		return "/"
	}
	u, err := url.Parse(p.endSession)
	if err != nil {
		return "/"
	}
	if state != "" {
		q := u.Query()
		q.Set("state", state)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
