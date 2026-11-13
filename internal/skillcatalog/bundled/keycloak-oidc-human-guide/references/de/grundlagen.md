# Grundlagen: Keycloak und OpenID Connect

Deutsch · [English](../foundations.md)

## Erste sichere Erklärung

1. **Das Identitätsprofil bleibt an einer Stelle.** Im strengen Grundmodell ist Keycloak das einzige persistente führende System für Name, E-Mail-Adresse, Profilanschrift und Telefonnummer.
2. **Anwendungen verwenden eine Referenz.** Sie speichern normalerweise `iss` und `sub`, aber kein zweites Profil. Erhalten sie einen Profilwert zur Anzeige, dürfen sie ihn nicht unbemerkt dauerhaft speichern, zwischenspeichern, exportieren oder protokollieren.
3. **Audit findet an beiden Grenzen statt.** Keycloak protokolliert Anmeldung und Administration; die Anwendung protokolliert Zugriffe auf ihre sensiblen Fachdaten mit Referenzen statt Namen, Anschriften oder rohen Token.
4. **Ein Berechtigungsoperator darf kein von ihm kontrolliertes Konto hochstufen.** Das gilt für das eigene, ein anderes und ein neu angelegtes Konto. Kontoanlage, Zugangsdaten-Reset, Zielberechtigung, Freigabe und privilegierte Rollenzuweisung brauchen deshalb getrennte Kontrolle und Negativtests.

„Nur in Keycloak“ bedeutet: Keycloak ist das einzige **persistente führende System** für das zentrale Identitätsprofil. Ein Wert kann trotzdem kurzzeitig in Browser oder Dienst verarbeitet werden. Keycloak-Datenbanksicherungen bleiben Orte personenbezogener Daten innerhalb des kontrollierten Identitätslebenszyklus und benötigen begrenzte Fristen sowie erneute Löschung nach Restore. Eine Liefer- oder Rechnungsanschrift, die Teil eines erforderlichen Geschäftsdatensatzes ist, ist eine eigene dokumentierte Ausnahme und keine Kopie des Login-Profils.

## Das kürzeste brauchbare Bild

Keycloak ist der Empfang eines Gebäudes. Dort wird die Identität geprüft und ein signierter Ausweis ausgegeben. Jeder einzelne Raum muss trotzdem prüfen, ob der Ausweis gültig ist, für diesen Raum bestimmt ist und ob die genannte Person die gewünschte Handlung ausführen darf.

Damit werden vier Fragen getrennt:

| Ebene | Frage | Hauptverantwortung |
|---|---|---|
| Identität | Welches Konto ist das? | Keycloak |
| Authentifizierung | Wie wurde die Person geprüft? | Keycloak und angebundene Identity Provider |
| Autorisierung | Was darf dieses Konto hier tun? | Keycloak liefert Informationen; die Anwendung entscheidet und erzwingt die Entscheidung |
| Audit | Wer tat wann was und mit welchem Ergebnis? | Keycloak und jede beteiligte Anwendung |

Die meisten Architekturfehler entstehen, wenn diese Ebenen vermischt werden.

## OAuth 2.0, OIDC und Keycloak

OAuth 2.0 ist ein Rahmenwerk für delegierten Zugriff. OpenID Connect (OIDC) ergänzt eine standardisierte Identitätsschicht. Keycloak arbeitet dabei als OpenID Provider und kann seinerseits externe Identity Provider anbinden.

OIDC ermöglicht einer Anwendung, verlässlich festzustellen, dass ein Benutzer authentifiziert wurde. Es beantwortet aber nicht automatisch fachliche Berechtigungsfragen wie:

- Darf dieser Mitarbeiter Akte 74291 ansehen?
- Darf diese Führungskraft die eigene Reisekostenabrechnung genehmigen?
- Gehört dieser Dozent zu dem Kurs in dieser URL?

Solche Entscheidungen bleiben Aufgabe der Fachanwendung. Rollen und Scopes aus einem Token sind Eingaben für die Entscheidung; sie setzen die Entscheidung nicht von selbst durch.

## Drei Token, drei Aufgaben

### ID-Token

