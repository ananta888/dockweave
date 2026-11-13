# Datenschutz, Löschung und Aufbewahrung

Deutsch · [English](../privacy-and-retention.md)

## Die wichtigste Korrektur

In einer Anwendungsdatenbank nur eine UUID zu verwenden, kann sinnvolle Datenminimierung sein. Der Datensatz wird dadurch nicht anonym.

Kann Keycloak, eine Personalstammtabelle, ein Audit-Archiv oder eine andere verfügbare Quelle die UUID wieder einer natürlichen Person zuordnen, handelt es sich um pseudonyme personenbezogene Daten. Die DSGVO gilt weiterhin.

Ebenso wichtig ist das strenge Architektur-Grundmodell dieses Leitfadens: Keycloak ist das einzige persistente führende System für das zentrale Identitätsprofil mit Name, E-Mail-Adresse, Profilanschrift und Telefonnummer. Anwendungen speichern normalerweise nur `iss` und `sub`. Wird ein Profilfeld in Token, Cache, Log, Journal, Export, Index, Analyse oder Backup verschoben, entsteht ein weiterer Verarbeitungsort. Außerhalb des kontrollierten Keycloak-Datenspeicher- und Backup-Lebenszyklus verletzt eine solche Kopie das Grundmodell „nur in Keycloak“, sofern sie keine dokumentierte fachliche Ausnahme ist.

## Anonym und pseudonym

### Pseudonym

```text
Anwendungsdatensatz: subject_id = 8f53...
Identitätssystem:    8f53... = Max Beispiel
```

Das Anwendungsteam sieht den Namen möglicherweise nicht, die Organisation kann die Verbindung aber herstellen. Das senkt Risiko und Zugriffsmöglichkeiten, hebt den Personenbezug jedoch nicht auf.

### Anonym

Die Verbindung zu einer Person wurde so dauerhaft entfernt, dass eine Identifizierung mit wahrscheinlich eingesetzten Mitteln vernünftigerweise nicht mehr möglich ist. Nur den Namen in einer Tabelle zu entfernen und die Zuordnung anderswo zu behalten, ist keine Anonymisierung.

## Datensparsame Architektur

Backends verwenden `iss` und `sub` als Identitätsreferenz. Veränderliche zentrale Identitätsattribute werden im strengen Grundmodell dauerhaft nur in Keycloak gespeichert. Eine Anwendung darf einen eng begrenzten Wert bei Bedarf verarbeiten, führt aber kein Schattenprofil.

Mögliche Muster:

- einen Claim aus Token oder UserInfo anzeigen, ohne ihn dauerhaft zu speichern;
- einen Anzeigenamen nicht zwischenspeichern; ist ein Cache aus nachgewiesenen Leistungsgründen nötig, gilt er als genehmigte Ausnahme mit kurzer Frist und wirksamer Löschung;
- eine für Bestellung, Rechnung oder Vertrag erforderliche Anschrift im jeweiligen Geschäftsdatensatz bewahren, sie aber nicht als aktuelles Identitätsprofil weiterverwenden;
- für nicht zusammengehörige Clients paarweise Subjektkennungen einsetzen;
- Analysen aggregieren oder wirklich anonymisieren, statt eine globale Personenkennung weiterzugeben.

Die Regel lautet nicht: „Alle personenbezogenen Daten müssen in Keycloak liegen.“ Rechnungen, Bestellungen, Krankenakten oder andere Fachdaten gehören nicht nach Keycloak. Richtig ist: „Zentrale Identitätsattribute werden dauerhaft nur in Keycloak gespeichert; jede fachliche Kopie muss für ihren eigenen Zweck erforderlich und eigenständig geregelt sein.“

Vor jeder weiteren dauerhaften Kopie gilt diese Entscheidungsschranke:

| Frage | Wenn nein |
|---|---|
| Ist das Feld Bestandteil des eigenen Geschäftsdatensatzes statt einer Komfortkopie des Login-Profils? | nicht speichern |
| Sind Zweck und Rechtsgrundlage dokumentiert? | nicht speichern |
| Sind Zugriff, Empfänger, Frist, Einschränkung, Löschung, Backup und Restore umgesetzt? | nicht speichern |
| Kann die Kopie für Auskunft und Löschung gefunden werden? | nicht speichern |
| Hat ein benannter Ausnahmeverantwortlicher zugestimmt und wird regelmäßig geprüft? | nicht speichern |

## Token verbreiten Claims

Ein in ein Access-Token kopierter Claim kann folgende Stellen erreichen:

- Client;
- API-Gateway;
- Backend-Dienste;
- Tracing- und Fehlerwerkzeuge;
- Browser- oder Gerätespeicher;
- Support-Screenshots.

