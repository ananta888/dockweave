// Package mounts implements the privileged mount manager of DockWeave.
//
// Security contract (see docs/validation/phase1-mounts.de.md):
//   - Only the daemon (root, CAP_SYS_ADMIN) may use this package.
//   - Read-only binds are staged privately, remounted ro (recursively
//     enforced via mount_setattr(AT_RECURSIVE) where available), then moved
//     into the shared tree. Remount flags do not propagate, so direct
//     `bind -o ro` into the shared tree would leak RW clones into slave
//     namespaces.
//   - Unmount uses umount(2) without flags; EBUSY is returned as a typed
//     error. MNT_DETACH (lazy) is only used when force is explicitly set.
//   - Source paths are canonicalized, allowlisted and re-verified with
//     openat2(RESOLVE_BENEATH|RESOLVE_NO_MAGICLINKS); the verified fd is
//     kept open and the mount is performed via /proc/self/fd/<n>, so no
//     path-swap (TOCTOU) can redirect the mount after verification.
package mounts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/ananta888/dockweave/internal/model"
)

// Config configures the Manager.
type Config struct {
	// Root is the dockweave state root, e.g. /var/lib/dockweave.
	Root string
	// AllowedRoots restricts which host directories may be mounted into agents.
	AllowedRoots []string
	// MountinfoPath overrides /proc/self/mountinfo (tests).
	MountinfoPath string
}

// Manager performs mount operations with per-agent serialization.
type Manager struct {
	cfg   Config
	mu    sync.Mutex // global for tree preparation
	locks map[string]*sync.Mutex
	lmu   sync.Mutex

	allowedMu sync.RWMutex
	allowed   []string
}

// New creates a Manager.
func New(cfg Config) (*Manager, error) {
	if cfg.Root == "" {
		return nil, errors.New("mounts: Root must not be empty")
	}
	if cfg.MountinfoPath == "" {
		cfg.MountinfoPath = "/proc/self/mountinfo"
	}
	if err := os.MkdirAll(cfg.Root, 0o755); err != nil {
		return nil, fmt.Errorf("create root %q: %w", cfg.Root, err)
	}
	return &Manager{cfg: cfg, locks: map[string]*sync.Mutex{}, allowed: append([]string(nil), cfg.AllowedRoots...)}, nil
}

// AddAllowedRoot registers an additional allowed mount source root at runtime
// (e.g. a newly registered workspace). Duplicates are ignored.
func (m *Manager) AddAllowedRoot(path string) {
	m.allowedMu.Lock()
	defer m.allowedMu.Unlock()
	for _, r := range m.allowed {
		if r == path {
			return
		}
	}
	m.allowed = append(m.allowed, path)
}

// allowedRoots returns a snapshot of the current allowlist.
func (m *Manager) allowedRoots() []string {
	m.allowedMu.RLock()
	defer m.allowedMu.RUnlock()
	return append([]string(nil), m.allowed...)
}

func (m *Manager) agentLock(agentID string) *sync.Mutex {
	m.lmu.Lock()
	defer m.lmu.Unlock()
	l, ok := m.locks[agentID]
	if !ok {
		l = &sync.Mutex{}
		m.locks[agentID] = l
	}
	return l
}

// WorkspaceTree returns the host path of an agent's workspace tree.
func (m *Manager) WorkspaceTree(agentID string) string {
	return filepath.Join(m.cfg.Root, "agents", agentID, "workspace")
}

// AgentDir / MemoryDir return the other two standard host directories.
func (m *Manager) AgentDir(agentID string) string {
	return filepath.Join(m.cfg.Root, "agents", agentID, "agent")
}

// MemoryDir returns the host path of an agent's memory directory.
func (m *Manager) MemoryDir(agentID string) string {
	return filepath.Join(m.cfg.Root, "agents", agentID, "memory")
}

// stagingDir is OUTSIDE the shared workspace tree (see security contract).
func (m *Manager) stagingDir() string {
	return filepath.Join(m.cfg.Root, ".staging")
}

