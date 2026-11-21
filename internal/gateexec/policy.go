package gateexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
func scanSecrets(root string, changed []string) ([]string, error) {
	return scanSecretsContext(context.Background(), root, changed)
}
func scanSecretsContext(ctx context.Context, root string, changed []string) ([]string, error) {
	r, err := openRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.close()
	var found []string
	for _, rel := range changed {
		data, err := r.readFile(ctx, rel, 1<<20)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("scan changed file %q: %w", rel, err)
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
	return found, nil
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
