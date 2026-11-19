package gateexec

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewDependencies(t *testing.T) {
	before := []byte(`{"dependencies":{"a":"1"},"devDependencies":{"b":"1"}}`)
	after := []byte(`{"dependencies":{"a":"1","c":"2"},"devDependencies":{"b":"1","d":"2"}}`)
	got := newDependencies(before, after)
	want := []string{"c", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("newDependencies = %v, want %v", got, want)
	}
}

func TestOutOfScope(t *testing.T) {
	changed := []string{"src/a.ts", "src/b.ts", "README.md", "package.json"}
	if got := outOfScope(changed, []string{"src/"}); !reflect.DeepEqual(got, []string{"README.md", "package.json"}) {
		t.Fatalf("outOfScope = %v", got)
	}
	if got := outOfScope(changed, []string{"*.md", "package.json"}); !reflect.DeepEqual(got, []string{"src/a.ts", "src/b.ts"}) {
		t.Fatalf("outOfScope glob = %v", got)
	}
	if got := outOfScope(changed, nil); got != nil {
		t.Fatalf("unrestricted scope must not report violations: %v", got)
	}
}

func TestScanSecrets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "clean.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "leak.txt"), []byte("key=AKIAABCDEFGHIJKLMNOP"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := scanSecrets(dir, []string{"clean.txt", "leak.txt"})
	want := []string{"leak.txt: aws-access-key"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scanSecrets = %v, want %v", got, want)
	}
}

func TestChangedHashes(t *testing.T) {
	before := map[string]string{"a": "1", "b": "2"}
	after := map[string]string{"a": "1", "b": "3", "c": "4"}
	if got := changedHashes(before, after); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("changedHashes = %v", got)
	}
}

func TestTailLines(t *testing.T) {
	if got := tailLines("1\n2\n3\n4\n", 2); got != "3\n4" {
		t.Fatalf("tailLines = %q", got)
	}
}
