# Syncthing Server

A simple sidecar and web wrapper for [Syncthing](https://docs.syncthing.net/index.html) that adds multi-user support through an OpenID Connect (OIDC) identity provider. It uses SQLite to store application metadata.

This project is under active development.

## Build and test

Go 1.26 or newer is required.

```sh
go test ./...
go vet ./...
go build ./cmd/syncthing-server
docker build -t syncthing-server .
```

### Versioning

Release tags use `vMAJOR.MINOR.PATCH`, optionally followed by a prerelease suffix such as `v1.2.3-rc.1`. Publishing a stable GitHub Release tags the Docker image with both its version (for example, `v1.2.3`) and `latest`; a prerelease receives only its version tag.

### Local Docker Compose

```sh
docker compose up --build -d
# If Compose is installed as a standalone binary: docker-compose up --build -d
```

Visit <http://127.0.0.1:48080/>. The Compose stack starts Syncthing, the wrapper, a one-shot storage initializer, and a disposable OIDC fixture that automatically signs in as `smoke-user`. This development stack runs Syncthing and the wrapper as root to share the volumes and uses fixed test-only defaults: never use them for production or expose the web port to other machines.

## Application configuration

Copy `config.example.json` to a private, non-secret application config file and set:

- `baseURL`: the public wrapper URL, with `/callback` registered as an OIDC redirect URI.
- `oidcIssuer`: the issuer URL exactly as advertised in its OIDC discovery document.
- `oidcClientID`: the OIDC client's ID. The app uses the standard authorization-code flow with PKCE, not issuer-specific claims.
- `syncthingURL`: the daemon's **private** GUI/API URL; configure Syncthing's GUI listener on `127.0.0.1:8384` and keep the stock GUI inaccessible to users.
- `syncthingMajor`: the tested Syncthing major version; defaults to `2`, and startup fails on a mismatch. Syncthing v2.1.0 or newer is required for native folder groups.
- `databasePath`: an absolute path for the SQLite store, in a writable directory that survives restarts.
- `storageRoot`: an existing, writable absolute directory containing only user storage areas. Keep it distinct from Syncthing's config directory.
- `registrationEnabled`: defaults to `true`. If disabled, new OIDC identities are denied; existing users can still log in. There is no provisioning UI in this release.
- `admins`: optional stable `{ "issuer": "...", "subject": "..." }` identities. An admin can inspect all users and delete any folder, including another user's; other management actions remain owner-only. Do not use email or display name for this list.

Provide secrets as environment variables `APP_OIDC_CLIENT_SECRET` and `APP_SYNCTHING_API_KEY`, not in the JSON config file. Set `STGUIAPIKEY` on the Syncthing process to the same value as the wrapper's `APP_SYNCTHING_API_KEY`; Syncthing uses that key for its GUI/REST API without changing `config.xml`.
