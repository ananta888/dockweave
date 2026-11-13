# Auditierbarkeit und Journaling

Deutsch · [English](../audit-and-journaling.md)

## Die Begriffe in je einem Satz

- Ein **Betriebsprotokoll** unterstützt Betrieb und Fehlersuche.
- Ein **Sicherheitsprotokoll** hilft, Angriffe und Regelverstöße zu erkennen.
- Ein **Audit-Log** weist nach, wer eine relevante Handlung ausgeführt hat und was dabei geschah.
- Eine **Zugriffshistorie** hält fest, dass geschützte Daten gelesen, exportiert oder anderweitig abgerufen wurden.
- Ein **Fachjournal** bewahrt die geordnete Abfolge fachlicher Ereignisse und Korrekturen.

Technisch können dieselben Transport- und Speichersysteme verwendet werden. Zweck, Leser, Aufbewahrungsfrist und Inhalt unterscheiden sich jedoch.

## Warum normale Logs nicht ausreichen

Eine übliche Log-Zeile ist für die Fehlersuche nützlich, aber als Nachweis schwach:

```text
Datensatz erfolgreich geladen
```

Sie beantwortet nicht:

- Wer hat gehandelt?
- Welcher Datensatz war betroffen?
- War der Zugriff erlaubt oder abgelehnt?
- Zu welchem Zweck erfolgte der Zugriff?
- Welcher Auftrag oder Workflow löste ihn aus?
- Wurde der Eintrag später verändert oder gelöscht?

Auditierbarkeit muss daher vorab entworfen und darf nicht erst nach einem Vorfall aus zufälligen Logs rekonstruiert werden.

## Die Audit-Grenze von Keycloak

Keycloak bietet zwei wesentliche Ereignisgruppen.

### Benutzerereignisse

Dazu gehören beispielsweise Anmeldung, fehlgeschlagene Anmeldung, Abmeldung, Registrierung, Code-zu-Token-Tausch, Token-Aktualisierung, Kennwortänderung und Kontoänderung.

Keycloak speichert nicht automatisch alle Benutzerereignisse für die Anzeige in der Admin Console. Unter **Realm settings → Events → User events settings → Save events** muss die Speicherung aktiviert und eine bewusste Ablaufzeit festgelegt werden. Ausgewählt werden nur die tatsächlich benötigten Ereignistypen.

### Administratorereignisse

Änderungen aus der Admin Console laufen über die Admin REST API und können als Admin Events erfasst werden. Dafür wird unter **Realm settings → Events → Admin events settings → Save events** die Speicherung aktiviert.

`Include Representation` ergänzt die übermittelte JSON-Repräsentation. Das kann bei der Rekonstruktion helfen, erhöht aber Datenmenge und Anteil personenbezogener oder vertraulicher Inhalte erheblich. Im strengen Profildaten-Grundmodell bleibt die Option bei Profilvorgängen deaktiviert; jede engere Ausnahme benötigt eine Prüfung von Feldern, Speicherort, Zugriff, Frist, Löschung und Restore. Sie liefert keinen garantiert vollständigen Vorher-Nachher-Vergleich.

### Was Keycloak nicht sehen kann

Keycloak weiß normalerweise nicht, dass eine Fachanwendung:

- Kundenakte 74291 geöffnet hat;
- 500 Mitarbeiterdatensätze exportiert hat;
- einen medizinischen Befund angezeigt hat;
- den Status einer Rechnung geändert hat;
- mit personenbezogenen Daten einen Score berechnet hat.

Solche Ereignisse muss die Fachanwendung oder ein gemeinsam genutztes Audit-Gateway erzeugen.

## Empfohlenes Audit-Ereignis einer Anwendung

```json
{
  "schema_version": 1,
  "event_id": "01J6A...",
  "occurred_at": "2026-08-27T10:15:22.431Z",
  "actor_iss": "https://login.example.org/realms/company",
  "actor_sub": "8f53b5c1-...",
  "actor_type": "human",
  "client_id": "case-management-ui",
  "service": "case-management-api",
  "action": "READ_CASE",
  "target_type": "case",
  "target_id": "case-74291",
  "purpose_ref": "support-ticket-1834",
  "decision": "ALLOW",
  "correlation_id": "req-91b..."
}
```

