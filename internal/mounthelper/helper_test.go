package mounthelper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func startHelper(t *testing.T) (*Client, string, string) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "mountd.sock")
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = Serve(ServerConfig{Socket: sock, StateRoot: state})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); err == nil {
			return &Client{Socket: sock}, state, dir
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("helper socket did not appear")
	return nil, "", ""
}

func openDir(t *testing.T, path string) int {
	t.Helper()
	fd, err := unix.Open(path, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	return fd
}

func TestHelperPing(t *testing.T) {
	c, _, _ := startHelper(t)
	if err := c.Do(context.Background(), Request{Op: OpPing}, nil); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestHelperRejectsTargetOutsideStateRoot(t *testing.T) {
	c, state, dir := startHelper(t)
	src := openDir(t, state)
	outside := openDir(t, dir)

	err := c.Do(context.Background(), Request{Op: OpBind, Flags: unix.MS_BIND}, []int{src, outside})
	if err == nil {
		t.Fatal("expected rejection for target outside the state root")
	}
	if !strings.Contains(err.Error(), "outside the state root") {
		t.Fatalf("error = %v", err)
	}
}

func TestHelperRejectsUnknownOp(t *testing.T) {
	c, _, _ := startHelper(t)
	if err := c.Do(context.Background(), Request{Op: "drop-tables"}, nil); err == nil {
		t.Fatal("expected unknown-op error")
	}
}

func TestFdSourceReturnsMagicLink(t *testing.T) {
	// The bind source must be mounted via /proc/self/fd/<n> so the kernel
	// resolves the open (already verified) descriptor, not a re-looked-up path
	// string. This is the TOCTOU pin across the privilege boundary.
	got, err := fdSourceLink([]int{-1, -1, -1, 5}, 3, "bind")
	if err != nil {
		t.Fatalf("fdSourceLink(3): %v", err)
	}
	if got != "/proc/self/fd/5" {
		t.Fatalf("fdSourceLink(3) = %q, want /proc/self/fd/5", got)
	}
	if _, err := fdSourceLink([]int{-1}, 0, "bind"); err == nil {
		t.Fatal("fdSourceLink must reject a negative fd")
	}
	if _, err := fdSourceLink(nil, 0, "bind"); err == nil {
		t.Fatal("fdSourceLink must reject an out-of-range index")
	}
}
