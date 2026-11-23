package oidc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// ParseConnectTo parses "host:port=addr:port[,host:port=addr:port...]".
// Both sides must be host:port; an empty string yields nil.
func ParseConnectTo(spec string) (map[string]string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		from, to, ok := strings.Cut(item, "=")
		if !ok {
			return nil, fmt.Errorf("connect-to %q: want host:port=addr:port", item)
		}
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		for _, hp := range []string{from, to} {
			h, p, err := net.SplitHostPort(hp)
			if err != nil || h == "" || p == "" {
				return nil, fmt.Errorf("connect-to %q: %q is not host:port", item, hp)
			}
		}
		out[strings.ToLower(from)] = to
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// httpClient returns the client used for all IdP back-channel calls, or nil
// for the library default when no ConnectTo mapping is configured.
func (c Config) httpClient() *http.Client {
	if len(c.ConnectTo) == 0 {
		return nil
	}
	m := make(map[string]string, len(c.ConnectTo))
	for k, v := range c.ConnectTo {
		m[strings.ToLower(k)] = v
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if to, ok := m[strings.ToLower(addr)]; ok {
			addr = to
		}
		return dialer.DialContext(ctx, network, addr)
	}
	// TLS ServerName stays the URL host, so the certificate is verified
	// against the issuer name, not the mapped address.
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}
}

func (c Config) withClient(ctx context.Context) context.Context {
	if hc := c.httpClient(); hc != nil {
		// Same key go-oidc (ClientContext) and oauth2 read the client from.
		return context.WithValue(ctx, oauth2.HTTPClient, hc)
	}
	return ctx
}
