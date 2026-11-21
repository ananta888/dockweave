package gateexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyTreeDoesNotChangeExternalSymlinkTarget(t *testing.T) {
	for _, relative := range []bool{false, true} {
		t.Run(map[bool]string{false: "absolute", true: "relative"}[relative], func(t *testing.T) {
			root := t.TempDir()
			src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
			writeTree(t, src, map[string]string{"normal": "content"})
			target := filepath.Join(root, "sentinel")
			if err := os.WriteFile(target, []byte("private"), 0o600); err != nil {
				t.Fatal(err)
			}
			link := target
			if relative {
				link = "../sentinel"
			}
			if err := os.Symlink(link, filepath.Join(src, "escape")); err != nil {
				t.Fatal(err)
			}
			_ = copyTree(context.Background(), src, dst)
			info, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("external sentinel permissions changed to %o", info.Mode().Perm())
			}
			body, err := os.ReadFile(target)
			if err != nil || string(body) != "private" {
				t.Fatalf("sentinel modified: %q %v", body, err)
			}
		})
	}
}

func TestFingerprintRejectsSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"source": "private"})
	link := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Fingerprint(context.Background(), link, FingerprintLimits{}); err == nil {
		t.Fatal("symlink root produced a valid fingerprint")
	}
}

func TestProtectedHashesNeverReadExternalSymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeTree(t, outside, map[string]string{"secret": "first"})
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	before, err := hashPaths(root, []string{"link"})
	if err != nil {
		return
	} // Rejection is also an acceptable fail-closed policy.
	writeTree(t, outside, map[string]string{"secret": "second"})
	after, err := hashPaths(root, []string{"link"})
	if err == nil && len(changedHashes(before, after)) != 0 {
		t.Fatal("hash followed external symlink content")
	}
}

func TestExpressionLimitsSharedByValidationAndEvaluation(t *testing.T) {
	for _, expression := range []string{strings.Repeat("(", 300) + "true" + strings.Repeat(")", 300), strings.Repeat("!", 300) + "true", strings.Repeat("-", 300) + "1", strings.Repeat("true || ", 3000) + "true"} {
		if _, err := Validate(expression); err == nil {
			t.Error("validator accepted oversized expression")
		}
		if _, err := Evaluate(expression, ExprEnv{}); err == nil {
			t.Error("evaluator accepted oversized expression")
		}
	}
}
