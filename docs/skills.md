# Skills (Agent Skill catalog)

DockWeave manages Agent Skills (`SKILL.md` + optional `references/`, `scripts/`,
`assets/`) centrally and materializes them into each harness's skills folder at
agent start.

## Standard skill

`keycloak-oidc-human-guide` is bundled with the daemon. It is written to the
managed skills directory (`<root>/skills/`) on startup and can be assigned to
any agent type with one click in the web UI. Its references (German and
English) are copied into the agent, not just a stub.

## Stores

The **Skills** tab in the web UI lists curated stores:

| Store | Repository |
|---|---|
| Claude Skills (Anthropic) | `anthropics/skills` |
| Codex Skills (OpenAI) | `openai/skills` |
| Cross-Agent Skills (TerminalSkills) | `TerminalSkills/skills` |

Install downloads only the selected skill folder from the store's GitHub
tarball. Downloads are restricted to the allowlisted repositories above (plus
the bundled skill's repository), capped by size/file count, and reject path
traversal and symlinks. `SKILL.md` is required and its frontmatter supplies the
name and description. Origin metadata is stored in `.dockweave-skill.json`.

## Assigning to an agent type

Installed skills appear in the catalog and can be added as a default for
`opencode`, `claude`, `codex`, `gemini`, `antigravity` or `custom`. Type
defaults are materialized on the next agent start; per-agent entries override
defaults with the same name.

Materialization targets:

| Type | Directory (inside the agent home) |
|---|---|
| opencode | `.config/opencode/skills/<name>` |
| claude | `.claude/skills/<name>` |
| codex | `.codex/skills/<name>` |
| gemini / antigravity | `.gemini/skills/<name>` |
| custom / default | `.dockweave/skills/<name>` |

## API

| Method | Path |
|---|---|
| `GET` | `/api/v1/skills/catalog` |
| `POST` | `/api/v1/skills/install` (`{repo, path, ref}`) |
| `DELETE` | `/api/v1/skills/{name}` |
| `GET/POST/PATCH/DELETE` | `/api/v1/agent-types/{type}/skills` |
| `GET/POST/PATCH/DELETE` | `/api/v1/agents/{id}/skills` |

The managed directory is `<root>/skills` (root: `/var/lib/dockweave/skills`).
Installed skills can be removed from the UI; type/agent assignments keep their
entry, so remove those too if the skill should disappear everywhere.
