# Delegierte Administration und Funktionstrennung

Deutsch · [English](../delegated-administration.md)

## Ziel

Eine Person, die Konten pflegt oder genehmigte Rollenzuweisungen technisch ausführt, soll nicht automatisch in der Lage sein:

- sich das vergebene Fachrecht selbst zu verschaffen;
- administrative Rechte zu vergeben;
- andere Administratoren zu verändern;
- indirekt einer privilegierten Gruppe beizutreten;
- eine scheinbar harmlose Rolle durch Änderung ihrer Definition mächtig zu machen;
- einen privilegierten Benutzer zu impersonieren;
- Nachweise über die eigene Handlung zu löschen.

Dies verbindet minimale Rechte, Kenntnis nur bei Bedarf, Funktionstrennung und unabhängige Auditierung.

Auf diese Kontrollen darf nur mit einer vollständig gepatchten Keycloak-Version vertraut werden. Am Prüftag 2026-08-28 war Keycloak 26.7.2 aktuell und enthielt Korrekturen für Umgehungen der Fine-Grained Admin Permissions sowie Informationspreisgaben. Vor Umsetzung sind [maßgebliche Quellen](quellen.md#keycloak) und die tatsächlich eingesetzte Version erneut zu prüfen; Konfiguration gleicht eine bekannte Autorisierungsumgehung nicht aus.

## Zu trennende Rollen

| Funktion | Typische Fähigkeit | Sollte nicht zugleich kontrollieren |
|---|---|---|
| Benutzeradministrator | Profil- und Zugangsdatenpflege | Rollenfreigabe oder privilegierte Rollenzuweisung |
| Kontoprovisionierer | Konten aus einer maßgeblichen Quelle anlegen und sperren | privilegierte Zieleignung oder Rollenzuweisung |
| Berechtigungsoperator | genehmigte Zuweisungen ausführen | eigenen Antrag genehmigen oder Rollen definieren |
| Eignungsverantwortlicher | bestimmen, wer eine privilegierte Rolle erhalten darf | Zuweisung ausführen oder Operatoren verwalten |
| Berechtigungsfreigeber | fachliche Notwendigkeit entscheiden | technische Zuweisung |
| Rollen-/Sicherheitsadministrator | Rollen und Richtlinien definieren | normale Kontopflege |
| Anwendungsverantwortlicher | fachlichen Anwendungsbedarf festlegen | Keycloak-Plattformadministration |
| Audit-Leser | Nachweise prüfen | Identitäten oder Protokolle verändern |
| Plattformadministrator | Keycloak betreiben und aktualisieren | tägliche Facharbeit mit Superuser-Zugang |
| Break-Glass-Verantwortlicher | Notfallzugriff verwahren | Routineadministration |

In kleinen Organisationen kann eine Person mehrere Funktionen innehaben. Kritische Kombinationen müssen dann erkannt, begründet, überwacht und durch andere Kontrollen ausgeglichen werden. Getrennte Konten und stärkere Authentifizierung für privilegierte Tätigkeiten bleiben sinnvoll.

## Grobe Keycloak-Administrationsrollen

Jeder Realm enthält einen Client `realm-management` mit Rollen wie:

- `realm-admin`;
- `manage-users` und `view-users`;
- `manage-clients` und `view-clients`;
- `manage-realm`;
- `manage-events` und `view-events`;
- `manage-identity-providers`;
- `manage-authorization`;
- `impersonation`.

Diese Rollen sind bequem, aber weitreichend. Laut aktueller Keycloak-Dokumentation erlaubt `manage-users` unter anderem breite Benutzerverwaltung, Rollenzuordnung und Gruppenmitgliedschaft. Für einen eng begrenzten Helpdesk-Administrator ist das normalerweise zu viel.

Keycloak besitzt eine Schutzregel, nach der ein Administrator mit `manage-users` administrative Rollen nur zuweisen kann, wenn er diese selbst besitzt. Diese einzelne Regel verhindert jedoch nicht alle Eskalationswege über Fachrollen, Gruppen, zusammengesetzte Rollen oder Client-Konfiguration.

## Fine-Grained Admin Permissions

Die aktuell unterstützten feingranularen Administrationsberechtigungen werden aktiviert unter:

```text
Realm settings → Admin Permissions → Enabled
```

Sie erlauben richtlinienbasierte Kontrolle über Ressourcen und Operationen eines Realms.

Für die delegierte Nutzung der Admin Console kann der Operator zusätzlich die jeweils passende Rolle `query-users`, `query-groups`, `query-clients` oder `query-organizations` benötigen. Diese Query-Rollen ermöglichen das Auffinden in der Konsole; allein gewähren sie kein Anzeigen oder Verwalten einer geschützten Ressource. Vergeben werden nur die für die vorgesehenen Konsolenseiten nötigen Query-Rollen.

Fine-Grained Admin Permissions dürfen beim selben delegierten Operator nicht mit einschlägigen groben `realm-management`-Rollen wie `view-users`, `manage-users`, `view-clients` oder `manage-clients` kombiniert werden. Nach aktuellem Keycloak-Verhalten wird die zugehörige feingranulare Prüfung umgangen, wenn eine ausreichende grobe Admin-Rolle den Vorgang bereits erlaubt. Zu prüfen sind auch über Gruppen und Composites geerbte wirksame Rollen.

Die aktuelle Dokumentation weist außerdem darauf hin, dass Fine-Grained Admin Permissions die Zugriffsdurchsetzung für föderierte Ressourcen nicht unterstützen. Ziele aus LDAP oder anderer Föderation sind in der konkret eingesetzten Version zu testen; nicht unterstützte Hochrisiko-Administration läuft über einen separat autorisierten Dienst-Workflow.

### Operationen für Benutzer

Relevante Scopes sind unter anderem:

- `view`;
- `manage`;
- `reset-password`;
- `map-roles`;
- `manage-group-membership`;
- `impersonate`.

### Operationen für Rollen

Wichtige Rollenberechtigungen sind:

- `map-role`, um eine ausgewählte Rolle einem Benutzer zuzuweisen;
- `map-role-composite`, um sie in eine andere Rolle aufzunehmen;
- `map-role-client-scope`, um sie über einen Client Scope auszugeben.

### Operationen für Gruppen

Dazu gehören das Anzeigen oder Verwalten von Gruppen, Mitgliedern und Mitgliedschaften. Ein Benutzer kann Rollen durch eine Gruppe erben; deshalb ist Gruppenpflege stets Teil der Eskalationsprüfung. Für die Zuweisung zu einer geschützten Gruppe werden sowohl die Benutzerberechtigung `manage-group-membership` als auch die Gruppenberechtigung `manage-membership` der Zielgruppe benötigt; beide Seiten sind zu begrenzen und zu testen.

### Operationen für Clients

Client-Administration kann Redirect-URIs, Zugangsdaten, Scopes und Protocol Mapper verändern. Sie ist eine andere und häufig mächtigere Funktion als das Zuweisen einer vorab definierten Fachrolle.

## Empfohlenes Gruppenmodell

Eindeutig benannte Gruppen können beispielsweise so aussehen:

```text
/administrators/delegated-user-admins
/administrators/access-operators
/administrators/security-role-admins
/administrators/platform-admins
/users
/privileged-business-users
```

`/administrators` und `/privileged-business-users` werden als Ziel geschützt. Delegierte Administratoren dürfen weder sich selbst noch andere Personen in diese Gruppen aufnehmen.

## Muster für Benutzeradministratoren

Für einen Helpdesk-ähnlichen Benutzeradministrator:

1. Eine Policy für `delegated-user-admins` erstellen.
2. `view` nur für zulässige normale Benutzer gewähren.
3. Von `manage` und `reset-password` nur tatsächlich erforderliche Teile gewähren.
4. `map-roles` nicht vergeben.
5. `manage-group-membership` nur vergeben, wenn die exakten Zielgruppen separat begrenzt sind.
6. `impersonate` nicht vergeben.
7. Verwaltung von Administrator- und privilegierten Benutzergruppen ausschließen.
8. Mitglieder von Administratorgruppen als verwaltbare Benutzerziele ausschließen.

Ist `manage` in der eingesetzten Keycloak-Version weiterhin weiter als der Anwendungsfall erlaubt, sollte die Aufgabe über einen eng begrenzten Dienst-Workflow statt über die Admin Console erfolgen.

## Muster für Berechtigungsoperatoren

Ein Berechtigungsoperator kann ausgewählte Fachrollen zuweisen, ohne sie selbst zu besitzen:

1. Zulässige Zielbenutzer über eine User Permission festlegen.
2. Dem Operator `map-roles` für diese Ziele gewähren.
3. Für jede erlaubte Rolle der Operator-Policy `map-role` gewähren oder eine bewusst begrenzte Client-Rollenberechtigung verwenden.
4. Keine Mapping-Berechtigung für administrative, zusammengesetzte oder privilegierte Rollen vergeben.
5. Operator und sämtliche Administratoren als Ziele ausschließen.
6. Gruppenwege schützen, die dieselbe Rolle vermitteln.
7. Dem Operator weder Kontoanlage, Setzen oder Zurücksetzen von Zugangsdaten, Impersonation noch Pflege der geschützten Eignungsquelle erlauben.
8. Freigabe- oder Workflow-Referenz außerhalb von Keycloak speichern.

Damit werden drei Fragen getrennt: Wer darf Rollen zuweisen, welche Rollen dürfen zugewiesen werden und welche Benutzer dürfen Ziel sein?

## Der Weg über ein neues oder alternatives Konto

Die Sperre einer Zuweisung an die aktuelle `sub` des Operators ist notwendig, reicht aber nicht. Dieselbe Person könnte ein weiteres Konto anlegen oder kontrollieren und diesem die Berechtigung zuweisen. Keycloak kann normalerweise nicht erkennen, dass zwei Konten demselben Menschen gehören.

Für privilegierte Rollen gelten deshalb alle folgenden Grenzen:

1. **Provisionierung ist getrennt.** Der Berechtigungsoperator darf weder Benutzer anlegen, Identitäten verknüpfen, Initialkennwörter setzen, Zugangsdaten zurücksetzen, Identity Provider verwalten noch Benutzer impersonieren.
2. **Ziele sind vorab geeignet.** Eine maßgebliche HR-/IAM-/SCIM-Quelle oder eine geschützte Eignungsgruppe liefert den zulässigen Zielbestand. Der Operator darf weder diese Quelle schreiben noch Mitglieder in die Gruppe aufnehmen. Ein neu angelegtes lokales Konto ist standardmäßig nicht geeignet.
3. **Freigabe bindet Mensch und Konto.** Ein kontrollierter Workflow verbindet Antragsteller, Genehmiger, Zielperson, Ziel-`iss` und -`sub`, Rolle, Grund und Zeitfenster. Bei kritischem Zugriff lehnt er ab, wenn Antragsteller oder Operator zugleich Zielperson sind – auch über ein anderes verknüpftes Konto.
4. **Ausführung ist eng begrenzt.** Ein Service Account ordnet nur die genehmigte Rolle dem genehmigten geeigneten Ziel zu und darf weder Benutzer anlegen, Rollendefinitionen ändern noch den Zielbestand erweitern.
5. **Nachweise verbinden die Systeme.** Provisionierung, Identitätsverknüpfung, Eignung, Freigabe, Rollenzuweisung, Zugangsdatenänderungen, abgelehnte Versuche und Nutzung des neuen Rechts werden in einem unabhängig kontrollierten Audit-Speicher korreliert.

Kann ein Administrator sowohl ein Konto anlegen oder dessen Zugangsdaten übernehmen **als auch** es für eine privilegierte Rolle geeignet machen und diese zuweisen, kann Keycloak-Konfiguration allein nicht nachweisen, dass Selbsteskalation verhindert ist. Diese Kombination ist aufzulösen oder durch eine unabhängige externe Freigabe- und Ausführungsgrenze zu kontrollieren.

## Direkte und indirekte Eskalationswege

Alle folgenden Wege müssen geprüft werden, bevor Selbsteskalation als verhindert gilt.

### Direkte Zuweisung

Kann der Operator die privilegierte Rolle seinem eigenen Benutzer zuweisen?

### Gruppenmitgliedschaft

Kann er sich einer Gruppe hinzufügen, die diese Rolle erbt?

### Zusammengesetzte Rollen

Kann er eine erlaubte Rolle so ändern, dass sie eine administrative Rolle enthält?

### Client Scopes und Protocol Mapper

Kann er einen Client so konfigurieren, dass dieser eine fest codierte Rolle oder zu breite Audience ausgibt? Die Keycloak-Dokumentation weist auf Risiken von Berechtigungslecks durch Client-Verwaltung und Protocol Mapper hin.

### Service Accounts

Kann der Operator Zugangsdaten eines höher berechtigten Service Accounts erhalten, wechseln oder benutzen?

### Impersonation

Kann er einen Benutzer impersonieren, der die gewünschte Berechtigung bereits besitzt?

### Kennwort- und Zugangsdaten-Reset

Kann er die Zugangsdaten eines privilegierten Benutzers zurücksetzen und sich anschließend als dieser anmelden?

### Neue und alternative Konten

Kann er ein Konto anlegen, dessen Zugangsdaten oder Föderationsverknüpfung kontrollieren, es zum geeigneten Ziel machen, ihm die Rolle zuweisen und sie anschließend nutzen? Kann er die Schritte auf zwei von ihm kontrollierte Konten verteilen?

### Identitätsföderation

Kann er einen LDAP-, SCIM- oder Identity-Provider-Mapper so ändern, dass privilegierte Mitgliedschaften importiert werden?

### Master Realm und Serveradministration

Kann er den Weg über Master-Realm-Benutzer, Bootstrap-Konto, Keycloak-Datenbank, Container-Plattform, Secret Store oder Backup-Restore nehmen?

Fine-Grained Admin Permissions regeln Vorgänge über Admin Console und Admin API. Sie begrenzen keine Person, die Datenbank oder Laufzeitumgebung direkt vollständig kontrolliert.

## Vier-Augen-Prinzip

Feingranulare Berechtigungen bestimmen, ob ein Akteur eine administrative Operation aufrufen darf. Sie beweisen nicht, dass eine zweite Person genau diese Zuweisung genehmigt hat.

Dafür ist ein externer IAM-, ITSM- oder kontrollierter Automatisierungsworkflow nötig:

```text
Antragsteller
    ↓ Antrag
fachlicher Genehmiger
    ↓ genehmigte Workflow-ID
begrenzter Service Account
    ↓ Rollenzuweisung
Keycloak
    ↓ Admin Event
unabhängiger Audit-Speicher
```

Anforderungen:

- Bei kritischen Rollen sind Antragsteller und Genehmiger verschieden.
- Der Service Account akzeptiert nur genehmigte Rolle, Zielperson und Zeitfenster.
- Er besitzt keine breite Realm-Administrationsrolle.
- Workflow-ID, Genehmiger, Entscheidung, Ziel, Rolle und Ausführungsergebnis werden aufbewahrt.
- Befristete Berechtigungen werden automatisch entfernt.
- Abgelehnte und fehlgeschlagene Versuche bleiben nachvollziehbar.

Der Administrator einer allgemeinen Automatisierungsplattform kann ähnlich mächtig wie ein IAM-Administrator sein. Workflow-Code, Secrets und Deployment-Rechte müssen entsprechend geschützt werden.

## Break-Glass-Zugriff

Für Notfälle wird ein separates Konto oder ein kontrollierter Erhöhungsweg verwendet:

- keine tägliche Nutzung;
- starke Mehrfaktorauthentifizierung;
- Zugangsdaten in einem Privileged-Access-Tresor;
- kurze Gültigkeit oder Just-in-time-Aktivierung;
- sofortiger Alarm bei Nutzung;
- verpflichtende Vorfalls- oder Begründungsreferenz;
- Prüfung nach jeder Verwendung;
- unabhängige Protokollkopie.

Das Notfallkonto darf nicht für normale Wartung benötigt werden.

## Keycloak-Konfigurationscheckliste

1. Sämtliche Master- und Realm-Administratoren erfassen.
2. Gemeinsame Administratorkonten entfernen.
3. Starke MFA oder Passkeys für privilegierte Konten erzwingen.
4. Unterstützte Fine-Grained Admin Permissions im Ziel-Realm aktivieren.
5. Nur notwendige `query-*`-Konsolenrollen vergeben und grobe Admin-Rollen entfernen, welche die zugehörigen feingranularen Prüfungen umgehen.
6. Für jede delegierte Administratorgruppe eine eigene Policy anlegen.
7. Zulässige Benutzerziele aus einer maßgeblichen, vom Operator unabhängigen Eignungsquelle festlegen.
8. Zulässige Rollenzuweisungen festlegen.
9. Privilegierte Gruppen, Eignungsgruppen und Administratorziele schützen.
10. Impersonation, Kontoanlage und Zugangsdaten-Reset aus der Funktion des Berechtigungsoperators entfernen.
11. Client-, Rollendefinitions-, Identity-Provider-, Kontoprovisionierungs-, Eignungs- und Realm-Administration trennen.
12. Admin Console und Admin API am Netzrand begrenzen.
13. Benutzer- und Administratorereignisse mit minimiertem Ereignisinhalt aktivieren.
14. Ereignisse in ein unabhängig kontrolliertes Audit-System exportieren.
15. Policies mit Keycloaks Permission Evaluation und echten Admin-REST-API-Aufrufen testen, soweit relevant auch mit föderierten Zielen.
16. Mitgliedschaften, wirksame Rollen, Provisionierungsrechte und Schreiber der Eignungsquelle regelmäßig rezertifizieren.
17. Zeitnah patchen und die Negativtests nach jedem Keycloak-Upgrade oder jeder Änderung des Berechtigungsmodells wiederholen.

## Abnahmetests

### Positivtests

1. Benutzeradministrator kann einen zulässigen normalen Benutzer sehen.
2. Benutzeradministrator kann die vorgesehene Profil- oder Zugangsdatenhandlung ausführen.
3. Berechtigungsoperator kann eine genehmigte Fachrolle einem zulässigen Ziel zuweisen.
4. Audit-Leser kann erforderliche Ereignisse lesen, aber keine Identitäten verändern.
5. Freigabeworkflow weist exakt die genehmigte Rolle zu und speichert seine Workflow-ID.

### Negativtests

1. Benutzeradministrator kann keinerlei Rolle zuweisen.
2. Berechtigungsoperator kann keine Administratorrolle zuweisen.
3. Berechtigungsoperator kann sich die Fachrolle nicht selbst zuweisen.
4. Berechtigungsoperator kann keinen anderen Administrator verändern.
5. Berechtigungsoperator kann niemanden einer privilegierten Gruppe hinzufügen.
6. Berechtigungsoperator kann keine zusammengesetzte Rolle ändern.
7. Delegierter Administrator kann weder Clients noch Protocol Mapper verwalten.
8. Delegierter Administrator kann keinen privilegierten Benutzer impersonieren.
9. Delegierter Administrator kann Zugangsdaten eines privilegierten Benutzers nicht zurücksetzen.
10. Delegierter Administrator kann das unabhängige Audit-Ereignis nicht löschen.
11. Service Account kann keine nicht genehmigte Zuweisung ausführen.
12. Eine abgelehnte Operation erscheint dort im Monitoring oder Audit, wo der Entwurf dies verlangt.
13. Berechtigungsoperator kann weder über Admin Console noch über `POST /admin/realms/{realm}/users` einen Benutzer anlegen.
14. Berechtigungsoperator kann bei einem neu angelegten Konto weder Zugangsdaten setzen oder zurücksetzen noch eine Identität verknüpfen oder es impersonieren.
15. Ein separat berechtigter Benutzeradministrator kann ein normales Konto anlegen, aber weder er noch der Berechtigungsoperator kann es allein zu einem privilegierten geeigneten Ziel machen.
16. Berechtigungsoperator kann ein neues oder alternatives Konto weder einer Eignungs-, Administrator-, rollenvermittelnden noch privilegierten Gruppe hinzufügen.
17. Eine über ein Konto beantragte Zuweisung an ein anderes Konto derselben kontrollierenden Person wird bei kritischem Zugriff abgelehnt.
18. Das Entfernen direkter grober Admin-Rollen wird nicht durch geerbtes `manage-users`, `view-users`, Client-Management oder eine zusammengesetzte Rolle ausgehebelt.
19. Das Ergebnis für ein föderiertes Ziel entspricht der dokumentierten Einschränkung und dem freigegebenen Ersatz-Workflow.

Die Tests sind gegen Admin Console und Admin REST API auszuführen. Es müssen auch über Gruppen und zusammengesetzte Rollen geerbte Rechte geprüft werden, nicht nur direkte Zuweisungen.

## Grenzen ehrlich benennen

Keine Keycloak-Policy kann zuverlässig erkennen, dass derselbe Mensch mehrere nicht verknüpfte Konten kontrolliert, oder einen Infrastrukturverantwortlichen mit vollständiger Kontrolle über Keycloak, Datenbank, Backups, Secrets und Audit-Empfänger technisch absolut am Umgehen der Regeln hindern. Diese Risiken werden durch maßgebliche Identitätsverknüpfung, organisatorische Trennung, Privileged Access Management, unabhängige Protokollierung, Änderungsfreigaben, Überwachung und externe Kontrolle reduziert.