Claims sind zu minimieren, vollständige Token dürfen nicht protokolliert werden und sensible Fachattribute gehören nicht allein deshalb in ein Token, weil ein Mapper dies technisch ermöglicht.

Ein Access-Token wird häufig durch mehrere Infrastrukturschichten kopiert und versehentlich in Telemetrie aufbewahrt. Im strengen Grundmodell bleiben Name, E-Mail-Adresse, Anschrift und Telefonnummer aus Access-Token heraus, sofern nicht genau ein Claim nachweislich erforderlich ist. Ein für den Client bestimmtes ID-Token oder eine UserInfo-Antwort ist keine Erlaubnis, dessen Profilfelder dauerhaft zu speichern.

## Rechtliche Folgen zusätzlicher Kopien

Eine zusätzliche Kopie ist nicht automatisch rechtswidrig, stellt aber eine weitere Verarbeitung oder einen weiteren Verarbeitungsort dar. Der Verantwortliche muss unter anderem nachweisen können:

- festgelegten Zweck und Rechtsgrundlage nach Art. 5 und 6 DSGVO;
- Datenminimierung, Speicherbegrenzung und Rechenschaftspflicht nach Art. 5;
- Datenschutz durch Technikgestaltung und datenschutzfreundliche Voreinstellungen nach Art. 25;
- angemessene Zugriffskontrolle, Vertraulichkeit, Integrität, Verfügbarkeit und Schutz vor unbefugter Veränderung oder Löschung nach Art. 32;
- vollständige Abläufe für Auskunft, Löschung und Einschränkung nach Art. 12, 15, 17 und 18;
- soweit erforderlich die Aufnahme in Verarbeitungsinventar sowie Aufbewahrungs- und Löschsteuerung.

Bleibt ein Profilwert in Log, Journal, Suchindex, Export, Analyseplattform oder wiederhergestelltem Backup bestehen, obwohl er zu löschen wäre, ist die Änderung oder Löschung allein in Keycloak keine vollständige Löschung. „Unveränderlich“, „Audit“, „Debug“ und „Backup“ sind technische Bezeichnungen und keine pauschalen Ausnahmen von Zweckbindung und Speicherbegrenzung. Eine Rechtspflicht oder ein Legal Hold kann nur die erforderlichen Kategorien für den erforderlichen Zeitraum und Zweck rechtfertigen.

