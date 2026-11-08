# SSH-Broker — Design (separates Inkrement)

Status: **nur Design** (2026-11-08). Der HTTP-Broker implementiert keine
SSH-Credentials. SSH-Private-Keys dürfen niemals über ein HTTP-Gateway laufen.

Sprache: Deutsch · [English](ssh-broker-design.md)

## Problem

Ein Agent braucht Git-/SSH-Zugriff, ohne einen langlebigen privaten Key zu
besitzen. Ein reiner `ssh-agent`-Socket ist allein keine sichere Grenze: Wer
den Socket erreicht, kann Signaturen für alle gehaltenen Keys anfordern, ohne
den Key zu lesen. Das OpenSSH-Agent-Protokoll enthält außerdem kein Zielhost
im `SIGN_REQUEST`; ein reiner Agent-Socket kann Zielbeschränkungen daher
nicht selbst durchsetzen.

## Ziele

1. Kein privater Key im Agentencontainer.
2. Kurzlebige, zielgebundene Credentials.
3. Pro-Agent-Identität und explizite Policy (Host, User, Repo, Zeit).
4. Sofortiger Widerruf; begrenzte Session-Laufzeit.
5. Kein SSH-Key verlässt den Broker.
6. Optional FIDO2/Hardware-Signer.

## Nicht-Ziele

- SSH-Host-Verifikation ersetzen.
- Beliebigen SSH-Verkehr ohne Wrapper transparent abfangen.
- Schutz gegen Host-Root.

## Architektur

```
Agent ──> dockweave ssh ... ──(Unix-Socket)──> dockweave-ssh-broker
                                                │
                                                ├── CA-Key (Vault, nie exportiert)
                                                ├── Policy: Agent → Host/User/Repo/TTL
                                                ├── kurzlebiges User-Zertifikat
                                                └── optional FIDO2-Signer
```

### Ablauf A — kurzlebiges Zertifikat (bevorzugt)

1. Der Agent ruft `dockweave ssh --dest github.com --user git -- git ...`
   oder einen Git-`core.sshCommand`-Wrapper auf.
2. Der Wrapper sendet das *gewünschte Ziel* (Host, Port, User, Repo-Hinweis)
   über den admin-authentifizierten Unix-Socket an den Broker.
3. Der Broker prüft die Policy (DENY per Default) und stellt aus:
   - ein OpenSSH-User-Zertifikat, signiert von einer SSH-CA,
   - einen kurzlebigen privaten Key in einem pro Session laufenden
     `ssh-agent` oder einer ephemeren Key-Datei in einem `0700`-Verzeichnis.
4. Der Wrapper startet `ssh -i <key> -o CertificateFile=<cert>`.
5. Zertifikatsoptionen (`permit-pty`, `permit-agent-forwarding`) nur wenn
   die Policy es erlaubt; TTL standardmäßig ≤ 15 min.

Das Zertifikat ist an die Wrapper-Anfrage gebunden; der Broker signiert nur
für den erlaubten Host/User. Der Key ist nach Ablauf nicht für andere Ziele
nutzbar. Für stärkere Bindung pro Ziel einen eigenen Key ausstellen und nach
dem Kommando löschen.

### Ablauf B — eingeschränkte Agent-Signaturen (optional)

Ein broker-eigener `ssh-agent`-Socket pro Session, nur sicher mit:
- einem Key pro Session,
- Wrapper, der das Ziel vor Freigabe des Sockets festlegt,
- sofortigem Socket-Teardown und `ssh-add -D` am Sessionende,
- keinem Forwarding des Sockets an andere Prozesse/Hosts.

Da `SIGN_REQUEST` kein Ziel enthält, ist Ablauf B allein **nicht**
zielbeschränkt. Akzeptabel nur, wenn Key/Zertifikat bereits auf ein Ziel
beschränkt und kurzlebig sind.

## Policy-Modell

```text
grant:
  agent: opencode-01
  host: github.com
  user: git
  repo: ananta888/dockweave       # optional
  command: git-upload-pack,git-receive-pack
  ttl: 15m
  max_sessions: 2
  require_touch: false
```

DENY per Default. Kein Wildcard-Host ohne explizite Angabe. Widerruf wird bei
jeder Signatur/Ausstellung geprüft.

## SSH-CA und Key-Handling

- SSH-CA-Key liegt im verschlüsselten Vault oder in einem dedizierten
  Hardware-Signer; niemals an Agenten exportiert.
- Zertifikate: `ssh-keygen -s ca -I <agent>-<session> -n <user> -V +15m -O ...`.
- Key-Material für Ablauf A pro Session in `0700`-Tempdir, nach Exit löschen,
  niemals loggen.
- FIDO2: Broker lässt die Hardware signieren; kein Export.
- Host-Keys werden normal vom Client geprüft; der Broker deaktiviert
  `StrictHostKeyChecking` nie.

## Risiken und offene Fragen

1. Wrapper-Bypass: Der Agent kann `ssh` direkt aufrufen, solange Egress
   Port 22/SSH nicht blockt. Das ist ein Egress-, kein Broker-Problem.
2. Git könnte Credentials cachen/forwarden; `GIT_SSH_COMMAND` erzwingen.
3. `permit-agent-forwarding` erweitert die Grenze; Default deny.
4. Host-Key-Verifikation muss aktiv bleiben.
5. FIDO2/Touch in headless Agenten.
6. Kopierter Session-Key/-Zertifikat ist innerhalb der TTL nutzbar.

## Phasen

1. Design + TODO (dieses Dokument). ✅
2. CA-Key-Speicherung, Policy, Zertifikatsausstellung, Wrapper und Tests
   gegen einen lokalen SSH-Server.
3. Git-Integration (`GIT_SSH_COMMAND`) und Key-Cleanup.
4. FIDO2-Signer und Hardware-Tests.
5. Privilegierte/E2E-Tests: direktes SSH ohne Wrapper scheitert im
   broker-only-Egress; Wrapper-SSH funktioniert; widerrufene/abgelaufene
   Zertifikate scheitern.