Das ID-Token ist das Anmeldeergebnis für einen Client. Es enthält Identitätsinformationen und ist für diesen Client bestimmt. Es sollte nicht als allgemeines API-Zugriffstoken verwendet werden.

### Access-Token

Das Access-Token wird einer API vorgelegt. Die API prüft es und kontrolliert anschließend die für den konkreten Vorgang erforderlichen Berechtigungen.

### Refresh-Token

Mit einem Refresh-Token kann der Client neue Token beziehen, ohne die vollständige Anmeldung zu wiederholen. Es lebt meist länger und ist besonders sensibel. Es gehört weder an unbeteiligte APIs noch in Protokolle.

## Empfohlene Anmeldeabläufe

Für interaktive Anmeldungen sollte der Authorization Code Flow mit Proof Key for Code Exchange (PKCE) und der Challenge-Methode `S256` eingesetzt werden.

Bei einer serverseitigen Anwendung oder einem Backend for Frontend (BFF) bleiben Token auf dem Server:

```text
Browser  -> Backend/BFF: Anmeldung starten
Backend  -> Browser:     Redirect-URL mit State und PKCE-Challenge
Browser  -> Keycloak:    Authorization Request
Keycloak -> Browser:     Redirect zum BFF-Callback mit kurzem Code
Browser  -> Backend/BFF: Callback mit Code und State
Backend  -> Keycloak:    Code + Prüfer
Keycloak -> Backend/BFF: Token
Backend  -> Browser:     sichere Anwendungssitzung
```

Bei einer öffentlichen nativen Anwendung oder einem Browser-Client erzeugt der Client den Prüfer und tauscht den Code selbst. Er besitzt kein Secret, das die installierte Anwendung verlässlich authentifizieren könnte. Wenn ein BFF zum Risiko und zur Architektur passt, sollten Token nicht dem Browser-Code ausgesetzt werden.

Öffentliche Clients **müssen** nach RFC 9700 PKCE verwenden; für vertrauliche Clients wird PKCE ebenfalls empfohlen. Zu verwenden sind `S256`, eine exakte Redirect-URI, `state` oder eine gleichwertige Bindung der Anfrage sowie die Prüfung des in der Authorization Response zurückgegebenen Issuers. Resource Owner Password Credentials, in Keycloak Direct Grant genannt, **dürfen nicht** verwendet werden. Der Implicit Flow sollte in neuen Entwürfen nicht verwendet werden.

## Was eine API prüfen muss

Das Base64-Dekodieren eines JSON Web Token zeigt lediglich dessen lesbaren Inhalt. Es beweist nicht, dass Keycloak das Token ausgestellt hat.

Eine API prüft mindestens:

- die Signatur anhand der veröffentlichten JSON Web Keys des Realms;
- den erwarteten Aussteller in `iss`;
- die eigene Zielgruppe in `aud`;
- den Ablaufzeitpunkt `exp` und, falls vorhanden, `nbf`;
- erlaubten Token-Typ und passenden Client-Kontext;
- erforderlichen Scope, Rolle oder sonstige Berechtigungsinformationen;
- fachliche Regeln anhand serverseitiger Daten.

Dafür sollte eine gepflegte OIDC-/JWT-Bibliothek statt selbst geschriebener Kryptografie verwendet werden. Signaturschlüssel dürfen sicher zwischengespeichert werden; Schlüsselwechsel müssen trotzdem funktionieren.

## Stabile Benutzeridentität: `iss` und `sub`

OIDC definiert `sub` als beim jeweiligen Aussteller lokal eindeutige und nicht erneut vergebene Subjektkennung. Die dauerhafte Identität ist daher das Paar:

```text
iss = https://login.example.org/realms/company
sub = 8f53b5c1-...
```

Auch bei derzeit nur einem Realm sollten beide Werte logisch gespeichert werden.

Das Paar ist nur innerhalb dieses Ausstellers stabil. Realm-Umzug, Änderung von Issuer oder Hostname, Austausch des Identity Providers, Import, Restore in einen anderen Realm oder Wechsel zwischen öffentlicher und paarweiser Subjektkennung benötigen eine ausdrückliche Migrationszuordnung. Gleiche `sub`-Werte über verschiedene Issuer hinweg beweisen keine Personenidentität.

