package gateexec

import (
	"encoding/json"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type packageManifest struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func dependencyNames(raw []byte) map[string]bool {
	out := map[string]bool{}
	if len(raw) == 0 {
		return out
	}
	var m packageManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return out
	}
	for name := range m.Dependencies {
		out[name] = true
	}
	for name := range m.DevDependencies {
		out[name] = true
	}
	return out
}

// newDependencies compares the package.json manifests before and after a run.
func newDependencies(before, after []byte) []string {
	b := dependencyNames(before)
	a := dependencyNames(after)
	var added []string
	for name := range a {
		if !b[name] {
			added = append(added, name)
		}
	}
	sort.Strings(added)
	return added
}

// matchesAllowList reports whether a changed path is covered by the configured
// allow patterns. A pattern ending in "/" is a prefix; otherwise path.Match
// semantics plus a directory-prefix match are used.
func matchesAllowList(p string, patterns []string) bool {
	p = filepath.ToSlash(p)
	for _, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		if strings.HasSuffix(pat, "/") {
			if strings.HasPrefix(p, pat) {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pat, p); ok {
			return true
		}
		if p == pat || strings.HasPrefix(p, pat+"/") {
			return true
		}
	}
	return false
}

func outOfScope(changed, allow []string) []string {
	if len(allow) == 0 {
		return nil
	}
	var out []string
	for _, p := range changed {
		if !matchesAllowList(p, allow) {
			out = append(out, p)
		}
	}
	return out
}

var secretPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"aws-access-key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"private-key", regexp.MustCompile(`-----BEGIN (RSA |OPENSSH |EC )?PRIVATE KEY-----`)},
	{"github-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`)},
	{"openai-key", regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`)},
	{"slack-token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"generic-bearer", regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._-]{24,}`)},
	{"google-api-key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`)},
	{"slack-webhook", regexp.MustCompile(`https://hooks\.slack\.com/services/T[A-Za-z0-9_]+/B[A-Za-z0-9_]+/[A-Za-z0-9_]+`)},
	{"jwt", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{"stripe-key", regexp.MustCompile(`(?:sk|pk)_(?:live|test)_[0-9A-Za-z]{16,}`)},
	{"gitlab-token", regexp.MustCompile(`glpat-[0-9A-Za-z_-]{20,}`)},
	{"npm-token", regexp.MustCompile(`npm_[A-Za-z0-9]{36,}`)},
}

// scanSecrets inspects changed text files for obvious credential material. The
// secret value itself is never returned, only the path and the pattern name.
// When no known pattern matches, a high-entropy token check catches
// credentials that do not follow a well-known shape.
func scanSecrets(root string, changed []string) []string {
	var found []string
	for _, rel := range changed {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() > 1<<20 {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		if strings.IndexByte(string(data), 0) >= 0 { // binary
			continue
		}
		text := string(data)
		matched := false
		for _, pat := range secretPatterns {
			if pat.re.MatchString(text) {
				found = append(found, rel+": "+pat.name)
				matched = true
				break
			}
		}
		if matched || isLockfile(rel) {
			continue
		}
		if label := highEntropyToken(text); label != "" {
			found = append(found, rel+": "+label)
		}
	}
	sort.Strings(found)
	return found
}

// highEntropyToken returns a label when the text contains a compact token with
// unusually high Shannon entropy — a common signal for generated secrets.
var highEntropyRe = regexp.MustCompile(`[A-Za-z0-9+/_-]{24,}`)

// hashDigestRe matches well-known content-hash notations (go.sum "h1:" and
// Subresource-Integrity "sha512-…" values). They are high-entropy by design
// but never secrets, so they are removed before the entropy check.
var hashDigestRe = regexp.MustCompile(`(?:\bh1:|\bsha(?:1|256|384|512)-)[A-Za-z0-9+/_=-]+`)

// lockfileNames are generated dependency lockfiles. They consist mostly of
// hashes, so the entropy fallback is skipped for them (the pattern scan for
// known credential shapes still applies).
var lockfileNames = map[string]bool{
	"go.sum":              true,
	"package-lock.json":   true,
	"npm-shrinkwrap.json": true,
	"yarn.lock":           true,
	"pnpm-lock.yaml":      true,
	"bun.lock":            true,
	"Cargo.lock":          true,
	"poetry.lock":         true,
	"Pipfile.lock":        true,
	"uv.lock":             true,
	"composer.lock":       true,
	"Gemfile.lock":        true,
}

func isLockfile(rel string) bool {
	return lockfileNames[path.Base(filepath.ToSlash(rel))]
}

func highEntropyToken(text string) string {
	text = hashDigestRe.ReplaceAllString(text, " ")
	for _, m := range highEntropyRe.FindAllString(text, -1) {
		if len(m) > 64 { // long base64 blobs are usually not secrets
			continue
		}
		if shannonEntropy(m) >= 4.3 {
			return "high-entropy-token"
		}
	}
	return ""
}

func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	freq := map[byte]int{}
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}
	var h float64
	for _, c := range freq {
		p := float64(c) / float64(len(s))
		h -= p * math.Log2(p)
	}
	return h
}

