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
		if matched {
			continue
		}
		if highEntropyToken(text) != "" {
			found = append(found, rel+": "+highEntropyToken(text))
		}
	}
	sort.Strings(found)
	return found
}

// highEntropyToken returns a label when the text contains a compact token with
// unusually high Shannon entropy — a common signal for generated secrets.
var highEntropyRe = regexp.MustCompile(`[A-Za-z0-9+/_-]{24,}`)

func highEntropyToken(text string) string {
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

// requirementsNames extracts requirement names from a pip requirements file.
func requirementsNames(raw []byte) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		name := line
		for _, sep := range []string{"==", ">=", "<=", "~=", "!=", ">", "<", "@", "["} {
			if i := strings.Index(name, sep); i >= 0 {
				name = name[:i]
			}
		}
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = true
		}
	}
	return out
}

// pyprojectNames extracts dependency names from a pyproject.toml, covering both
// the PEP 621 `[project] dependencies = [...]` form and the Poetry
// `[tool.poetry.dependencies]` table form.
func pyprojectNames(raw []byte) map[string]bool {
	out := map[string]bool{}
	inProjectDeps := false
	inPoetryDeps := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inProjectDeps = false
			inPoetryDeps = strings.HasPrefix(trimmed, "[tool.poetry.dependencies") ||
				(strings.HasPrefix(trimmed, "[tool.poetry.group.") && strings.HasSuffix(trimmed, ".dependencies]"))
			continue
		}
		if strings.HasPrefix(trimmed, "dependencies") && strings.Contains(trimmed, "=") {
			inProjectDeps = strings.Contains(trimmed, "[")
			for _, m := range quotedStrings(trimmed) {
				out[m] = true
			}
			continue
		}
		if inProjectDeps {
			if trimmed == "]" {
				inProjectDeps = false
				continue
			}
			for _, m := range quotedStrings(trimmed) {
				out[m] = true
			}
			continue
		}
		if inPoetryDeps {
			if idx := strings.Index(trimmed, "="); idx > 0 {
				name := strings.TrimSpace(trimmed[:idx])
				if name != "" && !strings.ContainsAny(name, " \t\"'") {
					out[name] = true
				}
			}
		}
	}
	return out
}

// cargoNames extracts dependency names from a Cargo.toml dependency table.
func cargoNames(raw []byte) map[string]bool {
	out := map[string]bool{}
	inDeps := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inDeps = trimmed == "[dependencies]" || trimmed == "[dev-dependencies]" ||
				trimmed == "[build-dependencies]" ||
				strings.HasPrefix(trimmed, "[dependencies.") ||
				strings.HasPrefix(trimmed, "[dev-dependencies.") ||
				strings.HasPrefix(trimmed, "[build-dependencies.")
			continue
		}
		if !inDeps || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if idx := strings.Index(trimmed, "="); idx > 0 {
			name := strings.TrimSpace(trimmed[:idx])
			if name != "" && !strings.ContainsAny(name, " \t\"'") {
				out[name] = true
			}
		}
	}
	return out
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