Je nach Sachverhalt können Fehler zu aufsichtsrechtlichen Abhilfebefugnissen wie Anordnung von Einschränkung, Löschung oder Verarbeitungsstopp sowie zu Geldbußen nach Art. 58 und 83 DSGVO führen. Betroffene können bei materiellem oder immateriellem Schaden nach Art. 82 Ersatz verlangen. Sind unkontrollierte Kopien Teil einer Verletzung des Schutzes personenbezogener Daten, können Meldungen an Aufsichtsbehörde oder Betroffene nach Art. 33 und 34 erforderlich werden. Diese Folgen sind einzelfallabhängig; der Leitfaden trifft keine individuelle rechtliche Entscheidung. Die Primärvorschriften sind in den [maßgeblichen Quellen](quellen.md#europäisches-datenschutzrecht) verzeichnet.

## Vier unterschiedliche Rechte und Handlungen

| Handlung | Grundwirkung |
|---|---|
| Einwilligung widerrufen | Beendet künftige Verarbeitung auf Grundlage dieser Einwilligung; die vorherige Verarbeitung wird nicht automatisch unrechtmäßig |
| Widerspruch nach Art. 21 DSGVO | Betrifft besonders Verarbeitung im öffentlichen Interesse oder aufgrund berechtigter Interessen; Direktwerbung für diesen Zweck muss enden |
| Löschung nach Art. 17 DSGVO | Verlangt Löschung, wenn ein Grund greift und keine Ausnahme verbleibt |
| Einschränkung nach Art. 18 DSGVO | Daten bleiben gekennzeichnet erhalten, ihre weitere Verarbeitung wird aber stark begrenzt |

Widerruf, Widerspruch, Einschränkung und Löschung sind keine Synonyme.

## Das Löschrecht gilt nicht ausnahmslos

Art. 17 DSGVO nennt Ausnahmen, wenn eine Verarbeitung weiterhin erforderlich ist, darunter:

- Erfüllung einer rechtlichen Verpflichtung;
- Wahrnehmung bestimmter Aufgaben im öffentlichen Interesse;
- Geltendmachung, Ausübung oder Verteidigung von Rechtsansprüchen;
- bestimmte Zwecke aus öffentlicher Gesundheit, Forschung oder Archivierung unter den jeweiligen Voraussetzungen.

Die Ausnahme gilt nur für die notwendigen Daten und den konkreten Zweck. Sie erlaubt nicht, ein vollständiges aktives Profil sowie sämtliche Komfort- und Marketingdaten weiterzuführen.

## Keine pauschale Zehnjahresfrist

Nach dem Stand 2026 enthalten deutsche handels- und steuerrechtliche Regeln beispielsweise:

- zehn Jahre für Bücher, Inventare, Eröffnungsbilanzen, Jahresabschlüsse, Lageberichte und die zu ihrem Verständnis erforderlichen Organisationsunterlagen;
- acht Jahre für gewöhnliche Buchungsbelege;
- sechs Jahre für viele Handels- und Geschäftsbriefe sowie sonstige steuerlich relevante Unterlagen;
- zehn Jahre für Buchungsbelege bestimmter regulierter Finanz- und Versicherungsunternehmen.

Die Frist beginnt häufig mit Ablauf des Kalenderjahres, in dem die betreffende Unterlage, Eintragung oder Handlung entstanden ist. Branchenrecht, Verjährung, laufende Prüfungen oder ein Legal Hold können das Ergebnis verändern.

Diese Liste darf nicht ungeprüft in eine konkrete Richtlinie kopiert werden. Aktuelle Gesetzesfassung, Organisationsart, Dokumentfunktion und Übergangsvorschriften sind jeweils zu prüfen.

## Aufbewahrungsmatrix

Statt einer globalen Einstellung wird eine Matrix geführt:

| Kategorie | Zweck und Grundlage | Fristbeginn | Dauer | Speicher | Zulässiger Zugriff | Endhandlung |
|---|---|---|---:|---|---|---|
| Aktives Keycloak-Konto | Identität und Vertragsdurchführung | Vertragsende | richtlinienspezifisch | Keycloak | IAM | sperren/löschen |
| Jahresabschluss | Handels-/Steuerrecht | Jahresende | prüfen, häufig 10 Jahre | Archiv | Finanzen/Audit | nach Frist löschen |
| Buchungsbeleg | Handels-/Steuerrecht | Jahresende | prüfen, häufig 8 Jahre | Archiv | Finanzen/Audit | nach Frist löschen |
| Geschäftsbrief | Handels-/Steuerrecht | Jahresende | prüfen, häufig 6 Jahre | Archiv | begrenzte Fachrolle | nach Frist löschen |
| Login-Sicherheitsereignis | Sicherheit | Ereigniszeit | risikobasiert | SIEM | Sicherheit | rollierend löschen |
| Zugriff auf sensible Daten | Rechenschaft | Ereigniszeit | risiko-/branchenspezifisch | Audit-Speicher | Audit-Rolle | rollierend löschen |
| Rollenfreigabe | Berechtigungssteuerung | Entscheidungszeit | richtlinien-/risikobasiert | Workflow-Archiv | IAM/Audit | nach Frist löschen |

Für jede Zeile wird die konkrete gesetzliche oder interne Regel referenziert.

## Ausgeschiedene Mitarbeiter und historische Zuordnung

### Die tatsächliche Anforderung

Hat ein später ausgeschiedener Mitarbeiter eine Handlung ausgeführt, die nachvollziehbar bleiben muss, kann die Organisation während der jeweiligen Audit- oder Aufbewahrungsfrist eine Auflösung der historischen Akteurskennung benötigen.

Das erfordert kein zehn Jahre lang aktives Keycloak-Konto.

Beim Ausscheiden sind:

1. die Anmeldung sofort zu sperren;
2. Sitzungen, Refresh-Token, Zugangsdaten, Dienstzugriffe und Administratorrollen zu widerrufen;
3. die Person aus aktiven Gruppen und Aufgabenwarteschlangen zu entfernen;
4. nur die zum Verständnis aufbewahrter Ereignisse nötigen historischen Identitätsinformationen zu erhalten;
5. diese Zuordnung von normaler Identitätsverwaltung und Analyse zu isolieren;
6. jeder Zugriff auf die Zuordnung selbst zu protokollieren;
7. die Zuordnung zu löschen, sobald kein verbliebenes Ereignis und kein Rechtszweck sie mehr benötigt.

### Minimale historische Zuordnung

```text
actor_iss
actor_sub
historische_personalnummer
historischer_anzeigename, nur falls nachweislich erforderlich
beschäftigungszeitraum
grund_der_aufbewahrung
delete_after
```

Nicht jedes Feld ist immer nötig. Abteilung, Privatanschrift, aktuelle Kontaktdaten, MFA-Geheimnisse und Kennwort-Hashes sind für das Verständnis einer alten Buchung normalerweise irrelevant.

### Speicherort der Zuordnung

Im strengen Profildaten-Grundmodell sind vorzuziehen:

- ein gesperrter, auf ein Minimum reduzierter Identitäts-Datensatz in Keycloak; oder
- eine zugriffsgeschützte Compliance-Zuordnung nur aus `iss`, `sub`, einer nicht zum Profil gehörenden Personalreferenz, Aufbewahrungsgrund und Löschdatum, während ein erforderlicher historischer Name in Keycloak bleibt.

Muss ein rechtmäßig aufbewahrter Geschäftsdatensatz selbst einen historischen Namen enthalten, ist dies eine dokumentierte fachliche Ausnahme und kein wiederverwendbares Identitätsprofil. Jede Variante benötigt Rechtsgrundlage, Zugriffsbeschränkung, Frist und Löschverfahren.

### Eine UUID ohne Zuordnung genügt nicht

Ein Audit-Ereignis mit lediglich `actor_sub = 8f53...` ist ein schwacher Nachweis, wenn sämtliche Identitätszuordnungen gelöscht wurden. Muss der Akteur zurechenbar bleiben, wird die minimale Zuordnung für die gerechtfertigte Dauer bewahrt.

### Nicht jeder Ersteller ist rechtlich gleich bedeutsam

Zu unterscheiden sind:

- Person, die Daten eingegeben oder importiert hat;
- Person, die sie geprüft hat;
- Person, die sie genehmigt hat;
- rechtlich verantwortliche unterzeichnende Person;
- für ordnungsgemäße Bücher verantwortliche juristische Person.

Beispielsweise kann ein Mitarbeiter Zahlen vorbereiten, während Kaufmann oder gesetzliche Vertreter den Jahresabschluss unterzeichnen. § 245 HGB verlangt die maßgebliche Unterschrift und das Datum. Daher muss nicht automatisch die vollständige Identität jedes Beteiligten über die gesamte Dokumentfrist aufbewahrt werden. Entscheidend ist, ob seine Zuordnung für Audit-Trail, internes Kontrollsystem oder gesetzliche Zurechnung erforderlich ist.

Die richtige Frage lautet:

> Welche Rolle welcher Person muss verständlich bleiben, um diesen Vorgang rekonstruieren, prüfen, genehmigen oder rechtlich zuordnen zu können?

## Praktischer Löschablauf

1. Identität der anfragenden Person angemessen prüfen.
2. Alle Identitäten der Person einschließlich aller relevanten `iss`-/`sub`-Paare ermitteln.
3. Betroffene Systeme anhand Verarbeitungsverzeichnis und Dateninventar finden.
4. Nicht nur Datenbanken, sondern auch Token-Telemetrie, Keycloak-Ereignisrepräsentationen, Betriebs- und Sicherheitslogs, Journale, Traces, Browser-/Server-Caches, Suchindizes, Supportsysteme, Exporte, Analysen, Archive und Backups durchsuchen.
5. Für jede Datenkategorie Zweck, Rechtsgrundlage, Frist und Legal Hold bestimmen.
6. Bei Beendigung der Beziehung Anmeldung sperren und aktive Sitzungen widerrufen.
7. Daten ohne verbleibenden Zweck oder Rechtsgrund löschen oder wirklich anonymisieren.
8. Aufzubewahrende Datensätze einschränken und isolieren.
9. Suchindizes, Caches, Exporte, Data-Lake-Kopien und Analysedaten entfernen oder anpassen.
10. Backups nach dokumentierter Rotation auslaufen lassen und verhindern, dass gelöschte Daten bei einer Wiederherstellung unbemerkt zurückkehren.
11. Ein Löschjournal führen, das nur die zur erneuten Durchsetzung der Löschung notwendigen Angaben enthält.
12. Abschluss und begründete Ausnahmen dokumentieren.
13. Die betroffene Person über Ergebnis und verbleibende Datenkategorien informieren.

Das Löschen des Keycloak-Benutzers ist nur eine Aufgabe dieses Ablaufs, nicht der gesamte Ablauf.

## Backups

Eine sofortige Einzellöschung aus jedem unveränderlichen Backup kann technisch unpraktikabel sein. Ein tragfähiger Entwurf umfasst normalerweise:

- feste und begrenzte Backup-Aufbewahrung;
- strengen Zugriff ausschließlich zur Wiederherstellung;
- keine normale Abfrage alter Sicherungen;
- Löschjournal oder Wiedereinspielprozess nach Restore;
- Vernichtung abgelaufener Sicherungsmedien;
- dokumentierte Wiederherstellungstests, die zeigen, dass gelöschte Konten nicht unbemerkt wieder aktiv werden.

„Die Daten sind im Backup“ rechtfertigt keine unbegrenzte Speicherung.

## Legal Holds

Ein Legal Hold setzt die Löschung nur für die betroffenen Unterlagen und das konkrete Verfahren oder den Streit aus. Dokumentiert werden:

- anordnende Stelle und Grund;
- betroffene Datenkategorien;
- Beginn;
- Verantwortlicher und Prüftermin;
- Aufhebungsereignis;
- Löschhandlung nach Aufhebung.

Ein Legal Hold darf nicht zu einem undokumentierten Dauerkennzeichen werden.

## Recht auf Auskunft über Zugriffe

Sind Zugriffsprotokolle vorhanden, kann ein Auskunftsersuchen Angaben über Zeitpunkt und Zweck von Abfragen erfordern. Namen von Beschäftigten müssen nicht automatisch in jedem Fall offengelegt werden. Intern sollte der Akteur bei berechtigtem Bedarf trotzdem identifiziert werden können.

Daraus entstehen zwei Sichten:

- **interne Audit-Sicht:** auflösbarer Akteur unter strenger Zugriffskontrolle;
- **Auskunft an die betroffene Person:** Zeit, Zweck, Empfänger und weitere erforderliche Informationen; die Beschäftigtenidentität wird anhand der einschlägigen rechtlichen Abwägung beurteilt.

## Typische Fehler

- Keycloak wird zuerst gelöscht und damit die einzige Zuordnung zerstört, die zum Auffinden von Anwendungsdaten nötig wäre.
- Jedes Konto bleibt dauerhaft erhalten, weil einzelne Belege zehn Jahre aufzubewahren sind.
- Ein Name wird entfernt, während eine leicht erreichbare Zuordnung bleibt, und das Ergebnis wird anonym genannt.
- Die Produktionsdatenbank wird bereinigt, durchsuchbare Warehouse- und Exportkopien bleiben bestehen.
- Ein Restore aktiviert einen gelöschten Benutzer erneut.
- Vollständige HTTP-Logs werden so lange wie ein Jahresabschluss behalten, ohne den eigenen Bedarf zu prüfen.
- Gesetzlich aufbewahrte Rechnungsdaten werden später für Werbung genutzt.
- Die vollständige Personalakte eines Erstellers bleibt erhalten, obwohl Personalnummer und historischer Name genügen würden.
- Es wird behauptet, Profildaten lägen nur in Keycloak, obwohl Name oder E-Mail-Adresse in Token, Ereignisrepräsentationen, Logs, Traces, Supportexporten oder wiederhergestellten Backups stehen.
- Ein Audit-Journal wird unveränderlich gemacht, ohne kategoriespezifischen Ablauf, Löschung, Einschränkung und Aufhebung eines Legal Hold umzusetzen.

## Abnahmetests

1. Eine Person kann über kontrollierte Zuordnungen in allen betroffenen Systemen gefunden werden.
2. Eine Löschung entfernt nicht mehr benötigte Profil- und Anwendungsdaten.
3. Erforderliche Finanzunterlagen bleiben isoliert und lesbar.
4. Aufbewahrte Unterlagen können nicht von der Marketinganwendung genutzt werden.
5. Ein ausgeschiedener Mitarbeiter kann sich nicht mehr anmelden.
6. Erforderliche historische Handlungen bleiben der ausgeschiedenen Person zurechenbar.
7. Der Zugriff auf die historische Zuordnung wird selbst auditiert.
8. Die Zuordnung wird nach Ablauf des letzten begründeten Datensatzes gelöscht.
9. Ein Restore setzt Lösch- und Widerrufszustände erneut durch.
10. Paarweise Subjektkennungen verhindern nicht, dass der Löschprozess sämtliche zugehörigen Datensätze findet.
11. Im normalen Anwendungsbestand gibt es außerhalb Keycloaks keinen persistenten Namen, keine E-Mail-Adresse, keine Profilanschrift und keine Telefonnummer.
12. Jede genehmigte fachliche Ausnahme weist Zweck, Rechtsgrundlage, Verantwortlichen, Zugriff, Aufbewahrung, Löschung, Einschränkung, Backup, Restore und Behandlung von Betroffenenanfragen nach.
13. Suchen in Logs und Journalen finden weder rohe Token noch ungenehmigte Profilattribute; eingesäte Ausnahmedaten verschwinden bei Fristablauf aus aktivem Speicher und Indizes.
