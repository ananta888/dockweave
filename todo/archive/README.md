# todo/archive

Abgeschlossene TODO-Dateien. Jede Datei enthält nur noch Tasks mit Status
`done` oder bewusst zurückgestellte `deferred`-Einträge — es gibt keine
offene Arbeit (`pending`/`in_progress`/`blocked`) mehr darin.

Aktive Arbeit liegt in `todo/todo.*.json` (Track-Format). Ältere Dateien hier
nutzen das Legacy-Format (`domain`/`tasks`); abgeschlossene Track-Dateien
werden unverändert im Track-Format archiviert (ohne `todo.`-Präfix).

Konvention: Wird eine TODO-Datei vollständig abgearbeitet (alle Tasks `done`
oder `deferred`), wird sie per `git mv todo/<name>.json todo/archive/` hierher
verschoben, statt sie in `todo/` zu belassen. Der Archiv-Ordner wird nicht
bereinigt; `deferred`-Einträge bleiben als bewusste Nicht-Ziele nachvollziehbar
(meist zusätzlich in `docs/security-decisions.md` oder der Fachdoku vermerkt).
