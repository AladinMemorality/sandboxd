# Template convention

Every template in this directory ships the same contract — the Baarcha
equivalent of Lovable's `.lovable` folder, split by consumer:

| Path | Consumer | Contents |
|---|---|---|
| `.baarcha/template.json` | the platform | machine-readable identity: schema, slug, name, stack (preset id), kind (starter \| app), version, description, whether skills ship |
| `CLAUDE.md` | the coding agent | exactly `@AGENTS.md` — never anything else |
| `AGENTS.md` | the coding agent | stable stack facts: Starter state (zero-discovery inventory), How it runs, Design playbook, Bridge protocol, Session memory. THE file to keep truthful |
| `.claude/skills/` | the coding agent | the shared skill pack (taste, web-design-guidelines + vendored rules, design-inspiration, image-to-code, self-screenshot). Single-sourced from `image/agent-skills/` and copied in at image build — edit it THERE, not per-template |
| `sandbox.yaml` | runtimed | written by the preset on first boot; templates do not ship it |
| `BRAIN.md` | the coding agent | NOT shipped — created at runtime; per-project decisions and gotchas |

Rules:
- A new template = app files + `AGENTS.md` + `CLAUDE.md` + `.baarcha/template.json`.
  Frontend templates get `"skills": true` and are listed in the Dockerfile's
  skill-copy loop.
- `.gitignore` in a template must never exclude `.claude` or `.baarcha`.
- Imported/community templates (converted in a sandbox, published as
  snapshots) follow the same contract; the conversion checklist installs it.
