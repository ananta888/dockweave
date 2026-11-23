package oidc

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestParseConnectTo(t *testing.T) {
	got, err := ParseConnectTo(" IdP.example:8444=127.0.0.1:8444 , b:1=c:2 ")
	if err != nil {
		t.Fatal(err)
	}
	if got["idp.example:8444"] != "127.0.0.1:8444" || got["b:1"] != "c:2" || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if m, err := ParseConnectTo(""); err != nil || m != nil {
		t.Fatalf("empty: %v %v", m, err)
	}
	for _, bad := range []string{"idp:8444", "idp=127.0.0.1:1", "idp:1=127.0.0.1", ":1=a:2"} {
		if _, err := ParseConnectTo(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestNoConnectToUsesDefaultClient(t *testing.T) {
	ctx := Config{}.withClient(context.Background())
	if ctx.Value(oauth2.HTTPClient) != nil {
		t.Fatal("no client must be injected without ConnectTo")
	}
}

// The mapped connection reaches the real server, and TLS is verified against
// the original (unroutable) host name rather than the mapped address.
func TestConnectToDialsMappedAddressAndVerifiesOriginalName(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok "+r.Host)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	// httptest certificates are valid for example.com.
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	cfg := Config{ConnectTo: map[string]string{"example.com:" + port: srv.Listener.Addr().String()}}
	hc := cfg.httpClient()
	hc.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool

	resp, err := hc.Get("https://example.com:" + port + "/")
	if err != nil {
		t.Fatalf("mapped request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(string(body), "ok example.com:") {
		t.Fatalf("body %q: Host header must keep the issuer name", body)
	}

	// A name the certificate does not cover must still fail verification,
	// even though the dial is redirected to the same server.
	cfg.ConnectTo = map[string]string{"wrong.test:" + port: srv.Listener.Addr().String()}
	hc = cfg.httpClient()
	hc.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool
	if _, err := hc.Get("https://wrong.test:" + port + "/"); err == nil ||
		!strings.Contains(err.Error(), "certificate") {
		t.Fatalf("want certificate error for unmatched name, got %v", err)
	}
}
