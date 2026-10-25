// Package knowledge manages the shared, human-readable knowledge store
// that is mounted at /knowledge in every agent (review follow-up): an
// explicit memory area for facts and decisions that agent systems
// (codex, opencode, ...) and the operator read and write together.
package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
)

// README is the usage guide seeded into an empty store. It is written for
// humans and agents alike — the conventions keep the store followable.
const README = `# Wissensspeicher (/knowledge)

Gemeinsamer, menschenlesbarer Speicher für ALLE Agenten (Codex,
OpenCode, Claude, ...) und für dich. Jeder sieht denselben Stand.

## Ablage-Konvention

- eine Datei pro Thema, Klartext/Markdown, z.B.:
  - projekte/<projekt>.md  — Fakten, Entscheidungen, offene Punkte
  - glossar.md             — Begriffe und Bedeutungen
  - entscheidungen.md      — Beschlüsse mit Datum (neueste zuerst)
- Wissen ANHÄNGEN oder in bestehende Datei eintragen, nie blind
  überschreiben
- jeder Eintrag: kurze Aussage + Datum + Quelle ("laut README", "Kunde")

## Für Agenten

- Fakten aus deiner Arbeit hier ablegen (Ordner /knowledge im Container)
- vor Annahmen hier nachschlagen, was schon bekannt ist
- menschliche Notizen respektieren und erhalten

## Für Menschen

- alles liegt als normale Textdateien vor (ls/cat/Editor)
- Host-Pfad steht in: dockweave agent paths <agent>
`

// Ensure creates the store and seeds the usage guide when empty.
func Ensure(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create knowledge dir: %w", err)
	}
	readme := filepath.Join(dir, "README.md")
	if _, err := os.Stat(readme); os.IsNotExist(err) {
		if err := os.WriteFile(readme, []byte(README), 0o644); err != nil {
			return fmt.Errorf("seed knowledge README: %w", err)
		}
	}
	return nil
}
