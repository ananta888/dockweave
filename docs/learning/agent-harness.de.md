# Lernprojekt: Harnesses und Agenten-Systeme verstehen

Dieses Repository ist in erster Linie eine **Lern- und Experimentierplattform**.
Es geht nicht darum, DockWeave als fertiges Produkt zu betreiben, sondern
nachvollziehbar zu machen:

- was ein **Agenten-Harness** ist,
- wie ein **Agenten-System** aus Modell, Harness, Sandbox, Identität,
  Werkzeugen, Gedächtnis und Orchestrierung aufgebaut ist,
- welche **Technologien** dafür verwendet werden und warum,
- und welche **Sicherheitsentscheidungen** nötig sind, wenn Agenten Code
  ausführen, Dateien lesen/schreiben und APIs aufrufen dürfen.

> **Kein Produktionsversprechen.** DockWeave ist ein Lehrprojekt. Es ist nicht
> gehärtet für Mehrbenutzerbetrieb, Hosting oder den Schutz gegen einen
> kompromittierten Host-Root. Offene Punkte stehen ausdrücklich in
> `todo/credential-vault.json` und in den Sicherheitsdokumenten.

## 1. Was ist ein Harness?

Ein LLM ist nur ein Textmodell. Ein **Harness** ist das Programm um das Modell
herum, das aus einer Anfrage eine tatsächliche Aufgabe macht:

| Aufgabe des Harness | Beispiel |
|---|---|
| Prompting / Systemkontext | Regeln, Rollen, Projektkontext, AGENTS.md |
| Tool-Calling | Dateien lesen, Shell ausführen, Patch anwenden |
| Ausführung | Befehle im Container starten, Ausgaben zurückführen |
| Kontextverwaltung | Gesprächsverlauf, Dateien, Suchergebnisse |
| Freigaben / Grenzen | Sandbox, erlaubte Pfade, Netzwerk, Budgets |
| Schleifenkontrolle | planen → handeln → beobachten → wiederholen |
| Fehlerbehandlung | Retries, Abbruch, Timeouts |

Bekannte Harnesses sind OpenCode, Codex CLI, Claude Code oder Gemini CLI. Sie
unterscheiden sich in UI, Tool-Set und Konfiguration, folgen aber demselben
Grundmuster.

Wichtig: **Harness ≠ Modell ≠ Agentenidentität ≠ Container.** DockWeave trennt
diese Dinge bewusst:
- Das Modell kommt vom Provider (OpenRouter, OpenAI, Anthropic, …).
- Der Harness läuft im Agentencontainer.
- Die Agentenidentität ist ein stabiler Datensatz (Name, Image, Command,
  Berechtigungen), unabhängig von Container-IDs.
- Der Container ist die Sandbox, nicht die Identität.

## 2. Schichten eines Agenten-Systems

```
Operator / UI
   │  Identität, Policy, Audit
   ▼
Orchestrierung  (Agenten, Ketten, Gates, Sessions)
   │
   ├── Sandbox      (Container, Filesystem, Netzwerk, Ressourcen)
   ├── Harness      (Prompt, Tools, Loop, Kontext)
   ├── Modell       (LLM-Provider)
   ├── Gedächtnis   (Memory, Knowledge, Skills)
   └── Credentials  (Vault, Broker, Session-Tokens)
```

Jede Schicht braucht eine eigene Sicherheitsgrenze. Ein Proxy allein ist keine
Netzgrenze, ein Container allein keine Identität, und ein API-Key in einer
Umgebungsvariable ist kein Secret-Management.

## 3. DockWeave als Fallstudie

DockWeave setzt dieses Schichtenmodell lokal um:

| Schicht | Umsetzung | Einstiegspunkt |
|---|---|---|
| Agentenidentität | SQLite, stabile Agent-ID, Typ/Image/Command | `internal/model`, `internal/persistence` |
| Sandbox | Docker-Container, drei Mounts, Cap-Drop, non-root | `internal/dockerclient`, `internal/agents` |
| Dateizugriff | Workspace-Grants ro/rw, Mount-Manager | `internal/workspaces`, `internal/mounts` |
| Harness | Der Agent bringt seinen Harness selbst mit (OpenCode, Codex, …) | Agent-Command |
| Gedächtnis | `/memory`, `/knowledge`, `/notes` | `internal/memory`, `internal/knowledge`, `internal/agents` |
| Skills/MCP | pro Agent und pro Agententyp in SQLite, Materialisierung ins Memory-Home | `internal/agents`, `internal/agentconfig` |
| Credentials | unprivilegierter Broker, Vault, Sessions, Budgets | `internal/credentialvault`, `internal/credentialbroker` |
| Egress | `offline` / `broker-only` über Docker-Netze | `internal/agents`, `internal/dockerclient` |
| Audit | Hash-Kette über Ereignisse | `internal/persistence` |
| UI | CLI, TUI, Angular-Weboberfläche, Graph | `cmd/dockweave`, `ui/tui`, `ui/web` |
| UI-Steuerung durch Agenten | UI-MCP-Server + WebSocket-Bridge | `internal/webui` |
| Test/Simulation | Fake-Provider, MCP-UI, Demos | `examples/`, `tests/` |

Ein typischer Ablauf:

```text
CLI/Web → dockweaved (root) → Docker Engine → Agentencontainer
                              │
                              ├─ /agent   (Harness-Installation, AGENTS.md, Skills)
                              ├─ /memory  (persistentes Home)
                              ├─ /workspace (Projekt-Grants, rslave)
                              └─ /knowledge
```

Der Agent selbst läuft **nicht** als Root und bekommt keinen Docker-Socket.

## 4. Verwendete Technologien und warum

| Technologie | Verwendung | Didaktischer Grund |
|---|---|---|
| **Go** | Daemon, CLI, Broker, Web-Gateway | Nebenläufigkeit, statische Binaries, klare Interfaces |
| **Docker Engine API** | Container-Erzeugung, Exec, Attach | reproduzierbare Isolation ohne eigene Container-Runtime |
| **Linux Mounts / Namespaces** | `rslave`, RO-Staging, `openat2` | reale Dateisystemgrenzen statt reiner Konvention |
| **SQLite (modernc, pure Go)** | Registry, Grants, MCP/Skills, Audit | lokal, transaktional, keine externe Datenbank |
| **Unix-Sockets + `SO_PEERCRED`** | API/Admin-Terminal | lokale Authentifizierung ohne TCP-/Token-Leak |
| **Argon2id + AES-256-GCM** | Credential-Vault | etablierte KDF und AEAD, keine eigene Krypto |
| **JSON Schema / MCP** | Tool- und Provider-Contracts | Werkzeuge brauchen explizite, prüfbare Schnittstellen |
| **OpenAI-kompatible HTTP-APIs** | Broker-Gateway | viele Harnesses unterstützen `baseURL` + Dummy-Key |
| **Angular 20** | Web-UI | klare Komponenten, typisierte API, browserbasierte Bedienung |
| **Foblex Flow** | Agenten-/Ressourcen-Graph | Knoten, Kanten, Ports und Layouts visuell erklärbar |
| **xterm.js** | Chat/Terminal im Browser | ein echtes PTY statt einer nachgebauten Chat-Blase |
| **Docker-`internal`-Netze** | `broker-only` | externe Route blockieren, Host-Gateway für Broker behalten |
| **nftables (geplant)** | Host-Gateway-Filterung | Netzgrenze außerhalb der Agentenkontrolle |

### Warum kein eigener Krypto-Code?

Weil selbst geschriebene Kryptografie fast immer falsch ist. Der Vault nutzt
Argon2id (Speicher-hart, GPU-resistent) und AES-256-GCM (authenticated
encryption). Jeder Eintrag ist über AAD an seinen Namen gebunden, damit
Ciphertexte nicht zwischen Einträgen getauscht werden können.

