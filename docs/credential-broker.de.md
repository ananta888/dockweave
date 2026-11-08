# Credential Vault & API-Broker

Der Credential-Broker von DockWeave erlaubt einem Agenten, eine Provider-API
zu nutzen, **ohne jemals den echten API-Key zu besitzen**. Secrets liegen in
einem verschlüsselten lokalen Vault; der Agent erhält ein kurzlebiges
Session-Token, das nur für seinen expliziten Grant gilt.

> **Stand dieses Inkrements (2026-11-08):** Vault, Policy, Sessions,
> HTTP-Gateway, Budgets und CLI sind implementiert und getestet.
> **Egress-Enforcement ist NICHT implementiert.** Solange das fehlt, kann ein
> Agent mit Internetzugang den Broker umgehen. Siehe
> [egress-security.de.md](egress-security.de.md) und die offenen Aufgaben in
> `todo/credential-vault.json`.

Sprache: Deutsch · [English](credential-broker.md)

## Warum

DockWeave isoliert bereits Dateisysteme, Mounts und Capabilities. API-Keys
wurden aber weiterhin über `EnvFile`, Image-Env oder `/memory` übergeben – für
einen kompromittierten Agenten oder Prompt Injection lesbar. Der Broker
entfernt langlebige Credentials vollständig aus dem Agenten.

## Architektur

```
Operator-CLI ──(Unix, SO_PEERCRED)──> dockweave-broker (unprivilegiert)
                                          │
                                          ├── verschlüsselter Vault (AES-256-GCM)
                                          ├── Policy (Standard: DENY)
                                          ├── Sessions (dws_…, nur Hash)
                                          └── Budget/Rate-Limits
                                          │
Agent ──(Unix-Socket oder eingeschränktes TCP)──> /p/{provider}/... ──> Provider-API
        Authorization: Bearer dws_…                                       (echter Key
                                                                           injiziert)
```

- `dockweave-broker` ist ein eigener Prozess, nie root, ohne Mount-/Docker-
  Rechte.
- Die Provider-Konfiguration des Agenten zeigt auf den Broker und nutzt das
  Session-Token als Dummy-API-Key.
- Der Broker ersetzt den Auth-Header nur für das fest erlaubte Provider-Ziel
  durch das echte Credential.

## Komponenten

| Paket | Aufgabe |
|---|---|
| `internal/credentialvault` | Verschlüsselter Datei-Vault: Argon2id + AES-256-GCM, AAD pro Eintrag, atomare Writes, Lock |
| `internal/credentialbroker` | Policy, Sessions, Budgets, HTTP-Gateway, Admin-API, Request-Log |
| `internal/brokerclient` | CLI-Client über den Admin-Unix-Socket |
| `cmd/dockweave-broker` | Broker-Prozess und Listener |
| `cmd/dockweave` | Befehle `secret`, `credential`, `broker`, `egress` |

## Schnellstart (lokal, ohne echtes Secret)

1. Bauen: `make build`

2. Vault anlegen (Passphrase nur über die Umgebung, nie in Args/Datei):

   ```bash
   export DOCKWEAVE_VAULT_PASSPHRASE='eine lange lokale Passphrase'
   ./bin/dockweave-broker --init
   ```

3. Credential speichern (nur über stdin):

   ```bash
   printf '%s' 'sk-or-dummy' | ./bin/dockweave secret put openrouter-main --provider openrouter
   ./bin/dockweave secret list
   ```

4. Grant und Session:

   ```bash
   ./bin/dockweave credential grant developer openrouter-main --models '*' \
     --rpm 30 --concurrent 2 --tokens-day 200000
   ./bin/dockweave broker session create developer openrouter-main --ttl 1h
   # gibt dws_… genau einmal aus
   ```

5. Session-Token als API-Key des Agenten verwenden:

   ```bash
   export OPENAI_BASE_URL='http://127.0.0.1:8787/p/openrouter'
   export OPENAI_API_KEY='dws_…'
   ./bin/dockweave-broker --listen 127.0.0.1:8787
   ```

