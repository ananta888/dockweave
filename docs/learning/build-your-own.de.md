# Baue dein eigenes DockWeave — Leitfaden für Einsteiger

Dieser Leitfaden erklärt, wie man ein Agenten-System wie DockWeave von Grund
auf selbst baut. Er richtet sich an Entwickler:innen, die Go und Linux-Grundlagen
kennen, aber noch nicht wissen, wie ein Coding-Agent, ein Harness, eine Sandbox
und ein sicherer Credential-Fluss zusammenhängen.

Ziel ist **Verstehen und Nachbauen**, nicht das Kopieren eines fertigen
Produkts. DockWeave selbst ist die Referenzimplementierung; jede Phase hier
verweist auf die tatsächlichen Dateien.

---

## 0. Das mentale Modell

Ein Agent ist **kein** einzelnes Programm. Er besteht aus mehreren Schichten:

```text
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
```

Merksätze:

- Das **Modell** erzeugt Text.
- Der **Harness** macht daraus Aktionen (Tools, Shell, Dateien).
- Die **Sandbox** begrenzt, was diese Aktionen erreichen können.
- Die **Steuerungsebene** entscheidet, wer was darf.
- **Credentials** gehören nicht in den Agenten.

Wenn du diese fünf Dinge sauber trennst, hast du die wichtigste Architektur
verstanden.

---

## 1. Der kleinste Harness: der Agent-Loop

Ein Harness ist im Kern eine Schleife:

```go
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
```

Worauf achten:

- **Maximale Schritte** und **Timeouts** setzen.
- Tool-Aufrufe validieren, nicht blind ausführen.
- Ergebnisse begrenzen (keine riesigen Ausgaben in den Kontext).
- Abbruch durch den Nutzer ermöglichen.
- Jede Tool-Ausführung ist eine potenzielle Sicherheitsgrenze.

Ein Tool braucht einen klaren Contract:

```json
{
  "name": "read_file",
  "description": "Read a file",
  "inputSchema": {
    "type": "object",
    "properties": { "path": { "type": "string" } },
    "required": ["path"]
  }
}
```

DockWeave bringt keinen eigenen Harness mit: OpenCode, Codex, Claude Code usw.
sind die Harnesses. DockWeave liefert die Umgebung, Identität und Grenzen.

---

## 2. Sandbox: Warum ein Container allein nicht reicht

Ein minimaler Sandbox-Start könnte so aussehen:

```bash
docker run --rm -it \
  --user 1000:1000 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --network none \
  --memory 2g --cpus 1 \
  -v /srv/project:/workspace/project:ro \
  -v /var/lib/agents/<id>/memory:/memory \
  codex-image
```

Wichtige Regeln:

- **Kein `--privileged`**
- **Kein `/var/run/docker.sock` im Agenten**
- **Kein `CAP_SYS_ADMIN`**
- **Non-root** als Default
- Ressourcenlimits setzen
- Netzwerk explizit wählen (`none`, internes Netz, bridge)

Ein Container isoliert Prozesse und Dateien, aber nicht automatisch:

- das Netzwerk (Bridge erlaubt oft Internet),
- Secrets in Umgebungsvariablen,
- andere Container,
- den Host über offene Ports.

Deshalb braucht es zusätzlich Policy und Egress-Kontrolle.

Referenz: `internal/dockerclient/dockerclient.go`, `internal/agents/agents.go`.

---

## 3. Steuerungsebene: Daemon + CLI + Unix-Socket

Ein naiver Ansatz wäre, die CLI direkt Docker-Operationen ausführen zu lassen.
Sicherer ist eine kleine Steuerungsebene:

```text
CLI/TUI/Web ──(Unix-Socket, SO_PEERCRED)──> dockweaved (root)
                                              │
                                              ├── Docker API
                                              ├── SQLite
                                              └── Mount-Manager
```

Warum:

- Privilegierte Operationen bleiben an einer Stelle.
- Die CLI ist ein dünner Client.
- Der Socket ist lokal und per Peer-Credentials geschützt.
- Rollen (`admin`, `monitor`) lassen sich zentral prüfen.

Minimaler API-Ausschnitt:

```text
GET    /api/v1/agents
POST   /api/v1/agents
POST   /api/v1/agents/{id}/start
POST   /api/v1/agents/{id}/stop
GET    /api/v1/workspaces
POST   /api/v1/grants
```

Referenz: `internal/api`, `cmd/dockweaved`, `pkg/sdk`.

---

## 4. Identität und Persistenz

Die Agentenidentität muss **unabhängig vom Container** sein. Container werden
gelöscht und neu erstellt; die Identität bleibt.

Minimales Schema:

```sql
CREATE TABLE agents (
  id TEXT PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  type TEXT NOT NULL,
  image TEXT NOT NULL,
  command TEXT,
  state TEXT NOT NULL
);
```

Weitere Tabellen:

- `workspaces` — erlaubte Host-Verzeichnisse
- `grants` — Agent × Workspace × Modus
- `events` — Audit
- `agent_mcp` / `agent_skills` — Werkzeuge pro Agent
- `agent_type_mcp` / `agent_type_skills` — Vorlagen pro Agententyp

