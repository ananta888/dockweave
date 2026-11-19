# Egress-Sicherheit — Design und aktueller Stand

> **Stand 2026-11-19:** `offline` und `broker-only` werden mit
> Docker-Netzwerken erzwungen. `broker-only` nutzt ein internes Docker-Netz
> (keine externe Route); ein privilegierter Integrationstest belegt:
> externer Egress schlägt fehl (`ENETUNREACH`), der Host-Gateway bleibt für
> den Broker erreichbar. **Die nftables-Durchsetzung (`egress-003`) ist
> implementiert** als opt-in, fail-closed Host-Firewall
> (`EGRESS_NFTABLES=true`); die vollständige Bypass-Suite braucht noch einen
> privilegierten Lauf. `restricted-development` bleibt unimplementiert
> (fail closed).

Sprache: Deutsch · [English](egress-security.md)

## Warum ein Proxy allein nicht reicht

Hat ein Agentencontainer Bridge-Netzwerk und eine Route ins Internet, kann
jeder Prozess (curl, Python, Node, Go, eigener TCP-Client, QUIC) direkt zum
Provider verbinden und den Broker überspringen. Proxy-Umgebungsvariablen sind
unverbindlich: ein Agent kann sie entfernen oder Raw-Sockets nutzen.
Credential-Brokerage ist daher nur mit erzwungenem Egress glaubwürdig.

## Implementiert: Netzwerkprofile

| Profil | Erzwungenes Verhalten |
|---|---|
| `offline` | Docker `--network none`; kein Netz |
| `broker-only` | Dediziertes internes Docker-Netz `dockweave-broker-only`; keine externe Route; Host-Gateway für den Broker erreichbar |
| `restricted-development` | **Nicht implementiert.** Agentenstart wird verweigert (fail closed) |
| `unrestricted-legacy` | Bisheriges Bridge-Verhalten; ausdrücklich unsicher |

Auswahl per `dockweave agent create --profile broker-only ...` oder
`dockweave egress profile AGENT broker-only` (wirkt beim nächsten
Start/Restart). `dockweave egress status AGENT` zeigt Profil, Enforcement,
Netz, Gateway und Broker-Base-URL. Unbekannte Profile werden abgelehnt; ein
Fehler beim Anlegen/Prüfen des Netzes verweigert den Start.

Der Daemon setzt in broker-only-Agenten
`DOCKWEAVE_BROKER_BASE_URL=http://<gateway>:8787`. Das ist kein Secret.

## Verifikation

`tests/integration/egress_network_test.go` legt ein internes Netz an und
belegt:

1. der Host-Gateway ist aus dem Container erreichbar (Broker-Pfad),
2. eine externe HTTPS-Verbindung scheitert mit `ENETUNREACH`.

Aufruf: `go test -tags integration ./tests/integration -run Egress`.

## Restrisiko (offen)

- **Host-Gateway-Exposition:** Das interne Netz erlaubt weiterhin Zugriff auf
  alle Host-Dienste, die am Gateway lauschen – nicht nur den Broker. Dafür
  sind Host-nftables-Regeln nötig (`egress-003`).
- **IPv6/DNS/IMDS:** Docker blockt externe Routen, aber nicht für jede
  Host-/Docker-Version exhaustiv getestet; vor vollständiger
  Isolationsbehauptung explizit prüfen.
- **WSL2/Docker-Varianten:** nicht verifiziert.
- **Container-Neuerstellung/Daemon-Neustart:** `EnsureNetwork` prüft bei
  jedem Start; automatisches Aufräumen veralteter Netze fehlt.

## Host-Firewall (`egress-003`)

Implementiert in `internal/egress` und in den Daemon hinter
`EGRESS_NFTABLES=true` verdrahtet (standardmäßig aus). Ist es aktiv, legt der
Start eines `broker-only`-Agenten eine DockWeave-eigene nftables-Tabelle
(`inet dockweave-egress`) an, die:

1. nur Traffic auf der broker-only-Bridge filtert,
2. den Broker-Port auf der Gateway-Adresse erlaubt,
3. private Bereiche, Link-Local/Metadata (IMDS), IPv6-ULA/Link-Local/
   Multicast und DNS droppt,
4. alles andere von dieser Bridge droppt (Default-Deny).

Das Anwenden ist fail-closed: fehlendes `nft`, kein root oder ein fehlgeschla-
genes Anwenden verweigert den Agentenstart. Die Tabelle ist idempotent
(Prüfung per `nft list table`).

Restrisiko: Die vollständige Bypass-Suite (direktes curl/Python/Node zum
Provider muss scheitern, der Broker-Pfad funktioniert; IPv6/DNS-Rebinding/
Private-IP/IMDS-Matrix) ist ein privilegierter Test
(`tests/integration/egress_nftables_test.go` deckt derzeit nur den
Tabellen-Roundtrip ab) und muss in der privilegierten CI-Umgebung laufen.

## Plattform-Kompatibilität (WSL2 / Windows)

DockWeave ist **nur unter Linux** lauffähig. Es gibt keine native
Windows-Unterstützung: Der Daemon nutzt `mount(2)`, `openat2`, `/proc`,
`SO_PEERCRED` und eine Linux-Docker-Engine — all das existiert unter Windows
nicht nativ.

| Umgebung | Status |
|---|---|
| Linux (nativ, Kernel ≥ 5.12) | Unterstützt; Referenzplattform. |
| Linux in einer VM | Unterstützt; verhält sich wie natives Linux. |
| WSL2 (Ubuntu etc.) | Funktioniert mit Docker Desktop (WSL2-Backend) für Agenten-Runtime und Netzwerkprofile, mit Einschränkungen: |
|  | — Host-seitige nftables-Regeln (`egress-003`) wirken in der WSL2-VM, nicht auf dem Windows-Host; Enforcement gilt je WSL2-Distribution. |
|  | — Die interne Netztopologie von Docker Desktop kann je Version abweichen; `broker-only`-Isolation ist pro Version neu zu verifizieren (der privilegierte Egress-Test tut das). |
|  | — Der Broker muss eine aus Agentencontainern erreichbare Adresse binden (`127.0.0.1` in derselben Distribution oder das WSL2-VM-Gateway). |
| macOS / Windows (nativ) | Nicht unterstützt. Docker Desktop betreibt eine Linux-VM; DockWeave müsste in dieser VM laufen, nie auf dem Host-Betriebssystem. |

Die Privilegientrennung (Daemon + `mountd`) und der Mount-Manager setzen einen
Linux-Host mit `CAP_SYS_ADMIN` voraus. Unter WSL2 bedeutet das: innerhalb der
WSL2-VM, nicht auf dem Windows-Host.

## Profil-Vertrag

`broker-only` ist der bevorzugte Modus für credential-gestützte Agenten.
`offline` ist für Agenten ohne Netz. `unrestricted-legacy` bleibt nur aus
Kompatibilitätsgründen und wird als unsicher gemeldet.
