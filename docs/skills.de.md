# Skills (Agent-Skill-Katalog)

DockWeave verwaltet Agent Skills (`SKILL.md` + optionale `references/`,
`scripts/`, `assets/`) zentral und materialisiert sie beim Agentenstart in den
Skills-Ordner des jeweiligen Harness.

## Standard-Skill

`keycloak-oidc-human-guide` ist im Daemon gebündelt. Er wird beim Start in das
verwaltete Skill-Verzeichnis (`<root>/skills/`) geschrieben und kann in der
Web-UI mit einem Klick einem beliebigen Agententyp zugewiesen werden. Die
Referenzen (Deutsch und Englisch) werden vollständig in den Agenten kopiert,
nicht nur ein Stub.

## Stores

Der **Skills**-Tab der Web-UI listet kuratierte Stores:

| Store | Repository |
|---|---|
| Claude Skills (Anthropic) | `anthropics/skills` |
| Codex Skills (OpenAI) | `openai/skills` |
| Cross-Agent Skills (TerminalSkills) | `TerminalSkills/skills` |

Die Installation lädt nur den gewählten Skill-Ordner aus dem GitHub-Tarball des
Stores. Downloads sind auf die allowlisteten Repositories (plus das Repository
des gebündelten Skills) beschränkt, nach Größe/Dateizahl begrenzt und lehnen
Pfad-Traversal sowie Symlinks ab. `SKILL.md` ist Pflicht; Name und Beschreibung
kommen aus dem Frontmatter. Herkunftsdaten stehen in `.dockweave-skill.json`.

## Einem Agententyp zuweisen

Installierte Skills erscheinen im Katalog und lassen sich als Default für
`opencode`, `claude`, `codex`, `gemini`, `antigravity` oder `custom` setzen.
Typ-Defaults werden beim nächsten Agentenstart materialisiert; Per-Agent-
Einträge mit gleichem Namen überschreiben sie.

Materialisierungsziele:

| Typ | Verzeichnis (im Agenten-Home) |
|---|---|
| opencode | `.config/opencode/skills/<name>` |
| claude | `.claude/skills/<name>` |
| codex | `.codex/skills/<name>` |
| gemini / antigravity | `.gemini/skills/<name>` |
| custom / default | `.dockweave/skills/<name>` |

## API

| Methode | Pfad |
|---|---|
| `GET` | `/api/v1/skills/catalog` |
| `POST` | `/api/v1/skills/install` (`{repo, path, ref}`) |
| `DELETE` | `/api/v1/skills/{name}` |
| `GET/POST/PATCH/DELETE` | `/api/v1/agent-types/{type}/skills` |
| `GET/POST/PATCH/DELETE` | `/api/v1/agents/{id}/skills` |

Das verwaltete Verzeichnis ist `<root>/skills` (root:
`/var/lib/dockweave/skills`). Installierte Skills lassen sich in der UI
entfernen; Typ-/Agent-Zuweisungen bleiben als Eintrag bestehen und müssen
separat gelöscht werden, wenn der Skill überall verschwinden soll.