### Warum ein separater Broker?

Der Root-Daemon darf klein bleiben. Credentials gehören nicht in den Daemon,
der Docker und Mounts kontrolliert. Der Broker läuft unprivilegiert, hält
Secrets nur im Speicher und gibt Agenten nur kurzlebige Session-Tokens.

## 5. Sicherheit: das Warum

### 5.1 Bedrohungsmodell (kurz)

| Angreifer | Ziel | Kontrolle |
|---|---|---|
| Kompromittierter Agent | Secrets lesen | Keine Keys im Container; Vault außerhalb; Broker |
| Kompromittierter Agent | Dateisystem verlassen | Cap-Drop, non-root, Mount-Allowlist, `openat2` |
| Kompromittierter Agent | Andere Agenten beeinflussen | getrennte Memory-/Workspace-Bäume |
| Prompt Injection | Daten exfiltrieren | feste Provider-Ziele, keine freie Zielwahl, keine Redirects |
| Agent | Budget missbrauchen | Rate-/Concurrency-/Kostenlimits im Broker |
| Lokaler Nutzer | Daemon steuern | Unix-Socket-Rechte + `SO_PEERCRED`-Rollen |
| Manipulation | Audit verändern | Hash-Kette der Events |

### 5.2 Zentrale Prinzipien

1. **Least Privilege:** Agentencontainer bekommen `CapDrop: ALL`, keinen
   Docker-Socket und standardmäßig non-root.
2. **Deny by default:** Kein Workspace-Grant, kein Credential-Grant, kein
   Provider, kein Modell → abgelehnt.
3. **Secretless Agents:** Ein Agent bekommt nie den echten Provider-Key,
   sondern höchstens ein kurzlebiges, eingeschränktes Session-Token.
4. **Fail closed:** Fehler bei Mount, Netzwerk, Vault oder Policy führen zur
   Ablehnung, nicht zu mehr Rechten.
5. **Trennung von Vertrauensgrenzen:** Operator, Broker, Daemon und Agent sind
   unterschiedliche Rollen; der Agent ist nicht vertrauenswürdig.
6. **Keine Sicherheit vortäuschen:** Was nicht erzwungen ist (z. B. vollständige
   Egress-Isolation, Host-Gateway-Portfilterung), wird als offen dokumentiert.
7. **Nachvollziehbarkeit:** sicherheitsrelevante Änderungen werden auditiert,
   Entscheidungen in `docs/security-decisions.md` begründet.

### 5.3 Warum Credential-Broker statt EnvFiles?

Ein Key in einer Env-Variable ist für den Agenten, für `docker inspect`, für
`/proc/*/environ` und für versehentliche Logs sichtbar. Prompt Injection kann
ihn auslesen. Der Broker dreht das Modell um:

- Der Agent kennt nur einen **Dummy-/Session-Token**.
- Der Broker setzt den echten Key erst unmittelbar vor dem erlaubten
  Provider-Request ein.
- Das Ziel ist fest pro Provider; Redirects werden nicht verfolgt.
- Budgets und Rate-Limits werden broker-seitig geprüft.

Grenzen: Ein kopiertes Session-Token bleibt bis Ablauf/Widerruf nutzbar; ein
kompromittierter Host-Root kann Prozessspeicher lesen; Budgets sind
Schätzungen, keine Abrechnungsgarantie.

### 5.4 Warum Egress-Isolation?

Ein HTTP-Proxy ist keine Sicherheitsgrenze, wenn der Agent direkt ins Internet
darf. Deshalb gibt es Netzwerkprofile:

- `offline`: kein Netz.
- `broker-only`: internes Docker-Netz, keine externe Route, Broker am
  Host-Gateway.
- `restricted-development`: derzeit nicht implementiert → Fail closed.
- `unrestricted-legacy`: ausdrücklich unsicher.