// PrepareAgentTree creates the three directories and converts the workspace
// tree into a private bind mount with shared propagation, so mounts created
// beneath it propagate into agent containers that bind it with rslave.
// Idempotent.
func (m *Manager) PrepareAgentTree(agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range []string{m.AgentDir(agentID), m.MemoryDir(agentID), m.WorkspaceTree(agentID)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create %q: %w", d, err)
		}
	}
	if err := os.MkdirAll(m.stagingDir(), 0o700); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	tree := m.WorkspaceTree(agentID)
	infos, err := ReadMountinfo(m.cfg.MountinfoPath)
	if err != nil {
		return err
	}
	isMount := false
	for _, mi := range infos {
		if mi.MountPoint == tree {
			isMount = true
			if mi.Shared() {
				return nil // already prepared
			}
			break
		}
	}
	if !isMount {
		if err := unix.Mount(tree, tree, "none", unix.MS_BIND|unix.MS_REC, ""); err != nil {
			return fmt.Errorf("bind workspace tree %q onto itself: %w", tree, err)
		}
	}
	if err := unix.Mount("none", tree, "", unix.MS_REC|unix.MS_SHARED, ""); err != nil {
		return fmt.Errorf("make workspace tree %q rshared: %w", tree, err)
	}
	return nil
}

// Check verifies that the agent workspace tree is a shared mount (i.e.
// propagation into rslave agent containers will work). Used by `mount check`.
func (m *Manager) Check(agentID string) error {
	tree := m.WorkspaceTree(agentID)
	infos, err := ReadMountinfo(m.cfg.MountinfoPath)
	if err != nil {
		return err
	}
	for _, mi := range infos {
		if mi.MountPoint == tree {
			if !mi.Shared() {
				return fmt.Errorf("workspace tree %q is mounted but not shared (propagation would not reach agents)", tree)
			}
			return nil
		}
	}
	return fmt.Errorf("workspace tree %q is not a mountpoint (run PrepareAgentTree / agent start)", tree)
}

// ValidateSource checks a user-supplied host directory (allowlist,
// canonicalized, is-a-directory) and returns its canonical path. Used for
// registered /agent and /memory folders.
func (m *Manager) ValidateSource(path string) (string, error) {
	canon, fd, err := ResolveSource(path, m.allowedRoots())
	if err != nil {
		return "", err
	}
	_ = unix.Close(fd)
	return canon, nil
}

// ChownAgentTree recursively hands the agent's host tree (agent install,
// memory, workspace) to the container identity (review-005). Must be called
// at creation time, before workspace grants can have mounted foreign host
// directories beneath the tree.
func (m *Manager) ChownAgentTree(agentID string, uid, gid int) error {
	for _, dir := range []string{m.AgentDir(agentID), m.MemoryDir(agentID), m.WorkspaceTree(agentID)} {
		err := filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			return os.Chown(p, uid, gid)
		})
		if err != nil {
			return fmt.Errorf("chown %s to %d:%d: %w", dir, uid, gid, err)
		}
	}
	return nil
}

// TeardownAgentTree unmounts everything beneath the agent workspace tree
// and the tree's self-bind (deepest first). Used by tests and agent pruning;
// never touches mounts outside the tree. Busy mounts are only lazily
// detached (MNT_DETACH) when force is set — every such detach is returned
// in the second result so callers can audit it. With force, unmounting is
// still tried gracefully first.
func (m *Manager) TeardownAgentTree(agentID string, force bool) (detached []string, err error) {
	tree := m.WorkspaceTree(agentID)
	infos, err := ReadMountinfo(m.cfg.MountinfoPath)
	if err != nil {
		return nil, err
	}
	unmount := func(mp string) error {
		err := unix.Unmount(mp, 0)
		if err == nil || errors.Is(err, unix.EINVAL) {
			return nil // EINVAL: not a mountpoint (already gone)
		}
		if !errors.Is(err, unix.EBUSY) || !force {
			if errors.Is(err, unix.EBUSY) {
				return ErrBusy
			}
			return err
		}
		detached = append(detached, mp)
		return unix.Unmount(mp, unix.MNT_DETACH)
	}
	subs := MountsUnder(infos, tree)
	// Deepest first (submounts before their parents).
	for i := len(subs) - 1; i >= 0; i-- {
		mi := subs[i]
		if mi.MountPoint == tree {
			continue // handled below
		}
		if err := unmount(mi.MountPoint); err != nil {
			return detached, fmt.Errorf("teardown submount %q: %w", mi.MountPoint, err)
		}
	}
	if err := unmount(tree); err != nil {
		return detached, fmt.Errorf("teardown tree %q: %w", tree, err)
	}
	return detached, nil
}

