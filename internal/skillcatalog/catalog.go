// Package skillcatalog manages DockWeave's bundled and installed Agent Skills.
//
// A skill is a folder with a SKILL.md (YAML frontmatter + instructions) plus
// optional references/scripts/assets. The catalog keeps installed skills in a
// managed directory; agents get a copy materialized into their harness-specific
// skills folder by internal/agentconfig.
package skillcatalog

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed all:bundled
var bundledFS embed.FS

const bundledRoot = "bundled"
const maxSkillBytes = 32 << 20
const maxSkillFiles = 800

// Entry is one skill available to DockWeave.
type Entry struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Path        string    `json:"path"`
	Store       string    `json:"store"`
	Bundled     bool      `json:"bundled,omitempty"`
	Repo        string    `json:"repo,omitempty"`
	Ref         string    `json:"ref,omitempty"`
	InstalledAt time.Time `json:"installed_at,omitempty"`
}

// installedMeta records where an installed skill came from.
type installedMeta struct {
	Repo        string    `json:"repo"`
	Path        string    `json:"path"`
	Ref         string    `json:"ref"`
	InstalledAt time.Time `json:"installed_at"`
}

const metaFile = ".dockweave-skill.json"

// StoreEntry is a curated skill in an external store.
type StoreEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

// Store is a curated external skill repository.
type Store struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	URL         string       `json:"url"`
	Repo        string       `json:"repo"`
	Ref         string       `json:"ref"`
	Description string       `json:"description"`
	Entries     []StoreEntry `json:"entries"`
}

// Snapshot is the catalog payload returned to the web UI.
type Snapshot struct {
	Bundled   []Entry `json:"bundled"`
	Installed []Entry `json:"installed"`
	Stores    []Store `json:"stores"`
}

// Catalog manages skills in a directory on the host.
type Catalog struct {
	dir         string
	http        *http.Client
	archiveBase string
}

// New creates a catalog rooted at dir and syncs the embedded skills.
func New(dir string) (*Catalog, error) {
	if dir == "" {
		return nil, errors.New("skills directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create skills dir: %w", err)
	}
	c := &Catalog{dir: dir, http: &http.Client{Timeout: 60 * time.Second}, archiveBase: "https://codeload.github.com"}
	if err := c.syncBundled(); err != nil {
		return nil, err
	}
	return c, nil
}

// Dir returns the managed skills directory.
func (c *Catalog) Dir() string { return c.dir }

// Snapshot returns bundled, installed and curated store entries.
func (c *Catalog) Snapshot() Snapshot {
	installed, _ := c.List()
	out := Snapshot{Installed: installed, Stores: defaultStores()}
	for _, e := range installed {
		if e.Bundled {
			out.Bundled = append(out.Bundled, e)
		}
	}
	return out
}

