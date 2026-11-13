# Credential Vault & API-Broker

Der Credential-Broker von DockWeave erlaubt einem Agenten, eine Provider-API
zu nutzen, **ohne jemals den echten API-Key zu besitzen**. Secrets liegen in
einem verschlüsselten lokalen Vault; der Agent erhält ein kurzlebiges
Session-Token, das nur für seinen expliziten Grant gilt.

> **Stand dieses Inkrements (2026-11-08):** Vault, Policy, Sessions,
> HTTP-Gateway, Budgets, CLI, Daemon-/TUI-Metadaten sowie die Egress-Profile
> `offline` und `broker-only` sind implementiert und getestet. `broker-only`
> nutzt ein internes Docker-Netz ohne externe Route. Offen bleiben:
> Host-Gateway-Portfilterung per nftables, `restricted-development`,
> echter Provider-E2E-Test und der SSH-Broker. Siehe
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

## OAuth-Credentials (Codex ChatGPT)

Codex nutzt eine ChatGPT-OAuth-Anmeldung, keinen API-Key. Der Broker speichert
das OAuth-Bundle im Vault und erneuert den Access-Token vor der Injektion. Der
Agent sieht nur ein `dws_…`-Session-Token.

1. Bestehenden Login importieren (die Quelldatei bleibt unangetastet):

   ```bash
   ./bin/dockweave secret import-codex codex-main \
     --auth-file ~/.codex/auth.json \
     --grant --agent developer --models '*'
   ```

   Das Bundle enthält `access_token`, `refresh_token`, `id_token` und
   `account_id`; nichts davon wird ausgegeben.

2. Session erzeugen und Codex auf den Broker zeigen lassen
   (`~/.codex/config.toml` des Agenten):

   ```toml
   model = "gpt-6.1-sol"
   model_provider = "dockweave"

   [model_providers.dockweave]
   name = "DockWeave"
   base_url = "http://<broker>:8787/p/codex-chatgpt"
   env_key = "DOCKWEAVE_TOKEN"
   wire_api = "responses"
   ```

   ```bash
   export DOCKWEAVE_TOKEN="$(dockweave broker session create developer codex-main --ttl 8h | tail -1)"
   ```

3. Der Broker mappt `/p/codex-chatgpt/responses` auf
   `https://chatgpt.com/backend-api/codex/responses`, erneuert den Token
   innerhalb von 120 s vor Ablauf, persistiert den rotierten Refresh-Token und
   setzt `chatgpt-account-id` plus die festen Header `OpenAI-Beta`/`originator`.

Hinweise:

- Refresh-Tokens rotieren. Wird derselbe Login auch außerhalb des Brokers
  genutzt, invalidieren sich beide Seiten gegenseitig. Für die direkte
  Terminal-Nutzung einen separaten Login verwenden oder den Broker allein
  besitzen lassen.
- Die `headers`-Map eines Providers ist Operator-definiert und kann vom Agenten
  nicht gesetzt werden.
- `secret import-opencode NAME --auth-file …` importiert einen OpenCode-Zen-Key
  (Provider `opencode`).
- Das ChatGPT-Codex-Backend verlangt `stream: true` und `store: false`; die
  Codex-CLI sendet beides bereits.

### Dauerbetrieb

Der Broker muss vom Agentencontainer erreichbar bleiben und das Session-Token
vor Ablauf erneuert werden. Ein fertiges systemd-User-Setup liegt in
[`deployments/credential-broker/`](../deployments/credential-broker/README.de.md):
Broker am Docker-Bridge-Gateway, stündlicher Timer mit
`dockweave-codex-refresh` schreibt ein frisches `dws_…`-Token nach
`/memory/.config/dockweave/codex-token`, Codex liest es über
`model_providers.*.auth.command`.

## Operator-CLI