// List returns the kernel-verified mounts beneath the agent workspace tree,
// excluding the tree root itself.
func (m *Manager) List(agentID string) ([]model.MountState, error) {
	tree := m.WorkspaceTree(agentID)
	infos, err := ReadMountinfo(m.cfg.MountinfoPath)
	if err != nil {
		return nil, err
	}
	var out []model.MountState
	for _, mi := range MountsUnder(infos, tree) {
		if mi.MountPoint == tree {
			continue
		}
		mode := model.ModeRW
		if mi.Readonly() {
			mode = model.ModeRO
		}
		out = append(out, model.MountState{
			AgentID:    agentID,
			HostPath:   mi.Root, // bind source root within its filesystem
			TargetPath: mi.MountPoint,
			Mode:       mode,
			Status:     model.MountActive,
		})
	}
	return out, nil
}

// Add mounts source beneath the agent's workspace tree at targetName.
// RO mounts are staged privately first (see package doc). Idempotent: if the
// target is already a mountpoint for the same source and mode, Add returns nil.
// A target mounted from a *different* source is a conflict (ErrConflict),
// never silently accepted.
func (m *Manager) Add(agentID, source, targetName string, mode model.AccessMode) (string, error) {
	l := m.agentLock(agentID)
	l.Lock()
	defer l.Unlock()

	if mode != model.ModeRO && mode != model.ModeRW {
		return "", fmt.Errorf("invalid mode %q", mode)
	}
	if err := SanitizeTargetName(targetName); err != nil {
		return "", err
	}
	canon, srcFD, err := ResolveSource(source, m.allowedRoots())
	if err != nil {
		return "", err
	}
	defer unix.Close(srcFD)
	// Mount via the verified fd: the kernel dereferences the fd to the exact
	// object that was checked (no path-string TOCTOU window).
	src := fmt.Sprintf("/proc/self/fd/%d", srcFD)
	tree := m.WorkspaceTree(agentID)
	if err := m.Check(agentID); err != nil {
		return "", err
	}
	target := filepath.Join(tree, targetName)

	// Deduplicate: already mounted?
	infos, err := ReadMountinfo(m.cfg.MountinfoPath)
	if err != nil {
		return "", err
	}
	for _, mi := range infos {
		if mi.MountPoint != target {
			continue
		}
		// Identity check first: the same target must show the *same*
		// filesystem object as the requested source (Dev+Ino of the
		// bind root), otherwise the old mount would silently shadow the
		// new source (e.g. RW-project A instead of requested RO-project B).
		same, err := sameBindSource(target, srcFD)
		if err != nil {
			return "", fmt.Errorf("dedup source check on %q: %w", target, err)
		}
		if !same {
			return "", fmt.Errorf("%w: target %q is already mounted from a different source (requested %q)", ErrConflict, target, canon)
		}
		ro := mi.Readonly()
		if (mode == model.ModeRO) != ro {
			return "", fmt.Errorf("target %q already mounted with different mode (ro=%v)", target, ro)
		}
		return target, nil // idempotent: same source and mode
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("create target %q: %w", target, err)
	}

	if mode == model.ModeRW {
		if err := unix.Mount(src, target, "none", unix.MS_BIND|unix.MS_REC, ""); err != nil {
			os.Remove(target)
			return "", fmt.Errorf("bind mount %q -> %q: %w", canon, target, err)
		}
		return target, nil
	}

	// RO staging: bind privately outside the shared tree, remount ro, move in.
	staging := filepath.Join(m.stagingDir(), agentID+"-"+targetName)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		os.Remove(target)
		return "", fmt.Errorf("create staging %q: %w", staging, err)
	}
	cleanup := func() {
		_ = unix.Unmount(staging, unix.MNT_DETACH)
		_ = os.Remove(staging)
		_ = os.Remove(target)
	}
	if err := unix.Mount(src, staging, "none", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		os.Remove(staging)
		os.Remove(target)
		return "", fmt.Errorf("stage bind %q: %w", canon, err)
	}
	if err := unix.Mount("none", staging, "", unix.MS_REMOUNT|unix.MS_BIND|unix.MS_RDONLY|unix.MS_REC, ""); err != nil {
		cleanup()
		return "", fmt.Errorf("remount ro on staging %q: %w", staging, err)
	}
	// MS_REMOUNT does not reach submounts of a recursive bind on all
	// kernels. mount_setattr(AT_RECURSIVE) enforces read-only on the whole
	// staged subtree (Linux >= 5.12); on older kernels the defensive
	// rw-submount check below refuses the mount instead of leaking writes.
	if err := unix.MountSetattr(unix.AT_FDCWD, staging, unix.AT_RECURSIVE,
		&unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY}); err != nil {
		if !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.EPERM) {
			cleanup()
			return "", fmt.Errorf("recursive ro enforcement on %q: %w", staging, err)
		}
		// fall through: submount check decides
	}
	// Defensive submount check: ensure no rw submount survived the remount.
	if rw, err := m.rwSubmounts(staging); err == nil && len(rw) > 0 {
		cleanup()
		return "", fmt.Errorf("read-only enforcement failed: rw submounts under %q: %v", staging, rw)
	}
	if err := unix.Mount(staging, target, "", unix.MS_MOVE, ""); err != nil {
		cleanup()
		return "", fmt.Errorf("move staged mount into shared tree: %w", err)
	}
	_ = os.Remove(staging) // empty staging dir after move
	return target, nil
}