Noch offen: Host-Gateway-Portfilterung per nftables, damit der Agent am Gateway
nur den Broker und keine anderen Host-Dienste erreicht.

### 5.5 Warum MCP/JSON-Schema-Contracts?

MCP beschreibt nicht „einen Server“ als ein JSON, sondern JSON-RPC-Methoden
und pro Tool ein `inputSchema` (JSON Schema). DockWeave ergänzt einen
normalisierten `mcpServer`-Contract, importiert `mcpServers`-Konfigurationen
und validiert Tool-Schemas. So werden Verbindungen zwischen Agenten, Tools,
Gates und Providern explizit statt implizit.

## 6. Was dieses Projekt ausdrücklich nicht ist

- Keine produktionsreife Plattform.
- Keine vollständige Egress-Isolation (nftables-Teil offen).
- Kein Schutz gegen kompromittierten Host-Root oder Kernel.
- Kein Multi-Tenant-System.
- Keine Garantie, dass Broker-Kosten exakt der Provider-Rechnung entsprechen.
- Kein Ersatz für Provider-seitige Limits, Rotation und Audits.

## 7. Lernpfad (Übungen)

1. **Drei Mounts:** Agent erstellen, starten, `docker inspect` und
   `dockweave agent paths` vergleichen.
2. **Workspace-Grant:** Workspace registrieren, ro/rw zuweisen, im Container
   Schreibversuche machen, Mount-Tabelle prüfen.
3. **Harness verstehen:** OpenCode/Codex im Container starten, Prozessbaum,
   Config-Dateien und Session-Marker untersuchen.
4. **MCP/Skills:** Typ-Defaults ansehen, pro Agent übernehmen, materialisierte
   `opencode.json`/`.claude.json` prüfen.
5. **Credential-Broker:** Demo starten, Fake-Provider beobachten, beweisen,
   dass der echte Wert nur im Broker ankommt.
6. **Egress:** `broker-only` setzen, direkten Provider-Zugriff testen, Fehler
   erwarten; danach Host-Gateway-Restrisiko dokumentieren.
7. **Policy:** Falschen Agenten, falsches Modell, abgelaufene Session,
   Budgetüberschreitung testen.
8. **Threat Model:** Eine neue Angriffsidee ergänzen und eine Kontrolle
   entwerfen; Akzeptanzkriterium und Test notieren.

## 8. Glossar

| Begriff | Bedeutung |
|---|---|
| **Harness** | Programm, das ein Modell steuert und Werkzeuge ausführt |
| **Agent** | Identität + Konfiguration + Harness-Laufzeit |
| **Sandbox** | Container/Namespace, der Code und Dateien isoliert |
| **Grant** | explizite Erlaubnis (Datei oder Credential) |
| **Broker** | Vermittler, der echte Credentials einsetzt |
| **Session** | kurzlebiges Capability-Token für einen Grant |
| **MCP** | Model Context Protocol für Tools/Ressourcen |
| **Egress** | ausgehender Netzwerkverkehr |
| **Fail closed** | bei Fehler wird abgelehnt, nicht geöffnet |
| **Audit-Kette** | verkettete Ereignisse, die Manipulation erkennbar machen |

## 9. Weiterlesen

- `docs/learning/build-your-own.de.md` — Schritt-für-Schritt-Bauanleitung
- Interaktive Website: `docs/index.html` / https://ananta888.github.io/dockweave/

- `docs/architecture.md` — Komponenten und Datenfluss
- `docs/security-decisions.md` — getroffene Entscheidungen
- `docs/threat-model-credential-broker.md` — Broker-Bedrohungsmodell
- `docs/egress-security.md` — Egress-Konzept und offene Punkte
- `docs/adr/0001-credential-broker.md` — Architekturvergleich mit Agent Vault
- `todo/credential-vault.json` — offene Arbeiten mit Akzeptanzkriterien