6. Status/Verbrauch (ohne Secrets):

   ```bash
   ./bin/dockweave broker status
   ./bin/dockweave broker requests
   ./bin/dockweave credential usage --grant cg_…
   ```

Eine vollständig lokale Demo mit Fake-Provider liegt unter
`examples/credential-broker/`.

## OpenCode + OpenRouter

Der echte OpenRouter-Key bleibt im Vault; OpenCode kennt nur das
Session-Token.

1. Broker auf einer vom Container erreichbaren Adresse starten, z. B.
   `./bin/dockweave-broker --listen 172.30.0.1:8787`.
2. Secret speichern, Grant mit Modell-/Budgetgrenzen anlegen, Session
   erzeugen (siehe oben).
3. In der OpenCode-Konfiguration einen OpenAI-kompatiblen Provider auf den
   Broker zeigen lassen:

   ```json
   {
     "$schema": "https://opencode.ai/config.json",
     "provider": {
       "dockweave-openrouter": {
         "npm": "@ai-sdk/openai-compatible",
         "name": "OpenRouter via DockWeave",
         "options": {
           "baseURL": "http://172.30.0.1:8787/p/openrouter",
           "apiKey": "dws_REPLACE_WITH_SESSION_TOKEN"
         },
         "models": {
           "openrouter/auto": { "name": "OpenRouter Auto" }
         }
       }
     }
   }
   ```

4. Prüfen, dass der echte Key nicht im Container ist:

   ```bash
   docker inspect <container> | grep -i sk-or    # keine Ausgabe
   docker exec <container> env | grep -i api_key # nur dws_… oder nichts
   ```

Hinweise:

- Streaming, Tool-Calling, Abbruch und Provider-Fehler werden durchgereicht.
- Redirects werden **nicht** verfolgt; ein 3xx geht an den Client zurück,
  damit das injizierte Credential nicht an ein fremdes Ziel gelangt.
- Modell-Allowlists sind exakte Strings; `*` erlaubt alle Modelle.
- Kostenbudgets sind Broker-seitige Schätzungen (Usage-Metadaten plus
  konfigurierte Preise), keine Abrechnungsgarantie. Zusätzlich harte Limits
  beim Provider setzen.

## Operator-CLI

```bash
dockweave secret put NAME --provider PROVIDER    # Wert aus stdin
dockweave secret list
dockweave secret rotate NAME
dockweave secret revoke NAME

dockweave credential grant AGENT CREDENTIAL [flags]
dockweave credential list --agent AGENT
dockweave credential revoke AGENT CREDENTIAL
dockweave credential usage --grant GRANT_ID

dockweave broker status
dockweave broker health
dockweave broker session create AGENT CREDENTIAL --ttl 1h
dockweave broker session list --agent AGENT
dockweave broker session revoke ID
dockweave broker requests

dockweave egress status [AGENT]                  # schlägt fehl, wenn nicht erzwungen
```

Admin-Socket: `$DOCKWEAVE_BROKER_ADMIN_SOCKET`, Standard
`~/.local/share/dockweave/broker/admin.sock`, Modus `0600`, `SO_PEERCRED`
gegen die Prozess-EUID (oder `--admin-uids`).

## Admin-API

Unix-Socket, JSON, Peer-UID-autorisiert. Secret-Werte werden nur in `PUT`-
Bodies akzeptiert und nie zurückgegeben.

| Methode | Pfad | Zweck |
|---|---|---|
| GET | `/v1/status` | Health, Zähler, Provider, Egress-Status |
| GET | `/v1/secrets` | nur Metadaten |
| PUT | `/v1/secrets/{name}` | `{provider, value}` |
| DELETE | `/v1/secrets/{name}` | löschen |
| GET | `/v1/grants?agent=` | Grants |
| POST | `/v1/grants` | Grant anlegen/ersetzen |
| DELETE | `/v1/grants/{id}` | widerrufen |
| GET | `/v1/sessions?agent=` | Sessions (keine Tokens/Hashes) |
| POST | `/v1/sessions` | `{agent, credential|grant_id, ttl}` |
| DELETE | `/v1/sessions/{id}` | sofort widerrufen |
| GET | `/v1/requests` | secret-freies Request-Log |
| GET | `/v1/usage?grant=` | Budget-Snapshot |