Wichtig:

- Stabile IDs (`ag_…`), keine Container-IDs als Identität.
- Migrationen versionieren.
- Keine Secrets in normalen Metadaten speichern.

Referenz: `internal/model/model.go`, `internal/persistence/persistence.go`.

---

## 5. Workspace-Grants und Mounts

Ein Grant ist eine **Erlaubnis**, kein Mount. Erst der Mount macht sie wirksam.

Ablauf:

```text
Workspace registrieren
  → Grant erstellen (Agent, Workspace, ro/rw)
    → Mount anwenden
      → Container sieht /workspace/<name>
```

Worauf achten:

- **Allowlist**: Nur registrierte Host-Pfade sind mountbar.
- **Kanonisieren**: `filepath.EvalSymlinks`, danach erneut prüfen.
- **TOCTOU**: Quelle mit `openat2(RESOLVE_BENEATH|NO_MAGICLINKS)` öffnen und
  per `/proc/self/fd/<n>` mounten.
- **Read-only**: privat binden → `remount,ro` → in den Shared-Baum moven.
  Ein direktes `-o ro` propagiert nicht zuverlässig in Slave-Namespaces.
- **Kein `umount -l` als Default**: Bei `EBUSY` strukturiert fehlschlagen.
  Lazy-Unmount nur mit explizitem Force-Flag und Audit.

Referenz: `internal/mounts`, `internal/workspaces`, `docs/security-decisions.md`.

---

## 6. Secrets: Warum EnvFiles nicht reichen

Ein API-Key in einer Env-Variable ist sichtbar für:

- den Agenten und seine Tools,
- `docker inspect`,
- `/proc/<pid>/environ`,
- versehentliche Logs,
- Prompt Injection.

Besser: **Secretless Agent**.

```text
Agent ──(Session-Token)──> Broker ──(echter Key)──> Provider
```

Vault-Anforderungen:

- Argon2id für die Passphrase
- AES-256-GCM mit zufälliger Nonce
- AAD pro Eintrag, damit Ciphertexte nicht getauscht werden können
- atomare Writes, `0600`, Verzeichnis `0700`
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

Referenz: `internal/credentialvault`, `internal/credentialbroker`,
`docs/threat-model-credential-broker.md`.

---

## 7. Egress: Ein Proxy ist keine Netzgrenze

Wenn der Agent direkt ins Internet darf, kann er den Broker umgehen. Deshalb:

- `offline`: `--network none`
- `broker-only`: internes Docker-Netz, keine externe Route, Broker am
  Host-Gateway
- `restricted-development`: nicht implementiert → Fail closed
- `unrestricted-legacy`: bewusst unsicher

Wichtig:

- Netzwerkregeln müssen **außerhalb** des Agenten liegen.
- Fehler müssen **fail closed** sein.
- Der Host-Gateway bleibt eine Restgefahr; Portfilterung per nftables ist ein
  eigener Schritt.
- IPv4/IPv6, DNS, IMDS und private Bereiche separat testen.

Referenz: `docs/egress-security.md`, `internal/agents`,
`tests/integration/egress_network_test.go`.

---

## 8. Audit und Monitoring

Ohne Audit kannst du Sicherheitsvorfälle nicht nachvollziehen.

Minimal:

```go
event := Event{Type: "grant.created", Actor: user, AgentID: a, Message: msg}
event.PrevHash = lastHash
event.Hash = SHA256(prevHash | fields)
```

Zusätzlich:

- Docker-Events und Prozessdaten (procfs) beobachten.
- Schreibkonflikte zwischen Agenten erkennen.
- Keine Secrets in Events.

Referenz: `internal/persistence` (Hash-Kette), `internal/monitor`.

---

## 9. Oberflächen: CLI zuerst, dann TUI/Web

Baue in dieser Reihenfolge:

1. **CLI** — schnell testbar, skriptbar, ehrlich.
2. **TUI** — interaktive Bedienung im Terminal.
3. **Web** — Agentenliste, Chat/Terminal, Konfiguration, Analyse.

Ein Browser kann keinen Unix-Socket sprechen. Für Web brauchst du ein
Gateway:

```text
Browser ──HTTP/WebSocket──> Gateway ──Unix-Socket──> Daemon
```

Wichtig:

- Token/Cookie für den Browser-Zugriff.
- Kein privilegiertes Verhalten im Gateway.
- Terminal über ein echtes PTY (z. B. xterm.js + WebSocket).
- Keine Secrets in der UI anzeigen.

Referenz: `cmd/dockweave`, `ui/tui`, `ui/web`, `internal/webui`.

---

## 10. MCP und Skills

MCP ist JSON-RPC 2.0 (stdio oder Streamable HTTP). Wichtige Punkte:

- `initialize` beschreibt Protokollversion und Capabilities.
- `tools/list` liefert Tools mit `inputSchema` (JSON Schema).
- Ein MCP-Server ist **kein** einzelnes Standard-JSON-Dokument.

Für ein Agenten-System:

- Skills/MCP pro Agententyp als Vorlage speichern.
- Beim Anlegen eines Agenten in eigene, editierbare Zeilen kopieren.
- Vor dem Start in das Memory-Home materialisieren
  (`opencode.json`, `.claude.json`, generisches Manifest).
- Env-Werte nicht speichern, nur Namen; Werte in den Vault.

Referenz: `internal/agentconfig`, `internal/agents`, `docs/web-ui.md`.

---

## 11. Tests und Threat Modeling

Mindestens:

- Unit-Tests für Policy, Vault, Pfade.
- Race Detector.
- Integrationstests mit echten Mounts nur in isolierter Umgebung.
- Fake-Provider statt echter API-Keys.
- Angriffstests: Secret-Exfiltration, Pfad-Traversal, Redirect, Budget,
  abgelaufene Session, falscher Agent.

Threat-Model-Fragen:

1. Wer ist vertrauenswürdig? (Operator, Daemon, Broker, Host)
2. Was ist unvertrauenswürdig? (Agent, Prompt-Injection, Netzwerk)
3. Welche Grenze schützt welches Gut?
4. Was passiert bei Fehlern? (fail closed?)
5. Welche Restrisiken bleiben?

---

## 12. Bau-Reihenfolge (Milestones)

| Phase | Ziel | Abnahmekriterium |
|---|---|---|
| M0 | Konzepte | Du kannst Harness, Agent, Sandbox, Grant erklären |
| M1 | Sandbox | Container startet non-root, ohne Caps, ohne Docker-Socket |
| M2 | Steuerung | CLI → Daemon → Docker funktioniert |
| M3 | Identität | Agent überlebt Container-Neuerstellung |
| M4 | Workspaces | Grant ro/rw funktioniert, Allowlist greift |
| M5 | Mounts sicher | RO-Staging, TOCTOU-Schutz, kein Lazy-Unmount-Default |
| M6 | Secrets | Vault + Broker, Agent sieht nur Session-Token |
| M7 | Egress | `offline`/`broker-only` erzwungen, Fail closed |
| M8 | Audit | Hash-Kette, keine Secrets in Events |
| M9 | UI | CLI/TUI/Web mit klarer Rechteanzeige |
| M10 | MCP/Skills | pro Agent konfigurierbar und materialisiert |

Baue **eine Phase nach der anderen**. Ein halbfertiger Broker ohne
Egress-Schutz ist keine Sicherheitslösung.

---

## 13. Häufige Fehler

1. API-Keys in Env-Variablen.
2. Docker-Socket im Agenten.
3. `--privileged`, weil es „einfacher“ ist.
4. Mounts ohne Allowlist.
5. Symlinks nicht auflösen.
6. `umount -l` als Standard.
7. Proxy als „Netzwerksicherheit“ verkaufen.
8. Session-Tokens im Klartext speichern.
9. Budgets ohne Reservierung bei parallelen Requests.
10. Sicherheit behaupten, obwohl sie nicht erzwungen ist.

---

## 14. Sicherheits-Checkliste

- [ ] Agent läuft non-root, ohne Caps, ohne Docker-Socket.
- [ ] Nur registrierte Host-Pfade sind mountbar.
- [ ] RO-Mounts werden korrekt gestaged.
- [ ] Keine Secrets in Env, Inspect, SQLite oder Logs.
- [ ] Broker: Deny by default, feste Ziele, keine Redirects.
- [ ] Session-Tokens gehasht, ablaufbar, widerrufbar.
- [ ] Egress: `broker-only` blockt externe Routen.
- [ ] Fehler führen zu Ablehnung, nicht zu mehr Rechten.
- [ ] Audit-Events enthalten keine Secrets.
- [ ] Offene Risiken sind dokumentiert.

---

## 15. Referenz-Map

| Thema | DockWeave-Datei |
|---|---|
| Agentenmodell | `internal/model/model.go` |
| Persistenz/Migrationen | `internal/persistence` |
| Docker-Sandbox | `internal/dockerclient` |
| Agenten-Lifecycle | `internal/agents` |
| Workspace-Grants | `internal/workspaces` |
| Mount-Sicherheit | `internal/mounts` |
| Vault | `internal/credentialvault` |
| Broker | `internal/credentialbroker` |
| Egress | `internal/agents`, `tests/integration/egress_network_test.go` |
| UI-Gateway | `internal/webui` |
| Angular-UI | `ui/web` |
| TUI | `ui/tui` |
| Lernleitfaden | `docs/learning/agent-harness.de.md` |

---

## 16. Fazit

Ein sicheres Agenten-System besteht nicht daraus, einen Container zu starten.
Es besteht daraus, **Vertrauensgrenzen bewusst zu ziehen**:

- Was darf der Agent?
- Was darf er niemals sehen?
- Wer entscheidet?
- Was passiert bei Fehlern?
- Was ist tatsächlich erzwungen — und was nur dokumentiert?

Wenn du diese Fragen für dein eigenes System beantworten kannst, hast du das
Wichtigste verstanden. Der Rest ist Implementierung.
