# Maßgebliche Quellen

Deutsch · [English](../authoritative-sources.md)

**Zuletzt geprüft: 2026-08-28**

Diese Datei ist eine Quellenübersicht. Sie ersetzt nicht die Prüfung der jeweils aktuellen Originalfassung. Produktverhalten und Recht können sich ändern.

## Keycloak

### Server Administration Guide

https://www.keycloak.org/docs/latest/server_admin/

Zu verwenden für:

- Benutzer- und Administratorereignisse;
- `realm-management`-Rollen;
- Fine-Grained Admin Permissions;
- Client Scopes, Rollenzuordnungen, Gruppen und Organisationen;
- Authentifizierungsabläufe, MFA und WebAuthn.

Wichtige Suchbegriffe und Abschnitte:

- `Configuring auditing to track events`
- `Auditing user events`
- `Auditing admin events`
- `Managing access to realm resources`
- `Delegating realm administration using permissions`
- `Fine grained admin permissions`
- `Role scope mappings`

Versionshinweis: Für dieses Projekt wurde zuletzt die aktuelle Dokumentation der Keycloak-26.7-Linie geprüft. Maßgeblich bleibt die tatsächlich eingesetzte Version.

### Aktuelle Version und Sicherheitskorrekturen

https://www.keycloak.org/2026/08/keycloak-2672-released

Am Prüftag war Keycloak 26.7.2 aktuell. Die Version enthält Sicherheitskorrekturen für Fine-Grained Admin Permissions, darunter CVE-2026-14613 (Autorisierungsumgehung über den Role-Groups-Endpunkt) und CVE-2026-15945 (Preisgabe der Gruppenhierarchie). Die Quelle belegt, warum vollständig gepatchte Software und wiederholte Negativtests Voraussetzung für Aussagen zur delegierten Administration sind. Vor Verwendung ist stets auf eine neuere Version oder Sicherheitsmeldung zu prüfen.

### Anwendungen mit OIDC absichern

https://www.keycloak.org/securing-apps/oidc-layers

Zu verwenden für:

- Discovery-Endpunkt;
- Authorization-, Token-, UserInfo-, Zertifikats-, Introspektions- und Logout-Endpunkte;
- Authorization Code Flow;
- Prüfung von Access-Token;
- Sicherheit von Redirect-URIs.

### Protocol Mapper

https://www.keycloak.org/admin-api/protocol-mappers

Zu verwenden für verfügbare OIDC Protocol Mapper, den SHA-256 Pairwise Subject Mapper sowie Claim- und Audience-Mapping.

### Produktions- und Proxy-Konfiguration

- https://www.keycloak.org/server/configuration
- https://www.keycloak.org/server/hostname
- https://www.keycloak.org/server/reverseproxy
- https://www.keycloak.org/server/logging

Zu verwenden für Produktionsmodus, HTTPS, festen Hostnamen, vertrauenswürdige Proxy-Konfiguration, getrennten Administrationshostnamen, Management-Endpunkte, strukturierte Logs, Syslog und Beobachtbarkeit.

## OpenID Connect und OAuth

### OpenID Connect Core 1.0

https://openid.net/specs/openid-connect-core-1_0-final.html

Zu verwenden für:

- Bedeutung von `iss`, `sub` und `aud`;
- Stabilität und Eindeutigkeit von `iss` und `sub`;
- öffentliche und paarweise Subjektkennungen;
- Prüfung des ID-Token.

### OAuth 2.0 Security Best Current Practice, RFC 9700

https://www.rfc-editor.org/rfc/rfc9700.html

Zu verwenden für:

- PKCE-Anforderungen und -Empfehlungen;
- Vermeidung des Implicit Flow;
- Verbot von Resource Owner Password Credentials;
- Begrenzung von Token-Zielgruppe und Rechten;
- moderne Schutzmaßnahmen gegen OAuth-Angriffe.

## Europäisches Datenschutzrecht

### Datenschutz-Grundverordnung

https://eur-lex.europa.eu/eli/reg/2016/679/2016-05-04/deu

Wichtige Artikel:

- Art. 4: personenbezogene Daten und Pseudonymisierung;
- Art. 5: Zweckbindung, Datenminimierung, Speicherbegrenzung und Rechenschaftspflicht;
- Art. 6: Rechtsgrundlagen;
- Art. 7: Widerruf einer Einwilligung;
- Art. 12: transparente Bearbeitung und Antwortfristen;
- Art. 15: Auskunftsrecht;
- Art. 17: Löschung und Ausnahmen;
- Art. 18: Einschränkung der Verarbeitung;
- Art. 21: Widerspruch;
- Art. 25: Datenschutz durch Technikgestaltung und datenschutzfreundliche Voreinstellungen;
- Art. 30: Verzeichnis von Verarbeitungstätigkeiten;
- Art. 32: Sicherheit der Verarbeitung;
- Art. 33 und 34: Meldung von Schutzverletzungen, sofern die Voraussetzungen vorliegen;
- Art. 58: Abhilfebefugnisse der Aufsichtsbehörde;
- Art. 82: Haftung und Schadensersatz;
- Art. 83: Geldbußen.

