// Package brokerclient is the CLI client for the DockWeave credential
// broker's operator API. It speaks HTTP over the broker's admin unix socket.
package brokerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/ananta888/dockweave/internal/credentialbroker"
	"github.com/ananta888/dockweave/internal/credentialvault"
)

// Client calls the broker admin API.
type Client struct {
	hc *http.Client
}

// New creates a client for the admin unix socket.
func New(socketPath string) *Client {
	return &Client{hc: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://broker"+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("broker unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("%s", e.Error)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Status is the broker health/status document.
type Status struct {
	Status    string   `json:"status"`
	Secrets   int      `json:"secrets"`
	Grants    int      `json:"grants"`
	Sessions  int      `json:"sessions"`
	Providers []string `json:"providers"`
	Egress    string   `json:"egress"`
}

func (c *Client) Status(ctx context.Context) (*Status, error) {
	var s Status
	return &s, c.do(ctx, "GET", "/v1/status", nil, &s)
}

func (c *Client) ListSecrets(ctx context.Context) ([]credentialvault.Metadata, error) {
	var out []credentialvault.Metadata
	return out, c.do(ctx, "GET", "/v1/secrets", nil, &out)
}

func (c *Client) PutSecret(ctx context.Context, name, provider, value string) error {
	return c.do(ctx, "PUT", "/v1/secrets/"+url.PathEscape(name),
		map[string]string{"provider": provider, "value": value}, nil)
}

func (c *Client) DeleteSecret(ctx context.Context, name string) error {
	return c.do(ctx, "DELETE", "/v1/secrets/"+url.PathEscape(name), nil, nil)
}

func (c *Client) ListGrants(ctx context.Context, agent string) ([]credentialbroker.Grant, error) {
	var out []credentialbroker.Grant
	p := "/v1/grants"
	if agent != "" {
		p += "?agent=" + url.QueryEscape(agent)
	}
	return out, c.do(ctx, "GET", p, nil, &out)
}

func (c *Client) AddGrant(ctx context.Context, g credentialbroker.Grant) (*credentialbroker.Grant, error) {
	var out credentialbroker.Grant
	return &out, c.do(ctx, "POST", "/v1/grants", g, &out)
}

func (c *Client) DeleteGrant(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/grants/"+url.PathEscape(id), nil, nil)
}

func (c *Client) ListSessions(ctx context.Context, agent string) ([]credentialbroker.SessionInfo, error) {
	var out []credentialbroker.SessionInfo
	p := "/v1/sessions"
	if agent != "" {
		p += "?agent=" + url.QueryEscape(agent)
	}
	return out, c.do(ctx, "GET", p, nil, &out)
}

// MintResponse is returned once by MintSession; Token is never persisted.
type MintResponse struct {
	Token   string                       `json:"token"`
	Session credentialbroker.SessionInfo `json:"session"`
}

func (c *Client) MintSession(ctx context.Context, agent, grantID, credential, ttl string) (*MintResponse, error) {
	var out MintResponse
	return &out, c.do(ctx, "POST", "/v1/sessions", map[string]string{
		"agent": agent, "grant_id": grantID, "credential": credential, "ttl": ttl,
	}, &out)
}

func (c *Client) RevokeSession(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/sessions/"+url.PathEscape(id), nil, nil)
}

func (c *Client) Requests(ctx context.Context) ([]credentialbroker.RequestLogEntry, error) {
	var out []credentialbroker.RequestLogEntry
	return out, c.do(ctx, "GET", "/v1/requests", nil, &out)
}

func (c *Client) Usage(ctx context.Context, grantID string) (*credentialbroker.Usage, error) {
	var out credentialbroker.Usage
	p := "/v1/usage?grant=" + url.QueryEscape(grantID)
	return &out, c.do(ctx, "GET", p, nil, &out)
}