Weitere Felder sind nur sinnvoll, wenn sie den Untersuchungs- oder Nachweiswert tatsächlich erhöhen. Möglich sind etwa Mandant, wirksame Rolle, Genehmigungs-ID, Rechtsgrundlagen-Code, Anzahl exportierter Zeilen oder Begründung eines Notfallzugriffs.

Nicht in das Ereignis gehören:

- Kennwörter und Client-Secrets;
- Access-, ID- oder Refresh-Token;
- vollständige Request- oder Response-Inhalte;
- Name, E-Mail-Adresse, Anschrift oder Telefonnummer, wenn eine Subjektreferenz genügt;
- unstrukturierte Begründungen, wenn ein kontrollierter Code möglich ist;
- sensible Inhalte des betroffenen Fachdatenobjekts.

## Profildaten in Logs und Journalen

Im strengen Grundmodell verwenden Audit-Datensätze `actor_iss` und `actor_sub`, eine Zielreferenz und kontrollierte Zweckcodes. Sie duplizieren nicht das zentrale Keycloak-Profil. Das gilt für Anwendungslogs, Keycloak Event Listener, Reverse Proxies, API-Gateways, verteiltes Tracing, SIEM, Support-Bundles, Analysen, Exporte, Screenshots und Backups.

Gelangt ein Name, eine E-Mail-Adresse, eine Anschrift, eine Telefonnummer, ein rohes Token oder eine vollständige Keycloak-Admin-REST-Repräsentation in eines dieser Systeme, entsteht ein weiterer Ort der Verarbeitung personenbezogener Daten. Zu erfassen sind Zweck, Rechtsgrundlage, Empfänger, Zugriffsrechte, Schutz, Frist, Verhalten bei Suche und Auskunft, Löschung oder Einschränkung, Backup-Verhalten und verantwortliche Stelle. Die Bezeichnung „Debug“ oder „Audit“ ist keine Rechtsgrundlage; ein unveränderliches Journal rechtfertigt keine unbegrenzte Speicherung.

Keycloaks Option `Include Representation` erfordert besondere Vorsicht, weil übermittelte Benutzerrepräsentationen vollständige Profilattribute enthalten können. Im strengen Grundmodell darf sie bei Profilvorgängen nicht allein für eine bequeme Änderungshistorie aktiviert werden. Vorzuziehen ist ein eigens entworfener Listener oder eine Pipeline, die ein freigegebenes minimales Änderungsereignis erzeugt, ohne zuvor die vollständige Repräsentation zu speichern. Ist die vollständige Repräsentation nachweislich unverzichtbar, gilt sie als dokumentierte Ausnahme; Zugriff, Größenbegrenzung, Ablauf, Löschung und Restore-Verhalten müssen getestet werden.

Wird eine Altkopie in Protokollen gefunden:

1. Erzeugung neuer Kopien beenden;
2. nur aufbewahren, was ein dokumentierter Vorfall, eine Rechtspflicht oder ein Legal Hold erfordert;
3. Zugriff begrenzen und Replikate in Indizes, Archiven, Exporten und Backups finden;
4. den Rest nach dem freigegebenen Lebenszyklus löschen oder unumkehrbar anonymisieren;
5. die Bereinigung nachweisen, ohne den Profilwert erneut in den Löschungsnachweis zu kopieren.

