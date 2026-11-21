package gateexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/mod/modfile"
)

var manifestFiles = []string{"package.json", "go.mod", "requirements.txt", "pyproject.toml", "Cargo.toml"}

type manifestBaseline struct {
	scope []string
	names map[string]map[string]bool
}

func readManifestBaseline(ctx context.Context, root string, cfgRaw []byte, enabled bool) (manifestBaseline, error) {
	b := manifestBaseline{names: map[string]map[string]bool{}}
	if !enabled {
		return b, nil
	}
	var scope struct {
		Paths []string `json:"dependency_manifests"`
	}
	if len(cfgRaw) > 0 {
		if err := json.Unmarshal(cfgRaw, &scope); err != nil {
			return b, fmt.Errorf("manifest scope: %w", err)
		}
	}
	b.scope = mergeStrings(manifestFiles, scope.Paths)
	for _, p := range b.scope {
		if !filepath.IsLocal(p) || p == "." || filepath.Clean(p) != p {
			return b, fmt.Errorf("invalid manifest path: %q", p)
		}
	}
	var err error
	b.names, err = readManifests(ctx, root, b.scope)
	return b, err
}

func readManifests(ctx context.Context, root string, scope []string) (map[string]map[string]bool, error) {
	r, err := openRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.close()
	names := map[string]map[string]bool{}
	for _, name := range scope {
		data, err := r.readFile(ctx, name, 4<<20)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read dependency manifest %q: %w", name, err)
		}
		parsed, err := manifestDependencyNames(filepath.Base(name), data)
		if err != nil {
			return nil, fmt.Errorf("parse dependency manifest %q: %w", name, err)
		}
		names[name] = parsed
	}
	return names, nil
}

func (b manifestBaseline) compare(ctx context.Context, root string) ([]string, error) {
	after, err := readManifests(ctx, root, b.scope)
	if err != nil {
		return nil, err
	}
	var added []string
	for m, names := range after {
		for name := range names {
			if !b.names[m][name] {
				added = append(added, m+": "+name)
			}
		}
	}
	sort.Strings(added)
	return added, nil
}

func newDependenciesAcrossManifests(beforeRoot, afterRoot string) ([]string, error) {
	b, err := readManifestBaseline(context.Background(), beforeRoot, nil, true)
	if err != nil {
		return nil, err
	}
	return b.compare(context.Background(), afterRoot)
}

func manifestDependencyNames(name string, raw []byte) (map[string]bool, error) {
	out := map[string]bool{}
	switch name {
	case "package.json":
		var m struct{ Dependencies, DevDependencies, OptionalDependencies, PeerDependencies map[string]string }
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, errors.New("invalid package.json")
		}
		if strings.TrimSpace(string(raw)) == "null" {
			return nil, errors.New("package.json must be an object")
		}
		for _, group := range []map[string]string{m.Dependencies, m.DevDependencies, m.OptionalDependencies, m.PeerDependencies} {
			for k := range group {
				out[k] = true
			}
		}
	case "go.mod":
		m, err := modfile.Parse("go.mod", raw, nil)
		if err != nil {
			return nil, errors.New("invalid go.mod")
		}
		for _, r := range m.Require {
			out[r.Mod.Path] = true
		}
	case "requirements.txt":
		return requirementsNamesStrict(raw)
	case "pyproject.toml":
		return pyprojectNames(raw)
	case "Cargo.toml":
		return cargoNames(raw)
	default:
		return nil, fmt.Errorf("unsupported manifest basename: %q", name)
	}
	return out, nil
}

var pythonSepRe = regexp.MustCompile(`[-_.]+`)

func normalizePythonName(spec string) string {
	spec = strings.TrimSpace(spec)
	end := len(spec)
	for i, c := range spec {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			end = i
			break
		}
	}
	return pythonSepRe.ReplaceAllString(strings.Trim(strings.ToLower(spec[:end]), "-_."), "-")
}

func requirementsNamesStrict(raw []byte) (map[string]bool, error) {
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Includes, editable/local requirements and continuations need a separately
		// declared manifest and cannot silently disappear from dependency policy.
		if strings.HasPrefix(line, "-") || strings.Contains(line, "\\") || strings.Contains(line, " @ ") || strings.HasPrefix(line, ".") {
			return nil, errors.New("unsupported requirement directive; declare and check manifests explicitly")
		}
		name := normalizePythonName(line)
		if name == "" {
			return nil, errors.New("invalid requirement name")
		}
		out[name] = true
	}
	return out, nil
}

func pyprojectNames(raw []byte) (map[string]bool, error) {
	var doc struct {
		Project struct {
			Dependencies []string
			Optional     map[string][]string `toml:"optional-dependencies"`
			Dynamic      []string
		}
		Tool struct {
			Poetry struct {
				Dependencies map[string]any
				Group        map[string]struct{ Dependencies map[string]any }
			}
		}
		DependencyGroups map[string][]any `toml:"dependency-groups"`
	}
	if err := toml.Unmarshal(raw, &doc); err != nil {
		return nil, errors.New("invalid pyproject TOML")
	}
	out := map[string]bool{}
	add := func(spec string) error {
		name := normalizePythonName(spec)
		if name == "" {
			return errors.New("invalid Python dependency name")
		}
		out[name] = true
		return nil
	}
	for _, field := range doc.Project.Dynamic {
		if field == "dependencies" || field == "optional-dependencies" {
			return nil, errors.New("dynamic Python dependencies are unsupported")
		}
	}
	for _, spec := range doc.Project.Dependencies {
		if err := add(spec); err != nil {
			return nil, err
		}
	}
	for _, group := range doc.Project.Optional {
		for _, spec := range group {
			if err := add(spec); err != nil {
				return nil, err
			}
		}
	}
	for name := range doc.Tool.Poetry.Dependencies {
		if name != "python" {
			if err := add(name); err != nil {
				return nil, err
			}
		}
	}
	for _, group := range doc.Tool.Poetry.Group {
		for name := range group.Dependencies {
			if err := add(name); err != nil {
				return nil, err
			}
		}
	}
	for _, group := range doc.DependencyGroups {
		for _, value := range group {
			spec, ok := value.(string)
			if !ok {
				return nil, errors.New("python dependency-group includes are unsupported")
			}
			if err := add(spec); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func cargoNames(raw []byte) (map[string]bool, error) {
	type dependencies struct {
		Dependencies map[string]any
		Dev          map[string]any `toml:"dev-dependencies"`
		Build        map[string]any `toml:"build-dependencies"`
	}
	var doc struct {
		Dependencies map[string]any
		Dev          map[string]any `toml:"dev-dependencies"`
		Build        map[string]any `toml:"build-dependencies"`
		Workspace    dependencies
		Target       map[string]dependencies
	}
	if err := toml.Unmarshal(raw, &doc); err != nil {
		return nil, errors.New("invalid Cargo TOML")
	}
	out := map[string]bool{}
	add := func(d dependencies) {
		for _, m := range []map[string]any{d.Dependencies, d.Dev, d.Build} {
			for name := range m {
				out[name] = true
			}
		}
	}
	add(dependencies{doc.Dependencies, doc.Dev, doc.Build})
	add(doc.Workspace)
	for _, d := range doc.Target {
		add(d)
	}
	return out, nil
}
