# Shared design skills in Cube

The canonical pack lives in `control-plane/internal/designskills/pack`. It
contains the six existing design skills, the vendored web interface rules, and
a short `GUIDE.md` that routes visual work to relevant resources. User briefs,
supplied assets and custom project conventions take precedence over aesthetic
defaults. The guide requires deliberate art direction and visual refinement
without prescribing the same layout or palette for every project.

The controller embeds this directory. Before accepting any Cube coding task,
it verifies and, if necessary, repairs all pack files through the authenticated
guest file API. Delivery uses `.baarcha/design-skills/<content-sha256>/` relative
to the application workspace. The dispatched prompt points to that exact
directory; the stored user request is unchanged. This covers all owners,
presets, existing projects, imports and remixes without replacing their
`AGENTS.md`, `CLAUDE.md` or custom `.claude/skills` files. Stopped projects receive
the pack when they next run a task; active tasks finish with their existing
context. No mass VM wake or guest supervisor replacement is needed.

Verification compares file contents, not a completion marker. Concurrent file
operations are bounded to four and preparation has a 45-second deadline.
Unavailable or rejected delivery returns `design_skills_unavailable` before
task persistence/model scope/agent launch. Retrying repairs a partial transfer;
unchanged files are not rewritten. The existing guest file API rejects symlink
paths and performs atomic writes. Platform-owned versioned files are repaired;
customizations belong in project instructions or custom skills.

Both image recipes copy the same source pack into starter skills. In addition,
`cube-template` embeds and materializes it into every preset, including when
the dependency base is older. It writes both the native `.claude/skills` catalog
and the versioned task path. It only runs against a fresh, trusted build
directory, never an existing customer workspace. Controller task-time delivery
also covers existing deployed template snapshots, so access does not wait for
a fleet-wide template or runtime rebuild.

Checks:

```sh
cd control-plane
go test ./internal/designskills ./internal/api ./internal/agentprompt ./cmd/cube-template
go test -race ./internal/designskills ./internal/api -run 'TestDelivery|TestPack|TestCubeTaskWorkflow|TestCubeDoesNotAccept|TestCubeUncertain'
```

The template tests verify every preset receives the actual pack contents. Task
tests verify prompt delivery and preservation of the original request, and
that a failed transfer cannot start or leave an accepted task. Pack tests cover
repair, idempotence, custom-skill preservation and rejected writes.

The screenshot skill uses the existing authenticated bridge. A successful
capture must be opened and inspected; a build check is not visual verification.
After two repeated infrastructure errors, it reports the limitation and stops
retrying. The current bridge has desktop hero/full modes, not a mobile-width
argument; agents must not claim a mobile capture from those modes. External
design-reference fetches are optional and fall back to the local guidance.

Deployment evidence and the pinned controller rollout script live in
`ops/cube/design-skills/`. Rollback restores the prior controller image and
configuration without rewinding user data or deleting delivered resources.
