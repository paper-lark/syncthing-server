# 05: Add an owned peer device

**What to build:** A user can register a normal Syncthing peer by entering its device ID and display name. The UI shows the shared server's device ID and explains the peer-side pairing step; devices already owned by another user remain unavailable.

**Blocked by:** 03 — Show an ownership-filtered overview.

**Status:** resolved

- [x] The app validates and adds an explicitly supplied Syncthing device ID, records exactly one owner, and shows server device ID and reciprocal pairing guidance.
- [x] A device ID owned by another user cannot be claimed, edited, or viewed through the ordinary user flow; repeat/concurrent additions do not change ownership.
- [x] A pending request matching a user's explicit device-add action may be paired without displaying other users' or global pending requests.
- [x] Failure in the daemon or ownership store is reported, logged, durably tracked if incomplete, and blocks that user's further writes rather than leaving silent inconsistent ownership.
- [x] Module tests cover device ID validation, unique ownership, explicit pairing, and error handling; HTTP tests exercise two users with a simulated daemon. A real-Syncthing smoke check verifies device-add behavior.
