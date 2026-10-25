# 04: Create an owned folder safely

**What to build:** A user supplies one subfolder name in the web UI and gets an unshared send-and-receive Syncthing folder inside their own user storage area. The app generates the folder ID and server path, and a failed multi-step add cannot be mistaken for success.

**Blocked by:** 03 — Show an ownership-filtered overview.

**Status:** resolved

- [x] Each user has a stable, opaque user storage area under the configured storage root; changing a display name/email never moves that area.
- [x] One validated immediate subfolder name is accepted; traversal, nested or absolute paths, symlink escapes, and existing unregistered directories are rejected without modifying other users' files.
- [x] The generated Syncthing folder ID is distinct from the supplied name; a newly created folder is ordinary send-and-receive and has no peer shares.
- [x] The app records ownership exactly once and checks both daemon configuration and storage after creation; concurrent or repeated requests cannot create ambiguous ownership.
- [x] If filesystem, SQLite, or Syncthing operations fail partway through, show failure, log details, durably record the incomplete state, and block further writes for that user until operator repair.
- [x] Module tests cover path confinement, names, IDs, ownership, and incomplete-operation state; HTTP tests use temporary storage and a simulated daemon. A real-Syncthing smoke check confirms folder configuration semantics.