```bash
dockweave secret put NAME --provider PROVIDER    # Wert aus stdin
dockweave secret list
dockweave secret rotate NAME
dockweave secret revoke NAME

dockweave secret migrate --agent AGENT --env-file DATEI          # nur explizite Datei
dockweave secret migrate --agent AGENT --env-file DATEI --dry-run
dockweave secret migrate --agent AGENT --env-file DATEI --yes --grant --models '*'
dockweave secret migrate --agent AGENT --env-file DATEI --yes --disable-env-file

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

dockweave egress status AGENT
dockweave egress profile AGENT broker-only
```

`secret migrate` liest nur die explizit angegebene EnvFile, scannt keine
Verzeichnisse, gibt keine Werte aus und leert die `env_file`-Referenz des
Agenten sowie benennt die Datei erst um, nachdem alle Stores erfolgreich
waren. Ohne `--yes` ist eine interaktive Bestätigung nötig.

Admin-Socket: `$DOCKWEAVE_BROKER_ADMIN_SOCKET`, Standard
`~/.local/share/dockweave/broker/admin.sock`, Modus `0600`, `SO_PEERCRED`
gegen die Prozess-EUID (oder `--admin-uids`).

## Egress-Profile

| Profil | Durchsetzung |
|---|---|
| `offline` | Docker `none` (kein Netz) |
| `broker-only` | Internes Docker-Netz, keine externe Route; Host-Gateway für den Broker erreichbar |
| `restricted-development` | Nicht implementiert; Start wird verweigert |
| `unrestricted-legacy` / leer | Legacy-Bridge; ausdrücklich unsicher |

`dockweave egress status AGENT` zeigt Profil, Enforcement, Netz, Gateway und
Broker-Base-URL. In broker-only-Agenten setzt der Daemon
`DOCKWEAVE_BROKER_BASE_URL=http://<gateway>:8787`; den Broker dort binden.
Siehe [egress-security.de.md](egress-security.de.md) für das Host-Gateway-
Restrisiko.

## REST-API und TUI

Der Daemon stellt read-only Broker-Metadaten (keine Secrets, Admin/Monitor-
GET) bereit, wenn `BROKER_ADMIN_SOCKET` konfiguriert ist:

| Methode | Pfad |
|---|---|
| GET | `/api/v1/broker/status` |
| GET | `/api/v1/broker/grants?agent=` |
| GET | `/api/v1/broker/sessions?agent=` |
| GET | `/api/v1/broker/requests` |
| GET | `/api/v1/broker/usage?grant=` |
| GET | `/api/v1/agents/{id}/egress` |
| PUT | `/api/v1/agents/{id}/network-profile` |
| PUT | `/api/v1/agents/{id}/env-file` |

Die TUI zeigt den Broker-Status im Dashboard sowie Egress-Profil und
Credential-Grants in den Agent-Details. Secrets werden nie angezeigt.

Der Admin-Socket des Brokers bleibt der einzige Mutationspfad für Secrets und
Grants.

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

1. **Host-Gateway-Filterung ist unvollständig.** `broker-only` blockt
   externen Egress über ein internes Docker-Netz, aber der Agent erreicht
   weiterhin Host-Dienste am Gateway. nftables-Regeln sind offen
   (`egress-003`).
2. **Kein automatisches Broker-Socket-Mounting.** Die Erreichbarkeit und die
   Harness-Konfiguration sind Operator-Aufgabe (der Daemon setzt in
   broker-only-Agenten `DOCKWEAVE_BROKER_BASE_URL`).
3. **`restricted-development` ist nicht implementiert**; der Agentenstart
   schlägt fail-closed fehl.
4. **Session-Token ist ein Bearer-Capability.** Kopiert bleibt es bis
   Ablauf/Widerruf innerhalb seines Grants nutzbar; keine workload-gebundene
   Auth.
5. **Budget ist eine Schätzung.** Provider-Usage kann abweichen; keine
   Abrechnungsobergrenze.
6. **Gleicher Host ist keine starke Grenze** gegen Host-Root. Root kann
   Prozessspeicher lesen.
7. **SSH-Keys** sind nicht Teil dieses Inkrements.
8. **EnvFile-Migration** ist operatorgestützt (`secret migrate`), kein
   Hintergrund-Scanner.

Vollständiges Bedrohungsmodell:
`docs/threat-model-credential-broker.de.md`.
