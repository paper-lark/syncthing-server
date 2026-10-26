## Agent skills

### Issue tracker

Issues and specs live as local Markdown files under `.scratch/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Local issue `Status:` values use the default five triage roles. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout: root `CONTEXT.md` and `docs/adr/`. See `docs/agents/domain.md`.

## Project

- Goal: A Go web wrapper gives OIDC users separate folders and peer devices on one shared Syncthing daemon; admins have a read-only cross-user view. See `CONTEXT.md` and `.scratch/shared-syncthing-wrapper/spec.md`.
- Scope as of 2026-10-25: The application and Dockerfile are implemented; homelab deployment and a Helm chart are deferred. See `README.md` for configuration and the PocketID check.
- Isolation: Ownership is enforced by the wrapper, not Syncthing ACLs; keep the daemon GUI/API loopback-only and give both processes access to the same storage paths. This is not adversarial multi-tenancy.
- Verification: Run `go test -race ./...` and `go vet ./...`; use `.claude/skills/verify/SKILL.md` for runtime checks.
- Limits: PocketID login and data transfer with a live peer have not yet been verified; logout ends the app session but may leave the OIDC provider session active.


I have a service in k8s. It exposes a gRPC API. My clients access it from outside the cluster. Right now they go through an L3 balancer in front of the k8s cluster. I want to replace it with client side load balancing for grpc. Ideally I want to rely on name resolvers present in the stock gRPC clients and use OSS software to provide them with endpoints