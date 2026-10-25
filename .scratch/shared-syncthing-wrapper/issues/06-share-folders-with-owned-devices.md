# 06: Share folders with owned devices

**What to build:** A user can start and stop sharing one of their server-created folders with one of their registered peer devices through the web UI. A normal peer can accept the resulting folder offer using the server-generated folder ID.

**Blocked by:** 04 — Create an owned folder safely; 05 — Add an owned peer device.

**Status:** resolved

- [x] A user can add or remove a share only between their own folder and peer device; forged or guessed cross-user IDs leave Syncthing and storage unchanged.
- [x] Updating one folder's sharing list preserves its other shares and unrelated daemon configuration, including under repeated or concurrent requests.
- [x] Newly shared folders carry the generated folder ID expected on the accepting peer; the UI explains reciprocal setup and acceptance.
- [x] A partial config/ownership mismatch fails visibly, logs details, and blocks the affected user's writes pending operator repair.
- [x] Module tests cover share eligibility, safe configuration changes, and failure behavior; HTTP tests drive sharing across two users. A real-Syncthing smoke check verifies the folder-share configuration.
