# 07: Remove a peer device safely

**What to build:** A user can inspect which of their folders share with a peer, confirm removal, and have that peer unshared from every affected folder and removed from the server without claiming to erase files on the external device.

**Blocked by:** 06 — Share folders with owned devices.

**Status:** resolved

- [x] The confirmation lists affected folder shares; another user's device or shares cannot be removed by a guessed identifier.
- [x] Removal clears the device from all of its owner's folder shares before removing its daemon configuration and ownership, leaving unrelated resources untouched.
- [x] The UI correctly states that files on the external peer remain; no operation attempts remote filesystem deletion.
- [x] Failure at any step is logged and shown as a failed action; incomplete work is durable and blocks affected-user writes until operator repair.
- [x] Module tests cover share enumeration, removal ordering, ownership checks, and partial failure; HTTP tests cover several shared folders and two users. A real-Syncthing smoke check verifies removal semantics.
