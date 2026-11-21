package gateexec

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestManifestBaselineIsStableAndIncludesDeclaredSubprojects(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"ui/package.json": `{"dependencies":{"a":"1"}}`})
	baseline, err := readManifestBaseline(context.Background(), root, []byte(`{"dependency_manifests":["ui/package.json"]}`), true)
	if err != nil {
		t.Fatal(err)
	}
	writeTree(t, root, map[string]string{"ui/package.json": `{"dependencies":{"a":"2","b":"1"}}`})
	added, err := baseline.compare(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(added, []string{"ui/package.json: b"}) {
		t.Fatalf("added=%v", added)
	}
}

func TestManifestErrorsFailClosed(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"package.json", "{"}, {"package.json", "null"}, {"go.mod", "require invalid v2.0.0"},
		{"pyproject.toml", "[project\n"}, {"Cargo.toml", "[dependencies]\na = {"},
		{"requirements.txt", "-r other.txt"}, {"pyproject.toml", "[project]\ndynamic = ['dependencies']"},
	} {
		t.Run(tc.name+tc.body, func(t *testing.T) {
			if _, err := manifestDependencyNames(tc.name, []byte(tc.body)); err == nil {
				t.Fatal("invalid/unsupported manifest accepted")
			}
		})
	}
	root := t.TempDir()
	if _, err := readManifestBaseline(context.Background(), root, []byte(`{"dependency_manifests":["../package.json"]}`), true); err == nil {
		t.Fatal("traversal accepted")
	}
	out := t.TempDir()
	writeTree(t, out, map[string]string{"package.json": "{}"})
	if err := os.Symlink(filepath.Join(out, "package.json"), filepath.Join(root, "package.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifestBaseline(context.Background(), root, nil, true); err == nil {
		t.Fatal("external manifest accepted")
	}
}

func TestOptionalPythonAndCargoWorkspaceDependencies(t *testing.T) {
	python, err := manifestDependencyNames("pyproject.toml", []byte("[project.optional-dependencies]\ntest = ['Py_Test>=1'] # comment\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !python["py-test"] {
		t.Fatalf("optional dependencies=%v", python)
	}
	cargo, err := manifestDependencyNames("Cargo.toml", []byte("[workspace.dependencies]\nserde = { version = '1', features = ['derive'] } # comment\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cargo["serde"] {
		t.Fatalf("workspace dependencies=%v", cargo)
	}
}

func TestSecretScanErrorsDoNotBecomeCleanResults(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"dir/source": "safe"})
	if _, err := scanSecrets(root, []string{"dir"}); err == nil {
		t.Fatal("unreadable changed entry treated as clean")
	}
	if _, err := scanSecrets(root, []string{"../outside"}); err == nil {
		t.Fatal("escaping changed path accepted")
	}
}
