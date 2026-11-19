package gateexec

import (
	"encoding/json"
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
}

// scanSecrets inspects changed text files for obvious credential material. The
// secret value itself is never returned, only the path and the pattern name.
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
		for _, pat := range secretPatterns {
			if pat.re.MatchString(text) {
				found = append(found, rel+": "+pat.name)
				break
			}
		}
	}
	sort.Strings(found)
	return found
}
