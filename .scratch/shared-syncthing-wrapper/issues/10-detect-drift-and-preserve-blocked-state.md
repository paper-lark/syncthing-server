# 10: Detect drift and preserve blocked state across restarts

**What to build:** An operator can trust the wrapper to notice when Syncthing's managed folders and storage diverge, rather than silently adopting or deleting data. Affected users get a brief failed-action message if they try to make changes, while detailed evidence is logged for out-of-band operator repair.

**Blocked by:** 04 — Create an owned folder safely; 05 — Add an owned peer device; 07 — Remove a peer device safely; 08 — Delete a folder and its server-side files safely.

**Status:** resolved

- [x] A missing registered folder directory, unexpected unregistered folder directory, unowned daemon configuration, or incomplete write is logged and handled fail-closed; ordinary file changes inside a registered folder are not drift.
- [x] Affected users' writes remain blocked after application restart until an operator repairs the mismatch out of band; reads remain scoped and no persistent drift banner or in-app repair control is introduced.
- [x] Every folder/device write path respects the blocked state, returns a brief action error, and never reports a partial change as successful.
- [x] Other users' resources remain inaccessible and unaffected; any mismatch that cannot safely be attributed to one user fails closed rather than guessing an owner.
- [x] Module tests cover reconciliation and durable operation state, including restart and simulated daemon/storage failures; HTTP tests verify blocked actions and unchanged cross-user resources against temporary storage and a simulated daemon.