// sameBindSource reports whether the directory visible at target is the same
// filesystem object as the open source fd, by comparing device and inode
// numbers. This is the authoritative identity check for bind mounts: the
// mountinfo "root" field is filesystem-relative and must not be compared to
// absolute paths directly.
func sameBindSource(target string, srcFD int) (bool, error) {
	var st, sc unix.Stat_t
	if err := unix.Stat(target, &st); err != nil {
		return false, fmt.Errorf("stat target %q: %w", target, err)
	}
	if err := unix.Fstat(srcFD, &sc); err != nil {
		return false, fmt.Errorf("fstat source: %w", err)
	}
	return st.Dev == sc.Dev && st.Ino == sc.Ino, nil
}

// rwSubmounts lists rw mounts at or below path.
func (m *Manager) rwSubmounts(path string) ([]string, error) {
	infos, err := ReadMountinfo(m.cfg.MountinfoPath)
	if err != nil {
		return nil, err
	}
	var rw []string
	for _, mi := range MountsUnder(infos, path) {
		if !mi.Readonly() {
			rw = append(rw, mi.MountPoint)
		}
	}
	return rw, nil
}

// Remove unmounts targetName from the agent workspace tree.
// Without force, EBUSY is returned as ErrBusy. With force, MNT_DETACH is
// used (lazy unmount) — callers must audit this choice.
func (m *Manager) Remove(agentID, targetName string, force bool) error {
	l := m.agentLock(agentID)
	l.Lock()
	defer l.Unlock()
	if err := SanitizeTargetName(targetName); err != nil {
		return err
	}
	target := filepath.Join(m.WorkspaceTree(agentID), targetName)

	infos, err := ReadMountinfo(m.cfg.MountinfoPath)
	if err != nil {
		return err
	}
	// Unmount submounts first (deepest last in list order => sort desc by depth).
	subs := MountsUnder(infos, target)
	for i := len(subs) - 1; i >= 0; i-- {
		if subs[i].MountPoint == target {
			continue
		}
		if err := m.unmount(subs[i].MountPoint, force); err != nil {
			return fmt.Errorf("unmount submount %q: %w", subs[i].MountPoint, err)
		}
	}
	isMount := false
	for _, mi := range infos {
		if mi.MountPoint == target {
			isMount = true
			break
		}
	}
	if isMount {
		if err := m.unmount(target, force); err != nil {
			return err
		}
	}
	// Best-effort directory cleanup; keep dir if non-empty.
	_ = os.Remove(target)
	return nil
}

