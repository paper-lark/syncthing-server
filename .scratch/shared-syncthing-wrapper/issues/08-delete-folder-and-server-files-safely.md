# 08: Delete a folder and its server-side files safely

**What to build:** A user can remove an owned folder only after typing its name in a confirmation that explains server-side files will be permanently deleted while peer copies may survive. Deletion removes synchronization before removing server files, so it cannot accidentally propagate ordinary file deletions to peers.

**Blocked by:** 04 — Create an owned folder safely; 06 — Share folders with owned devices.

**Status:** resolved

- [x] Incorrect, missing, or forged confirmation and cross-user folder IDs leave configuration, ownership, and files unchanged.
- [x] Confirmation clearly distinguishes server-side deletion from external peer copies; deleting the folder disables/removes its Syncthing configuration before deleting files in that folder's bounded user storage area.
- [x] The deletion removes only the owned folder directory and finishes by clearing its ownership; it never follows symlinks or deletes another user's data.
- [x] A failure before, during, or after filesystem deletion is logged, reported as failed, recorded durably, and blocks that user's writes until operator repair; no partial operation is reported as success.
- [x] Module tests cover typed confirmation, bounded deletion, operation ordering, and recovery state; HTTP tests exercise success and injected failures with actual temporary files. A real-Syncthing smoke check verifies configuration removal does not itself delete files.
