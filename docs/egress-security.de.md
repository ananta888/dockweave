# Egress-Sicherheit — Design und aktueller Stand

> **Dieser Build erzwingt kein Egress.** Es gibt keine Docker-Netzwerk-,
> nftables-, Namespace- oder eBPF-Regel, die einen Agenten daran hindert, den
> Provider direkt aufzurufen. Der Broker ist ein Policy-Gateway, keine
> Netzgrenze. DockWeave darf erst als egress-isoliert bezeichnet werden, wenn
> die `egress-*`-Aufgaben in `todo/credential-vault.json` erledigt sind.

Sprache: Deutsch · [English](egress-security.md)

## Warum ein Proxy allein nicht reicht

Hat ein Agentencontainer Bridge-Netzwerk und eine Route ins Internet, kann
jeder Prozess (curl, Python, Node, Go, eigener TCP-Client, QUIC) direkt zum
Provider verbinden und den Broker überspringen. Proxy-Umgebungsvariablen sind
unverbindlich: ein Agent kann sie entfernen oder Raw-Sockets nutzen. Credential-
Brokerage ist daher nur mit erzwungenem Egress glaubwürdig.

## Erforderliche Eigenschaften (Zielbild)

1. Dediziertes Docker-Netzwerk pro Agentenklasse, keine Default-Route ins
   Internet.
2. Host-seitige nftables-Regeln außerhalb der Agentenkontrolle:
   - DNS nur zum kontrollierten Resolver (oder keine DNS bei festen IPs),
   - nur Broker-Ziel (Host-Gateway/Broker-Container),
   - sonst IPv4 und IPv6 droppen, inklusive Link-Local und IMDS
     (`169.254.169.254`, `fd00:ec2::254`),
   - Host-private Bereiche und Docker-Socket blocken.
3. Fail-Closed: fehlen Regeln oder ist der Broker down, hat der Container
   kein Netz – nicht unbegrenztes Netz.
4. Reconcile bei Daemon-Neustart und Container-Neuerstellung; keine
   veralteten permissiven Regeln.
5. Kein `/var/run/docker.sock`, kein `CAP_NET_ADMIN` im Agenten.
6. DNS-Rebinding-Schutz: IP beim Connect prüfen, nicht nur den Hostnamen.
7. IPv6-Parität und expliziter Umgang mit Redirects/alternativen Protokollen.

## Netzwerkprofile (Vertrag; noch nicht erzwungen)

| Profil | Beabsichtigtes Verhalten |
|---|---|
| `offline` | Kein Netz außer Loopback |
| `broker-only` | Nur Broker-Endpunkt; kein direktes Internet |
| `restricted-development` | Broker + explizite Allowlist (Paketspiegel, Git) |
| `unrestricted-legacy` | Aktuelles Bridge-Verhalten; ausdrücklich unsicher |

`broker-only` muss nach Einführung der Standard für credential-gestützte
Agenten sein.

## Warum jetzt nicht implementiert

Korrekte Umsetzung erfordert privilegierte Host-Firewall-Änderungen und
isolierte Integrationstests (Root, nftables, Docker-Netzwerke). Die
Repository-Regeln verlangen dafür den privilegierten Testcontainer und
Reversibilität; ein halb implementiertes Regelset könnte still mehr Zugriff
gewähren als vorher. Metadaten, die *so tun als ob*, würden gegen die
Projektregel „keine Sicherheit vortäuschen“ verstoßen.

`dockweave egress status` sagt daher die Wahrheit und endet mit Fehlercode:
`egress isolation is NOT enforced by this build`.

## Umsetzungsplan (`egress-001`… in der TODO)

1. `network_profile` im Agentenmodell und in der Persistenz ergänzen.
2. Daemon legt dediziertes Docker-Netzwerk pro Profil an (`internal=true`,
   soweit möglich).
3. Daemon installiert nftables-Regeln in eigener Tabelle mit Kommentar/Label;
   Reconcile entfernt nur eigene Regeln.
4. Broker erhält feste Adresse in diesem Netz.
5. Bei jedem Setup-Fehler Fail-Closed; Agentenstart verweigern.
6. Privilegierte Tests: direktes curl/Python/Node zum Provider muss scheitern;
   Broker-Pfad muss funktionieren; IPv6- und DNS-Rebinding-Versuche müssen
   scheitern; Daemon-Neustart muss konvergieren.
7. Wechselwirkungen mit Host-Firewall und WSL2-Grenzen dokumentieren.
