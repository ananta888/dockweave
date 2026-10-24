package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/user"
	"strconv"

	"golang.org/x/sys/unix"
)

// Role authorizes socket peers (review-005). SO_PEERCRED identifies the
// peer; the role then gates what it may do. Unknown UIDs are denied.
type Role string

const (
	RoleAdmin   Role = "admin"   // full API and terminal access
	RoleMonitor Role = "monitor" // read-only (GET/HEAD) API access
)

// AuthPolicy maps peer UIDs to roles.
type AuthPolicy struct {
	AdminUIDs   map[uint32]bool
	MonitorUIDs map[uint32]bool
}

// NewAuthPolicy builds a policy from UID lists.
func NewAuthPolicy(admin, monitor []uint32) AuthPolicy {
	p := AuthPolicy{AdminUIDs: map[uint32]bool{}, MonitorUIDs: map[uint32]bool{}}
	for _, u := range admin {
		p.AdminUIDs[u] = true
	}
	for _, u := range monitor {
		p.MonitorUIDs[u] = true
	}
	return p
}

// RoleOf returns the role for a uid, or "" when the uid is not authorized.
func (p AuthPolicy) RoleOf(uid uint32) Role {
	if p.AdminUIDs[uid] {
		return RoleAdmin
	}
	if p.MonitorUIDs[uid] {
		return RoleMonitor
	}
	return ""
}

type peerKey struct{}     // display name for audit events
type peerUIDKey struct{}  // SO_PEERCRED uid
type peerRoleKey struct{} // resolved Role

// ContextWithPeer injects peer identity into a context (ConnContext and
// tests).
func ContextWithPeer(ctx context.Context, name string, uid uint32, role Role) context.Context {
	ctx = context.WithValue(ctx, peerKey{}, name)
	ctx = context.WithValue(ctx, peerUIDKey{}, uid)
	return context.WithValue(ctx, peerRoleKey{}, role)
}

// PeerAuthMiddleware enforces the AuthPolicy on every request and injects
// the audit actor. Monitor peers may only read (GET/HEAD); unknown UIDs and
// TCP connections (no SO_PEERCRED) are denied entirely.
func PeerAuthMiddleware(policy AuthPolicy, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		role, _ := ctx.Value(peerRoleKey{}).(Role)
		if role == "" {
			if uid, ok := ctx.Value(peerUIDKey{}).(uint32); ok {
				role = policy.RoleOf(uid)
			}
		}
		if role == "" {
			http.Error(w, "forbidden: peer uid not authorized", http.StatusForbidden)
			return
		}
		if role == RoleMonitor && r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "forbidden: monitor role is read-only", http.StatusForbidden)
			return
		}
		if ctx.Value(peerKey{}) == nil {
			r = r.WithContext(context.WithValue(ctx, peerKey{}, "unknown"))
		}
		next.ServeHTTP(w, r)
	})
}

// PeerConnContext implements http.Server.ConnContext for unix listeners,
// resolving the peer credentials of each connection.
func PeerConnContext(ctx context.Context, c net.Conn) context.Context {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return ctx
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return ctx
	}
	var cred *unix.Ucred
	_ = raw.Control(func(fd uintptr) {
		cred, err = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil || cred == nil {
		return ctx
	}
	name := fmt.Sprintf("uid:%d", cred.Uid)
	if u, err := user.LookupId(strconv.Itoa(int(cred.Uid))); err == nil {
		name = u.Username
	}
	ctx = context.WithValue(ctx, peerUIDKey{}, cred.Uid)
	return context.WithValue(ctx, peerKey{}, name)
}
