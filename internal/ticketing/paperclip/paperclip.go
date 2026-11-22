// Package paperclip is the ticketing adapter for Paperclip (ADR 0003 D-2).
// It talks to Paperclip's HTTP API (not the CLI) and maps Paperclip issues,
// comments and process agents onto the canonical ticketing types
// (anti-corruption layer).
//
// Agent identity: a Paperclip process agent drives a DockWeave agent when
// its adapterConfig.args[0] names that agent (that is how
// deployments/paperclip/dockweave-run.sh is wired). Paperclip agents without
// such a binding appear as "paperclip-agent:<uuid>"; Paperclip users as
// "paperclip-user:<id>".
package paperclip

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ananta888/dockweave/internal/ticketing"
)

// Name is the provider name in refs and configuration.
const Name = "paperclip"

// MinVersion is the oldest Paperclip release the adapter was verified
// against (API shapes in fake_test.go).
const MinVersion = "2026.1106.0"

// versionAtLeast compares dotted numeric versions (YYYY.MMDD.N).
func versionAtLeast(v, min string) bool {
	a, b := strings.Split(v, "."), strings.Split(min, ".")
	for i := 0; i < len(b); i++ {
		var x, y int
		if i < len(a) {
			if _, err := fmt.Sscanf(a[i], "%d", &x); err != nil {
				return false
			}
		}
		fmt.Sscanf(b[i], "%d", &y)
		if x != y {
			return x > y
		}
	}
	return true
}

// Prefixes for Paperclip identities without a DockWeave counterpart.
const (
	ForeignAgentPrefix = "paperclip-agent:"
	UserPrefix         = "paperclip-user:"
)

// AgentDirectory resolves DockWeave agents (implemented by the daemon).
type AgentDirectory interface {
	// Resolve maps a DockWeave agent name or id to its stable id.
	Resolve(ctx context.Context, nameOrID string) (id string, ok bool)
}

// Options configure the adapter.
type Options struct {
	BaseURL   string // default http://127.0.0.1:3100
	CompanyID string // empty: the single company of the instance
	// TokenFile holds a Paperclip board API key (optional; local_trusted
	// instances need none). The token is never logged.
	TokenFile string
	HTTP      *http.Client
	Agents    AgentDirectory
	// AgentCacheTTL bounds how long the agent mapping is reused.
	AgentCacheTTL time.Duration
}

// Provider implements ticketing.Provider against Paperclip.
type Provider struct {
	opt Options

	mu        sync.Mutex
	company   string
	agentsAt  time.Time
	pcToDW    map[string]string // paperclip agent id -> dockweave agent id
	dwToPC    map[string]string
	pcNames   map[string]string
	tokenOnce sync.Once
	token     string
}

// New returns the adapter.
func New(opt Options) *Provider {
	if opt.BaseURL == "" {
		opt.BaseURL = "http://127.0.0.1:3100"
	}
	opt.BaseURL = strings.TrimRight(opt.BaseURL, "/")
	if opt.HTTP == nil {
		opt.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	if opt.AgentCacheTTL == 0 {
		opt.AgentCacheTTL = 30 * time.Second
	}
	return &Provider{opt: opt, company: opt.CompanyID}
}

// Register makes "paperclip" selectable. Config options: paperclip.url,
// paperclip.company, paperclip.token_file.
func Register(dir AgentDirectory) {
	ticketing.Register(Name, func(cfg ticketing.Config) (ticketing.Provider, error) {
		return New(Options{
			BaseURL: cfg.Options["paperclip.url"], CompanyID: cfg.Options["paperclip.company"],
			TokenFile: cfg.Options["paperclip.token_file"], Agents: dir,
		}), nil
	})
}

func (p *Provider) Name() string { return Name }
func (p *Provider) Capabilities() ticketing.Capabilities {
	return ticketing.Capabilities{Threads: true, Inbox: true, Children: true, Wake: true}
}
func (p *Provider) Tickets() ticketing.Tickets { return p }
func (p *Provider) Threads() ticketing.Threads { return p }
func (p *Provider) Inbox() ticketing.Inbox     { return p }
func (p *Provider) Waker() ticketing.Waker     { return p }

// ---- HTTP ----

type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return fmt.Sprintf("paperclip %d: %s", e.status, e.msg) }

