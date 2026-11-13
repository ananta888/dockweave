# Keycloak & OIDC – verständlicher Leitfaden

Deutsch · [English](README.md)

Ein installierbarer Skill und eine verständliche Wissenssammlung zu:

- Keycloak und OpenID Connect (OIDC),
- Authentifizierung und Anwendungsberechtigungen,
- datensparsamen Personenkennungen,
- Auditierbarkeit und Journaling,
- DSGVO-Löschung und gesetzlichen Aufbewahrungspflichten,
- delegierter Administration und Funktionstrennung.

Das Projekt richtet sich gleichzeitig an zwei Zielgruppen:

1. Menschen, die vor einer Entscheidung eine verständliche Erklärung benötigen;
2. Assistenten und technische Fachleute, die präzise Grenzen, Umsetzungsschritte und Tests brauchen.

## Sicherer Einstieg

1. Keycloak ist das einzige persistente führende System für das zentrale Identitätsprofil: Name, E-Mail-Adresse, Profilanschrift und Telefonnummer.
2. Anwendungen speichern normalerweise `iss` und `sub`. Jeder Profilwert in Token, Cache, Log, Journal, Export, Index, Screenshot oder Backup ist ein weiterer Ort der Verarbeitung personenbezogener Daten mit eigener Rechtsgrundlage, Sicherheit, Aufbewahrung, Auskunft, Einschränkung und Löschung.
3. Keycloak auditiert Authentifizierung und Administration; jede Anwendung auditiert Zugriffe auf ihre eigenen sensiblen Fachdaten mit Kennungen und Referenzen statt kopierter Profile oder roher Token.
4. Ein Berechtigungsoperator darf weder sein eigenes noch ein alternatives oder neu angelegtes Konto hochstufen können. Kontoanlage, Zugangsdaten-Reset, Zieleignung, Freigabe, Ausführung und Audit-Kontrolle müssen getrennt und getestet werden.

Eine erforderliche Liefer- oder Rechnungsanschrift darf Bestandteil des Geschäftsdatensatzes sein. Das ist eine dokumentierte Ausnahme mit eigenem Zweck und Lebenszyklus, keine wiederverwendbare Kopie des Login-Profils. Die rechtlichen Folgen unkontrollierter Kopien werden in den Datenschutz- und Audit-Referenzen ausdrücklich erläutert.

## Warum es diesen Leitfaden gibt

Identitätsprojekte scheitern häufig an den Systemgrenzen:

- Eine Anwendung dekodiert ein Token, prüft aber Signatur und Inhalt nicht vollständig.
- E-Mail-Adressen werden als dauerhafte Datenbankschlüssel verwendet.
- Eine UUID wird fälschlich als anonym angesehen.
- Von Keycloak wird erwartet, dass es Zugriffe auf Fachdaten protokolliert.
- Sämtliche personenbezogenen Daten erhalten pauschal eine Aufbewahrungsfrist von zehn Jahren.
- Ein Helpdesk-Administrator kann sich über Gruppen oder zusammengesetzte Rollen indirekt selbst berechtigen.

Der Leitfaden macht diese Grenzen sichtbar und übersetzt sie in überprüfbare Systementwürfe.

## Typische Fragen

- Wie erklärt man Keycloak und OIDC verständlich, ohne technisch ungenau zu werden?
- Prüft eine API ihre Keycloak-Access-Tokens korrekt?
- Wie sieht eine datensparsame Identitätsarchitektur mit `iss` und `sub` aus?
- Was unterscheidet Betriebsprotokoll, Sicherheitsprotokoll, Audit-Log und Journal?
- Wie löscht man nach DSGVO, ohne gesetzlich aufzubewahrende Unterlagen zu vernichten?
- Wie bleibt eine ausgeschiedene Person bei alten Vorgängen zuordenbar, ohne ihr Konto aktiv zu halten?
- Wie dürfen Keycloak-Administratoren Rechte vergeben, ohne sie sich selbst verschaffen zu können?
- Welche Positiv- und Negativtests braucht die feingranulare Administration?