Nicht als dauerhafter Schlüssel geeignet sind:

- E-Mail-Adresse,
- Benutzername,
- Anzeigename,
- Telefonnummer.

Diese Werte können sich ändern oder wiedervergeben werden und besitzen keine OIDC-Stabilitätsgarantie.

`sub` ist als undurchsichtige Zeichenfolge zu behandeln. Anwendungen dürfen den Wert nicht zerlegen, UUID-Syntax voraussetzen oder fachliche Informationen daraus ableiten.

## Öffentliche und paarweise Subjektkennungen

Bei einer öffentlichen Subjektkennung erhalten mehrere Clients dieselbe `sub`. Das ist bequem, erleichtert aber die systemübergreifende Verknüpfung einer Person.

Bei einer paarweisen Subjektkennung erhält dieselbe Person je Client-Sektor eine andere `sub`. Keycloak bietet dafür einen SHA-256-Pairwise-Subject-Mapper.

Vor der Einführung sind insbesondere zu klären:

- Welche Clients müssen dieselbe Person erkennen?
- Welche Redirect-Hosts gehören zu einem gemeinsamen Sektor?
- Wie lösen gemeinsam genutzte APIs unterschiedliche Kennungen auf?
- Wie werden Salt und Sektorkonfiguration gesichert?
- Wie werden vorhandene Datensätze migriert?

Paarweise Kennungen erschweren einen Korrelationsweg. Über E-Mail, Name, Telefonnummer, Browsermerkmale oder Fachdaten kann eine Person trotzdem wiedererkannt werden.

## Claims sind Kopien personenbezogener Daten

Wird eine E-Mail-Adresse aus Keycloak in ein signiertes Token kopiert, bleibt sie personenbezogen. Jeder Client, Proxy, Backend-Dienst, jedes Debug-Protokoll und jedes Monitoring-Werkzeug, das dieses Token erhält, verarbeitet möglicherweise diese Claims.

Mit Client Scopes und Protocol Mappern sollten nur die tatsächlich benötigten Claims ausgegeben werden. Optionale Claims gehören nicht in Standard-Scopes. Rollen-Scopes und Token-Zielgruppen sind möglichst eng zu begrenzen.

Im strengen Profildaten-Grundmodell bleiben Name, E-Mail-Adresse, Anschrift und Telefonnummer aus Access-Token heraus, sofern nicht ein dokumentierter Bedarf genau den jeweiligen Claim erfordert. Eine Anzeige bei Bedarf bleibt Verarbeitung und muss geschützt werden, wird dadurch aber nicht zur dauerhaften Speicherung. Vollständige Token dürfen auch zur Fehlersuche nicht protokolliert werden.

## Welche Daten wohin gehören

### Keycloak

Geeignete Daten sind typischerweise:

- Zugangsdaten und Authentifizierungsfaktoren;
- Kontostatus;
- im strengen Grundmodell das einzige persistente führende System für zentrale Identitätsattribute wie Name, E-Mail-Adresse, Profilanschrift und Telefonnummer;
- Gruppen, Organisationen und ausgewählte gemeinsame Rollen;
- Verknüpfungen zu LDAP, Active Directory oder anderen Identity Providern.

### Backend der Anwendung

Geeignete Daten sind typischerweise:

- `iss` und `sub`;
- notwendige Fachdaten wie Bestellungen, Fälle oder Kursteilnahmen;
- objektbezogene Berechtigungsbeziehungen;
- Referenzen auf gesetzlich aufzubewahrende Unterlagen;
- anwendungsspezifische Audit-Ereignisse mit Subjekt- und Objektreferenzen statt Profilattributen;
- eine Liefer-, Rechnungs- oder Vertragsanschrift nur dann, wenn sie wirklich Teil des Geschäftsdatensatzes ist und eigener Zweck, Rechtsgrundlage, Zugriff, Aufbewahrung und Löschung dokumentiert sind.

Das Backend kann einen eng begrenzten Profilwert bei Bedarf zur Anzeige abrufen. Standardmäßig darf es ihn nicht in Datenbank, Session Store, verteilten Cache, Suchindex, Trace, Analyse, Fehlerbericht oder Audit-Journal schreiben. Browser- und Proxy-Speicher gehören in die Datenflussprüfung.