func (m *Manager) unmount(target string, force bool) error {
	flags := 0
	if force {
		flags = unix.MNT_DETACH
	}
	if err := unix.Unmount(target, flags); err != nil {
		if errors.Is(err, unix.EBUSY) && !force {
			return ErrBusy
		}
		return fmt.Errorf("unmount %q: %w", target, err)
	}
	return nil
}

// ReconcileResult reports the outcome of a reconcile pass.
type ReconcileResult struct {
	Restored []string // targets re-mounted from desired state
	Removed  []string // stale dockweave mounts removed
	Orphaned []string // mounts present in kernel but not in desired state (reported, not removed)
	Failed   map[string]string
}

// Reconcile compares desired mounts (from the registry) with the kernel
// state for one agent and converges towards desired. Unknown mounts beneath
// the tree are reported as orphaned but never removed blindly (they may not
// belong to DockWeave).
func (m *Manager) Reconcile(agentID string, desired []model.MountState) (*ReconcileResult, error) {
	res := &ReconcileResult{Failed: map[string]string{}}
	if err := m.Check(agentID); err != nil {
		// Tree not prepared: nothing mounted, recreate below.
		if perr := m.PrepareAgentTree(agentID); perr != nil {
			return res, fmt.Errorf("prepare agent tree during reconcile: %w", perr)
		}
	}
	actual, err := m.List(agentID)
	if err != nil {
		return res, err
	}
	desiredByTarget := map[string]model.MountState{}
	for _, d := range desired {
		desiredByTarget[d.TargetPath] = d
	}
	actualByTarget := map[string]model.MountState{}
	for _, a := range actual {
		actualByTarget[a.TargetPath] = a
	}
	// Restore missing desired mounts.
	for _, d := range desired {
		name := filepath.Base(d.TargetPath)
		if _, ok := actualByTarget[d.TargetPath]; ok {
			continue
		}
		if _, err := m.Add(agentID, d.HostPath, name, d.Mode); err != nil {
			res.Failed[d.TargetPath] = err.Error()
		} else {
			res.Restored = append(res.Restored, d.TargetPath)
		}
	}
	// Classify unexpected mounts.
	for _, a := range actual {
		if _, ok := desiredByTarget[a.TargetPath]; !ok {
			res.Orphaned = append(res.Orphaned, a.TargetPath)
		}
	}
	return res, nil
}

// PropagationProbe is used by capability detection at daemon start: it
// verifies that this process can create bind mounts and control propagation.
func (m *Manager) PropagationProbe() error {
	tmp, err := os.MkdirTemp(m.cfg.Root, ".probe-")
	if err != nil {
		return fmt.Errorf("probe dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	if err := unix.Mount(tmp, tmp, "none", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind mount failed (need CAP_SYS_ADMIN, not rootless docker): %w", err)
	}
	if err := unix.Mount("none", tmp, "", unix.MS_SHARED, ""); err != nil {
		_ = unix.Unmount(tmp, 0)
		return fmt.Errorf("make-shared failed: %w", err)
	}
	if err := unix.Unmount(tmp, 0); err != nil {
		return fmt.Errorf("probe unmount: %w", err)
	}
	return nil
}

// IsNotFoundTarget reports whether err indicates the target was not mounted.
func IsNotFoundTarget(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "not mounted") || errors.Is(err, unix.EINVAL))
}
