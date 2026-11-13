package credentialbroker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/ananta888/dockweave/internal/credentialvault"
)

// Config wires the broker. All fields are required except HTTPClient,
// MaxBodyBytes and Logger.
type Config struct {
	Vault        *credentialvault.Store
	Policy       *Policy
	Sessions     *SessionStore
	Budget       *Budget
	Providers    map[string]Provider
	HTTPClient   *http.Client
	MaxBodyBytes int64
	Logger       *slog.Logger
	// AllowInsecureHTTP permits http:// provider BaseURLs. Only for tests.
	AllowInsecureHTTP bool
}

// RequestLogEntry is a secret-free audit record of one broker request.
type RequestLogEntry struct {
	Time             time.Time `json:"time"`
	Agent            string    `json:"agent"`
	Provider         string    `json:"provider"`
	Credential       string    `json:"credential,omitempty"`
	Method           string    `json:"method"`
	Path             string    `json:"path"`
	Model            string    `json:"model,omitempty"`
	Status           int       `json:"status"`
	Denied           bool      `json:"denied,omitempty"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	DurationMS       int64     `json:"duration_ms"`
}

// Server is the agent-facing HTTP gateway.
type Server struct {
	cfg     Config
	client  *http.Client
	logMu   sync.Mutex
	recent  []RequestLogEntry
	maxLogs int

	// refreshMu serializes OAuth refreshes; refresh tokens rotate and two
	// concurrent refreshes would invalidate each other.
	refreshMu sync.Mutex
}

// NewServer validates configuration and returns a broker handler.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Vault == nil || cfg.Policy == nil || cfg.Sessions == nil || cfg.Budget == nil {
		return nil, errors.New("vault, policy, sessions and budget are required")
	}
	if cfg.Providers == nil {
		cfg.Providers = DefaultProviders()
	}
	for _, p := range cfg.Providers {
		if err := validateProvider(p); err != nil {
			return nil, err
		}
		if !cfg.AllowInsecureHTTP && !strings.HasPrefix(p.BaseURL, "https://") {
			return nil, fmt.Errorf("provider %q: base_url must use https", p.Name)
		}
		if p.OAuth != nil && !cfg.AllowInsecureHTTP && !strings.HasPrefix(p.OAuth.TokenURL, "https://") {
			return nil, fmt.Errorf("provider %q: oauth token_url must use https", p.Name)
		}
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 32 << 20
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Minute,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				// Never follow redirects automatically: the injected credential
				// must not be forwarded to a destination that was not policy
				// checked. A 3xx is passed through to the client instead.
				return http.ErrUseLastResponse
			},
		}
	}
	return &Server{cfg: cfg, client: client, maxLogs: 512}, nil
}

// ServeHTTP implements the agent-facing gateway.
//
//	/p/{provider}/{upstream-path}?{query}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}
	start := time.Now()
	entry := RequestLogEntry{Time: start.UTC(), Method: r.Method, Path: r.URL.Path}

	token := sessionToken(r)
	sess, err := s.cfg.Sessions.Validate(token, start)
	if err != nil {
		entry.Status, entry.Denied = http.StatusUnauthorized, true
		s.record(entry)
		writeError(w, http.StatusUnauthorized, "invalid or expired session")
		return
	}
	entry.Agent = sess.Agent

	providerName, upstreamPath, ok := splitGatewayPath(r.URL.Path)
	if !ok {
		entry.Status, entry.Denied = http.StatusNotFound, true
		s.record(entry)
		writeError(w, http.StatusNotFound, "unknown broker path")
		return
	}
	entry.Provider = providerName
	provider, ok := s.cfg.Providers[providerName]
	if !ok {
		entry.Status, entry.Denied = http.StatusNotFound, true
		s.record(entry)
		writeError(w, http.StatusNotFound, "unknown provider")
		return
	}

	body, err := readLimited(r.Body, s.cfg.MaxBodyBytes)
	if err != nil {
		entry.Status = http.StatusRequestEntityTooLarge
		s.record(entry)
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	model := extractModel(body)
	entry.Model = model

	grant, err := s.cfg.Policy.Match(sess.Agent, providerName, model, start)
	if err != nil {
		entry.Status, entry.Denied = http.StatusForbidden, true
		s.record(entry)
		writeError(w, http.StatusForbidden, "credential access denied")
		return
	}
	entry.Credential = grant.Credential

	secret, err := s.cfg.Vault.Get(grant.Credential)
	if err != nil || secret.Provider != providerName {
		entry.Status = http.StatusForbidden
		entry.Denied = true
		s.record(entry)
		writeError(w, http.StatusForbidden, "credential unavailable")
		return
	}

	promptEst, completionEst := estimateTokens(body)
	res, err := s.cfg.Budget.Acquire(grant, promptEst, completionEst)
	if err != nil {
		entry.Status = http.StatusTooManyRequests
		entry.Denied = true
		s.record(entry)
		writeError(w, http.StatusTooManyRequests, "credit or rate limit exceeded")
		return
	}
	committed := false
	defer func() {
		if !committed {
			res.Release()
		}
	}()

	target, err := buildTargetURL(provider, upstreamPath, r.URL.RawQuery)
	if err != nil {
		entry.Status = http.StatusBadRequest
		s.record(entry)
		writeError(w, http.StatusBadRequest, "invalid upstream path")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		entry.Status = http.StatusBadRequest
		s.record(entry)
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	copyRequestHeaders(req.Header, r.Header)
	// Remove any agent-supplied credential material before injecting ours.
	req.Header.Del("Authorization")
	req.Header.Del("X-Api-Key")
	req.Header.Del("X-Goog-Api-Key")
	req.Header.Del("Proxy-Authorization")
	req.Header.Del("X-Dockweave-Session")
	if err := s.injectCredential(r.Context(), req, provider, secret); err != nil {
		entry.Status = http.StatusBadGateway
		s.record(entry)
		writeError(w, http.StatusBadGateway, "credential refresh failed")
		return
	}

	resp, err := s.client.Do(req)
	if err != nil {
		entry.Status = http.StatusBadGateway
		s.record(entry)
		writeError(w, http.StatusBadGateway, "upstream request failed")
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	uw := newUsageWriter(w, resp.Header.Get("Content-Type"))
	written, copyErr := io.Copy(uw, resp.Body)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	prompt, completion := uw.usage()
	if prompt == 0 && completion == 0 {
		prompt = promptEst
		completion = int64(written) / 4
	}
	res.Commit(prompt, completion, grant)
	committed = true

	entry.Status = resp.StatusCode
	entry.PromptTokens = prompt
	entry.CompletionTokens = completion
	entry.DurationMS = time.Since(start).Milliseconds()
	s.record(entry)
	if copyErr != nil {
		s.cfg.Logger.Warn("broker stream interrupted", "agent", sess.Agent,
			"provider", providerName, "error", copyErr.Error())
	}
}

// RecentRequests returns a snapshot of the secret-free request log.
func (s *Server) RecentRequests() []RequestLogEntry {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	out := make([]RequestLogEntry, len(s.recent))
	copy(out, s.recent)
	return out
}

func (s *Server) record(e RequestLogEntry) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.recent = append(s.recent, e)
	if len(s.recent) > s.maxLogs {
		s.recent = s.recent[len(s.recent)-s.maxLogs:]
	}
	if e.Denied {
		s.cfg.Logger.Warn("broker denied", "agent", e.Agent, "provider", e.Provider,
			"path", e.Path, "status", e.Status)
	}
}

func sessionToken(r *http.Request) string {
	if v := r.Header.Get("X-Dockweave-Session"); v != "" {
		return v
	}
	if v, ok := bearerToken(r.Header.Get("Authorization")); ok {
		return v
	}
	if v := r.Header.Get("X-Api-Key"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Goog-Api-Key"); v != "" {
		return v
	}
	return ""
}

func splitGatewayPath(p string) (provider, upstream string, ok bool) {
	const prefix = "/p/"
	if !strings.HasPrefix(p, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(p, prefix)
	provider, upstream, found := strings.Cut(rest, "/")
	if !found || provider == "" {
		return "", "", false
	}
	clean := path.Clean("/" + upstream)
	if strings.Contains(upstream, "..") || strings.Contains(upstream, "\\") {
		return "", "", false
	}
	return provider, clean, true
}

func buildTargetURL(p Provider, upstream, rawQuery string) (string, error) {
	base, err := url.Parse(p.BaseURL)
	if err != nil {
		return "", err
	}
	if base.Scheme != "https" && base.Scheme != "http" {
		return "", errors.New("unsupported provider scheme")
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + upstream
	base.RawQuery = rawQuery
	return base.String(), nil
}

func copyRequestHeaders(dst, src http.Header) {
	for k, vv := range src {
		if isHopByHop(k) || strings.EqualFold(k, "Host") || strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for k, vv := range src {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func isHopByHop(k string) bool {
	switch http.CanonicalHeaderKey(k) {
	case "Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
		"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	}
	return false
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("body too large")
	}
	return b, nil
}

func extractModel(body []byte) string {
	var v struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return ""
	}
	return v.Model
}

func estimateTokens(body []byte) (prompt, completion int64) {
	prompt = int64(len(body)) / 4
	var v struct {
		MaxTokens           int64 `json:"max_tokens"`
		MaxCompletionTokens int64 `json:"max_completion_tokens"`
	}
	_ = json.Unmarshal(body, &v)
	completion = v.MaxTokens
	if v.MaxCompletionTokens > completion {
		completion = v.MaxCompletionTokens
	}
	if completion <= 0 {
		completion = 1024 // conservative default when the caller omits a cap
	}
	return prompt, completion
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// usageWriter forwards the response while extracting token usage without
// buffering the whole (possibly streaming) body.
type usageWriter struct {
	w        io.Writer
	isSSE    bool
	buf      []byte
	lineBuf  []byte
	prompt   int64
	complete int64
	seen     bool
}

func newUsageWriter(w io.Writer, contentType string) *usageWriter {
	return &usageWriter{w: w, isSSE: strings.Contains(strings.ToLower(contentType), "text/event-stream")}
}

func (u *usageWriter) Write(p []byte) (int, error) {
	n, err := u.w.Write(p)
	if u.isSSE {
		u.lineBuf = append(u.lineBuf, p[:n]...)
		for {
			i := bytes.IndexByte(u.lineBuf, '\n')
			if i < 0 {
				break
			}
			line := bytes.TrimSpace(u.lineBuf[:i])
			u.lineBuf = u.lineBuf[i+1:]
			u.parseSSELine(line)
		}
		if len(u.lineBuf) > 1<<20 {
			u.lineBuf = u.lineBuf[len(u.lineBuf)-(1<<20):]
		}
	} else if len(u.buf) < 1<<20 {
		remaining := (1 << 20) - len(u.buf)
		if len(p) < remaining {
			remaining = len(p)
		}
		u.buf = append(u.buf, p[:remaining]...)
	}
	return n, err
}

func (u *usageWriter) parseSSELine(line []byte) {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(line[len("data:"):])
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return
	}
	if p, c, ok := parseUsage(payload); ok {
		u.setUsage(p, c)
	}
}

func (u *usageWriter) setUsage(p, c int64) {
	if p >= u.prompt {
		u.prompt = p
	}
	if c >= u.complete {
		u.complete = c
	}
	u.seen = true
}

func (u *usageWriter) usage() (int64, int64) {
	if u.isSSE {
		return u.prompt, u.complete
	}
	if p, c, ok := parseUsage(u.buf); ok {
		return p, c
	}
	return 0, 0
}

// parseUsage walks a JSON document for common usage fields.
func parseUsage(data []byte) (prompt, completion int64, ok bool) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return 0, 0, false
	}
	var p, c int64
	walkUsage(v, &p, &c)
	if p == 0 && c == 0 {
		return 0, 0, false
	}
	return p, c, true
}

func walkUsage(v any, p, c *int64) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			switch k {
			case "prompt_tokens", "input_tokens":
				addInt(val, p)
			case "completion_tokens", "output_tokens":
				addInt(val, c)
			case "total_tokens":
				var total int64
				addInt(val, &total)
				if *p == 0 && *c == 0 {
					*p = total
				}
			default:
				walkUsage(val, p, c)
			}
		}
	case []any:
		for _, e := range t {
			walkUsage(e, p, c)
		}
	}
}

func addInt(v any, dst *int64) {
	switch n := v.(type) {
	case float64:
		*dst += int64(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			*dst += i
		}
	}
}

// Check interface at compile time.
var _ http.Handler = (*Server)(nil)
