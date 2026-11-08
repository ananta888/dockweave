# ADR 0001 — Credential-Broker-Architektur

- Status: akzeptiert (MVP)
- Datum: 2026-11-08
- Branch: `feat/secure-credential-broker`
- Englische Fassung: [0001-credential-broker.md](0001-credential-broker.md)

## Kontext

DockWeave isoliert Dateisysteme, Mounts und Capabilities, aber nicht
API-Credentials. EnvFiles werden beim Start in die Container-Umgebung
übernommen (`internal/agents/agents.go`, `Start()`), sind per Docker Inspect
lesbar und für einen kompromittierten Agenten bzw. Prompt Injection
exfiltrierbar. Ziel ist ein lokaler, quelloffener Broker, bei dem der Agent
einen API-Dienst nutzen kann, ohne den echten Key zu besitzen.

## Ist-Analyse (Referenzen)

| Bereich | Befund | Referenz |
|---|---|---|
| Agentenidentität | `Agent` mit Type/Image/EnvFile in SQLite | `internal/model/model.go`, `internal/agents/agents.go` |
| Env-Secrets | EnvFile wird beim Start gelesen und in Runtime-Spec gemischt | `agents.go` `Start()` |
| Container | Drei Binds, Hardening, Env aus Agent | `internal/dockerclient/dockerclient.go` |
| Socket-Auth | SO_PEERCRED + UID→Rolle | `internal/api/auth.go` |
| Datei-Grants | ro/rw, TTL, Mount-Lifecycle | `internal/workspaces/workspaces.go` |
| Audit | Hash-Kette, keine Secret-Redaktion nötig, weil bisher keine Secrets im Log | `internal/persistence` |
| Netzwerk | Nur bridge/none, kein Egress-Enforcement | `internal/dockerclient`, `internal/agents` |
| Oberflächen | REST v1, CLI, TUI, SDK | `internal/api`, `cmd/dockweave`, `ui/tui`, `pkg/sdk` |

## Upstream-Vergleich — Infisical Agent Vault

Commit `16ec8d5a5db38be9d2c77fe9c19c7fd7125246ba`, Lizenz MIT außerhalb
`ee/`; `ee/` wurde nicht verwendet. Das Standalone-Projekt ist laut README für
Neuinstallationen zugunsten von Infisical Agent Vault abgekündigt.

| Komponente | Bewertung |
|---|---|
| `internal/crypto` (AES-GCM + Argon2id, ~100 LOC) | Konzept übernommen, mit Attribution, AAD und atomaren Writes gehärtet |
| `internal/store` (~3.6k LOC, SQLite/Postgres, Multi-Tenant) | Zu groß/gekoppelt, nicht übernommen |
| `internal/mitm` (eigene CA, WebSocket) | Nicht übernommen; kein TLS-MITM gewünscht |
| `internal/netguard` | Konzept für Egress-Design übernommen, im MVP nicht verdrahtet |
| `internal/ratelimit` | Konzept übernommen, minimal in `budget.go` umgesetzt |
| `internal/server` (Web-UI, Multi-User) | Nicht übernommen; DockWeave hat eigene Operator-Identität |

## Optionen

| Option | Sicherheit | Wartbarkeit | Aufwand | Eignung | Entscheidung |
|---|---|---|---|---|---|
| A. Original als Sidecar | Gut, aber MITM-CA + zweite Admin-Ebene | Upstream abgekündigt, große Dependency-Kette | Kurzfristig niedrig, langfristig hoch | Schlecht | verworfen |
| B. MIT-Kern forken | Gut | Fork-Drift, ~50k LOC | Hoch | Schlecht | verworfen |
| C. Krypto übernehmen + schlanker DockWeave-Broker | Gut für HTTP-Gateway; Egress separat | Klein, eigener Code | Mittel | Hoch | **gewählt** |
| D. Gateway mit festem Endpunkt | Wie C | Klein | Mittel | Hoch | **gewählt (Form von C)** |

## Entscheidung

1. Eigener, unprivilegierter Prozess `dockweave-broker` (nie root, keine
   Mount-/Docker-Operationen).
2. Verschlüsselter lokaler Vault: AES-256-GCM, Argon2id, versionierter
   Envelope, atomare Writes, `0600`, AAD pro Eintrag, Passphrase nur aus der
   Umgebung.
3. HTTP-Gateway statt MITM: `/p/{provider}/...` auf feste Provider-Base-URL,
   Credential-Injection nur dort.
4. DENY-by-default-Policy mit Agenten-Grants, Modell-Allowlist, Rate-,
   Concurrency-, Request-, Token- und Kostenlimits, TTL.
5. Kurzlebige Session-Tokens (`dws_…`, nur SHA-256 gespeichert, max. 24h,
   sofortiger Widerruf).
6. Keine Ausweitung des Root-Daemons: der Daemon bleibt ohne Klartext-Secrets.
7. Egress-Enforcement ist **nicht** Teil dieses Inkrements und wird als
   offener Blocker dokumentiert (`docs/egress-security.de.md`).

## Konsequenzen

Positiv: kein Provider-Key in Env, Inspect, SQLite, `/memory`, Logs; kleine
prüfbare Krypto-Fläche; kein MITM-CA; kein neuer Root-Code.

Offen: Agent-Erreichbarkeit des Brokers ist noch manuell; ohne Egress-
Enforcement kann ein Agent mit Internetzugang den Broker umgehen; Budgets
sind Schätzungen; kopierte Session-Tokens bleiben bis Ablauf/Widerruf
nutzbar; TUI/REST-Integration fehlen.

## Lizenz und Herkunft

- Krypto-Ansatz und API-Form orientieren sich an Infisical Agent Vault
  `internal/crypto` (MIT, Copyright (c) 2022 Infisical Inc.), Commit
  `16ec8d5a5db38be9d2c77fe9c19c7fd7125246ba`; die hiesige Implementierung ist
  eine Neuimplementierung mit AAD-Bindung und atomaren Writes.
- Kein `ee/`-Code, keine Web-/UI-Assets übernommen.
- Übriger Code dieses Änderungssatzes ist original DockWeave-Code.
