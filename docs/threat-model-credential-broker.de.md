# Bedrohungsmodell — Credential Vault & Broker

Umfang: `internal/credentialvault`, `internal/credentialbroker`,
`cmd/dockweave-broker` sowie die CLI-Befehle `secret`/`credential`/`broker`.

## Schutzgüter

1. Langfristige Provider-Credentials (OpenRouter, OpenAI, Anthropic,
   DeepSeek, Gemini, GitHub/HTTP-Tokens).
2. Master-Passphrase und abgeleitete Schlüssel.
3. Broker-Session-Tokens (kurzlebige Capabilities).
4. Operator-Policy, Budgetzähler und Audit-/Request-Logs.

## Vertrauensgrenzen

```
 nicht vertrauenswürdiger Agent │ Agent-Gateway (Session, Policy, Budget)
                                │
 vertrauenswürdiger Operator    │ Admin-Unix-Socket (SO_PEERCRED)
                                │
 vertrauenswürdiger Broker      │ entschlüsselt Credentials im Speicher
                                │
 vertrauenswürdiger Host-Kernel / Host-Root
```

Der Broker ist ein separater, unprivilegierter Prozess. Er ist **keine**
Sandbox: Host-Root, ptrace oder Prozessspeicher-Zugriff hebeln ihn aus.

## Angreifer und Kontrollen

| Angreifer | Versuch | Kontrolle | Restrisiko |
|---|---|---|---|
| Kompromittierter Agent | Env/Docker Inspect lesen | Keys nie in Container-Env; nur Session-Token | Token im Grant bis Ablauf/Widerruf missbrauchbar |
| Kompromittierter Agent | `/memory`, `/knowledge`, Logs lesen | Vault außerhalb der Agent-Mounts; keine Secrets in Logs | Agent darf sein eigenes Session-Token besitzen |
| Kompromittierter Agent | Broker ohne Grant nutzen | DENY per Default, Agent-/Provider-/Modellprüfung | Kopiertes gültiges Token funktioniert |
| Kompromittierter Agent | Policy/Grant ändern | Admin-Socket `0600` + SO_PEERCRED-EUID-Allowlist | Host-Root oder gleiche UID kann administrieren |
| Kompromittierter Agent | Broker auf Angreiferziel lenken | Feste Provider-Base-URL; Traversal/unbekannter Provider abgelehnt; Redirects nicht verfolgt | Provider-Kompromittierung außerhalb des Modells |
| Kompromittierter Agent | Budget überschreiten | Rate-/Concurrency-/Request-/Token-/Kostenlimits, Reservierungen | Schätzungen können zu niedrig sein |
| Lokaler Nutzer | Vault-Datei öffnen | AES-256-GCM + Argon2id; `0600`, `0700`, Lock | Offline-Passphrase-Raten bei kopierter Datei |
| Lokaler Nutzer | Admin-Socket verbinden | Peer-UID-Prüfung, Socket `0600` | Prozesse gleicher UID gelten als vertrauenswürdig |
| Netzwerkangreifer | Proxy-Socket erreichen | Unix-Socket `0600`; optionales TCP nur mit Firewall | TCP-Listener ist opt-in und warnt |
| Operatorfehler | Schwache Passphrase | nur Minimum 8 Zeichen | Kein Strength-Meter/HSM |
| Logs/Audit | Secret-Leak | keine Werte in Request-Log, Fehlern, Events | Provider-Fehlerbodies werden durchgereicht und können Prompts enthalten |

## Kryptografie

- AES-256-GCM, zufällige 96-Bit-Nonce pro Write.
- Argon2id (64 MiB, 3 Iterationen, 4 Lanes, 16-Byte-Salt) als Default.
- AAD pro Eintrag bindet Ciphertext an den Credential-Namen (kein Swap).
- Versionierter Envelope; atomare Writes; Passphrase wird nicht persistiert.
- Nur der SHA-256-Hash von Session-Tokens wird gespeichert.

## Ausdrückliche Nicht-Ziele

- Kein Schutz gegen kompromittierten Host-Kernel, Host-Root, ptrace oder
  manipulierten Broker.
- Keine Netzwerkisolation. Ein Agent mit direktem Egress kann den Broker
  umgehen.
- Kein TLS-MITM, keine eigene Root-CA.
- Kein Schutz vor Provider-Logging oder Abrechnungsfehlern.
- Keine SSH-Key-Vermittlung in diesem Inkrement.
- Keine Mehrmandanten-Isolation zwischen Operatoren desselben Brokers.

## Fail-Closed-Verhalten

- Broker startet nicht ohne Passphrase oder mit korruptem/falschem Vault.
- Unbekannte/abgelaufene/widerrufene Session → 401.
- Kein/abgelaufener/fremder Grant, verbotener Provider/Modell → 403.
- Rate/Concurrency/Budget überschritten → 429.
- Upstream-Fehler → 502, Reservierung zurückgerollt.
- Unbekannter Provider/Pfad → 404.
- Policy-/Store-Lesefehler beim Matching führen zum selben Deny-Pfad.

## Offene Risiken (verpflichtend getrackt)

1. Egress-Bypass (höchste Priorität).
2. Session-Token-Replay innerhalb der Grant-Laufzeit.
3. Broker-Kostenschätzung vs. Provider-Abrechnung.
4. Gleicher-Host-Grenze vs. Host-Root.
5. Kompromittierter Agent kann sein erlaubtes Budget ausschöpfen.
6. Keine workload-gebundene Identität (Token ist portabel).
7. Noch keine TUI-/API-Sichtbarkeit.

## Verifikation in diesem Inkrement

- Vault: falsche Passphrase, Tamper-Erkennung, AAD-Swap, kein Klartext auf
  Disk, Rechte, Lock, Rekey.
- Broker: Auth, Widerruf, Modell-/Provider-Deny, SSRF/Pfad-Traversal,
  Redirect-Nichtverfolgung, Budget/Rate/Concurrency, Streaming-Usage, kein
  Secret in Request-Log oder Metadaten-API.
- Nicht verifiziert: echter Provider, Container-Egress, Host-Firewall, TUI,
  REST-API.
