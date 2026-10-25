---
name: verify
description: Verify the shared Syncthing wrapper by running its web UI against a disposable OIDC issuer and real daemon.
---

# Runtime verification

Use an isolated, disposable Syncthing instance and storage root. Drive the wrapper via its HTTP pages and forms; unit tests are not runtime evidence.

1. Start the local test issuer with `go run ./dev/oidc-fixture` (loopback port 48555, test identity `smoke-user`). Start a fresh `syncthing/syncthing` container with its GUI published **only** on host loopback, and mount a temporary storage root at the **same absolute path** in both the container and host. Configure the daemon API key without printing it into command history. On Colima, `~/Documents` and `/private/tmp` may not be mounted into the VM; use a `mktemp -d ~/.cache/syncthing-app-verify.XXXXXX` root instead. Colima maps host-owned files to root in containers, so a disposable test daemon may need `PUID=0 PGID=0`; production must instead configure shared ownership/group permissions.
2. Copy the non-secret config example to that temporary root; use `baseURL` `http://127.0.0.1:48080`, `oidcIssuer` `http://127.0.0.1:48555`, `oidcClientID` `smoke-client`, the daemon's host-loopback GUI URL, and the shared storage root. Run `go run ./cmd/syncthing-server` with `APP_CONFIG`, `APP_OIDC_CLIENT_SECRET=test-only-secret`, and the disposable daemon's `APP_SYNCTHING_API_KEY`. The OIDC fixture is loopback-only and unsuitable for production.
3. Use a browser (or a cookie-jar HTTP client if no browser runner exists) to follow `/` → OIDC login → `/callback` → dashboard. Observe the displayed server device ID. Submit the dashboard's CSRF-protected forms to create a folder, add a peer by a valid ID, share/unshare, remove the peer, and delete a folder with its typed-name confirmation. Compare the UI against the real daemon's folder/device configuration and the mounted files. For destructive flows, put only disposable test files in the temporary folder.
4. Probe an incorrect confirmation, duplicate folder, unknown route, and an unexpected user-area directory. Observe errors, structured drift log, blocked writes, and the blocked state after wrapper restart. Stop and remove the disposable container and local test processes when done; inspect the temporary root before removing the test data.

The daemon's test GUI may bind `0.0.0.0` **inside its container** only to support a host-loopback port binding. In the actual shared-pod architecture the Syncthing GUI must bind `127.0.0.1` inside the pod. Never publish its API key, and never point this procedure at existing user data.