Agent-Gateway: `/p/{provider}/{upstream-pfad}` mit
`Authorization: Bearer dws_…` (oder `X-Dockweave-Session`, `X-Api-Key`,
`X-Goog-Api-Key`).

## Speicherung und Entsperrung

- Datei: `<data-dir>/vault.json`, `0600`, Verzeichnis `0700`.
- Master-Passphrase: `DOCKWEAVE_VAULT_PASSPHRASE`, mindestens 8 Zeichen.
  Nicht in Konfigurationsdateien, Argumenten oder im Vault.
- Argon2id-Parameter stehen im Envelope; AES-256-GCM mit zufälliger Nonce pro
  Write und dem Credential-Namen als AAD.
- Atomarer Replace (`temp + fsync + rename`) plus exklusives `flock`, damit
  nicht zwei Broker-Prozesse denselben Vault schreiben.
- `Store.RotatePassphrase()` verschlüsselt alle Werte unter neuem Salt/Key neu.
- **Backup:** `vault.json` bei gestopptem Broker kopieren; Wiederherstellung
  braucht dieselbe Passphrase. Backups sind reiner Ciphertext. Policy-,
  Session- und Budget-Dateien enthalten keine Secret-Werte und können
  zusätzlich gesichert werden.

Unbeaufsichtigter Start: ohne `DOCKWEAVE_VAULT_PASSPHRASE` startet der Broker
nicht. Automatische Unlock-Helfer (systemd `LoadCredential`, TPM, OS-Keyring)
sind Operator-Entscheidung und **nicht** implementiert. Passphrase niemals in
weltlesbare Unit-Dateien schreiben.

## Garantiert

- Kein echter Provider-Key in Container-Env, Docker Inspect, SQLite,
  `/memory`, `/knowledge`, Logs oder der Provider-Konfiguration des Agenten.
- DENY per Default: kein Grant, falscher Agent, falscher Provider, falsches
  Modell, abgelaufener Grant, widerrufene/abgelaufene Session werden
  abgelehnt.
- Secret-Werte werden nie über die Admin-API oder das Request-Log
  zurückgegeben.
- Redirects werden mit injiziertem Credential nicht verfolgt.
- Festes Ziel pro Provider; Pfad-Traversal und unbekannte Provider werden
  abgelehnt.

## Nicht garantiert (offene Blocker)

1. **Kein Egress-Enforcement.** Ein Agent mit Bridge-Netzwerk kann den
   Provider direkt erreichen und den Broker umgehen. DoD-Punkt 6 ist offen.
2. **Keine Docker-/Daemon-Integration.** Der Broker-Socket wird noch nicht
   automatisch in Agentencontainer gemountet; Erreichbarkeit ist
   Operator-Aufgabe.
3. **Keine TUI-Integration** und keine REST-API-v1-Endpunkte in `dockweaved`.
4. **Session-Token ist ein Bearer-Capability.** Kopiert bleibt es bis
   Ablauf/Widerruf innerhalb seines Grants nutzbar; keine workload-gebundene
   Auth.
5. **Budget ist eine Schätzung.** Provider-Usage kann abweichen; keine
   Abrechnungsobergrenze.
6. **Gleicher Host ist keine starke Grenze** gegen Host-Root. Root kann
   Prozessspeicher lesen.
7. **SSH-Keys** sind nicht Teil dieses Inkrements (keine HTTP-Credentials).
8. **Migration aus EnvFile** ist manuell, nicht automatisiert.

Vollständiges Bedrohungsmodell:
`docs/threat-model-credential-broker.de.md`.
