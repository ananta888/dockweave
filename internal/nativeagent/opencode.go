package nativeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const opencodeServiceFile = "/memory/.local/state/opencode/service.json"
const opencodeSessionFile = "/memory/.dockweave/opencode-session"

// opencodePromptScript drives the OpenCode v2 HTTP API from inside the
// container. It never sees the DockWeave prompt as shell text: argv is passed
// directly to execve.
const opencodePromptScript = `
import base64, json, sys, time, urllib.request
url, sid, password, prompt, timeout_s = sys.argv[1:6]
timeout_s = float(timeout_s)
auth = "Basic " + base64.b64encode(("opencode:" + password).encode()).decode()

def call(path, method="GET", body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url + path, data=data, method=method,
        headers={"Authorization": auth, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=30) as resp:
        raw = resp.read()
        return json.loads(raw) if raw else {}

submitted = False
try:
    if not sid:
        sessions = call("/api/session").get("data", [])
        if not sessions:
            raise RuntimeError("no opencode session")
        sid = sessions[0]["id"]
    user = call("/api/session/%s/prompt" % sid, "POST", {"text": prompt})
    submitted = True
    started = user.get("data", {}).get("time", {}).get("created", 0)
    deadline = time.time() + timeout_s
    state = {}
    while time.time() < deadline:
        state = call("/api/session/%s" % sid).get("data", {})
        t = state.get("time", {})
        if t.get("idle", 0) and t.get("idle", 0) >= started:
            break
        time.sleep(0.3)
    msgs = call("/api/session/%s/message" % sid).get("data", [])
    msgs = [m for m in msgs if m.get("time", {}).get("created", 0) >= started]
    msgs.sort(key=lambda m: m.get("time", {}).get("created", 0))
    parts = []
    for m in msgs:
        if m.get("type") != "assistant":
            continue
        for c in m.get("content", []):
            if c.get("type") == "text" and c.get("text"):
                parts.append(c["text"])
    outcome = state.get("outcome", "")
    if outcome and outcome not in ("succeeded", "idle"):
        raise RuntimeError("session outcome: " + outcome)
    print(json.dumps({"session": sid, "text": "\n".join(parts).strip()}))
except Exception as e:
    print(json.dumps({"session": sid, "submitted": submitted, "error": str(e)}))
`

type opencodeService struct {
	URL      string `json:"url"`
	Password string `json:"password"`
}

func (m *Manager) promptOpenCode(ctx context.Context, idOrName, prompt string) (string, error) {
	cid, err := m.FindContainer(ctx, idOrName)
	if err != nil {
		return "", err
	}
	if cid == "" {
		return "", errors.New("agent is not running")
	}
	raw, err := m.Docker.ExecOutput(ctx, cid, []string{"cat", opencodeServiceFile})
	if err != nil {
		return "", fmt.Errorf("read opencode service: %w", err)
	}
	var svc opencodeService
	if err := json.Unmarshal([]byte(raw), &svc); err != nil || svc.URL == "" || svc.Password == "" {
		return "", errors.New("opencode server is not running with a native API")
	}
	sid := ""
	if b, err := m.Docker.ExecOutput(ctx, cid, []string{"cat", opencodeSessionFile}); err == nil {
		sid = strings.TrimSpace(b)
	}
	if sid == "" {
		// Not started through the native entrypoint (or the file is missing):
		// let the caller fall back to the TUI session driver.
		return "", errors.New("opencode native session is not pinned")
	}
	out, err := m.Docker.ExecOutput(ctx, cid, []string{
		"python3", "-c", opencodePromptScript,
		svc.URL, sid, svc.Password, prompt, strconv.Itoa(int(m.Timeout.Seconds())),
	})
	if err != nil {
		return "", fmt.Errorf("opencode api: %w", err)
	}
	var res struct {
		Session   string `json:"session"`
		Text      string `json:"text"`
		Submitted bool   `json:"submitted"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &res); err != nil {
		return "", fmt.Errorf("opencode api returned invalid JSON: %w", err)
	}
	if res.Error != "" {
		if res.Submitted {
			return "", fmt.Errorf("%w: %s", ErrAlreadySubmitted, res.Error)
		}
		return "", errors.New(res.Error)
	}
	return res.Text, nil
}
