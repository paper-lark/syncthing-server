## Agent skills

### Issue tracker

Issues and specs live as local Markdown files under `.scratch/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Local issue `Status:` values use the default five triage roles. See `docs/agents/triage-labels.md`.

### Domain docs

Before exploring the codebase, read `CONTEXT.md`; consult relevant `docs/adr/` entries when present. See `docs/agents/domain.md`.

## Pull requests

Always use Conventional Commits format for PR titles (for example, `feat: refine device actions`).

## Project

- Goal: A Go web wrapper gives OIDC users separate folders and peer devices on one shared Syncthing daemon; admins can inspect all users and delete any folder, but cannot otherwise edit another user's resources. See `CONTEXT.md` and `.scratch/shared-syncthing-wrapper/spec.md`.
- Scope as of 2026-10-25: The application and Dockerfile are implemented; homelab deployment and a Helm chart are deferred. See `README.md` for configuration and the PocketID check.
- Isolation: Ownership is enforced by the wrapper, not Syncthing ACLs; keep the daemon GUI/API loopback-only and give both processes access to the same storage paths. This is not adversarial multi-tenancy.
- Source of truth: Read and update Syncthing-owned settings and resources through Syncthing's API; persist only wrapper-specific state, never duplicate anything Syncthing already stores.
- README ownership: Never edit `README.md`; it is human-maintained. Suggest changes to the user only when project functionality expands significantly.
- Verification: Run `go test -race ./...` and `go vet ./...`; use `.claude/skills/verify/SKILL.md` for runtime checks.
- Local Compose: After finishing and testing an application change, rebuild only the wrapper with `docker-compose up --build -d --no-deps wrapper` and smoke-test the live page at `http://127.0.0.1:48080/`. Leave Syncthing, the OIDC fixture, and volumes untouched. This applies to the local development stack, not other deployments.
- Limits: PocketID login and data transfer with a live peer have not yet been verified; logout ends the app session but may leave the OIDC provider session active.
