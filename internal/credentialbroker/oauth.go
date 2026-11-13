package credentialbroker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/credentialvault"
)

// oauthBundle is the secret value format for OAuth-backed providers. It is
// stored encrypted in the vault like any other secret; the agent never sees it.
type oauthBundle struct {
	Type         string    `json:"type,omitempty"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
}

func parseOAuthBundle(value string) (*oauthBundle, error) {
	var b oauthBundle
	if err := json.Unmarshal([]byte(value), &b); err != nil {
		return nil, errors.New("credential is not an OAuth bundle")
	}
	if b.AccessToken == "" && b.RefreshToken == "" {
		return nil, errors.New("OAuth bundle has no tokens")
	}
	return &b, nil
}

func (b *oauthBundle) marshal() (string, error) {
	b.Type = "oauth"
	raw, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// valid reports whether the access token is present and not near expiry.
func (b *oauthBundle) valid(now time.Time, skew time.Duration) bool {
	if b.AccessToken == "" {
		return false
	}
	if b.ExpiresAt.IsZero() {
		// No explicit expiry: fall back to the JWT exp claim when present.
		if exp, ok := jwtExpiry(b.AccessToken); ok {
			return now.Add(skew).Before(exp)
		}
		// Unknown lifetime; assume it is usable. A 401 from upstream surfaces
		// to the agent and the next request can refresh after a forced expiry.
		return true
	}
	return now.Add(skew).Before(b.ExpiresAt)
}

type oauthRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
}

// injectCredential sets the upstream authentication headers. For OAuth
// providers it refreshes an expired access token first and persists the
// rotated bundle back into the vault.
func (s *Server) injectCredential(ctx context.Context, req *http.Request, p Provider, secret credentialvault.Secret) error {
	if p.OAuth == nil {
		req.Header.Set(p.AuthHeader, p.AuthPrefix+secret.Value)
		for k, v := range p.Headers {
			req.Header.Set(k, v)
		}
		return nil
	}

	b, err := s.freshOAuthBundle(ctx, p, secret)
	if err != nil {
		return err
	}
	req.Header.Set(p.AuthHeader, p.AuthPrefix+b.AccessToken)
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	if p.OAuth.AccountIDHeader != "" {
		id := b.AccountID
		if id == "" {
			id = accountIDFromTokens(b)
		}
		if id != "" {
			req.Header.Set(p.OAuth.AccountIDHeader, id)
		}
	}
	return nil
}

// freshOAuthBundle returns a usable bundle, refreshing it under a lock so
// concurrent requests do not race on a rotating refresh token.
func (s *Server) freshOAuthBundle(ctx context.Context, p Provider, secret credentialvault.Secret) (*oauthBundle, error) {
	b, err := parseOAuthBundle(secret.Value)
	if err != nil {
		return nil, err
	}
	if b.valid(time.Now(), time.Duration(p.OAuth.skew())*time.Second) {
		return b, nil
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	// Re-read: another request may have refreshed while we waited.
	if cur, err := s.cfg.Vault.Get(secret.Name); err == nil {
		if nb, err := parseOAuthBundle(cur.Value); err == nil {
			b = nb
			if b.valid(time.Now(), time.Duration(p.OAuth.skew())*time.Second) {
				return b, nil
			}
		}
	}

	nb, err := s.refreshOAuth(ctx, p, b)
	if err != nil {
		s.cfg.Logger.Error("oauth refresh failed", "provider", p.Name, "credential", secret.Name, "err", err)
		return nil, err
	}
	raw, err := nb.marshal()
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Vault.Put(secret.Name, secret.Provider, raw); err != nil {
		s.cfg.Logger.Error("persist refreshed oauth bundle failed", "credential", secret.Name, "err", err)
		return nil, fmt.Errorf("persist refreshed credential: %w", err)
	}
	s.cfg.Logger.Info("oauth bundle refreshed", "provider", p.Name, "credential", secret.Name)
	return nb, nil
}

func (s *Server) refreshOAuth(ctx context.Context, p Provider, b *oauthBundle) (*oauthBundle, error) {
	if b.RefreshToken == "" {
		return nil, errors.New("access token expired and no refresh token stored")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", b.RefreshToken)
	form.Set("client_id", p.OAuth.ClientID)
	if p.OAuth.Scope != "" {
		form.Set("scope", p.OAuth.Scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.OAuth.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("token endpoint returned HTTP %d", resp.StatusCode)
	}
	var tr oauthRefreshResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, errors.New("token endpoint returned invalid JSON")
	}
	if tr.AccessToken == "" {
		return nil, errors.New("token endpoint returned no access token")
	}

	nb := &oauthBundle{
		Type:         "oauth",
		AccessToken:  tr.AccessToken,
		RefreshToken: b.RefreshToken,
		AccountID:    b.AccountID,
		IDToken:      b.IDToken,
	}
	if tr.RefreshToken != "" {
		nb.RefreshToken = tr.RefreshToken
	}
	if tr.IDToken != "" {
		nb.IDToken = tr.IDToken
	}
	switch {
	case tr.ExpiresIn > 0:
		nb.ExpiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	default:
		if exp, ok := jwtExpiry(tr.AccessToken); ok {
			nb.ExpiresAt = exp
		}
	}
	if id := accountIDFromTokens(nb); id != "" {
		nb.AccountID = id
	}
	return nb, nil
}

// accountIDFromTokens extracts the ChatGPT account id from the id_token (or the
// access token as a fallback) without exposing the token material.
func accountIDFromTokens(b *oauthBundle) string {
	for _, tok := range []string{b.IDToken, b.AccessToken} {
		if id := claimChatGPTAccountID(tok); id != "" {
			return id
		}
	}
	return ""
}

func claimChatGPTAccountID(token string) string {
	claims, ok := jwtClaims(token)
	if !ok {
		return ""
	}
	var c struct {
		Auth struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(claims, &c); err != nil {
		return ""
	}
	return c.Auth.AccountID
}

func jwtExpiry(token string) (time.Time, bool) {
	claims, ok := jwtClaims(token)
	if !ok {
		return time.Time{}, false
	}
	var c struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(claims, &c); err != nil || c.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(c.Exp, 0), true
}

func jwtClaims(token string) ([]byte, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	return raw, true
}