### EuGH-Rechtssache C-579/21

https://eur-lex.europa.eu/legal-content/DE/TXT/?uri=CELEX:62021CJ0579

Zu verwenden für:

- Auskunft über Zeitpunkt und Zweck von Abfragen;
- Unterscheidung zwischen Empfängern und Beschäftigten, die unter Verantwortung des Verantwortlichen handeln;
- fallabhängige Offenlegung von Beschäftigtenidentitäten.

Aus dem Urteil darf keine unbelegte Pauschalregel abgeleitet werden, nach der jeder Lesezugriff jeder Anwendung unbegrenzt zu protokollieren wäre.

## Deutsche handels- und steuerrechtliche Aufbewahrung

### § 147 Abgabenordnung

https://www.gesetze-im-internet.de/ao_1977/__147.html

Zu verwenden für Dokumentkategorien, aktuelle Aufbewahrungsfristen, Fristbeginn, Verfügbarkeit, Lesbarkeit und Datenzugriff der Finanzbehörde.

### § 257 Handelsgesetzbuch

https://www.gesetze-im-internet.de/hgb/__257.html

Zu verwenden für handelsrechtliche Dokumentarten und Fristen einschließlich besonderer Behandlung von Buchungsbelegen bestimmter regulierter Institute.

### §§ 238, 239 und 245 Handelsgesetzbuch

- https://www.gesetze-im-internet.de/hgb/__238.html
- https://www.gesetze-im-internet.de/hgb/__239.html
- https://www.gesetze-im-internet.de/hgb/__245.html

Zu verwenden für Nachvollziehbarkeit von Geschäftsvorfällen, Vollständigkeit, Ordnung, Unveränderbarkeit und die vorgeschriebene Unterzeichnung des Jahresabschlusses.

### GoBD

Amtliches Material des Bundesfinanzministeriums:

- https://amtliche-handbuecher.bundesfinanzministerium.de/ao/2024/Anhaenge/BMF-Schreiben-und-gleichlautende-Laendererlasse/Anhang-33/inhalt.html
- https://www.bundesfinanzministerium.de/Content/DE/Downloads/BMF_Schreiben/Weitere_Steuerthemen/Abgabenordnung/2025-07-14-GoBD-2-aenderung.html

Zu verwenden für:

- progressive und retrograde Prüfbarkeit;
- Beleg- und Journalfunktion;
- internes Kontrollsystem und Funktionstrennung;
- Änderungsprotokollierung und Unveränderbarkeit;
- Verfahrensdokumentation;
- elektronische Aufbewahrung und Datenzugriff.

Vor Nennung von Randnummern oder technischen Beispielen sind die neuesten Änderungen zu prüfen.

## Deutsche Informationssicherheit

### BSI: Identitäts- und Berechtigungsmanagement

https://www.bsi.bund.de/SharedDocs/Downloads/DE/BSI/Grundschutz/Umsetzungshinweise/Umsetzungshinweise_2022/Umsetzungshinweis_zum_Baustein_ORP_4_Identitaets_und_Berechtigungsmanagement.pdf?__blob=publicationFile&v=2

Zu verwenden für minimale Rechte, Kenntnis nur bei Bedarf, Verantwortung, Freigaben, Rollentrennung und regelmäßige Berechtigungsprüfung.

### BSI: Protokollierung

https://www.bsi.bund.de/SharedDocs/Downloads/DE/BSI/Grundschutz/IT-GS-Kompendium_Einzel_PDFs_2022/04_OPS_Betrieb/OPS_1_1_5_Protokollierung_Edition_2022.html

Zu verwenden für Sammlung sicherheitsrelevanter Ereignisse, geschützte Speicherung, Auswertung und Löschung.

### BSI: Softwareentwicklung

https://www.bsi.bund.de/SharedDocs/Downloads/DE/BSI/Grundschutz/IT-GS-Kompendium_Einzel_PDFs_2023/03_CON_Konzepte_und_Vorgehensweisen/CON_8_Software_Entwicklung_Edition_2023.pdf?__blob=publicationFile&v=3

Zu verwenden für minimale Rechte, vertrauenswürdige Authentifizierung, Verschlüsselung, Sicherheitsprotokollierung, Dokumentation und Tests.

## Prüfliste für aktuelle Aussagen

Bevor eine Aussage als aktuell dargestellt wird:

1. Primärquelle öffnen.
2. Veröffentlichungsstand, Änderung oder Softwareversion bestätigen.
3. Kontext lesen, nicht nur einen Suchtreffer.
4. Exakten Gesetzeswortlaut von Umsetzungsempfehlungen trennen.
5. Abrufdatum erfassen, wenn das Ergebnis dauerhaft in Leitfaden oder Richtlinie eingeht.
6. Ungeklärte branchen- oder fallspezifische Punkte benennen, statt zu raten.