// List returns all installed skills (including bundled).
func (c *Catalog) List() ([]Entry, error) {
	des, err := os.ReadDir(c.dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, de := range des {
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		dir := filepath.Join(c.dir, de.Name())
		name, desc, err := readSkillMeta(dir)
		if err != nil {
			continue
		}
		fi, _ := os.Stat(dir)
		e := Entry{Name: name, Description: desc, Path: dir, Store: "installed"}
		if fi != nil {
			e.InstalledAt = fi.ModTime()
		}
		if m, err := readInstalledMeta(dir); err == nil {
			e.Repo, e.Ref, e.Store = m.Repo, m.Ref, "installed"
			if !m.InstalledAt.IsZero() {
				e.InstalledAt = m.InstalledAt
			}
		}
		if isBundledName(name) {
			e.Bundled = true
			e.Store = "bundled"
			e.Repo = "ananta888/keycloak-oidc-human-guide"
			e.Ref = "main"
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Path resolves an installed skill name to its host directory.
func (c *Catalog) Path(name string) (string, bool) {
	if !validName(name) {
		return "", false
	}
	dir := filepath.Join(c.dir, name)
	if _, _, err := readSkillMeta(dir); err != nil {
		return "", false
	}
	return dir, true
}

// Delete removes an installed skill. Bundled skills can be removed too; they
// are restored on the next daemon start.
func (c *Catalog) Delete(name string) error {
	if !validName(name) {
		return errors.New("invalid skill name")
	}
	dir := filepath.Join(c.dir, name)
	if _, err := os.Stat(dir); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// Install fetches a skill folder from an allowlisted GitHub repository and
// stores it in the managed directory. Only the subdirectory at path is
// extracted; symlinks, devices and oversized archives are rejected.
func (c *Catalog) Install(ctx context.Context, repo, skillPath, ref string) (Entry, error) {
	if !allowedRepo(repo) {
		return Entry{}, fmt.Errorf("repository %q is not in the skill catalog", repo)
	}
	repo, ref = normalizeRepo(repo, ref)
	if !validRef(ref) {
		return Entry{}, errors.New("invalid git ref")
	}
	clean, err := cleanSkillPath(skillPath)
	if err != nil {
		return Entry{}, err
	}
	url := fmt.Sprintf("%s/%s/tar.gz/%s", c.archiveBase, repo, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Entry{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Entry{}, fmt.Errorf("download %s: %w", repo, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Entry{}, fmt.Errorf("download %s: HTTP %d", repo, resp.StatusCode)
	}
	gz, err := gzip.NewReader(io.LimitReader(resp.Body, maxSkillBytes))
	if err != nil {
		return Entry{}, fmt.Errorf("read archive: %w", err)
	}
	defer gz.Close()

	tmp, err := os.MkdirTemp(c.dir, ".install-*")
	if err != nil {
		return Entry{}, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(tmp)
		}
	}()

	tr := tar.NewReader(gz)
	var total int64
	files := 0
	// The tarball has a single top-level directory; the requested skill path
	// starts after it.
	prefix := ""
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Entry{}, fmt.Errorf("read archive: %w", err)
		}
		name := path.Clean("/" + hdr.Name)
		name = strings.TrimPrefix(name, "/")
		if name == "" || strings.HasPrefix(name, "../") {
			return Entry{}, errors.New("unsafe archive path")
		}
		if prefix == "" {
			prefix = strings.SplitN(name, "/", 2)[0]
		}
		rel := strings.TrimPrefix(name, prefix+"/")
		if rel == name || !strings.HasPrefix(rel, clean+"/") {
			continue
		}
		rel = strings.TrimPrefix(rel, clean+"/")
		if rel == "" || strings.HasPrefix(rel, "../") {
			return Entry{}, errors.New("unsafe archive path")
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
			files++
			total += hdr.Size
			if files > maxSkillFiles || total > maxSkillBytes {
				return Entry{}, errors.New("skill archive too large")
			}
			dst := filepath.Join(tmp, filepath.FromSlash(rel))
			if !strings.HasPrefix(dst, tmp+string(os.PathSeparator)) {
				return Entry{}, errors.New("unsafe archive path")
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return Entry{}, err
			}
			mode := os.FileMode(0o644)
			if hdr.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return Entry{}, err
			}
			if _, err := io.CopyN(f, tr, hdr.Size); err != nil {
				f.Close()
				return Entry{}, err
			}
			f.Close()
		default:
			// Symlinks, hardlinks, devices and fifos are not allowed.
			continue
		}
	}

	metaName, desc, err := readSkillMeta(tmp)
	if err != nil {
		return Entry{}, fmt.Errorf("archive does not contain a valid SKILL.md: %w", err)
	}
	name := metaName
	if !validName(name) {
		name = path.Base(clean)
	}
	if !validName(name) {
		return Entry{}, errors.New("skill has no usable name")
	}
	if old, err := readInstalledMeta(filepath.Join(c.dir, name)); err == nil {
		if old.Repo != repo || old.Path != clean {
			return Entry{}, fmt.Errorf("a skill named %q is already installed from %s; delete it first", name, old.Repo)
		}
	}
	meta, _ := json.Marshal(installedMeta{Repo: repo, Path: clean, Ref: ref, InstalledAt: time.Now()})
	if err := os.WriteFile(filepath.Join(tmp, metaFile), meta, 0o644); err != nil {
		return Entry{}, err
	}
	dst := filepath.Join(c.dir, name)
	if err := os.RemoveAll(dst); err != nil {
		return Entry{}, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return Entry{}, err
	}
	ok = true
	return Entry{Name: name, Description: desc, Path: dst, Store: "installed",
		Repo: repo, Ref: ref, InstalledAt: time.Now()}, nil
}

// syncBundled writes the embedded standard skill into the managed directory
// when it is missing or has a different embedded version.
func (c *Catalog) syncBundled() error {
	return copyEmbedded(bundledFS, bundledRoot, c.dir)
}

func copyEmbedded(fsys embed.FS, root, dstRoot string) error {
	entries, err := fsys.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skill := e.Name()
		dst := filepath.Join(dstRoot, skill)
		if _, _, err := readSkillMeta(dst); err == nil {
			continue // already installed; do not overwrite user changes
		}
		if err := extractEmbeddedTree(fsys, path.Join(root, skill), dst); err != nil {
			return err
		}
	}
	return nil
}

func extractEmbeddedTree(fsys embed.FS, root, dst string) error {
	tmp, err := os.MkdirTemp(filepath.Dir(dst), ".bundle-*")
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(tmp)
		}
	}()
	var walk func(string, string) error
	walk = func(src, out string) error {
		es, err := fsys.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range es {
			sp := path.Join(src, e.Name())
			dp := filepath.Join(out, e.Name())
			if e.IsDir() {
				if err := os.MkdirAll(dp, 0o755); err != nil {
					return err
				}
				if err := walk(sp, dp); err != nil {
					return err
				}
				continue
			}
			b, err := fsys.ReadFile(sp)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dp), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(dp, b, 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, tmp); err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	ok = true
	return nil
}

func isBundledName(name string) bool {
	_, err := bundledFS.ReadFile(path.Join(bundledRoot, name, "SKILL.md"))
	return err == nil
}

func readInstalledMeta(dir string) (installedMeta, error) {
	var m installedMeta
	b, err := os.ReadFile(filepath.Join(dir, metaFile))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, nil
}

func readSkillMeta(dir string) (name, description string, err error) {
	b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return "", "", err
	}
	name, description = parseFrontmatter(string(b))
	if name == "" {
		name = filepath.Base(dir)
	}
	return name, description, nil
}

