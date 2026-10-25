# Shared Syncthing wrapper

A Go web UI for a single shared Syncthing daemon. People sign in through any standards-compliant OIDC issuer and manage only their own folders and peer devices. The wrapper holds Syncthing's full-power API key; never expose the daemon's GUI/API or that key to users. This is family-scale separation enforced by the wrapper, not an adversarial multi-tenant security boundary.

## Build and test

Go 1.26 or newer is required.

```sh
go test ./...
go vet ./...
go build ./cmd/syncthing-server
docker build -t syncthing-server .
```

The Docker image contains **only the wrapper**. Syncthing runs separately; its GUI/API must listen on loopback and be reachable from the wrapper in the same network namespace. Peers connect to Syncthing's ordinary sync listener, not to the wrapper's web port. The wrapper creates user storage areas with private (`0700`) permissions: both processes must have access to the same mounted paths, typically by running with the same effective UID. Align runtime ownership before trying a separate daemon container; merely mounting the same volume is insufficient.

## Application configuration

Copy `config.example.json` to a private, non-secret application config file and set:

- `baseURL`: the public wrapper URL, with `/callback` registered as an OIDC redirect URI.
- `oidcIssuer`: the issuer URL exactly as advertised in its OIDC discovery document.
- `oidcClientID`: the OIDC client's ID. The app uses the standard authorization-code flow with PKCE, not issuer-specific claims.
- `syncthingURL`: the daemon's **private** GUI/API URL; configure Syncthing's GUI listener on `127.0.0.1:8384` and keep the stock GUI inaccessible to users.
- `syncthingMajor`: the tested Syncthing major version; defaults to `2`, and startup fails on a mismatch.
- `databasePath`: an absolute path for the SQLite store, in a writable directory that survives restarts.
- `storageRoot`: an existing, writable absolute directory containing only user storage areas. Keep it distinct from Syncthing's config directory.
- `registrationEnabled`: defaults to `true`. If disabled, new OIDC identities are denied; existing users can still log in. There is no provisioning UI in this release.
- `admins`: optional stable `{ "issuer": "...", "subject": "..." }` identities. An admin can inspect all users and manage only their own resources. Do not use email or display name for this list.

Provide secrets separately as `APP_OIDC_CLIENT_SECRET` and `APP_SYNCTHING_API_KEY`. They are rejected if included in the JSON config file. A confidential OIDC client is expected. The database, storage root, and Syncthing daemon must persist separately from the wrapper process. Do not start with preexisting unowned Syncthing folders or devices: the wrapper fails closed instead of adopting them.

Start the app with `APP_CONFIG=/path/to/config.json` or `-config /path/to/config.json`. The unauthenticated `GET /healthz` endpoint returns `ok` for process liveness. Use HTTPS for the public `baseURL` outside local testing; the app's session cookies are secure for HTTPS. A startup error or blocked write indicates an operator should inspect the structured logs, SQLite ownership, Syncthing configuration, and folder directories before repairing drift. There is no browser repair shortcut.

## Try login with PocketID

PocketID is the intended first OIDC provider, but the application does not use PocketID-specific endpoints or claims.

1. Create a **confidential** OIDC client in PocketID. Record its client ID and secret. Register the exact redirect URI `<baseURL>/callback` (for local testing, for example `http://localhost:8080/callback`). Logging out invalidates the application session; the provider's SSO session may remain active.
2. Set `oidcIssuer` to PocketID's issuer, for example `https://id.home.paperlark.ru` (check `/.well-known/openid-configuration` and use the document's exact `issuer` string). Set `oidcClientID` in the JSON file and put the client secret into `APP_OIDC_CLIENT_SECRET`.
3. Configure the Syncthing GUI listener on loopback and supply its API key through `APP_SYNCTHING_API_KEY`; create writable directories for `storageRoot` and the database parent. Start Syncthing, then run the wrapper and visit its `baseURL`.
4. Visit the app; you will be redirected to PocketID to authenticate, then returned to **My resources**. Sign out and verify the protected page requires login again. For an admin view, read the stable `sub` claim for your PocketID account, add the exact issuer/subject pair under `admins`, and restart the app.
5. Add a peer by its Syncthing device ID. Add the server's displayed ID to the peer, then create a folder in the wrapper and share it with that peer; accept the offered folder on the peer. To verify separation, sign in as a second user and confirm their view does not show the first user's folders or devices.

## Data safety

Folder names are one immediate directory name inside an opaque per-user storage area; the full path is not browser-editable. Folder removal asks the user to type its name and **permanently deletes the server-side directory only after removing it from Syncthing configuration**. Files already present on external peers may remain. Removal of a peer unshares it from the user's folders but cannot remove that peer's files. Partial writes and unexpected directory/configuration changes are logged and block the affected user's writes across restarts until an operator reconciles them out of band. For recovery, stop the wrapper, inspect the `operations`, `blocks`, and `global_blocks` SQLite tables alongside Syncthing configuration and actual folders, repair the mismatch, then clear only the verified block records before restarting. Clearing a block without repairing its cause leaves data safety unresolved.
