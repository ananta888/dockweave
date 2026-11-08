# Egress-Sicherheit — Design und aktueller Stand

> **Stand 2026-11-08:** `offline` und `broker-only` werden mit
> Docker-Netzwerken erzwungen. `broker-only` nutzt ein internes Docker-Netz
> (keine externe Route); ein privilegierter Integrationstest belegt:
> externer Egress schlägt fehl (`ENETUNREACH`), der Host-Gateway bleibt für
> den Broker erreichbar. **Noch offen:** Port-Filterung am Host-Gateway
> (nftables), DNS-/IMDS-Härtung über das interne Netz hinaus und
> `restricted-development`.

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

## Nächste Schritte (`egress-003`)

1. nftables-Tabelle mit eigenem DockWeave-Label; nur Broker-Port am Gateway
   erlauben, Host-private Bereiche und IMDS explizit droppen.
2. Fail-Closed bei fehlenden Regeln; Reconcile bei Daemon-Neustart.
3. Privilegierte Suite: direktes curl/Python/Node zum Provider scheitert;
   Broker funktioniert; IPv6/DNS-Rebinding/private IP/IMDS scheitern.
4. Wechselwirkungen mit Host-Firewall und WSL2-Grenzen dokumentieren.

## Profil-Vertrag

`broker-only` ist der bevorzugte Modus für credential-gestützte Agenten.
`offline` ist für Agenten ohne Netz. `unrestricted-legacy` bleibt nur aus
Kompatibilitätsgründen und wird als unsicher gemeldet.