### Was nicht Aufgabe von Keycloak ist

Keycloak sollte nicht zur Datenbank für folgende Inhalte gemacht werden:

- Rechnungen und Bestellungen;
- Personal- oder Krankenakten;
- Support-Kommunikation;
- Lernfortschritt;
- vollständige Vertragsverläufe.

Zentrale Identitätsverwaltung ist sinnvoll. Keycloak ist im strengen Grundmodell dieses Leitfadens das einzige persistente führende System für das **zentrale Identitätsprofil**, aber keine Datenbank für sämtliche Personen- und Fachdaten. Notwendige Ausnahmen gehören zum jeweiligen Geschäftsdatensatz und dürfen nicht zu einem unkontrollierten Schattenprofil werden.

## Zugriff zwischen Diensten

Ist kein Mensch beteiligt, sollte ein eigener vertraulicher Client mit Service Account verwendet werden. Das Subjekt repräsentiert dann den Dienst und nicht eine Person.

Pro Dienst oder wesentlicher Vertrauensgrenze ist ein eigener Client sinnvoll. Er erhält nur benötigte Rollen und eine enge Audience. Secrets sind zu wechseln oder durch stärkere Client-Authentifizierung zu ersetzen. Im Audit wird die Dienstidentität als Akteur gespeichert.

Ein Benutzertoken sollte nicht nach Ende der Sitzung für Hintergrundaufgaben weiterverwendet werden, sofern das Delegationsmodell dies nicht ausdrücklich vorsieht und nachvollziehbar protokolliert.

## Abmeldung, Sperrung und Token-Lebensdauer

Ein lokal validiertes Access-Token kann bis zu seinem Ablauf gültig bleiben. Das Sperren eines Benutzers in Keycloak erreicht nicht augenblicklich jede offline prüfende API.

Je nach Risiko sind zu kombinieren:

- kurze Laufzeit von Access-Token;
- Widerruf von Refresh-Token und Sitzungen;
- Backchannel Logout;
- Token-Introspektion bei besonders riskanten Vorgängen;
- lokale Sperr- oder Kontostatusprüfung, wenn sofortige Ablehnung zwingend ist.

## Mindeststandard für den Produktivbetrieb

- Keycloak im Produktionsmodus und ausschließlich über HTTPS betreiben.
- Festen korrekten Hostnamen und vertrauenswürdiges Proxy-Verhalten konfigurieren.
- Redirect-URIs exakt und Web Origins bewusst festlegen.
- Wirksame Default und Optional Client Scopes prüfen; anhand echter Token bestätigen, dass API-Token keine ungenehmigten Profil-Claims enthalten.
- Management-Port, Health- und Metrik-Endpunkte intern halten.
- Admin Console und Admin API auf Administrationsnetz oder Gateway begrenzen.
- Starke Mehrfaktorauthentifizierung oder Passkeys für Administratoren verlangen.
- Normale und privilegierte Konten trennen.
- Realm-Konfiguration, Schlüssel, Datenbank und Pairwise-Subject-Geheimnisse sichern.
- Wiederherstellung und Schlüsselwechsel praktisch testen.

## Verständnisfragen für Schulungen

Die Grundlagen sind verstanden, wenn jemand erklären kann, warum:

1. ein ID-Token kein universelles API-Token ist;
2. JWT-Dekodierung keine Validierung ist;
3. eine E-Mail-Adresse ein schlechter Fremdschlüssel ist;
4. `sub` allein nicht global eindeutig ist;
5. eine Token-Rolle weiterhin im Backend durchgesetzt werden muss;
6. das Löschen eines Keycloak-Kontos keine Bestelldatenbank löscht;
7. eine UUID weiterhin personenbezogen sein kann;
8. die Anzeige eines E-Mail-Claims auch ohne Speicherung eine Verarbeitung ist;
9. eine Lieferanschrift in einer Rechnung einen anderen Zweck als die aktuelle Profilanschrift hat;
10. ein neu angelegtes Konto auch bei gesperrter direkter Selbstzuweisung ein Eskalationsweg sein kann.