// ---- multi-manifest dependency detection ----

// manifestFiles are the dependency manifests compared by deny_new_dependencies.
var manifestFiles = []string{
	"package.json",
	"go.mod",
	"requirements.txt",
	"pyproject.toml",
	"Cargo.toml",
}

// newDependenciesAcrossManifests compares every known dependency manifest
// before and after a run and returns newly added names, each prefixed with the
// manifest file so the diff scope report is actionable.
func newDependenciesAcrossManifests(beforeRoot, afterRoot string) []string {
	var added []string
	for _, m := range manifestFiles {
		before, _ := os.ReadFile(filepath.Join(beforeRoot, m))
		after, _ := os.ReadFile(filepath.Join(afterRoot, m))
		b := manifestDependencyNames(m, before)
		a := manifestDependencyNames(m, after)
		for name := range a {
			if !b[name] {
				added = append(added, m+": "+name)
			}
		}
	}
	sort.Strings(added)
	return added
}

func manifestDependencyNames(name string, raw []byte) map[string]bool {
	switch name {
	case "package.json":
		return dependencyNames(raw)
	case "go.mod":
		return goModRequires(raw)
	case "requirements.txt":
		return requirementsNames(raw)
	case "pyproject.toml":
		return pyprojectNames(raw)
	case "Cargo.toml":
		return cargoNames(raw)
	}
	return map[string]bool{}
}

// goModRequires extracts required module paths from a go.mod file (both the
// block form and the inline form).
func goModRequires(raw []byte) map[string]bool {
	out := map[string]bool{}
	inBlock := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "require (" {
			inBlock = true
			continue
		}
		if line == ")" {
			inBlock = false
			continue
		}
		if strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "require ") {
			fields := strings.Fields(strings.TrimPrefix(line, "require "))
			if len(fields) > 0 {
				out[fields[0]] = true
			}
			continue
		}
		if inBlock {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				out[fields[0]] = true
			}
		}
	}
	return out
}

// normalizePythonName reduces a PEP 508 requirement string to its PEP 503
// normalised project name: extras, version specifiers, URLs and environment
// markers are dropped, the name is lower-cased and runs of "-", "_" and "."
// collapse to a single "-". This keeps version bumps from looking like new
// dependencies.
func normalizePythonName(spec string) string {
	spec = strings.TrimSpace(spec)
	if i := strings.IndexByte(spec, ';'); i >= 0 {
		spec = spec[:i]
	}
	end := len(spec)
	for i, c := range spec {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			end = i
			break
		}
	}
	name := strings.ToLower(spec[:end])
	return pythonSepRe.ReplaceAllString(strings.Trim(name, "-_."), "-")
}

var pythonSepRe = regexp.MustCompile(`[-_.]+`)

// requirementsNames extracts requirement names from a pip requirements file.
func requirementsNames(raw []byte) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, " #"); i >= 0 {
			line = line[:i]
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		if name := normalizePythonName(line); name != "" {
			out[name] = true
		}
	}
	return out
}