Werden solche Kopien nicht beherrscht oder ordnungsgemäß gelöscht, können DSGVO-Verstöße sowie aufsichtsrechtliche Maßnahmen, Schadensersatz- oder Meldepflichtfolgen entstehen. Welche Folge eintritt, hängt vom Einzelfall ab; siehe [Datenschutz, Löschung und Aufbewahrung](datenschutz-und-aufbewahrung.md#rechtliche-folgen-zusätzlicher-kopien).

## Akteur, Ziel und betroffene Person unterscheiden

Ein Ereignis kann mehrere Personen betreffen:

- **Akteur:** Mitarbeiter oder Dienst, der handelt;
- **Eigentümer des Zielobjekts:** Person, deren Daten abgerufen werden;
- **Genehmiger:** Person, die eine Ausnahme freigegeben hat;
- **Antragsteller:** Person, die den Workflow ausgelöst hat.

Ein einziges Feld `user_id` darf nicht für alle Rollen zugleich stehen. Die Rollen werden bei Bedarf ausdrücklich modelliert.

## Lesezugriffe und Änderungen

### Sensible Lesezugriffe

Ein Lesezugriff sollte protokolliert werden, wenn bereits die Tatsache des Abrufs bedeutsam ist, etwa bei Kranken-, Personal- oder Bankdaten, großen Exporten oder ungewöhnlichem Administrationszugriff.

Nicht jeder Seitenaufruf muss automatisch für immer gespeichert werden. Für Datenklassen und Vorgänge wird dokumentiert, welche Zugriffe aus welchem Grund auditpflichtig sind.

### Änderungen

Ein brauchbares Änderungsereignis enthält:

- Akteur und Zeitpunkt;
- Ziel und Operation;
- alten und neuen Zustand oder einen sicheren Unterschied, sofern nötig;
- Workflow-, Genehmigungs- oder Begründungsreferenz;
- Erfolg oder Fehler;
- Korrelation mit Anwendungs- und Infrastruktur-Traces.

Vollständige sensible Objekte gehören nicht allein zur Historienbildung in das Audit-Log. Besser sind ein genehmigter minimaler Unterschied, ein kryptografischer Digest oder eine Referenz auf einen separat geschützten Journaleintrag.

## Verhalten eines Fachjournals

Ein Journal ist fachlich nur ergänzbar:

```text
10:00 Rechnung 17 gebucht
10:15 Rechnung 17 storniert, Grund: Duplikat
10:16 Korrigierte Rechnung 18 gebucht
```

Der erste Vorgang wird nicht überschrieben, als hätte er nie existiert. Eine Korrektur ist ein neues Ereignis mit Bezug auf den ursprünglichen Vorgang.

Bei buchführungsnahen Systemen muss für die erforderliche Dauer die Verbindung vom Ursprungsbeleg über Buchungssatz, Journal und Konto bis zur Auswertung nachvollziehbar bleiben. Historische System- und Verfahrensinformationen, die zum Verständnis nötig sind, müssen ebenfalls erhalten werden.

## Schutz gegen Manipulation

Der lokale Ereignisspeicher von Keycloak ist nützlich, aber kein vollständiges unveränderliches Archiv. Ein Administrator mit Ereignisrechten kann Einträge löschen; ein Datenbankadministrator hat noch tieferen Zugriff.

Für wichtige Nachweise sollte man:

- Ereignisse in ein getrennt administriertes System übertragen;
- Append-only- oder unveränderliche Aufbewahrungsmechanismen nutzen;
- Schreib-, Lese-, Administrations- und Löschrechte trennen;
- authentifizierten Transport verwenden;
- verlässliche UTC-Zeitstempel und Zeitsynchronisation sicherstellen;
- Sequenzlücken, Übertragungsfehler und Zeitabweichungen überwachen;
- bei hohem Schutzniveau Hash-Ketten, Signaturen oder WORM-Speicher erwägen;
- Zugriffe auf den Audit-Speicher selbst protokollieren;
- verhindern, dass die auditierte Administration sämtliche Kopien kontrolliert.

Unveränderlichkeit bedeutet nicht unbegrenzte Aufbewahrung. Nach Fristablauf sind Daten gemäß Richtlinie zu löschen, sofern kein dokumentierter Legal Hold greift.

## Korrelation ohne Token-Leck

Am Systemrand wird eine Request-Korrelations-ID erzeugt und über vertrauenswürdige Header oder einen Tracing-Kontext weitergegeben. Sie kann in Keycloak-nahen, fachlichen, Audit- und Infrastrukturereignissen stehen.

Das rohe Access-Token darf nicht als Korrelationswert dienen. Wird wirklich ein tokenbezogener Fingerabdruck benötigt, muss er einseitig und zweckgebunden abgeleitet und sein Risiko dokumentiert werden.

## Zweck und Begründung

Bei normalen Vorgängen ergibt sich der Zweck häufig aus dem Workflow. Wer einen zugewiesenen Fall öffnet, muss nicht jedes Mal Freitext eingeben.

Eine ausdrückliche Zweck- oder Ticketangabe ist insbesondere sinnvoll für:

- Break-Glass-Zugriff;
- Zugriff außerhalb normaler Zuständigkeit;
- Massenexport;
- Support-Impersonation;
- privilegierte personübergreifende Suche;
- Übermittlung an externe Empfänger.

Kontrollierte Zweckcodes mit Workflow-Referenz sind freiem Text vorzuziehen.

## Auskunft gegenüber betroffenen Personen

Der Gerichtshof der Europäischen Union hat in der Rechtssache C-579/21 entschieden, dass Informationen über Zeitpunkt und Zweck von Abfragen personenbezogener Daten vom Auskunftsrecht erfasst sein können. Die Identität von Beschäftigten wird nicht allein deshalb offengelegt, weil sie unter Verantwortung des Verantwortlichen eine Abfrage durchgeführt haben. In Ausnahmefällen kann eine Offenlegung erforderlich sein, wenn sie für die Rechte der betroffenen Person nötig ist und gegen die Rechte der Beschäftigten abgewogen wurde.

Technische Konsequenzen:

- Intern müssen Akteure bei berechtigtem Bedarf auflösbar bleiben.
- Zeitpunkt und Zweck müssen berichtsfähig sein.
- Es darf nicht zugesagt werden, dass jeder Beschäftigtenname stets offengelegt wird.
- Abfrage- und Schwärzungsprozess sollten vor dem ersten Auskunftsersuchen feststehen.

Das Urteil verlangt nicht pauschal, jeden Lesezugriff jedes risikoarmen Systems unbegrenzt zu speichern. Gesetz, Risiko, Branchenregeln und Sensibilität bestimmen den Umfang.

## Aufbewahrung von Protokollen

Getrennte Fristen sind festzulegen für:

- Anmelde- und Sicherheitsereignisse;
- administrative Änderungen;
- Zugriffe auf sensible Datensätze;
- Fachjournaleinträge;
- Infrastruktur-Traces;
- Genehmigungsnachweise.

Die Frist eines Geschäftsbelegs macht nicht automatisch jedes Feld eines Debug-Logs für dieselbe Dauer erforderlich.

## Betriebliche Fehlerfälle

Zu testen ist das Verhalten, wenn:

- das Audit-Ziel nicht erreichbar ist;
- eine Nachricht doppelt eintrifft;
- Ereignisse in falscher Reihenfolge eintreffen;
- die Zuordnung eines ausgeschiedenen Akteurs in aktivem Keycloak nicht mehr vorhanden ist;
- die Systemzeit falsch ist;
- ein Benutzer mehrere Sitzungen parallel hat;
- ein Administrator Ereignisse zu löschen versucht;
- nach einer Löschung ein Backup zurückgespielt wird;
- ein Ereignis größer als die Empfängergrenze ist.

Risikobasiert ist festzulegen, ob ein sensibler Vorgang bei fehlgeschlagener Audit-Übertragung abgelehnt wird oder mit einer dauerhaften lokalen Warteschlange und dringendem Alarm weiterlaufen darf.

## Abnahmetests

1. Erfolgreiche und fehlgeschlagene Anmeldungen erscheinen mit richtigem Realm- und Client-Kontext.
2. Eine administrative Rollenzuweisung erzeugt ein Keycloak Admin Event.
3. Das Lesen eines geschützten Fachdatenobjekts erzeugt ein Audit-Ereignis der Anwendung.
4. Ein abgelehnter Abruf wird ohne Preisgabe des Datensatzinhalts protokolliert.
5. Vollständige Token und Secrets erscheinen niemals im Audit-Inhalt.
6. Korrelations-IDs verbinden die zusammengehörigen Ereignisse.
7. Der delegierte Administrator kann das externe Ereignis nicht löschen.
8. Die historische Akteursreferenz eines ausgeschiedenen Mitarbeiters bleibt nur während der genehmigten Dauer auflösbar.
9. Abgelaufene Ereignisse werden gelöscht; ein Legal Hold verhindert nur die konkret begründete Löschung.
10. Ein Ausfall des Audit-Ziels löst das vorgesehene Warteschlangen-, Ablehnungs- und Alarmverhalten aus.
11. Name, E-Mail-Adresse, Anschrift, Telefonnummer und rohe Token fehlen standardmäßig in Logs, Traces, Journalen, Ereignisrepräsentationen und Support-Bundles.
12. Ein genehmigtes Ausnahmefeld kann in Primärspeicher, Indizes, Archiven und Restore-Tests gefunden, eingeschränkt, exportiert, fristgerecht entfernt und gelöscht werden.
