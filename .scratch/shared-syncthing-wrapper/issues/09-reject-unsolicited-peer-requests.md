# 09: Reject unsolicited peer requests and folder offers

**What to build:** The application enforces server-first, user-initiated pairing even when peers contact Syncthing independently. It dismisses unknown-device requests without blacklisting those IDs and ignores unsolicited folder offers from registered peers without breaking their legitimate shares.

**Blocked by:** 05 — Add an owned peer device; 06 — Share folders with owned devices.

**Status:** resolved

- [x] Unknown pending device requests are automatically dismissed and logged but not added to a permanent device ignore list; a later explicit add of the same ID can succeed.
- [x] A paired device's unsolicited folder offer is persistently ignored for that device/folder pair and logged, without blocking the device or an authorized server-created folder share.
- [x] Request handling cannot expose a global pending list to ordinary users or turn unsolicited offers into an unowned Syncthing folder.
- [x] Event/polling recovery handles missed events and repeat requests without accidental acceptance or a pairing race that prevents an explicit user add.
- [x] Module tests cover request classification, dismissal versus persistent ignore, retry handling, and pairing races; HTTP tests verify user-visible isolation. A real-Syncthing smoke check verifies pending-request operations.