// pyprojectNames extracts dependency names from a pyproject.toml, covering both
// the PEP 621 `[project] dependencies = [...]` form and the Poetry
// `[tool.poetry.dependencies]` table form. Names are PEP 503 normalised.
func pyprojectNames(raw []byte) map[string]bool {
	out := map[string]bool{}
	add := func(spec string) {
		if name := normalizePythonName(spec); name != "" {
			out[name] = true
		}
	}
	inProjectDeps := false
	inPoetryDeps := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && !inProjectDeps {
			inPoetryDeps = strings.HasPrefix(trimmed, "[tool.poetry.dependencies") ||
				(strings.HasPrefix(trimmed, "[tool.poetry.group.") && strings.HasSuffix(trimmed, ".dependencies]"))
			continue
		}
		if !inPoetryDeps && strings.HasPrefix(trimmed, "dependencies") && strings.Contains(trimmed, "=") {
			_, value, _ := strings.Cut(trimmed, "=")
			value = strings.TrimSpace(value)
			inProjectDeps = strings.HasPrefix(value, "[") && !strings.Contains(stripQuoted(value), "]")
			for _, m := range quotedStrings(value) {
				add(m)
			}
			continue
		}
		if inProjectDeps {
			for _, m := range quotedStrings(trimmed) {
				add(m)
			}
			if strings.Contains(stripQuoted(trimmed), "]") {
				inProjectDeps = false
			}
			continue
		}
		if inPoetryDeps {
			if idx := strings.Index(trimmed, "="); idx > 0 {
				key := strings.Trim(strings.TrimSpace(trimmed[:idx]), "\"'")
				if key != "" && key != "python" && !strings.ContainsAny(key, " \t") {
					add(key)
				}
			}
		}
	}
	return out
}

// cargoTableRe classifies a Cargo.toml table header (without brackets):
// group 1 is the dependency table kind, group 2 the crate name of the
// `[dependencies.<name>]` form. Target-specific tables
// (`target.'cfg(unix)'.dependencies`) are included.
var cargoTableRe = regexp.MustCompile(`^(?:target\.(?:'[^']*'|"[^"]*"|[^.'"]+)\.)?(dependencies|dev-dependencies|build-dependencies)(?:\.(.+))?$`)

// cargoNames extracts dependency names from the dependency tables of a
// Cargo.toml: inline entries (`serde = "1"`, `serde.workspace = true`) and the
// table form (`[dependencies.serde]`).
func cargoNames(raw []byte) map[string]bool {
	out := map[string]bool{}
	inDeps := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inDeps = false
			header := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
			if i := strings.Index(header, "]"); i >= 0 { // trailing comment
				header = strings.TrimSpace(header[:i])
			}
			m := cargoTableRe.FindStringSubmatch(header)
			if m == nil {
				continue
			}
			if m[2] != "" {
				if name := strings.Trim(strings.TrimSpace(m[2]), "\"'"); name != "" {
					out[name] = true
				}
				continue
			}
			inDeps = true
			continue
		}
		if !inDeps || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if idx := strings.Index(trimmed, "="); idx > 0 {
			key := strings.TrimSpace(trimmed[:idx])
			if !strings.HasPrefix(key, "\"") && !strings.HasPrefix(key, "'") {
				if i := strings.IndexByte(key, '.'); i > 0 { // serde.workspace = true
					key = key[:i]
				}
			}
			key = strings.Trim(strings.TrimSpace(key), "\"'")
			if key != "" && !strings.ContainsAny(key, " \t") {
				out[key] = true
			}
		}
	}
	return out
}

// stripQuoted removes quoted strings from a line so that brackets inside a
// requirement (extras such as "pkg[extra]") are not taken as array syntax.
func stripQuoted(line string) string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return b.String()
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// quotedStrings extracts the first single- or double-quoted string of each
// quote pair on a line.
func quotedStrings(line string) []string {
	var out []string
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c != '"' && c != '\'' {
			continue
		}
		end := strings.IndexByte(line[i+1:], c)
		if end < 0 {
			break
		}
		out = append(out, line[i+1:i+1+end])
		i = i + 1 + end
	}
	return out
}
