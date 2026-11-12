(() => {
  'use strict';

  const STORAGE = 'dockweave-learning-v2';
  const GH_DW = 'https://github.com/ananta888/dockweave/blob/main/';
  const GH_HR = 'https://github.com/herdrdev/herdr/blob/master/';

  const state = load();

  function load() {
    try {
      const s = JSON.parse(localStorage.getItem(STORAGE) || '{}');
      return { done: Array.isArray(s.done) ? s.done : [], checks: Array.isArray(s.checks) ? s.checks : [] };
    } catch { return { done: [], checks: [] }; }
  }
  function save() { try { localStorage.setItem(STORAGE, JSON.stringify(state)); } catch {} }
  const isDone = id => state.done.includes(id);

  /* ================= helpers ================= */
  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  }

  // Compact markdown renderer for the original build-your-own.de.md sections.
  // Supports ~~~/``` fences, headings, lists, checkbox lists, tables, quotes, hr,
  // inline code, bold and links.
  function renderMd(src) {
    const lines = String(src).split('\n');
    const out = [];
    let i = 0;
    const inline = s => escapeHtml(s)
      .replace(/`([^`]+)`/g, '<code>$1</code>')
      .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
      .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');

    while (i < lines.length) {
      const line = lines[i];
      const fence = line.match(/^\s*(```|~~~)([A-Za-z0-9_-]*)\s*$/);
      if (fence) {
        const buf = [];
        i++;
        while (i < lines.length && !/^\s*(```|~~~)\s*$/.test(lines[i])) { buf.push(lines[i]); i++; }
        i++; // closing fence
        out.push('<pre><code>' + escapeHtml(buf.join('\n')) + '</code></pre>');
        continue;
      }
      if (/^\s*$/.test(line)) { i++; continue; }
      if (/^\s*-{3,}\s*$/.test(line)) { out.push('<hr>'); i++; continue; }

      const h = line.match(/^(#{1,6})\s+(.*)$/);
      if (h) {
        const lvl = Math.min(2 + h[1].length, 6);
        out.push('<h' + lvl + '>' + inline(h[2]) + '</h' + lvl + '>');
        i++; continue;
      }

      if (/^\s*[-*]\s+\[[ xX]\]\s+/.test(line)) {
        const items = [];
        while (i < lines.length && /^\s*[-*]\s+\[[ xX]\]\s+/.test(lines[i])) {
          const m = lines[i].match(/^\s*[-*]\s+\[([ xX])\]\s+(.*)$/);
          const checked = m[1].toLowerCase() === 'x';
          items.push('<li><input type="checkbox" disabled ' + (checked ? 'checked' : '') + '> ' + inline(m[2]) + '</li>');
          i++;
        }
        out.push('<ul>' + items.join('') + '</ul>');
        continue;
      }
      if (/^\s*[-*+]\s+/.test(line)) {
        const items = [];
        while (i < lines.length && /^\s*[-*+]\s+/.test(lines[i])) {
          items.push('<li>' + inline(lines[i].replace(/^\s*[-*+]\s+/, '')) + '</li>');
          i++;
        }
        out.push('<ul>' + items.join('') + '</ul>');
        continue;
      }
      if (/^\s*\d+[.)]\s+/.test(line)) {
        const items = [];
        while (i < lines.length && /^\s*\d+[.)]\s+/.test(lines[i])) {
          items.push('<li>' + inline(lines[i].replace(/^\s*\d+[.)]\s+/, '')) + '</li>');
          i++;
        }
        out.push('<ol>' + items.join('') + '</ol>');
        continue;
      }
      if (/^\s*>/.test(line)) {
        const buf = [];
        while (i < lines.length && /^\s*>/.test(lines[i])) {
          buf.push(lines[i].replace(/^\s*>\s?/, ''));
          i++;
        }
        out.push('<blockquote>' + inline(buf.join(' ')) + '</blockquote>');
        continue;
      }
      if (/^\s*\|.*\|\s*$/.test(line) && i + 1 < lines.length && /^\s*\|?[\s:|-]+\|?\s*$/.test(lines[i + 1])) {
        const header = line.split('|').slice(1, -1).map(c => c.trim());
        i += 2;
        const rows = [];
        while (i < lines.length && /^\s*\|.*\|\s*$/.test(lines[i])) {
          rows.push(lines[i].split('|').slice(1, -1).map(c => c.trim()));
          i++;
        }
        out.push('<table><thead><tr>' + header.map(c => '<th>' + inline(c) + '</th>').join('') + '</tr></thead><tbody>'
          + rows.map(r => '<tr>' + r.map(c => '<td>' + inline(c) + '</td>').join('') + '</tr>').join('') + '</tbody></table>');
        continue;
      }

      out.push('<p>' + inline(line) + '</p>');
      i++;
    }
    return out.join('\n');
  }

  /* ================= phases (17 Abschnitte aus build-your-own.de.md) ================= */
  const phases = [
    {
      id: 'm0', code: 'M0', num: '§0', title: 'Denken: Agent ≠ Modell',
      sub: 'Das mentale Modell, bevor du baust',
      body: [
        'Ein Modell erzeugt Text. Ein Harness macht daraus Aktionen.',
        'Der Agent ist Identität + Konfiguration + Harness-Laufzeit.',
        'Der Container ist die Sandbox — nicht die Identität.',
        'Trenne Modell, Harness, Sandbox, Policy und Credentials.'
      ],
      codeFiles: [
        { file: 'internal/model/model.go', lang: 'go', code:
`type Agent struct {
    ID   string ` + "`json:\"id\"`" + `   // ag_…, stabil über Container-Lebenszyklen
    Name string ` + "`json:\"name\"`" + `
    Type string ` + "`json:\"type\"`" + ` // codex | opencode | claude | gemini | custom
    Image string ` + "`json:\"image\"`" + `
    State AgentState ` + "`json:\"state\"`" + `
    ContainerID string ` + "`json:\"container_id,omitempty\"`" + ` // wechselt — Identität nicht
}` }
      ],
      md: `## 0. Das mentale Modell

Ein Agent ist **kein** einzelnes Programm. Er besteht aus mehreren Schichten:

~~~text
Nutzer / Operator
      │
      ▼
Steuerungsebene (CLI, API, UI)
      │  Identität, Policy, Audit
      ▼
Orchestrierung (Agenten, Ketten, Gates)
      │
      ├── Sandbox      (Container, Dateien, Netzwerk)
      ├── Harness      (Prompt, Tools, Loop)
      ├── Modell       (LLM-Provider)
      ├── Gedächtnis   (Memory, Knowledge, Skills)
      └── Credentials  (Vault, Broker, Sessions)
~~~

Merksätze:

- Das **Modell** erzeugt Text.
- Der **Harness** macht daraus Aktionen (Tools, Shell, Dateien).
- Die **Sandbox** begrenzt, was diese Aktionen erreichen können.
- Die **Steuerungsebene** entscheidet, wer was darf.
- **Credentials** gehören nicht in den Agenten.

Wenn du diese fünf Dinge sauber trennst, hast du die wichtigste Architektur verstanden.`,
      links: [['Konzeptleitfaden', 'https://github.com/ananta888/dockweave/blob/main/docs/learning/agent-harness.de.md']],
      quiz: {
        q: 'Was ist ein Harness?',
        options: ['Das LLM selbst', 'Die Schleife und Werkzeuge um das Modell', 'Ein Docker-Container', 'Ein API-Key'],
        answer: 1,
        ok: 'Richtig. Der Harness steuert das Modell und führt Tools aus.',
        bad: 'Nicht ganz: Das Modell erzeugt Text; der Harness macht daraus Aktionen.'
      }
    },
    {
      id: 'm1', code: 'M1', num: '§1', title: 'Der Agent-Loop',
      sub: 'planen → handeln → beobachten → wiederholen',
      body: [
        'Eine Schleife ruft das Modell auf, führt Tool-Calls aus und gibt Ergebnisse zurück.',
        'Setze maximale Schritte, Timeouts und Abbruchmöglichkeiten.',
        'Validiere Tool-Aufrufe gegen ein JSON-Schema.',
        'Begrenze Tool-Ausgaben, damit der Kontext nicht überläuft.'
      ],
      codeFiles: [],
      md: `## 1. Der kleinste Harness: der Agent-Loop

Ein Harness ist im Kern eine Schleife:

~~~go
for step := 0; step < maxSteps; step++ {
    resp := model.Chat(ctx, tools, history)
    if len(resp.ToolCalls) == 0 {
        return resp.Text
    }
    for _, call := range resp.ToolCalls {
        result := executeTool(ctx, call)
        history = append(history, toolResult(call, result))
    }
}
return errors.New("max steps exceeded")
~~~

Worauf achten:

- **Maximale Schritte** und **Timeouts** setzen.
- Tool-Aufrufe validieren, nicht blind ausführen.
- Ergebnisse begrenzen (keine riesigen Ausgaben in den Kontext).
- Abbruch durch den Nutzer ermöglichen.
- Jede Tool-Ausführung ist eine potenzielle Sicherheitsgrenze.

Ein Tool braucht einen klaren Contract:

~~~json
{
  "name": "read_file",
  "description": "Read a file",
  "inputSchema": {
    "type": "object",
    "properties": { "path": { "type": "string" } },
    "required": ["path"]
  }
}
~~~

DockWeave bringt keinen eigenen Harness mit: OpenCode, Codex, Claude Code usw. sind die Harnesses. DockWeave liefert die Umgebung, Identität und Grenzen.`,
      links: [],
      quiz: {
        q: 'Warum braucht der Loop ein Max-Steps-Limit?',
        options: ['Damit das Modell schneller antwortet', 'Damit ein Agent nicht endlos Tools aufruft', 'Damit Docker schneller startet', 'Damit der API-Key gültig bleibt'],
        answer: 1,
        ok: 'Genau. Ohne Limit kann eine Schleife dauerhaft weiterlaufen.',
        bad: 'Es geht um Begrenzung: Endlosschleifen und unkontrollierte Tool-Aufrufe verhindern.'
      }
    },
    {
      id: 'm2', code: 'M2', num: '§2', title: 'Sandbox',
      sub: 'Container, Rechte, Dateien, Ressourcen',
      body: [
        'Agent non-root starten, alle Capabilities droppen.',
        'Kein Docker-Socket und kein --privileged im Agenten.',
        'Nur explizit erlaubte Host-Verzeichnisse mounten.',
        'CPU-, Memory- und Netzwerklimits setzen.'
      ],
      codeFiles: [
        { file: 'internal/dockerclient/dockerclient.go', lang: 'go', code:
`host := &container.HostConfig{
    Mounts: []mount.Mount{
        {Type: mount.TypeBind, Source: spec.AgentDir, Target: "/agent", ReadOnly: a.AgentRO},
        {Type: mount.TypeBind, Source: spec.MemoryDir, Target: "/memory"},
        {Type: mount.TypeBind, Source: spec.WorkspaceDir, Target: "/workspace",
         BindOptions: &mount.BindOptions{Propagation: mount.PropagationRSlave}},
    },
    // Hardening: drop all capabilities; agent work must not need any.
    CapDrop:     []string{"ALL"},
    // Explicitly never privileged; seccomp stays at engine default.
    Privileged:  false,
    SecurityOpt: []string{"no-new-privileges"},
    NetworkMode: container.NetworkMode(a.NetworkMode),
    User:        fmt.Sprintf("%d:%d", a.UID, a.GID),
}` }
      ],
      md: `## 2. Sandbox: Warum ein Container allein nicht reicht

Ein minimaler Sandbox-Start könnte so aussehen:

~~~bash
docker run --rm -it \
  --user 1000:1000 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --network none \
  --memory 2g --cpus 1 \
  -v /srv/project:/workspace/project:ro \
  -v /var/lib/agents/<id>/memory:/memory \
  codex-image
~~~

Wichtige Regeln:

- **Kein \`--privileged\`**
- **Kein \`/var/run/docker.sock\` im Agenten**
- **Kein \`CAP_SYS_ADMIN\`**
- **Non-root** als Default
- Ressourcenlimits setzen
- Netzwerk explizit wählen (\`none\`, internes Netz, bridge)

Ein Container isoliert Prozesse und Dateien, aber nicht automatisch:

- das Netzwerk (Bridge erlaubt oft Internet),
- Secrets in Umgebungsvariablen,
- andere Container,
- den Host über offene Ports.

Deshalb braucht es zusätzlich Policy und Egress-Kontrolle.

Referenz: \`internal/dockerclient/dockerclient.go\`, \`internal/agents/agents.go\`.`,
      links: [['dockerclient.go', GH_DW + 'internal/dockerclient/dockerclient.go']],
      quiz: {
        q: 'Was gehört nicht in einen Agentencontainer?',
        options: ['Ein Projektverzeichnis', 'Der Docker-Socket', 'Ein Memory-Verzeichnis', 'Ein non-root User'],
        answer: 1,
        ok: 'Richtig. Der Docker-Socket würde die Sandbox aushebeln.',
        bad: 'Der Docker-Socket ist tabu: Damit könnte der Agent den Host kontrollieren.'
      }
    },
    {
      id: 'm3', code: 'M3', num: '§3', title: 'Steuerungsebene',
      sub: 'Daemon, CLI, Unix-Socket, SO_PEERCRED',
      body: [
        'Privilegierte Operationen laufen nur im Daemon.',
        'CLI/TUI/Web sind dünne Clients über einen lokalen Socket.',
        'SO_PEERCRED identifiziert den Peer; UID-Rollen autorisieren.',
        'Unbekannte UIDs werden abgelehnt, Monitor ist read-only.'
      ],
      codeFiles: [
        { file: 'internal/api/auth.go', lang: 'go', code:
`// SO_PEERCRED identifiziert den Peer; die Rolle autorisiert dann.
func PeerConnContext(ctx context.Context, c net.Conn) context.Context {
    uc, ok := c.(*net.UnixConn)
    if !ok { return ctx }
    raw, _ := uc.SyscallConn()
    var cred *unix.Ucred
    _ = raw.Control(func(fd uintptr) {
        cred, _ = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
    })
    ctx = context.WithValue(ctx, peerUIDKey{}, cred.Uid)
    return context.WithValue(ctx, peerKey{}, nameFromUID(cred.Uid))
}` },
        { file: 'cmd/dockweaved/main.go', lang: 'go', code:
`// dockweaved ist der privilegierte Daemon. Er lehnt Start ohne root ab.
if os.Geteuid() != 0 {
    return errors.New("dockweaved must run as root (CAP_SYS_ADMIN); refusing")
}
ln, err := net.Listen("unix", cfg.SocketPath)
handler := api.PeerAuthMiddleware(authPolicy, apiSrv)
srv := &http.Server{Handler: handler, ConnContext: api.PeerConnContext}` }
      ],
      md: `## 3. Steuerungsebene: Daemon + CLI + Unix-Socket

Ein naiver Ansatz wäre, die CLI direkt Docker-Operationen ausführen zu lassen. Sicherer ist eine kleine Steuerungsebene:

~~~text
CLI/TUI/Web ──(Unix-Socket, SO_PEERCRED)──> dockweaved (root)
                                              │
                                              ├── Docker API
                                              ├── SQLite
                                              └── Mount-Manager
~~~

Warum:

- Privilegierte Operationen bleiben an einer Stelle.
- Die CLI ist ein dünner Client.
- Der Socket ist lokal und per Peer-Credentials geschützt.
- Rollen (\`admin\`, \`monitor\`) lassen sich zentral prüfen.

Minimaler API-Ausschnitt:

~~~text
GET    /api/v1/agents
POST   /api/v1/agents
POST   /api/v1/agents/{id}/start
POST   /api/v1/agents/{id}/stop
GET    /api/v1/workspaces
POST   /api/v1/grants
~~~

Referenz: \`internal/api\`, \`cmd/dockweaved\`, \`pkg/sdk\`.`,
      links: [['auth.go', GH_DW + 'internal/api/auth.go'], ['main.go', GH_DW + 'cmd/dockweaved/main.go']],
      quiz: {
        q: 'Warum läuft Docker nicht direkt in der CLI?',
        options: ['Weil die CLI sonst langsamer wäre', 'Weil Privilegien zentral und prüfbar bleiben müssen', 'Weil Docker nur root mag', 'Weil die CLI kein JSON kann'],
        answer: 1,
        ok: 'Genau. Eine zentrale Steuerungsebene macht Rechte und Audit prüfbar.',
        bad: 'Es geht um Privilegientrennung: nur der Daemon darf privilegiert handeln.'
      }
    },
    {
      id: 'm4', code: 'M4', num: '§4', title: 'Identität & Persistenz',
      sub: 'stabile Agenten, SQLite, Migrationen',
      body: [
        'Agenten-IDs bleiben stabil, auch wenn Container neu erstellt werden.',
        'Persistiere Identität, Typ, Image, Command und State.',
        'Workspaces, Grants, MCP/Skills und Events in eigenen Tabellen.',
        'Migrationen versionieren; keine Secrets in Metadaten.'
      ],
      codeFiles: [
        { file: 'internal/model/model.go', lang: 'go', code:
`// Stabile ID mit Typ-Präfix, z. B. "ag_9f2c…".
func NewID(prefix string) string {
    var b [12]byte
    rand.Read(b[:])
    return prefix + "_" + hex.EncodeToString(b[:])
}

const (
    AgentCreated AgentState = "created"
    AgentRunning AgentState = "running"
    AgentStopped AgentState = "stopped"
)` }
      ],
      md: `## 4. Identität und Persistenz

Die Agentenidentität muss **unabhängig vom Container** sein. Container werden gelöscht und neu erstellt; die Identität bleibt.

Minimales Schema:

~~~sql
CREATE TABLE agents (
  id TEXT PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  type TEXT NOT NULL,
  image TEXT NOT NULL,
  command TEXT,
  state TEXT NOT NULL
);
~~~

Weitere Tabellen:

- \`workspaces\` — erlaubte Host-Verzeichnisse
- \`grants\` — Agent × Workspace × Modus
- \`events\` — Audit
- \`agent_mcp\` / \`agent_skills\` — Werkzeuge pro Agent
- \`agent_type_mcp\` / \`agent_type_skills\` — Vorlagen pro Agententyp

Wichtig:

- Stabile IDs (\`ag_…\`), keine Container-IDs als Identität.
- Migrationen versionieren.
- Keine Secrets in normalen Metadaten speichern.

Referenz: \`internal/model/model.go\`, \`internal/persistence/persistence.go\`.`,
      links: [['model.go', GH_DW + 'internal/model/model.go']],
      quiz: {
        q: 'Was sollte die Agentenidentität überleben?',
        options: ['Nur den Container-Namen', 'Container-Neuerstellung und Neustarts', 'Nur den aktuellen Prozess', 'Den API-Key'],
        answer: 1,
        ok: 'Richtig. Identität ist unabhängig von Container-IDs.',
        bad: 'Die Identität muss eine Container-Neuerstellung überleben.'
      }
    },
    {
      id: 'm5', code: 'M5', num: '§5', title: 'Workspaces & Mounts',
      sub: 'Grants, Allowlist, TOCTOU, RO-Staging',
      body: [
        'Ein Grant ist eine Erlaubnis; der Mount macht sie wirksam.',
        'Nur registrierte Host-Pfade sind mountbar (Allowlist).',
        'Symlinks auflösen und die Quelle per openat2 erneut prüfen.',
        'Read-only sauber stagen; kein umount -l als Default.'
      ],
      codeFiles: [
        { file: 'internal/mounts/manager.go', lang: 'go', code:
`// RO-Staging: privat binden → remount ro → in den Shared-Baum moven.
// Ein direktes -o ro propagiert nicht zuverlässig in Slave-Namespaces.
unix.Mount(src, staging, "none", unix.MS_BIND|unix.MS_REC, "")
unix.Mount("none", staging, "", unix.MS_REMOUNT|unix.MS_BIND|unix.MS_RDONLY|unix.MS_REC, "")
unix.Mount(staging, target, "", unix.MS_MOVE, "")` },
        { file: 'internal/mounts/pathutil.go', lang: 'go', code:
`// TOCTOU-Härtung: openat2(RESOLVE_BENEATH|RESOLVE_NO_MAGICLINKS).
how := &unix.OpenHow{
    Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
    Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS,
}
fd, err := unix.Openat2(pfd, base, how)` }
      ],
      md: `## 5. Workspace-Grants und Mounts

Ein Grant ist eine **Erlaubnis**, kein Mount. Erst der Mount macht sie wirksam.

Ablauf:

~~~text
Workspace registrieren
  → Grant erstellen (Agent, Workspace, ro/rw)
    → Mount anwenden
      → Container sieht /workspace/<name>
~~~

Worauf achten:

- **Allowlist**: Nur registrierte Host-Pfade sind mountbar.
- **Kanonisieren**: \`filepath.EvalSymlinks\`, danach erneut prüfen.
- **TOCTOU**: Quelle mit \`openat2(RESOLVE_BENEATH|NO_MAGICLINKS)\` öffnen und per \`/proc/self/fd/<n>\` mounten.
- **Read-only**: privat binden → \`remount,ro\` → in den Shared-Baum moven. Ein direktes \`-o ro\` propagiert nicht zuverlässig in Slave-Namespaces.
- **Kein \`umount -l\` als Default**: Bei \`EBUSY\` strukturiert fehlschlagen. Lazy-Unmount nur mit explizitem Force-Flag und Audit.

Referenz: \`internal/mounts\`, \`internal/workspaces\`, \`docs/security-decisions.md\`.`,
      links: [['manager.go', GH_DW + 'internal/mounts/manager.go'], ['pathutil.go', GH_DW + 'internal/mounts/pathutil.go']],
      quiz: {
        q: 'Warum reicht ein direktes `mount -o ro` nicht?',
        options: ['Weil Docker das ignoriert', 'Weil Remount-Flags nicht zuverlässig in Slave-Namespaces propagieren', 'Weil es zu langsam ist', 'Weil es nur für Dateien geht'],
        answer: 1,
        ok: 'Genau. Deshalb wird privat gestaged und dann verschoben.',
        bad: 'Die RO-Flags propagieren nicht zuverlässig — deshalb das Staging.'
      }
    },
    {
      id: 'm6', code: 'M6', num: '§6', title: 'Secrets & Broker',
      sub: 'secretless agents, Vault, Sessions',
      body: [
        'Keine echten API-Keys in Env, Image, Logs oder /memory.',
        'Vault: Argon2id + AES-256-GCM, AAD pro Eintrag, atomare Writes.',
        'Broker injiziert den echten Key nur für feste Provider-Ziele.',
        'Agent bekommt nur ein kurzlebiges Session-Token; Deny by default.'
      ],
      codeFiles: [
        { file: 'internal/credentialvault/crypto.go', lang: 'go', code:
`// Argon2id (OWASP: 64 MiB, 3 Iterationen, 4 Lanes) → 32-Byte-Key.
func deriveKey(passphrase, salt []byte, p KDFParams) []byte {
    return argon2.IDKey(passphrase, salt, p.Time, p.Memory, p.Threads, p.KeyLen)
}
// AES-256-GCM mit zufälliger Nonce; AAD bindet den Ciphertext an den Eintrag.
func seal(plaintext, key, aad []byte) (nonce, ciphertext []byte, err error) {
    block, _ := aes.NewCipher(key)
    gcm, _ := cipher.NewGCM(block)
    nonce, _ = randomBytes(gcm.NonceSize())
    ciphertext = gcm.Seal(nil, nonce, plaintext, aad)
    return nonce, ciphertext, nil
}` },
        { file: 'internal/credentialbroker/util.go', lang: 'go', code:
`// Nur der SHA-256-Hash eines Session-Tokens wird gespeichert.
func hashToken(token string) string {
    h := sha256.Sum256([]byte(token))
    return hex.EncodeToString(h[:])
}` }
      ],
      md: `## 6. Secrets: Warum EnvFiles nicht reichen

Ein API-Key in einer Env-Variable ist sichtbar für:

- den Agenten und seine Tools,
- \`docker inspect\`,
- \`/proc/<pid>/environ\`,
- versehentliche Logs,
- Prompt Injection.

Besser: **Secretless Agent**.

~~~text
Agent ──(Session-Token)──> Broker ──(echter Key)──> Provider
~~~

Vault-Anforderungen:

- Argon2id für die Passphrase
- AES-256-GCM mit zufälliger Nonce
- AAD pro Eintrag, damit Ciphertexte nicht getauscht werden können
- atomare Writes, \`0600\`, Verzeichnis \`0700\`
- kein Klartext in SQLite, Logs, Docker-Metadaten

Broker-Anforderungen:

- feste Provider-Ziele (keine freie Zielwahl)
- Redirects nicht verfolgen
- Deny by default
- Session-Tokens nur als Hash speichern
- Ablauf und sofortiger Widerruf
- Rate-/Kostenlimits pro Grant

Grenzen:

- Ein kopiertes Session-Token ist bis Ablauf/Widerruf nutzbar.
- Host-Root kann Prozessspeicher lesen.
- Budgets sind Schätzungen.

Referenz: \`internal/credentialvault\`, \`internal/credentialbroker\`, \`docs/threat-model-credential-broker.md\`.`,
      links: [['crypto.go', GH_DW + 'internal/credentialvault/crypto.go'], ['Broker', GH_DW + 'internal/credentialbroker/broker.go']],
      quiz: {
        q: 'Was bekommt der Agent vom Broker?',
        options: ['Den echten Provider-Key', 'Ein kurzlebiges Session-Token', 'Den Vault-Master-Key', 'Gar nichts'],
        answer: 1,
        ok: 'Richtig. Der echte Key bleibt im Broker.',
        bad: 'Der Agent bekommt nur ein eingeschränktes Session-Token, nie den echten Key.'
      }
    },
    {
      id: 'm7', code: 'M7', num: '§7', title: 'Egress',
      sub: 'Proxy ist keine Netzgrenze',
      body: [
        'Wenn der Agent direkt ins Internet darf, umgeht er den Broker.',
        '`offline`: kein Netz; `broker-only`: internes Docker-Netz.',
        'Netzwerkregeln liegen außerhalb des Agenten und fail closed.',
        'Host-Gateway-Portfilterung (nftables) ist ein eigener offener Schritt.'
      ],
      codeFiles: [
        { file: 'internal/model/model.go', lang: 'go', code:
`type NetworkProfile string

const (
    NetProfileOffline    NetworkProfile = "offline"
    NetProfileBrokerOnly NetworkProfile = "broker-only"
    NetProfileRestricted NetworkProfile = "restricted-development"
    NetProfileLegacy     NetworkProfile = "unrestricted-legacy"
)` }
      ],
      md: `## 7. Egress: Ein Proxy ist keine Netzgrenze

Wenn der Agent direkt ins Internet darf, kann er den Broker umgehen. Deshalb:

- \`offline\`: \`--network none\`
- \`broker-only\`: internes Docker-Netz, keine externe Route, Broker am Host-Gateway
- \`restricted-development\`: nicht implementiert → Fail closed
- \`unrestricted-legacy\`: bewusst unsicher

Wichtig:

- Netzwerkregeln müssen **außerhalb** des Agenten liegen.
- Fehler müssen **fail closed** sein.
- Der Host-Gateway bleibt eine Restgefahr; Portfilterung per nftables ist ein eigener Schritt.
- IPv4/IPv6, DNS, IMDS und private Bereiche separat testen.

Referenz: \`docs/egress-security.md\`, \`internal/agents\`, \`tests/integration/egress_network_test.go\`.`,
      links: [['egress-security.md', GH_DW + 'docs/egress-security.md']],
      quiz: {
        q: 'Warum ist ein HTTP-Proxy allein keine Egress-Sicherheit?',
        options: ['Weil er zu langsam ist', 'Weil der Agent ihn umgehen kann, wenn er direkten Netzwerkzugriff hat', 'Weil er keine HTTPS kann', 'Weil er nur für Browser ist'],
        answer: 1,
        ok: 'Genau. Ohne erzwungene Netzgrenze ist der Proxy optional.',
        bad: 'Der Agent kann einen Proxy umgehen, wenn er direkt routen darf.'
      }
    },
    {
      id: 'm8', code: 'M8', num: '§8', title: 'Audit & Monitoring',
      sub: 'Hash-Kette, Events, Konflikte',
      body: [
        'Sicherheitsrelevante Aktionen als Events protokollieren.',
        'Hash-Kette macht nachträgliche Manipulation erkennbar.',
        'Docker- und Prozessdaten für Aktivität/Konflikte beobachten.',
        'Nie Secrets in Events oder Logs schreiben.'
      ],
      codeFiles: [
        { file: 'internal/model/model.go', lang: 'go', code:
`// Events bilden eine Hash-Kette (PrevHash/Hash), damit Manipulation
// an der gespeicherten Historie erkennbar wird.
type Event struct {
    Type     EventType ` + "`json:\"type\"`" + `
    Actor    string    ` + "`json:\"actor\"`" + `
    Message  string    ` + "`json:\"message\"`" + `
    PrevHash string    ` + "`json:\"prev_hash\"`" + `
    Hash     string    ` + "`json:\"hash\"`" + `
}` }
      ],
      md: `## 8. Audit und Monitoring

Ohne Audit kannst du Sicherheitsvorfälle nicht nachvollziehen.

Minimal:

~~~go
event := Event{Type: "grant.created", Actor: user, AgentID: a, Message: msg}
event.PrevHash = lastHash
event.Hash = SHA256(prevHash | fields)
~~~

Zusätzlich:

- Docker-Events und Prozessdaten (procfs) beobachten.
- Schreibkonflikte zwischen Agenten erkennen.
- Keine Secrets in Events.

Referenz: \`internal/persistence\` (Hash-Kette), \`internal/monitor\`.`,
      links: [['model.go', GH_DW + 'internal/model/model.go'], ['monitor', GH_DW + 'internal/monitor']],
      quiz: {
        q: 'Was macht eine Audit-Hash-Kette?',
        options: ['Sie verschlüsselt Events', 'Sie macht Manipulation an der Historie erkennbar', 'Sie löscht alte Events', 'Sie ersetzt Backups'],
        answer: 1,
        ok: 'Richtig. Jedes Event bindet an das vorherige.',
        bad: 'Die Kette dient der Manipulationserkennung, nicht der Verschlüsselung.'
      }
    },
    {
      id: 'm9', code: 'M9', num: '§9', title: 'UI: CLI, TUI, Web',
      sub: 'Bedienung ohne Sicherheitsverlust',
      body: [
        'CLI zuerst: testbar, skriptbar, ehrlich.',
        'TUI für interaktive Terminal-Bedienung.',
        'Web braucht ein Gateway: Browser kann keinen Unix-Socket.',
        'Token/Cookie, PTY über WebSocket, keine Secrets in der UI.'
      ],
      codeFiles: [
        { file: 'cmd/dockweaved/main.go', lang: 'go', code:
`// Terminal-Stream-Server (interaktive PTY-Sessions, eigener Raw-Socket).
termSock := filepath.Join(filepath.Dir(cfg.SocketPath), "dockweaved.term.sock")
termSrv := api.NewTermServer(api.TermConfig{
    SocketPath: termSock, Docker: docker, Auth: authPolicy, ...,
})` }
      ],
      md: `## 9. Oberflächen: CLI zuerst, dann TUI/Web

Baue in dieser Reihenfolge:

1. **CLI** — schnell testbar, skriptbar, ehrlich.
2. **TUI** — interaktive Bedienung im Terminal.
3. **Web** — Agentenliste, Chat/Terminal, Konfiguration, Analyse.

Ein Browser kann keinen Unix-Socket sprechen. Für Web brauchst du ein Gateway:

~~~text
Browser ──HTTP/WebSocket──> Gateway ──Unix-Socket──> Daemon
~~~

Wichtig:

- Token/Cookie für den Browser-Zugriff.
- Kein privilegiertes Verhalten im Gateway.
- Terminal über ein echtes PTY (z. B. xterm.js + WebSocket).
- Keine Secrets in der UI anzeigen.

Referenz: \`cmd/dockweave\`, \`ui/tui\`, \`ui/web\`, \`internal/webui\`.`,
      links: [['web-ui.md', GH_DW + 'docs/web-ui.de.md']],
      quiz: {
        q: 'Warum braucht eine Web-UI ein Gateway?',
        options: ['Weil Browser keinen Unix-Domain-Socket sprechen', 'Weil HTTP kein JSON kann', 'Weil Docker einen Browser braucht', 'Weil Tokens nur im Browser funktionieren'],
        answer: 0,
        ok: 'Genau. Das Gateway verbindet Browser HTTP/WS mit dem Unix-Socket.',
        bad: 'Browser können keine Unix-Sockets öffnen — daher das Gateway.'
      }
    },
    {
      id: 'm10', code: 'M10', num: '§10', title: 'MCP & Skills',
      sub: 'Werkzeuge pro Agent konfigurierbar',
      body: [
        'MCP ist JSON-RPC 2.0; Tools haben ein inputSchema (JSON Schema).',
        'Skills/MCP pro Agententyp als Vorlage speichern.',
        'Beim Agentenanlegen in eigene, editierbare Zeilen kopieren.',
        'Vor dem Start ins Memory-Home materialisieren.'
      ],
      codeFiles: [
        { file: 'internal/model/model.go', lang: 'go', code:
`// Secrets werden nie in EnvKeys gespeichert (nur Namen);
// die Werte liegen im Credential Vault.
type AgentMCP struct {
    ID        string       ` + "`json:\"id\"`" + `
    AgentID   string       ` + "`json:\"agent_id\"`" + `
    Transport MCPTransport ` + "`json:\"transport\"`" + ` // stdio | http | sse
    Command   string       ` + "`json:\"command,omitempty\"`" + `
    EnvKeys   []string     ` + "`json:\"env_keys,omitempty\"`" + `
}` }
      ],
      md: `## 10. MCP und Skills

MCP ist JSON-RPC 2.0 (stdio oder Streamable HTTP). Wichtige Punkte:

- \`initialize\` beschreibt Protokollversion und Capabilities.
- \`tools/list\` liefert Tools mit \`inputSchema\` (JSON Schema).
- Ein MCP-Server ist **kein** einzelnes Standard-JSON-Dokument.

Für ein Agenten-System:

- Skills/MCP pro Agententyp als Vorlage speichern.
- Beim Anlegen eines Agenten in eigene, editierbare Zeilen kopieren.
- Vor dem Start in das Memory-Home materialisieren (\`opencode.json\`, \`.claude.json\`, generisches Manifest).
- Env-Werte nicht speichern, nur Namen; Werte in den Vault.

Referenz: \`internal/agentconfig\`, \`internal/agents\`, \`docs/web-ui.md\`.`,
      links: [['mcp.md', GH_DW + 'docs/mcp.md']],
      quiz: {
        q: 'Beschreibt MCP einen ganzen Server als ein Standard-JSON?',
        options: ['Ja, genau ein Dokument', 'Nein: JSON-RPC-Methoden plus Schemas pro Tool', 'Nur für Claude', 'Nur für OpenAI'],
        answer: 1,
        ok: 'Richtig. Ein normalisierter Server-Contract ist eine Ergänzung, kein MCP-Standard.',
        bad: 'MCP definiert Methoden und Tool-Schemas, kein einzelnes Gesamt-JSON.'
      }
    },
    {
      id: 'm11', code: 'M11', num: '§11', title: 'Tests & Threat Modeling',
      sub: 'Beweise statt Behauptungen',
      body: [
        'Unit-Tests für Policy, Vault, Pfade + Race Detector.',
        'Integrationstests mit echten Mounts nur isoliert.',
        'Fake-Provider statt echter API-Keys.',
        'Angriffstests: Exfiltration, Traversal, Redirect, Budget, Session.'
      ],
      codeFiles: [],
      md: `## 11. Tests und Threat Modeling

Mindestens:

- Unit-Tests für Policy, Vault, Pfade.
- Race Detector.
- Integrationstests mit echten Mounts nur in isolierter Umgebung.
- Fake-Provider statt echter API-Keys.
- Angriffstests: Secret-Exfiltration, Pfad-Traversal, Redirect, Budget, abgelaufene Session, falscher Agent.

Threat-Model-Fragen:

1. Wer ist vertrauenswürdig? (Operator, Daemon, Broker, Host)
2. Was ist unvertrauenswürdig? (Agent, Prompt-Injection, Netzwerk)
3. Welche Grenze schützt welches Gut?
4. Was passiert bei Fehlern? (fail closed?)
5. Welche Restrisiken bleiben?`,
      links: [['Integrationstests', GH_DW + 'tests/integration']],
      quiz: {
        q: 'Wo laufen Integrationstests mit echten Mounts?',
        options: ['Direkt auf dem Entwickler-Host', 'Nur in einer isolierten, privilegierten Umgebung', 'In der CI ohne Docker', 'Nirgends, sie werden übersprungen'],
        answer: 1,
        ok: 'Richtig. Privilegierte Mount-Tests laufen nie direkt auf dem Host.',
        bad: 'Echte Mount-Tests brauchen CAP_SYS_ADMIN — nur isoliert, sonst t.Skip.'
      }
    },
    {
      id: 'm12', code: 'M12', num: '§12', title: 'Bau-Reihenfolge',
      sub: 'Milestones mit Abnahmekriterium',
      body: [
        'Eine Phase nach der anderen bauen.',
        'Jede Phase hat ein überprüfbares Abnahmekriterium.',
        'Ein halbfertiger Broker ohne Egress-Schutz ist keine Sicherheit.'
      ],
      codeFiles: [],
      md: `## 12. Bau-Reihenfolge (Milestones)

| Phase | Ziel | Abnahmekriterium |
|---|---|---|
| M0 | Konzepte | Du kannst Harness, Agent, Sandbox, Grant erklären |
| M1 | Sandbox | Container startet non-root, ohne Caps, ohne Docker-Socket |
| M2 | Steuerung | CLI → Daemon → Docker funktioniert |
| M3 | Identität | Agent überlebt Container-Neuerstellung |
| M4 | Workspaces | Grant ro/rw funktioniert, Allowlist greift |
| M5 | Mounts sicher | RO-Staging, TOCTOU-Schutz, kein Lazy-Unmount-Default |
| M6 | Secrets | Vault + Broker, Agent sieht nur Session-Token |
| M7 | Egress | \`offline\`/\`broker-only\` erzwungen, Fail closed |
| M8 | Audit | Hash-Kette, keine Secrets in Events |
| M9 | UI | CLI/TUI/Web mit klarer Rechteanzeige |
| M10 | MCP/Skills | pro Agent konfigurierbar und materialisiert |

Baue **eine Phase nach der anderen**. Ein halbfertiger Broker ohne Egress-Schutz ist keine Sicherheitslösung.`,
      links: []
    },
    {
      id: 'm13', code: 'M13', num: '§13', title: 'Häufige Fehler',
      sub: 'Die zehn Klassiker',
      body: [
        'API-Keys in Env, Docker-Socket im Agenten, --privileged.',
        'Mounts ohne Allowlist, Symlinks nicht aufgelöst, umount -l.',
        'Proxy als Netzwerksicherheit, Tokens im Klartext.',
        'Sicherheit behaupten, die nicht erzwungen ist.'
      ],
      codeFiles: [],
      md: `## 13. Häufige Fehler

1. API-Keys in Env-Variablen.
2. Docker-Socket im Agenten.
3. \`--privileged\`, weil es „einfacher" ist.
4. Mounts ohne Allowlist.
5. Symlinks nicht auflösen.
6. \`umount -l\` als Standard.
7. Proxy als „Netzwerksicherheit" verkaufen.
8. Session-Tokens im Klartext speichern.
9. Budgets ohne Reservierung bei parallelen Requests.
10. Sicherheit behaupten, obwohl sie nicht erzwungen ist.`,
      links: []
    },
    {
      id: 'm14', code: 'M14', num: '§14', title: 'Sicherheits-Checkliste',
      sub: 'Was wirklich zählt',
      body: [
        'Non-root, ohne Caps, ohne Docker-Socket.',
        'Nur registrierte Pfade mountbar, RO korrekt gestaged.',
        'Keine Secrets in Env/Inspect/SQLite/Logs; Broker Deny-by-default.',
        'Fehler führen zu Ablehnung, nicht zu mehr Rechten.'
      ],
      codeFiles: [],
      md: `## 14. Sicherheits-Checkliste

- [ ] Agent läuft non-root, ohne Caps, ohne Docker-Socket.
- [ ] Nur registrierte Host-Pfade sind mountbar.
- [ ] RO-Mounts werden korrekt gestaged.
- [ ] Keine Secrets in Env, Inspect, SQLite oder Logs.
- [ ] Broker: Deny by default, feste Ziele, keine Redirects.
- [ ] Session-Tokens gehasht, ablaufbar, widerrufbar.
- [ ] Egress: \`broker-only\` blockt externe Routen.
- [ ] Fehler führen zu Ablehnung, nicht zu mehr Rechten.
- [ ] Audit-Events enthalten keine Secrets.
- [ ] Offene Risiken sind dokumentiert.`,
      links: []
    },
    {
      id: 'm15', code: 'M15', num: '§15', title: 'Referenz-Map',
      sub: 'Thema → DockWeave-Datei',
      body: [
        'Für jeden Aspekt genau eine Datei im Repo.',
        'Die Lern-Map ist der Einstieg, die Referenz-Map der Index.'
      ],
      codeFiles: [],
      md: `## 15. Referenz-Map

| Thema | DockWeave-Datei |
|---|---|
| Agentenmodell | \`internal/model/model.go\` |
| Persistenz/Migrationen | \`internal/persistence\` |
| Docker-Sandbox | \`internal/dockerclient\` |
| Agenten-Lifecycle | \`internal/agents\` |
| Workspace-Grants | \`internal/workspaces\` |
| Mount-Sicherheit | \`internal/mounts\` |
| Vault | \`internal/credentialvault\` |
| Broker | \`internal/credentialbroker\` |
| Egress | \`internal/agents\`, \`tests/integration/egress_network_test.go\` |
| UI-Gateway | \`internal/webui\` |
| Angular-UI | \`ui/web\` |
| TUI | \`ui/tui\` |
| Lernleitfaden | \`docs/learning/agent-harness.de.md\` |`,
      links: []
    },
    {
      id: 'm16', code: 'M16', num: '§16', title: 'Fazit',
      sub: 'Vertrauensgrenzen bewusst ziehen',
      body: [
        'Was darf der Agent? Was darf er niemals sehen?',
        'Wer entscheidet? Was passiert bei Fehlern?',
        'Was ist tatsächlich erzwungen — und was nur dokumentiert?'
      ],
      codeFiles: [],
      md: `## 16. Fazit

Ein sicheres Agenten-System besteht nicht daraus, einen Container zu starten. Es besteht daraus, **Vertrauensgrenzen bewusst zu ziehen**:

- Was darf der Agent?
- Was darf er niemals sehen?
- Wer entscheidet?
- Was passiert bei Fehlern?
- Was ist tatsächlich erzwungen — und was nur dokumentiert?

Wenn du diese Fragen für dein eigenes System beantworten kannst, hast du das Wichtigste verstanden. Der Rest ist Implementierung.`,
      links: []
    }
  ];

  /* ================= comparison: DockWeave vs herdr ================= */
  const comparison = {
    tldr: 'Beide nennen sich eine „Runtime für Coding-Agents" — aber sie arbeiten auf <strong>verschiedenen Ebenen</strong>. <strong>DockWeave</strong> beantwortet die Sicherheitsfrage (<em>Was darf der Agent, was darf er sehen?</em>) mit Containern, Grants, Credential-Broker und Egress. <strong>herdr</strong> beantwortet die Betriebs-/UX-Frage (<em>Wo läuft der Agent, ohne dass du dabei bleiben musst?</em>) mit einem Terminal-Multiplexer, Detach/Reattach und Multi-Machine-Orchestrierung. Sie sind kein Ersatz füreinander — eher orthogonal.',
    dimensions: [
      ['Ebene', 'Container- / Sicherheitsebene', 'Terminal- / Prozess-Ebene'],
      ['Kernfrage', 'Was darf der Agent?', 'Wo läuft der Agent, ohne dass du dabei sein musst?'],
      ['Sprache / Auslieferung', 'Go, Daemon + CLI + TUI + Web', 'Rust, ein einziges Binary'],
      ['Isolation', '<span class="ok">Docker-Container, cap-drop ALL, non-root</span>', '<span class="no">keine — läuft mit deinen Rechten</span>'],
      ['Harness', 'keinen eigenen (OpenCode/Codex/Claude Code)', 'keinen eigenen (führt deine Agenten aus)'],
      ['Identität', '<span class="ok">stabile ID, unabhängig vom Container</span>', '<span class="warn">terminal_id / pane_id</span>'],
      ['Secrets', '<span class="ok">Vault + Broker (secretless)</span>', '<span class="no">keine Verwaltung — deine Keys</span>'],
      ['Egress / Netz', '<span class="ok">offline / broker-only, fail closed</span>', '<span class="no">keine Netzgrenze</span>'],
      ['Audit', '<span class="ok">Hash-Ketten-Events</span>', '<span class="no">kein Audit-Trail</span>'],
      ['Session / Detach', '<span class="warn">Container stop/start</span>', '<span class="ok">detach/reattach, Layout-Restore</span>'],
      ['Mehrere Maschinen', '<span class="no">nein (single host + Docker)</span>', '<span class="ok">ja (lokale + SSH-Maschinen)</span>'],
      ['Agent-nativ', 'über REST-API / CLI steuerbar', 'CLI + Socket-API: spawn, prompt, wait']
    ],
    code: [
      {
        label: 'Start & Isolation — wie wird ein Agent gestartet?',
        dw: {
          file: 'internal/dockerclient/dockerclient.go', lang: 'go', url: GH_DW + 'internal/dockerclient/dockerclient.go',
          code:
`// DockWeave: Container mit gehärtetem Profil
host := &container.HostConfig{
    CapDrop:     []string{"ALL"},
    Privileged:  false,
    SecurityOpt: []string{"no-new-privileges"},
    NetworkMode: container.NetworkMode(a.NetworkMode),
    User:        fmt.Sprintf("%d:%d", a.UID, a.GID),
}`
        },
        hr: {
          file: 'src/api/schema/agents.rs', lang: 'rust', url: GH_HR + 'src/api/schema/agents.rs',
          code:
`// herdr: Agent in einem Terminal-Pane starten
pub struct AgentStartParams {
    pub name: String,      // Anzeigename
    pub kind: String,      // claude-code | codex | opencode | cursor | …
    pub pane_id: String,   // welches Pane ihn besitzt
    pub args: Vec<String>, // an die Agent-CLI durchgereicht
    pub timeout_ms: Option<u64>, // Warten auf Readiness
}`
        }
      },
      {
        label: 'Identität & Datenmodell — was ist ein Agent?',
        dw: {
          file: 'internal/model/model.go', lang: 'go', url: GH_DW + 'internal/model/model.go',
          code:
`type Agent struct {
    ID   string ` + "`json:\"id\"`" + `    // ag_…, stabil
    Type string ` + "`json:\"type\"`" + `  // codex | opencode | claude | …
    Image string ` + "`json:\"image\"`" + `
    UID  int    ` + "`json:\"uid\"`" + `
    GID  int    ` + "`json:\"gid\"`" + `
    NetworkProfile NetworkProfile ` + "`json:\"network_profile\"`" + `
    State      AgentState ` + "`json:\"state\"`" + `
    ContainerID string    ` + "`json:\"container_id,omitempty\"`" + `
}`
        },
        hr: {
          file: 'src/api/schema/agents.rs', lang: 'rust', url: GH_HR + 'src/api/schema/agents.rs',
          code:
`pub struct AgentInfo {
    pub terminal_id: String,
    pub pane_id: String,
    pub tab_id: String,
    pub workspace_id: String,
    pub agent_status: AgentStatus, // idle | blocked | working | done | unknown
    pub agent_session: Option<AgentSessionInfo>,
    pub interactive_ready: bool,
    // …
}`
        }
      },
      {
        label: 'Zugriff & Zustand — woher weiß das System, was los ist?',
        dw: {
          file: 'internal/api/auth.go', lang: 'go', url: GH_DW + 'internal/api/auth.go',
          code:
`// Strukturierter Zustand + SO_PEERCRED-Autorisierung
cred, _ := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
role := policy.RoleOf(cred.Uid)
if role == "" {
    http.Error(w, "forbidden: peer uid not authorized", 403)
    return
}`
        },
        hr: {
          file: 'src/cli/agent.rs', lang: 'rust', url: GH_HR + 'src/cli/agent.rs',
          code:
`// Zustand wird aus dem Terminal-Screen erkannt (Regeln),
// nicht über eine Agent-API abgefragt:
println!("    matchers: contains={:?} regex={:?} line_regex={:?} all={} any={} not={}",
    evidence["contains"], evidence["regex"], evidence["line_regex"],
    evidence["all_count"], evidence["any_count"], evidence["not_count"]);`
        }
      }
    ],
    procontra: {
      dw: {
        goes: [
          'Echte Isolation: Container, cap-drop ALL, non-root, kein Docker-Socket im Agenten.',
          'Workspace-Grants mit Allowlist, TOCTOU-Schutz (openat2) und sauberem RO-Staging.',
          'Secretless Credentials: Vault (Argon2id + AES-256-GCM) + Broker, Agent sieht nur ein kurzlebiges Session-Token.',
          'Egress-Profile (offline / broker-only), fail closed.',
          'Audit mit Hash-Kette, stabile Agenten-Identität, Recovery/Supervisor.'
        ],
        noes: [
          'Kein Harness: nutzt OpenCode/Codex/Claude Code (bewusst so gebaut).',
          'Kein Terminal-Multiplexing: kein tmux-artiges Detach/Reattach-Erlebnis.',
          'Single-Host: braucht Docker + einen root-Daemon (CAP_SYS_ADMIN).',
          'Keine Multi-Machine-Orchestrierung über SSH.',
          'Mehr Setup-/Operations-Aufwand als ein einzelnes Binary.'
        ]
      },
      hr: {
        goes: [
          'Detach/Reattach: Terminals laufen im Hintergrund-Server weiter; Layout-Restore nach Neustart.',
          'Multi-Machine: lokale + gespeicherte SSH-Maschinen in einem Fenster.',
          'Agent-nativ: CLI + Socket-API — Panes spawnen, prompten, auf „blocked“ warten.',
          'Läuft mit dem, was du schon nutzt: Claude Code, Codex, Cursor, OpenCode, …',
          'Ein Rust-Binary, Plugins und ein Marketplace.'
        ],
        noes: [
          'Keine Isolation: Agenten laufen mit deinen Rechten, keine Sandbox.',
          'Keine Credential-Verwaltung: deine API-Keys bleiben deine (kein Vault/Broker).',
          'Keine Egress-/Netzgrenze.',
          'Kein Audit-Trail (keine Hash-Kette).',
          'Zustand wird über Screen-Detection erraten (Regeln auf Terminal-Output), nicht über eine Agent-API.'
        ]
      }
    },
    zusammen: 'Die beiden schließen sich nicht aus: Du kannst einen DockWeave-verwalteten Agenten in einem herdr-Pane laufen lassen — herdr <em>ownet</em> das Terminal und die Session, DockWeave <em>ownet</em> die Sandbox, die Secrets und die Grenzen. Oder du betreibst herdr selbst in einer DockWeave-Sandbox, wenn du dessen Zugriffe einschränken willst. Kurz: herdr löst die <strong>Bedienbarkeit</strong>, DockWeave die <strong>Sicherheit</strong>.'
  };

  /* ---------- progress ---------- */
  const totalEl = document.getElementById('progress-total');
  const countEl = document.getElementById('progress-count');
  const barEl = document.getElementById('progress-bar');
  const badgesEl = document.getElementById('badges');
  totalEl.textContent = String(phases.length);

  function renderProgress() {
    const done = phases.filter(p => isDone(p.id)).length;
    countEl.textContent = String(done);
    barEl.style.width = `${(done / phases.length) * 100}%`;
    const badges = [
      ['🌱', 'Erste Schritte', done >= 1],
      ['🧱', 'Sandbox-Versteher', done >= 4],
      ['🔐', 'Secretless', done >= 7],
      ['🛡️', 'Grenzgänger', done >= 11],
      ['🏆', 'Systemarchitekt', done >= phases.length]
    ];
    badgesEl.innerHTML = badges.map(([i, label, on]) => `<span class="badge ${on ? 'on' : ''}">${i} ${label}</span>`).join('');
    if (done === phases.length && !state.celebrated) {
      state.celebrated = true; save(); confetti();
    }
  }

  function complete(id) {
    if (!isDone(id)) { state.done.push(id); save(); renderProgress(); }
    const el = document.querySelector(`[data-phase="${id}"]`);
    if (el) { el.classList.add('done'); const s = el.querySelector('.phase-status'); if (s) s.textContent = '✓ verstanden'; }
  }

  /* ---------- phases ---------- */
  const phasesEl = document.getElementById('phases');
  function renderPhases() {
    phasesEl.innerHTML = phases.map(p => `
      <details class="phase ${isDone(p.id) ? 'done' : ''}" data-phase="${p.id}">
        <summary>
          <span class="phase-badge">${p.code}</span>
          <span class="phase-title"><b>${p.num} · ${p.title}</b><span>${p.sub}</span></span>
          <span class="phase-status">${isDone(p.id) ? '✓ verstanden' : 'offen'}</span>
        </summary>
        <div class="phase-body">
          <div class="phase-tabs">
            <button type="button" class="active" data-tab="kurz">Kurz</button>
            <button type="button" data-tab="md">Original (build-your-own.de.md)</button>
          </div>
          <div class="phase-panel kurz active">
            <ul>${p.body.map(x => `<li>${x}</li>`).join('')}</ul>
            ${(p.codeFiles || []).map(cf => `
              <div class="codefile">
                <div class="codefile-head"><span class="fname">${cf.file}</span><span class="lang">${cf.lang}</span></div>
                <pre><code>${escapeHtml(cf.code)}</code></pre>
              </div>`).join('')}
          </div>
          <div class="phase-panel md">${p.md ? `<div class="mdview">${renderMd(p.md)}</div>` : ''}</div>
          ${p.links && p.links.length ? `<div class="phase-links">${p.links.map(([l, u]) => `<a class="btn small ghost" href="${u}">${l}</a>`).join('')}</div>` : ''}
          ${p.quiz ? `
          <div class="quiz" data-quiz="${p.id}">
            <h4>Mini-Check</h4>
            <p>${p.quiz.q}</p>
            <div class="quiz-options">
              ${p.quiz.options.map((o, i) => `<button type="button" data-answer="${i}">${o}</button>`).join('')}
            </div>
            <div class="quiz-feedback"></div>
          </div>` : ''}
          <div class="phase-actions">
            <button class="btn primary small" data-complete="${p.id}">${p.quiz ? 'Als verstanden markieren' : 'Abschnitt gesehen'}</button>
          </div>
        </div>
      </details>
    `).join('');

    // tabs
    phasesEl.querySelectorAll('.phase-tabs').forEach(tabs => {
      const body = tabs.parentElement;
      tabs.querySelectorAll('button').forEach(btn => {
        btn.addEventListener('click', () => {
          tabs.querySelectorAll('button').forEach(b => b.classList.remove('active'));
          btn.classList.add('active');
          body.querySelectorAll('.phase-panel').forEach(panel => panel.classList.remove('active'));
          const panel = body.querySelector('.phase-panel.' + btn.dataset.tab);
          if (panel) panel.classList.add('active');
        });
      });
    });

    // quiz
    phasesEl.querySelectorAll('.quiz').forEach(quiz => {
      const id = quiz.dataset.quiz;
      const p = phases.find(x => x.id === id);
      const fb = quiz.querySelector('.quiz-feedback');
      quiz.querySelectorAll('button[data-answer]').forEach(btn => {
        btn.addEventListener('click', () => {
          const ans = Number(btn.dataset.answer);
          quiz.querySelectorAll('button[data-answer]').forEach(b => b.classList.remove('correct', 'wrong'));
          if (ans === p.quiz.answer) {
            btn.classList.add('correct');
            fb.className = 'quiz-feedback ok';
            fb.textContent = '✓ ' + p.quiz.ok;
            complete(id);
          } else {
            btn.classList.add('wrong');
            fb.className = 'quiz-feedback bad';
            fb.textContent = '✗ ' + p.quiz.bad;
          }
        });
      });
    });

    phasesEl.querySelectorAll('[data-complete]').forEach(btn => {
      btn.addEventListener('click', () => complete(btn.dataset.complete));
    });
  }

  /* ---------- comparison render ---------- */
  function renderComparison() {
    const c = comparison;

    const dimEl = document.getElementById('cmp-dimensions');
    if (dimEl) {
      dimEl.innerHTML = `
        <div class="cmp-tldr">${c.tldr}</div>
        <div class="cmp-grid">
          <div class="cmp-card dw">
            <h3>DockWeave <span class="cmp-tag">Go · Sandbox + Control Plane</span></h3>
            <div class="cmp-role">Container-Sandbox, Workspace-Grants, Credential-Broker, Egress, Audit — die <strong>Sicherheits- und Grenzebene</strong>.</div>
            <p><strong>Repository:</strong> <a href="https://github.com/ananta888/dockweave" target="_blank" rel="noopener">ananta888/dockweave</a></p>
            <p>Führt KI-Agenten in <strong>isolierten Docker-Containern</strong> aus. Liefert Identität, Umgebung und Grenzen — bringt aber <strong>keinen eigenen Harness</strong> mit (nutzt OpenCode, Codex, Claude Code).</p>
          </div>
          <div class="cmp-card hr">
            <h3>herdr <span class="cmp-tag">Rust · Terminal-Runtime</span></h3>
            <div class="cmp-role">Terminal-Multiplexer &amp; Session-Manager — die <strong>Betriebs- und Bedienebene</strong>.</div>
            <p><strong>Repository:</strong> <a href="https://github.com/herdrdev/herdr" target="_blank" rel="noopener">herdrdev/herdr</a> · <a href="https://herdr.dev" target="_blank" rel="noopener">herdr.dev</a></p>
            <p>„the runtime your coding agents live on" — hält Terminals in einem Hintergrund-Server am Leben (detach/reattach), bündelt lokale + SSH-Maschinen und lässt Agenten Panes steuern. <strong>Ownet die Terminals, nicht die Container.</strong></p>
          </div>
        </div>
        <div class="dim-wrap" style="margin-top:1.2rem">
          <table class="dim-table">
            <thead><tr><th class="dim">Dimension</th><th class="dw-col">DockWeave</th><th class="hr-col">herdr</th></tr></thead>
            <tbody>${c.dimensions.map(r => `<tr><td class="dim">${r[0]}</td><td>${r[1]}</td><td>${r[2]}</td></tr>`).join('')}</tbody>
          </table>
        </div>`;
    }

    const codeEl = document.getElementById('cmp-code');
    if (codeEl) {
      codeEl.innerHTML = c.code.map(pair => {
        const dw = pair.dw;
        const hr = pair.hr;
        return `
          <div class="cmp-code-label"><code>${pair.label}</code></div>
          <div class="cmp-code">
            <div class="codefile">
              <div class="codefile-head"><span class="fname">DockWeave · ${dw.file}</span><span class="lang">${dw.lang}</span><a href="${dw.url}" target="_blank" rel="noopener">↗</a></div>
              <pre><code>${escapeHtml(dw.code)}</code></pre>
            </div>
            <div class="codefile">
              <div class="codefile-head"><span class="fname">herdr · ${hr.file}</span><span class="lang">${hr.lang}</span><a href="${hr.url}" target="_blank" rel="noopener">↗</a></div>
              <pre><code>${escapeHtml(hr.code)}</code></pre>
            </div>
          </div>`;
      }).join('') + `
        <div class="procontra">
          <div class="pc-col goes"><h4>✓ Was DockWeave kann</h4><ul>${c.procontra.dw.goes.map(x => `<li>${x}</li>`).join('')}</ul></div>
          <div class="pc-col noes"><h4>✗ Was DockWeave nicht kann (bewusst)</h4><ul>${c.procontra.dw.noes.map(x => `<li>${x}</li>`).join('')}</ul></div>
          <div class="pc-col goes"><h4>✓ Was herdr kann</h4><ul>${c.procontra.hr.goes.map(x => `<li>${x}</li>`).join('')}</ul></div>
          <div class="pc-col noes"><h4>✗ Was herdr nicht kann</h4><ul>${c.procontra.hr.noes.map(x => `<li>${x}</li>`).join('')}</ul></div>
        </div>
        <div class="cmp-note"><strong>Zusammenspiel:</strong> ${c.zusammen}</div>`;
    }
  }

  /* ---------- layers ---------- */
  const layers = [
    ['Operator / UI', 'Identität, Policy, Audit', 'Der Operator entscheidet. Die UI ist nur ein Client; Rechte werden zentral geprüft.'],
    ['Orchestrierung', 'Agenten, Ketten, Gates', 'Startet Agenten, verbindet Ketten und deterministische Gates — ohne selbst privilegiert zu sein.'],
    ['Sandbox', 'Container, Dateien, Netz', 'Begrenzt Prozesse, Dateien und Netzwerk. Ein Container allein ist keine vollständige Netzgrenze.'],
    ['Harness', 'Prompt, Tools, Loop', 'Das Programm um das Modell: Tool-Calls, Kontext, Schleife, Fehlerbehandlung.'],
    ['Modell', 'LLM-Provider', 'Erzeugt Text. Der Modell-Provider sieht nur die Anfrage — nicht deine Secrets-Datei.'],
    ['Memory & Skills', 'Memory, Knowledge, MCP', 'Persistentes Wissen und Werkzeuge. Pro Agent konfigurierbar, vor dem Start materialisiert.'],
    ['Credentials', 'Vault, Broker, Sessions', 'Hält echte Keys außerhalb des Agenten und gibt nur kurzlebige Session-Tokens heraus.']
  ];
  const layersEl = document.getElementById('layers');
  const layerExp = document.getElementById('layer-explanation');
  if (layersEl) {
    layersEl.innerHTML = layers.map(([name, sub], i) => `<button class="layer" data-layer="${i}"><b>${name}</b><span>${sub}</span></button>`).join('');
    layersEl.querySelectorAll('[data-layer]').forEach(btn => {
      btn.addEventListener('click', () => {
        layersEl.querySelectorAll('.layer').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        const [name, , text] = layers[Number(btn.dataset.layer)];
        layerExp.innerHTML = `<h4>${name}</h4><p>${text}</p>`;
      });
    });
  }

  /* ---------- simulator ---------- */
  const sim = [
    ['plan', 'Agent plant: „Ich muss die Datei lesen und dann patchen.“'],
    ['act', 'Tool-Call: read_file({ path: "src/main.go" })'],
    ['observe', 'Beobachtung: 312 Zeilen gelesen. Kontext aktualisiert.'],
    ['plan', 'Agent plant: „Ich ändere Zeile 42 und prüfe den Build.“'],
    ['act', 'Tool-Call: apply_patch(...)'],
    ['observe', 'Beobachtung: Patch angewendet. Build gestartet.'],
    ['act', 'Tool-Call: shell({ cmd: "go test ./..." })'],
    ['observe', 'Beobachtung: Tests grün.'],
    ['done', 'Fertig: Antwort an den Nutzer. (Im echten Harness: Max-Steps, Timeouts, Abbruch.)']
  ];
  let simIndex = 0;
  const logEl = document.getElementById('sim-log');
  const simNext = document.getElementById('sim-next');
  const simReset = document.getElementById('sim-reset');
  if (simNext && logEl) {
    simNext.addEventListener('click', () => {
      if (simIndex >= sim.length) return;
      const [kind, text] = sim[simIndex++];
      const line = document.createElement('div');
      line.className = 'sim-line ' + kind;
      line.textContent = text;
      logEl.appendChild(line);
      logEl.scrollTop = logEl.scrollHeight;
    });
    simReset.addEventListener('click', () => {
      simIndex = 0;
      logEl.innerHTML = '<div class="sim-line plan">[bereit] Klicke „Nächster Schritt“.</div>';
    });
  }

  /* ---------- checklist ---------- */
  const checks = [
    'Agent läuft non-root, ohne Caps, ohne Docker-Socket.',
    'Nur registrierte Host-Pfade sind mountbar.',
    'RO-Mounts werden korrekt gestaged.',
    'Keine Secrets in Env, Inspect, SQLite oder Logs.',
    'Broker: Deny by default, feste Ziele, keine Redirects.',
    'Session-Tokens gehasht, ablaufbar, widerrufbar.',
    'Egress: broker-only blockt externe Routen.',
    'Fehler führen zu Ablehnung, nicht zu mehr Rechten.',
    'Audit-Events enthalten keine Secrets.',
    'Offene Risiken sind dokumentiert.'
  ];
  const checkEl = document.getElementById('checklist');
  if (checkEl) {
    checkEl.innerHTML = checks.map((c, i) => `<label><input type="checkbox" data-check="${i}" ${state.checks.includes(i) ? 'checked' : ''}><span>${c}</span></label>`).join('');
    checkEl.querySelectorAll('input[data-check]').forEach(input => {
      input.addEventListener('change', () => {
        const i = Number(input.dataset.check);
        if (input.checked) { if (!state.checks.includes(i)) state.checks.push(i); }
        else state.checks = state.checks.filter(x => x !== i);
        save();
      });
    });
  }

  /* ---------- confetti ---------- */
  function confetti() {
    const wrap = document.createElement('div');
    wrap.className = 'confetti';
    const colors = ['#58a6ff', '#bc8cff', '#f0c000', '#3fb950', '#f85149'];
    for (let i = 0; i < 70; i++) {
      const p = document.createElement('i');
      p.style.left = Math.random() * 100 + 'vw';
      p.style.background = colors[i % colors.length];
      p.style.animationDelay = (Math.random() * 0.8) + 's';
      p.style.animationDuration = (2.2 + Math.random() * 1.8) + 's';
      wrap.appendChild(p);
    }
    document.body.appendChild(wrap);
    setTimeout(() => wrap.remove(), 5000);
  }

  renderPhases();
  renderProgress();
  renderComparison();
})();