## Aufbau

```text
SKILL.md
agents/openai.yaml
.github/workflows/quality.yml
references/
  foundations.md
  audit-and-journaling.md
  privacy-and-retention.md
  delegated-administration.md
  authoritative-sources.md
  de/
    grundlagen.md
    audit-und-journaling.md
    datenschutz-und-aufbewahrung.md
    delegierte-administration.md
    quellen.md
scripts/
  validate_skill.py
  check_external_links.py
tests/
  prompt-evals.json
LICENSE
```

`SKILL.md` enthält die kompakten Entscheidungs- und Auswahlregeln. Die Referenzdateien enthalten eigenständige Erklärungen für Menschen und werden nur bei Bedarf geladen. Englische und deutsche Referenzen behandeln dieselben Themen.

## Installation

Für eine benutzerbezogene Installation wird das Repository in das aktuelle Skill-Verzeichnis geklont:

```bash
mkdir -p "$HOME/.agents/skills"
git clone https://github.com/ananta888/keycloak-oidc-human-guide.git \
  "$HOME/.agents/skills/keycloak-oidc-human-guide"
```

Für die Erkennung innerhalb eines einzelnen Repositories liegt der Skill dort unter `.agents/skills/keycloak-oidc-human-guide`. Danach kann er mit `$keycloak-oidc-human-guide` ausdrücklich aufgerufen oder bei passenden Fragen automatisch ausgewählt werden.

Die aktuelle [OpenAI-Dokumentation zu Skills](https://learn.chatgpt.com/docs/build-skills) nennt `$HOME/.agents/skills` und `.agents/skills` als lokale Speicherorte. Ältere Pfade sollten nur verwendet werden, wenn die Dokumentation der konkreten Installation sie ausdrücklich verlangt. Für breitere verwaltete Verteilung wird der Skill nach aktueller Dokumentation als Plugin paketiert.

## Grundposition des Leitfadens

Empfohlen werden insbesondere:

- Authorization Code Flow mit PKCE,
- minimale Rechte und eng begrenzte Token-Zielgruppen,
- `iss` und `sub` statt veränderlicher Profildaten als Personenreferenz,
- Keycloak als einziges persistentes führendes System für zentrale Identitätsattribute,
- möglichst wenige Claims und dokumentierte fachliche Ausnahmen statt Schattenprofilen,
- Zugriffsprotokollierung in der Fachanwendung bei sensiblen Daten,
- getrennte und gegen Manipulation geschützte Audit-Speicherung,
- eigene Aufbewahrungsregeln je Daten- und Dokumentkategorie,
- feingranulare delegierte Administration sowie ein externer Freigabeprozess für das Vier-Augen-Prinzip.

Keycloak wird ausdrücklich nicht als Fachdatenbank, unveränderliches Compliance-Archiv oder vollständiger Ersatz für die Berechtigungsprüfung der Anwendung dargestellt.

## Qualitätsprüfungen

Die deterministischen Prüfungen werden lokal so ausgeführt:

```bash
python3 scripts/validate_skill.py .
```

Geprüft werden Skill-Metadaten, lokale Links, zweisprachige Struktur, Quellen-Prüfdaten und Prompt-Evaluierungsfälle. Externe Links werden wegen möglicher vorübergehender Netzfehler separat geprüft:

```bash
python3 scripts/check_external_links.py .
```

## Rechtlicher Hinweis

Die rechtlichen Abschnitte erklären technische Konsequenzen aus der DSGVO und deutschen Aufbewahrungspflichten. Sie ersetzen keine Rechtsberatung für eine konkrete Organisation, Branche oder Dokumentart. Aktuelle Aussagen müssen anhand der Primärquellen in [references/de/quellen.md](references/de/quellen.md) geprüft werden.

## Lizenz

BSD-3-Clause-Lizenz, siehe [LICENSE](LICENSE).