func (p *Provider) bearer() string {
	p.tokenOnce.Do(func() {
		if p.opt.TokenFile == "" {
			return
		}
		if b, err := os.ReadFile(p.opt.TokenFile); err == nil {
			p.token = strings.TrimSpace(string(b))
		}
	})
	return p.token
}

// do performs a request; out may be nil. Errors map onto the ticketing
// sentinels: 404 NotFound, 409 Conflict, 400/422 Invalid, transport and 5xx
// Unavailable (fail closed).
func (p *Provider) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.opt.BaseURL+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if tok := p.bearer(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := p.opt.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ticketing.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error any `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Error != nil {
			msg = fmt.Sprint(e.Error)
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		ae := &apiError{status: resp.StatusCode, msg: msg}
		switch {
		case resp.StatusCode == http.StatusNotFound:
			return fmt.Errorf("%w: %v", ticketing.ErrNotFound, ae)
		case resp.StatusCode == http.StatusConflict:
			return fmt.Errorf("%w: %v", ticketing.ErrConflict, ae)
		case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity:
			return fmt.Errorf("%w: %v", ticketing.ErrInvalid, ae)
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode >= 500:
			return fmt.Errorf("%w: %v", ticketing.ErrUnavailable, ae)
		}
		return ae
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%w: invalid paperclip response: %v", ticketing.ErrUnavailable, err)
		}
	}
	return nil
}

// Health checks /api/health and that the company can be resolved.
func (p *Provider) Health(ctx context.Context) ticketing.Health {
	h := ticketing.Health{Checked: time.Now().UTC()}
	var st struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := p.do(ctx, http.MethodGet, "/api/health", nil, &st); err != nil {
		h.Detail = err.Error()
		return h
	}
	if st.Status != "ok" {
		h.Detail = "paperclip status " + st.Status
		return h
	}
	if !versionAtLeast(st.Version, MinVersion) {
		h.Detail = fmt.Sprintf("paperclip %s is older than the supported %s", st.Version, MinVersion)
		return h
	}
	if _, err := p.companyID(ctx); err != nil {
		h.Detail = err.Error()
		return h
	}
	h.OK, h.Detail = true, "paperclip "+st.Version
	return h
}

func (p *Provider) companyID(ctx context.Context) (string, error) {
	p.mu.Lock()
	cid := p.company
	p.mu.Unlock()
	if cid != "" {
		return cid, nil
	}
	var list []struct {
		ID string `json:"id"`
	}
	if err := p.do(ctx, http.MethodGet, "/api/companies", nil, &list); err != nil {
		return "", err
	}
	if len(list) != 1 {
		return "", fmt.Errorf("%w: %d paperclip companies; set paperclip.company", ticketing.ErrInvalid, len(list))
	}
	p.mu.Lock()
	p.company = list[0].ID
	p.mu.Unlock()
	return list[0].ID, nil
}

// ---- agent mapping ----

type pcAgent struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	AdapterType   string `json:"adapterType"`
	AdapterConfig struct {
		Args []string `json:"args"`
	} `json:"adapterConfig"`
}

func (p *Provider) loadAgents(ctx context.Context, force bool) error {
	p.mu.Lock()
	fresh := !force && p.pcToDW != nil && time.Since(p.agentsAt) < p.opt.AgentCacheTTL
	p.mu.Unlock()
	if fresh {
		return nil
	}
	cid, err := p.companyID(ctx)
	if err != nil {
		return err
	}
	var list []pcAgent
	if err := p.do(ctx, http.MethodGet, "/api/companies/"+url.PathEscape(cid)+"/agents", nil, &list); err != nil {
		return err
	}
	pcToDW, dwToPC, names := map[string]string{}, map[string]string{}, map[string]string{}
	for _, a := range list {
		names[a.ID] = a.Name
		if a.AdapterType != "process" || len(a.AdapterConfig.Args) == 0 || p.opt.Agents == nil {
			continue
		}
		dw, ok := p.opt.Agents.Resolve(ctx, a.AdapterConfig.Args[0])
		if !ok {
			continue
		}
		pcToDW[a.ID] = dw
		if _, dup := dwToPC[dw]; !dup {
			dwToPC[dw] = a.ID
		}
	}
	p.mu.Lock()
	p.pcToDW, p.dwToPC, p.pcNames, p.agentsAt = pcToDW, dwToPC, names, time.Now()
	p.mu.Unlock()
	return nil
}

func (p *Provider) dwAgent(ctx context.Context, pcID string) ticketing.Actor {
	_ = p.loadAgents(ctx, false)
	p.mu.Lock()
	defer p.mu.Unlock()
	if dw, ok := p.pcToDW[pcID]; ok {
		return ticketing.Actor{Kind: ticketing.ActorAgent, ID: dw, Name: p.pcNames[pcID]}
	}
	return ticketing.Actor{Kind: ticketing.ActorAgent, ID: ForeignAgentPrefix + pcID, Name: p.pcNames[pcID]}
}

func (p *Provider) pcAgentID(ctx context.Context, dwID string) (string, error) {
	if strings.HasPrefix(dwID, ForeignAgentPrefix) {
		return strings.TrimPrefix(dwID, ForeignAgentPrefix), nil
	}
	for _, force := range []bool{false, true} {
		if err := p.loadAgents(ctx, force); err != nil {
			return "", err
		}
		p.mu.Lock()
		id, ok := p.dwToPC[dwID]
		p.mu.Unlock()
		if ok {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: no Paperclip process agent drives DockWeave agent %q (sync agents first)", ticketing.ErrInvalid, dwID)
}

// ---- issue mapping ----

type pcIssue struct {
	ID              string  `json:"id"`
	Identifier      string  `json:"identifier"`
	Title           string  `json:"title"`
	Description     *string `json:"description"`
	Status          string  `json:"status"`
	Priority        string  `json:"priority"`
	AssigneeAgentID *string `json:"assigneeAgentId"`
	AssigneeUserID  *string `json:"assigneeUserId"`
	ParentID        *string `json:"parentId"`
	CreatedByAgent  *string `json:"createdByAgentId"`
	CreatedByUser   *string `json:"createdByUserId"`
	CheckoutRunID   *string `json:"checkoutRunId"`
	StatusVersion   int     `json:"statusVersion"`
	ProjectID       *string `json:"projectId"`
	GoalID          *string `json:"goalId"`
	CreatedAt       string  `json:"createdAt"`
	UpdatedAt       string  `json:"updatedAt"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func parseTS(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t.UTC()
}

// version is a digest of the canonical fields plus updatedAt, so it changes
// on every effective change even within one millisecond.
func version(is *pcIssue) string {
	h := sha256.New()
	for _, v := range []string{is.Title, str(is.Description), is.Status, is.Priority, str(is.AssigneeAgentID),
		str(is.AssigneeUserID), str(is.ParentID), is.UpdatedAt, fmt.Sprint(is.StatusVersion)} {
		h.Write([]byte(v))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func (p *Provider) toTicket(ctx context.Context, is *pcIssue) ticketing.Ticket {
	t := ticketing.Ticket{
		Ref: ticketing.Ref{Provider: Name, ID: is.ID}, Identifier: is.Identifier,
		Title: is.Title, Description: str(is.Description),
		Status: ticketing.Status(is.Status), Priority: ticketing.Priority(is.Priority),
		CreatedAt: parseTS(is.CreatedAt), UpdatedAt: parseTS(is.UpdatedAt), Version: version(is),
		Extra: map[string]any{},
	}
	if !t.Status.Valid() {
		t.Extra["paperclip_status"] = is.Status
		t.Status = ticketing.StatusTodo
	}
	if !t.Priority.Valid() {
		t.Extra["paperclip_priority"] = is.Priority
		t.Priority = ""
	}
	if id := str(is.AssigneeAgentID); id != "" {
		a := p.dwAgent(ctx, id)
		t.Assignee = &a
		// Paperclip's checkout puts the assignee in progress; that is the hold.
		if is.Status == string(ticketing.StatusInProgress) {
			t.CheckedOutBy = a.ID
		}
	} else if id := str(is.AssigneeUserID); id != "" {
		t.Assignee = &ticketing.Actor{Kind: ticketing.ActorHuman, ID: UserPrefix + id}
	}
	if id := str(is.ParentID); id != "" {
		t.Parent = &ticketing.Ref{Provider: Name, ID: id}
	}
	switch {
	case str(is.CreatedByAgent) != "":
		a := p.dwAgent(ctx, str(is.CreatedByAgent))
		t.CreatedBy = &a
	case str(is.CreatedByUser) != "":
		t.CreatedBy = &ticketing.Actor{Kind: ticketing.ActorHuman, ID: UserPrefix + str(is.CreatedByUser)}
	}
	for k, v := range map[string]string{"project_id": str(is.ProjectID), "goal_id": str(is.GoalID)} {
		if v != "" {
			t.Extra[k] = v
		}
	}
	if len(t.Extra) == 0 {
		t.Extra = nil
	}
	return t
}

func (p *Provider) getIssue(ctx context.Context, id string) (*pcIssue, error) {
	var is pcIssue
	if err := p.do(ctx, http.MethodGet, "/api/issues/"+url.PathEscape(id), nil, &is); err != nil {
		return nil, err
	}
	return &is, nil
}

// assigneeFields maps a canonical assignee onto Paperclip fields.
func (p *Provider) assigneeFields(ctx context.Context, a *ticketing.Actor, body map[string]any) error {
	if a == nil {
		body["assigneeAgentId"], body["assigneeUserId"] = nil, nil
		return nil
	}
	if a.Kind == ticketing.ActorAgent {
		id, err := p.pcAgentID(ctx, a.ID)
		if err != nil {
			return err
		}
		body["assigneeAgentId"], body["assigneeUserId"] = id, nil
		return nil
	}
	if !strings.HasPrefix(a.ID, UserPrefix) {
		return fmt.Errorf("%w: human %q has no Paperclip user (assign paperclip-user:<id>)", ticketing.ErrInvalid, a.ID)
	}
	body["assigneeUserId"], body["assigneeAgentId"] = strings.TrimPrefix(a.ID, UserPrefix), nil
	return nil
}

// ---- Tickets ----

func (p *Provider) Get(ctx context.Context, id string) (*ticketing.Ticket, error) {
	is, err := p.getIssue(ctx, id)
	if err != nil {
		return nil, err
	}
	t := p.toTicket(ctx, is)
	return &t, nil
}

func (p *Provider) List(ctx context.Context, f ticketing.Filter) ([]ticketing.Ticket, error) {
	cid, err := p.companyID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if len(f.Status) > 0 {
		var ss []string
		for _, s := range f.Status {
			ss = append(ss, string(s))
		}
		q.Set("status", strings.Join(ss, ","))
	}
	if f.Assignee != nil && f.Assignee.Kind == ticketing.ActorAgent {
		id, err := p.pcAgentID(ctx, f.Assignee.ID)
		if err != nil {
			return nil, nil // nobody in Paperclip can be assigned to it
		}
		q.Set("assigneeAgentId", id)
	}
	if f.Parent != nil {
		if f.Parent.Provider != Name {
			return nil, nil
		}
		q.Set("parentId", f.Parent.ID)
	}
	if s := strings.TrimSpace(f.Query); s != "" {
		q.Set("q", s)
	}
	path := "/api/companies/" + url.PathEscape(cid) + "/issues"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var list []pcIssue
	if err := p.do(ctx, http.MethodGet, path, nil, &list); err != nil {
		return nil, err
	}
	// Server filters are an optimisation; the canonical filter is applied
	// again here so semantics do not depend on Paperclip's query support.
	out := make([]ticketing.Ticket, 0, len(list))
	for i := range list {
		t := p.toTicket(ctx, &list[i])
		if matches(&t, f) {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func matches(t *ticketing.Ticket, f ticketing.Filter) bool {
	if len(f.Status) > 0 {
		ok := false
		for _, s := range f.Status {
			ok = ok || t.Status == s
		}
		if !ok {
			return false
		}
	}
	if f.Assignee != nil && (t.Assignee == nil || t.Assignee.Kind != f.Assignee.Kind || t.Assignee.ID != f.Assignee.ID) {
		return false
	}
	if f.Parent != nil && (t.Parent == nil || *t.Parent != *f.Parent) {
		return false
	}
	if !f.UpdatedSince.IsZero() && t.UpdatedAt.Before(f.UpdatedSince) {
		return false
	}
	if q := strings.ToLower(strings.TrimSpace(f.Query)); q != "" &&
		!strings.Contains(strings.ToLower(t.Title), q) && !strings.Contains(strings.ToLower(t.Description), q) {
		return false
	}
	return true
}

func (p *Provider) Create(ctx context.Context, n ticketing.NewTicket, _ ticketing.Actor) (*ticketing.Ticket, error) {
	if err := n.Validate(); err != nil {
		return nil, err
	}
	cid, err := p.companyID(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"title": n.Title, "status": string(n.Status)}
	if n.Description != "" {
		body["description"] = n.Description
	}
	if n.Priority != "" {
		body["priority"] = string(n.Priority)
	}
	if n.Assignee != nil {
		if err := p.assigneeFields(ctx, n.Assignee, body); err != nil {
			return nil, err
		}
	}
	if n.Parent != nil {
		if n.Parent.Provider != Name {
			return nil, fmt.Errorf("%w: parent belongs to provider %q", ticketing.ErrInvalid, n.Parent.Provider)
		}
		if _, err := p.getIssue(ctx, n.Parent.ID); err != nil {
			return nil, err
		}
		body["parentId"] = n.Parent.ID
	}
	var is pcIssue
	if err := p.do(ctx, http.MethodPost, "/api/companies/"+url.PathEscape(cid)+"/issues", body, &is); err != nil {
		return nil, err
	}
	t := p.toTicket(ctx, &is)
	return &t, nil
}

// Update patches the issue. Paperclip has no full optimistic version, so an
// ExpectedVersion is checked against a fresh read and status changes carry
// Paperclip's expectedStatusVersion (closing the race for status).
func (p *Provider) Update(ctx context.Context, id string, u ticketing.Update, _ ticketing.Actor) (*ticketing.Ticket, error) {
	if err := u.Validate(); err != nil {
		return nil, err
	}
	cur, err := p.getIssue(ctx, id)
	if err != nil {
		return nil, err
	}
	if u.ExpectedVersion != "" && u.ExpectedVersion != version(cur) {
		return nil, fmt.Errorf("%w: ticket %s changed", ticketing.ErrConflict, id)
	}
	body := map[string]any{}
	if u.Title != nil {
		body["title"] = strings.TrimSpace(*u.Title)
	}
	if u.Description != nil {
		body["description"] = *u.Description
	}
	if u.Status != nil {
		body["status"] = string(*u.Status)
		body["expectedStatusVersion"] = cur.StatusVersion
	}
	if u.Priority != nil {
		body["priority"] = string(*u.Priority)
	}
	if u.Assignee != nil {
		if err := p.assigneeFields(ctx, u.Assignee, body); err != nil {
			return nil, err
		}
	}
	if u.ClearAssignee {
		_ = p.assigneeFields(ctx, nil, body)
	}
	var is pcIssue
	if err := p.do(ctx, http.MethodPatch, "/api/issues/"+url.PathEscape(id), body, &is); err != nil {
		return nil, err
	}
	t := p.toTicket(ctx, &is)
	return &t, nil
}

// ---- Threads ----

type pcComment struct {
	ID            string  `json:"id"`
	IssueID       string  `json:"issueId"`
	Body          string  `json:"body"`
	AuthorType    string  `json:"authorType"`
	AuthorAgentID *string `json:"authorAgentId"`
	AuthorUserID  *string `json:"authorUserId"`
	CreatedAt     string  `json:"createdAt"`
	DeletedAt     *string `json:"deletedAt"`
}

func (p *Provider) Comments(ctx context.Context, id string) ([]ticketing.Comment, error) {
	if _, err := p.getIssue(ctx, id); err != nil {
		return nil, err
	}
	var list []pcComment
	if err := p.do(ctx, http.MethodGet, "/api/issues/"+url.PathEscape(id)+"/comments", nil, &list); err != nil {
		return nil, err
	}
	out := make([]ticketing.Comment, 0, len(list))
	for _, c := range list {
		if c.DeletedAt != nil {
			continue
		}
		out = append(out, p.toComment(ctx, id, &c))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (p *Provider) toComment(ctx context.Context, issueID string, c *pcComment) ticketing.Comment {
	out := ticketing.Comment{ID: c.ID, Ticket: ticketing.Ref{Provider: Name, ID: issueID}, Body: c.Body,
		CreatedAt: parseTS(c.CreatedAt), Origin: Name + ":" + c.ID}
	switch {
	case str(c.AuthorAgentID) != "":
		out.Author = p.dwAgent(ctx, str(c.AuthorAgentID))
	case str(c.AuthorUserID) != "":
		out.Author = ticketing.Actor{Kind: ticketing.ActorHuman, ID: UserPrefix + str(c.AuthorUserID)}
	default:
		out.Author = ticketing.Actor{Kind: ticketing.ActorSystem}
	}
	// A comment written through DockWeave keeps its real author and origin.
	if body, m, ok := decodeMarker(c.Body); ok {
		out.Body = body
		if m.Actor.Valid() {
			out.Author = m.Actor
		}
		if m.Origin != "" {
			out.Origin = m.Origin
		}
	}
	return out
}

// Paperclip's comment metadata has a closed schema, so DockWeave authorship
// travels in the body: a visible attribution line and an HTML comment that
// Markdown renderers hide. Both are removed again when reading.
const markerPrefix = "<!-- dockweave:"

type marker struct {
	Actor  ticketing.Actor `json:"actor"`
	Origin string          `json:"origin,omitempty"`
}

func encodeMarker(body string, m marker) string {
	who := m.Actor.Name
	if who == "" {
		who = m.Actor.ID
	}
	b, _ := json.Marshal(m)
	// "--" would end the HTML comment early.
	enc := strings.ReplaceAll(string(b), "--", `\u002d\u002d`)
	line := ""
	if who != "" {
		line = "\n\n— " + who + " (via DockWeave)"
	}
	return body + line + "\n" + markerPrefix + enc + " -->"
}

func decodeMarker(body string) (string, marker, bool) {
	i := strings.LastIndex(body, markerPrefix)
	if i < 0 || !strings.HasSuffix(strings.TrimRight(body, "\n "), "-->") {
		return body, marker{}, false
	}
	raw := strings.TrimSuffix(strings.TrimRight(body[i+len(markerPrefix):], "\n "), "-->")
	var m marker
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &m) != nil {
		return body, marker{}, false
	}
	text := strings.TrimRight(body[:i], "\n")
	if j := strings.LastIndex(text, "\n\n— "); j >= 0 && strings.HasSuffix(text, " (via DockWeave)") {
		text = text[:j]
	}
	return text, m, true
}

func (p *Provider) AddComment(ctx context.Context, id, body string, by ticketing.Actor) (*ticketing.Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("%w: empty comment", ticketing.ErrInvalid)
	}
	if !by.Valid() {
		return nil, fmt.Errorf("%w: invalid author", ticketing.ErrInvalid)
	}
	if _, err := p.getIssue(ctx, id); err != nil {
		return nil, err
	}
	req := map[string]any{"body": encodeMarker(body, marker{Actor: by})}
	var c pcComment
	if err := p.do(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(id)+"/comments", req, &c); err != nil {
		return nil, err
	}
	out := p.toComment(ctx, id, &c)
	return &out, nil
}

// ---- Inbox ----

var openForCheckout = []string{"backlog", "todo", "blocked", "in_review"}

func (p *Provider) Next(ctx context.Context, agentID string) (*ticketing.Ticket, error) {
	list, err := p.List(ctx, ticketing.Filter{Status: []ticketing.Status{ticketing.StatusTodo},
		Assignee: &ticketing.Actor{Kind: ticketing.ActorAgent, ID: agentID}})
	if err != nil || len(list) == 0 {
		return nil, err
	}
	rank := map[ticketing.Priority]int{ticketing.PriorityCritical: 0, ticketing.PriorityHigh: 1, ticketing.PriorityMedium: 2, "": 3, ticketing.PriorityLow: 4}
	sort.SliceStable(list, func(i, j int) bool { return rank[list[i].Priority] < rank[list[j].Priority] })
	return &list[0], nil
}

// Checkout uses Paperclip's atomic checkout (assignee + in_progress).
func (p *Provider) Checkout(ctx context.Context, id, agentID string) error {
	if strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("%w: empty agent", ticketing.ErrInvalid)
	}
	cur, err := p.Get(ctx, id)
	if err != nil {
		return err
	}
	if cur.CheckedOutBy == agentID {
		return nil
	}
	if cur.CheckedOutBy != "" {
		return fmt.Errorf("%w: ticket %s is checked out by %s", ticketing.ErrConflict, id, cur.CheckedOutBy)
	}
	pcID, err := p.pcAgentID(ctx, agentID)
	if err != nil {
		return err
	}
	err = p.do(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(id)+"/checkout",
		map[string]any{"agentId": pcID, "expectedStatuses": openForCheckout}, nil)
	if errors.Is(err, ticketing.ErrInvalid) {
		// Paperclip rejects a checkout from an unexpected status (e.g. a
		// concurrent winner moved it to in_progress) with 4xx.
		return fmt.Errorf("%w: %v", ticketing.ErrConflict, err)
	}
	return err
}

func (p *Provider) Release(ctx context.Context, id, agentID string) error {
	cur, err := p.Get(ctx, id)
	if err != nil {
		return err
	}
	if cur.CheckedOutBy != agentID || agentID == "" {
		return fmt.Errorf("%w: ticket %s is not checked out by %s", ticketing.ErrConflict, id, agentID)
	}
	return p.do(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(id)+"/release", map[string]any{}, nil)
}

// ---- Waker ----

func (p *Provider) Wake(ctx context.Context, agentID string) error {
	pcID, err := p.pcAgentID(ctx, agentID)
	if err != nil {
		return err
	}
	return p.do(ctx, http.MethodPost, "/api/agents/"+url.PathEscape(pcID)+"/wakeup",
		map[string]any{"source": "on_demand", "reason": "dockweave", "forceFreshSession": false}, nil)
}
