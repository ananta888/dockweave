package webui

import (
	"net/http/httptest"
	"testing"
)

func TestClientAddrTrustsForwardedOnlyFromLoopbackProxy(t *testing.T) {
	for _, tc := range []struct {
		name, remote, xff, want string
	}{
		{"direct client cannot spoof", "192.168.178.50:5555", "1.2.3.4", "192.168.178.50"},
		{"direct client without header", "192.168.178.50:5555", "", "192.168.178.50"},
		{"loopback proxy: last entry is the client", "127.0.0.1:40000", "6.6.6.6, 192.168.178.50", "192.168.178.50"},
		{"loopback proxy single entry", "[::1]:40000", "192.168.178.51", "192.168.178.51"},
		{"loopback proxy with garbage", "127.0.0.1:40000", "not-an-ip", "127.0.0.1"},
		{"local browser, no proxy", "127.0.0.1:40000", "", "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/login", nil)
			r.RemoteAddr = tc.remote
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientAddr(r); got != tc.want {
				t.Fatalf("clientAddr = %q, want %q", got, tc.want)
			}
		})
	}
}

// A LAN client rotating X-Forwarded-For still hits the login limiter.
func TestLoginLimiterNotBypassedBySpoofedForwardedFor(t *testing.T) {
	srv, err := New(Config{DaemonSocket: "/nonexistent", TermSocket: "/nonexistent", WebDir: t.TempDir(), Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	limited := false
	for i := 0; i < 50 && !limited; i++ {
		r := httptest.NewRequest("POST", "/login", nil)
		r.RemoteAddr = "192.168.178.50:5555"
		r.Header.Set("X-Forwarded-For", "10.0.0."+string(rune('0'+i%10)))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, r)
		limited = rec.Code == 429
	}
	if !limited {
		t.Fatal("spoofed X-Forwarded-For bypassed the login limiter")
	}
}
