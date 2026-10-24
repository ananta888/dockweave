package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthPolicyRoles(t *testing.T) {
	p := NewAuthPolicy([]uint32{0, 1000}, []uint32{2000})
	cases := []struct {
		uid  uint32
		want Role
	}{
		{0, RoleAdmin},
		{1000, RoleAdmin},
		{2000, RoleMonitor},
		{3000, ""}, // unknown: denied
	}
	for _, c := range cases {
		if got := p.RoleOf(c.uid); got != c.want {
			t.Fatalf("RoleOf(%d) = %q, want %q", c.uid, got, c.want)
		}
	}
}

func probe() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func reqWithPeer(method string, uid uint32, role Role) *http.Request {
	r := httptest.NewRequest(method, "/api/v1/agents", nil)
	return r.WithContext(ContextWithPeer(r.Context(), "tester", uid, role))
}

func TestPeerAuthMiddlewareEnforcesRoles(t *testing.T) {
	policy := NewAuthPolicy([]uint32{0}, []uint32{2000})
	h := PeerAuthMiddleware(policy, probe())

	cases := []struct {
		name   string
		method string
		uid    uint32
		role   Role
		want   int
	}{
		{"monitor may read", http.MethodGet, 2000, RoleMonitor, http.StatusNoContent},
		{"monitor may head", http.MethodHead, 2000, RoleMonitor, http.StatusNoContent},
		{"monitor may not write", http.MethodPost, 2000, RoleMonitor, http.StatusForbidden},
		{"monitor may not delete", http.MethodDelete, 2000, RoleMonitor, http.StatusForbidden},
		{"admin may write", http.MethodPost, 0, RoleAdmin, http.StatusNoContent},
		{"admin may delete", http.MethodDelete, 0, RoleAdmin, http.StatusNoContent},
		{"unknown uid denied on read", http.MethodGet, 3000, "", http.StatusForbidden},
		{"unknown uid denied on write", http.MethodPost, 3000, "", http.StatusForbidden},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, reqWithPeer(c.method, c.uid, c.role))
		if rec.Code != c.want {
			t.Fatalf("%s: got %d, want %d", c.name, rec.Code, c.want)
		}
	}
}

func TestPeerAuthMiddlewareResolvesRoleFromUID(t *testing.T) {
	// PeerConnContext stores only the uid; the middleware resolves the role.
	policy := NewAuthPolicy([]uint32{0}, []uint32{2000})
	h := PeerAuthMiddleware(policy, probe())

	r := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	r = r.WithContext(ContextWithPeer(r.Context(), "u", 2000, "")) // no pre-resolved role
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("monitor uid must resolve via policy, got %d", rec.Code)
	}

	r = httptest.NewRequest(http.MethodPost, "/api/v1/agents", nil)
	r = r.WithContext(ContextWithPeer(r.Context(), "u", 2000, ""))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("monitor write must be forbidden, got %d", rec.Code)
	}
}