func parseFrontmatter(s string) (name, description string) {
	s = strings.TrimPrefix(s, "\ufeff")
	if !strings.HasPrefix(s, "---") {
		return "", ""
	}
	rest := s[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", ""
	}
	fm := rest[:end]
	lines := strings.Split(fm, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		switch {
		case strings.HasPrefix(line, "name:"):
			name = yamlValue(line[len("name:"):])
		case strings.HasPrefix(line, "description:"):
			description = yamlValue(line[len("description:"):])
			if description == ">" || description == "|" {
				var parts []string
				for j := i + 1; j < len(lines); j++ {
					if !strings.HasPrefix(lines[j], " ") && !strings.HasPrefix(lines[j], "\t") {
						break
					}
					parts = append(parts, strings.TrimSpace(lines[j]))
					i = j
				}
				description = strings.Join(parts, " ")
			}
		}
	}
	return strings.TrimSpace(name), strings.TrimSpace(description)
}

func yamlValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		if v[0] == '"' {
			if u, err := strconv.Unquote(v); err == nil {
				return u
			}
		}
		return v[1 : len(v)-1]
	}
	return v
}

func validName(n string) bool {
	if n == "" || n == "." || n == ".." || strings.ContainsAny(n, `/\`) {
		return false
	}
	for _, r := range n {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

func validRef(ref string) bool {
	if ref == "" || len(ref) > 200 || strings.Contains(ref, "..") {
		return false
	}
	for _, r := range ref {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' && r != '.' && r != '/' {
			return false
		}
	}
	return true
}

func cleanSkillPath(p string) (string, error) {
	p = strings.TrimSpace(strings.Trim(p, "/"))
	if p == "" {
		return "", errors.New("skill path is required")
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("unsafe skill path")
		}
	}
	return p, nil
}

func normalizeRepo(repo, ref string) (string, string) {
	repo = strings.TrimSpace(strings.TrimPrefix(repo, "https://github.com/"))
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.Trim(repo, "/")
	if ref == "" {
		ref = "main"
	}
	return repo, ref
}

func defaultStores() []Store {
	return []Store{
		{
			ID: "claude", Name: "Claude Skills (Anthropic)",
			URL: "https://github.com/anthropics/skills", Repo: "anthropics/skills", Ref: "main",
			Description: "Offizielle Agent Skills von Anthropic: Dokumente, Design, MCP, Testing.",
			Entries: []StoreEntry{
				{Name: "docx", Path: "skills/docx", Description: "Word-Dokumente erstellen, lesen, bearbeiten"},
				{Name: "pdf", Path: "skills/pdf", Description: "PDFs lesen, teilen, Formulare, OCR"},
				{Name: "pptx", Path: "skills/pptx", Description: "Präsentationen erstellen und bearbeiten"},
				{Name: "xlsx", Path: "skills/xlsx", Description: "Tabellen erstellen, bearbeiten, Formeln"},
				{Name: "skill-creator", Path: "skills/skill-creator", Description: "Neue Skills bauen und evaluieren"},
				{Name: "mcp-builder", Path: "skills/mcp-builder", Description: "MCP-Server entwerfen und implementieren"},
				{Name: "webapp-testing", Path: "skills/webapp-testing", Description: "Web-Apps mit Playwright testen"},
				{Name: "brand-guidelines", Path: "skills/brand-guidelines", Description: "Marken- und Designvorgaben anwenden"},
			},
		},
		{
			ID: "codex", Name: "Codex Skills (OpenAI)",
			URL: "https://github.com/openai/skills", Repo: "openai/skills", Ref: "main",
			Description: "Kuratierte Skills für OpenAI Codex (Agent-Skills-Standard).",
			Entries: []StoreEntry{
				{Name: "openai-docs", Path: "skills/.curated/openai-docs", Description: "Offizielle OpenAI-Dokumentation nachschlagen"},
				{Name: "pdf", Path: "skills/.curated/pdf", Description: "PDF-Verarbeitung"},
				{Name: "playwright", Path: "skills/.curated/playwright", Description: "Browser-Automatisierung mit Playwright"},
				{Name: "security-best-practices", Path: "skills/.curated/security-best-practices", Description: "Sicherheits-Best-Practices"},
				{Name: "gh-fix-ci", Path: "skills/.curated/gh-fix-ci", Description: "Fehlgeschlagene GitHub-Actions-CI reparieren"},
				{Name: "cli-creator", Path: "skills/.curated/cli-creator", Description: "Robuste CLIs aus API-Doku bauen"},
				{Name: "linear", Path: "skills/.curated/linear", Description: "Linear-Issues verwalten"},
				{Name: "sentry", Path: "skills/.curated/sentry", Description: "Sentry-Fehler analysieren"},
			},
		},
		{
			ID: "agentskills", Name: "Cross-Agent Skills (TerminalSkills)",
			URL: "https://github.com/TerminalSkills/skills", Repo: "TerminalSkills/skills", Ref: "main",
			Description: "Offene Skill-Bibliothek nach dem Agent-Skills-Standard; kompatibel mit OpenCode, Claude und Codex.",
			Entries: []StoreEntry{
				{Name: "pdf-analyzer", Path: "skills/pdf-analyzer", Description: "PDFs analysieren und zusammenfassen"},
				{Name: "mcp-server-builder", Path: "skills/mcp-server-builder", Description: "MCP-Server bauen"},
			},
		},
	}
}

func allowedRepo(repo string) bool {
	for _, s := range defaultStores() {
		if repo == s.Repo {
			return true
		}
	}
	return repo == "ananta888/keycloak-oidc-human-guide"
}
