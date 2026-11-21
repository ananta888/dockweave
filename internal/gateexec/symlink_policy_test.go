package gateexec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ananta888/dockweave/internal/gatesync"
)

// Decision 2026-11-21: the snapshot's handling of links leaving the
// workspace is configurable (hash | ignored | fail); links are never followed.
func TestCopyTreeSymlinkPolicies(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{"main.go": "package main", "node_modules/pkg/index.js": "x"})
	outside := filepath.Join(t.TempDir(), "host-secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	inTree := filepath.Join("node_modules", ".bin", "tool")
	if err := os.MkdirAll(filepath.Join(src, filepath.Dir(inTree)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, inTree)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("main.go", filepath.Join(src, "local-link")); err != nil {
		t.Fatal(err)
	}
	copyWith := func(policy string) ([]string, string, error) {
		dst := filepath.Join(t.TempDir(), "snap")
		omitted, err := copyTree(context.Background(), src, dst, policy)
		return omitted, dst, err
	}
	for _, policy := range []string{"", gatesync.SymlinkHash, gatesync.SymlinkIgnored} {
		omitted, dst, err := copyWith(policy)
		if err != nil || len(omitted) != 1 || omitted[0] != inTree {
			t.Fatalf("policy %q with escaping link in node_modules: omitted=%v err=%v", policy, omitted, err)
		}
		if _, err := os.Lstat(filepath.Join(dst, inTree)); !os.IsNotExist(err) {
			t.Fatalf("policy %q copied the escaping link into the sandbox", policy)
		}
		if l, err := os.Readlink(filepath.Join(dst, "local-link")); err != nil || l != "main.go" {
			t.Fatalf("policy %q lost an internal link: %q %v", policy, l, err)
		}
	}
	if _, _, err := copyWith(gatesync.SymlinkFail); err == nil {
		t.Fatal("fail policy accepted an escaping link")
	}
	if _, _, err := copyWith("bogus"); err == nil {
		t.Fatal("unknown policy accepted")
	}

	// Outside ignored trees, "ignored" fails while "hash" omits.
	if err := os.Symlink(outside, filepath.Join(src, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := copyWith(gatesync.SymlinkIgnored); err == nil {
		t.Fatal("ignored policy accepted an escaping link outside dependency trees")
	}
	if omitted, _, err := copyWith(gatesync.SymlinkHash); err != nil || len(omitted) != 2 {
		t.Fatalf("hash policy: %v %v", omitted, err)
	}

	// The fingerprint still covers the omitted links (by text): the live
	// workspace differs from the snapshot exactly by them.
	full := mustFP(t, src)
	snapOmitted, dst, err := copyWith(gatesync.SymlinkHash)
	if err != nil {
		t.Fatal(err)
	}
	skip := map[string]bool{}
	for _, p := range snapOmitted {
		skip[p] = true
	}
	expected, err := fingerprintExcluding(context.Background(), src, FingerprintLimits{}, skip)
	if err != nil || expected != mustFP(t, dst) || expected == full {
		t.Fatalf("snapshot fingerprint mismatch: expected=%s snap=%s full=%s err=%v", expected, mustFP(t, dst), full, err)
	}
}

func TestStricterSymlinkPolicyWins(t *testing.T) {
	cases := [][3]string{
		{"", "", ""}, {"", "hash", "hash"}, {"hash", "ignored", "ignored"},
		{"ignored", "fail", "fail"}, {"fail", "hash", "fail"}, {"hash", "bogus", "fail"},
	}
	for _, c := range cases {
		if got := gatesync.StricterSymlinkPolicy(c[0], c[1]); got != c[2] {
			t.Fatalf("Stricter(%q,%q)=%q want %q", c[0], c[1], got, c[2])
		}
	}
}
