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

func TestNewDependenciesAcrossManifests(t *testing.T) {
	before := t.TempDir()
	after := t.TempDir()
	write := func(dir, name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(before, "package.json", `{"dependencies":{"a":"1"}}`)
	write(after, "package.json", `{"dependencies":{"a":"1","c":"2"}}`)
	write(before, "go.mod", "module example.com/x\n\nrequire github.com/x/y v1.0.0\n")
	write(after, "go.mod", "module example.com/x\n\nrequire (\n\tgithub.com/x/y v1.0.0\n\tgithub.com/new/dep v2.0.0\n)\n")

	got := newDependenciesAcrossManifests(before, after)
	want := []string{"go.mod: github.com/new/dep", "package.json: c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("newDependenciesAcrossManifests = %v, want %v", got, want)
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

func TestPythonDependencyNamesAreNormalised(t *testing.T) {
	before := pyprojectNames([]byte("[project]\nname = \"x\"\ndependencies = [\n  \"requests>=2.0\",\n  \"Flask[async] ~= 3.0 ; python_version >= '3.9'\",\n]\n"))
	after := pyprojectNames([]byte("[project]\nname = \"x\"\ndependencies = [\n  \"requests>=2.1\",\n  \"flask[async]>=3.1\",\n]\n\n[tool.other]\nkey = \"v\"\n"))
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("version bump changed pyproject names: before=%v after=%v", before, after)
	}
	if !reflect.DeepEqual(after, map[string]bool{"requests": true, "flask": true}) {
		t.Fatalf("pyproject names = %v", after)
	}
	inline := pyprojectNames([]byte("[project]\ndependencies = [\"a-b>=1\", \"Zope.Interface\"]\n"))
	if !reflect.DeepEqual(inline, map[string]bool{"a-b": true, "zope-interface": true}) {
		t.Fatalf("inline pyproject names = %v", inline)
	}
	poetry := pyprojectNames([]byte("[tool.poetry.dependencies]\npython = \"^3.10\"\nRequests = \"^2\"\n"))
	if !reflect.DeepEqual(poetry, map[string]bool{"requests": true}) {
		t.Fatalf("poetry names = %v", poetry)
	}
	req := requirementsNames([]byte("# c\nDjango_REST.framework==3.0 ; python_version<\"3.8\"\nnumpy>=1 # pinned\n-r other.txt\npkg @ https://x/y.whl\n"))
	if !reflect.DeepEqual(req, map[string]bool{"django-rest-framework": true, "numpy": true, "pkg": true}) {
		t.Fatalf("requirements names = %v", req)
	}
}

func TestCargoNamesTableForm(t *testing.T) {
	before := cargoNames([]byte("[package]\nname = \"x\"\nversion = \"0.1.0\"\n\n[dependencies.serde]\nversion = \"1\"\nfeatures = [\"derive\"]\n"))
	after := cargoNames([]byte("[package]\nname = \"x\"\nversion = \"0.1.0\"\n\n[dependencies.serde]\nversion = \"1\"\n\n[dependencies.tokio]\nversion = \"1\"\n\n" +
		"[target.'cfg(unix)'.dependencies]\nlibc = \"0.2\"\n\n[target.\"cfg(windows)\".dev-dependencies.winapi]\nversion = \"0.3\"\n\n[dependencies]\nanyhow.workspace = true\n\"quoted\" = \"1\"\n"))
	if !reflect.DeepEqual(before, map[string]bool{"serde": true}) {
		t.Fatalf("cargo before = %v", before)
	}
	want := map[string]bool{"serde": true, "tokio": true, "libc": true, "winapi": true, "anyhow": true, "quoted": true}
	if !reflect.DeepEqual(after, want) {
		t.Fatalf("cargo after = %v, want %v", after, want)
	}
}

func TestScanSecretsIgnoresLockfilesAndHashes(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.sum":            "github.com/foo/bar v1.2.3 h1:Xk3vF9qWm2Lp8zR1tYb6NcJ0aEuHs4dGoQiV7wKyT5M=\n",
		"package-lock.json": `{"integrity": "sha1-Q2uJp8vXk7Rz0mW3LcN9aYfB4tE="}`,
		"vendor.txt":        "dep sha512-Q2uJp8vXk7Rz0mW3LcN9aYfB4tE9fK2mLp\nmod h1:Xk3vF9qWm2Lp8zR1tYb6NcJ0aEuHs4dGoQiV7wKyT5M=\n",
		"config.env":        "TOKEN=q8Zr2Lm9Xv4Tb7Kp1Wn6Yc3Hd5Fs0Gj\n",
		"lock-leak/go.sum":  "key=AKIAABCDEFGHIJKLMNOP\n",
	}
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := scanSecrets(dir, []string{"go.sum", "package-lock.json", "vendor.txt", "config.env", "lock-leak/go.sum"})
	want := []string{"config.env: high-entropy-token", "lock-leak/go.sum: aws-access-key"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scanSecrets = %v, want %v", got, want)
	}
}
