# Shared Syncthing wrapper — application and Docker image

Status: ready-for-agent

## Problem Statement

A shared Syncthing daemon has one all-powerful REST API key and no user or role model. Putting its stock GUI behind single sign-on would let every authenticated person see and modify every folder and peer device. People in this homelab need to use one shared instance while managing only their own folders and peer devices, and an admin needs an overview with the ability to delete any folder. A mistake in the wrapper's authorization or storage handling could disclose another user's information or delete synced data. The first deliverable is the application and its Dockerfile, not its deployment.

## Solution

Provide a web application that signs people in with PocketID, maintains sessions, and mediates every permitted action against one shared Syncthing daemon. A user sees and manages only folders in their user storage area and peer devices they own. They can create folders, add peer devices by Syncthing device ID, share their folders with their devices, inspect relevant status, and remove resources with appropriate confirmations. Folder removal deletes the server-side files; removal does not reliably delete files held by peers. An admin can also inspect users and their folders, devices, and sync status, and delete any folder after typing its ID; other cross-user actions remain read-only. The application is packaged with a Dockerfile; Syncthing remains a separate daemon reached through its loopback-only GUI/API, while ordinary Syncthing peer connections continue independently.

## User Stories

1. As a user, I want to sign in with PocketID, so that I can use my own Syncthing resources without sharing a GUI credential.
2. As a user, I want my first successful sign-in to create my application account automatically by default, so that I can begin using the service.
3. As a previously registered user, I want to retain access when automatic registration is switched off, so that disabling sign-ups does not lock me out.
4. As an unregistered person, I want a clear access-denied result when automatic registration is disabled, so that I do not mistake rejection for a broken login.
5. As a user, I want changes to my email or display name to leave my account, ownership, and storage intact, so that profile changes do not move or expose my files.
6. As a user, I want my session to end on logout, so that a shared browser cannot continue accessing my resources.
7. As a user, I want to see only my folders and peer devices, so that another user's configuration and identity are not exposed to me.
8. As a user, I want to see relevant synchronization and device status for my resources, so that I can tell whether they are syncing.
9. As a user, I want to create a folder by entering one subfolder name, so that I do not need to know the server's filesystem layout.
10. As a user, I want the app to keep the full server path hidden, so that I cannot select another user's storage area.
11. As a user, I want a new folder to begin unshared, so that I decide when and with which of my devices to share it.
12. As a user, I want newly created folders to use ordinary send-and-receive behavior, so that the common sync workflow works without advanced configuration.
13. As a user, I want a folder I create to have a stable Syncthing identity independent of its display name, so that the app controls its pairing correctly.
14. As a user, I want an existing unregistered directory to be rejected rather than silently adopted or overwritten, so that unexpected files are not mistaken for my new folder.
15. As a user, I want to add a peer by entering its Syncthing device ID and a display name, so that I can pair a normal Syncthing client.
16. As a user, I want to see the shared server's device ID and pairing guidance, so that I can add the server on my peer and accept shares there.
17. As a user, I want to share one of my folders with one of my peer devices, so that those devices can synchronize normally.
18. As a user, I want to change which of my devices receive a folder, so that I control its shares without editing global daemon settings.
19. As a user, I want a duplicate peer device ID already owned by someone else to be rejected, so that ownership cannot transfer accidentally.
20. As a user, I want attempts to share my folder with another user's peer device to be rejected, so that my sharing boundary holds.
21. As a user, I want removal of a peer device to identify all affected folder shares, so that I understand its consequences.
22. As a user, I want removal of a peer device to unshare it from all my folders, so that no stale references remain on the server.
23. As a user, I want removing a peer device to leave its locally stored files alone, so that the app does not pretend to control an external device's filesystem.
24. As a user, I want to type the folder ID to confirm folder removal, so that I cannot easily delete server-side files by accident.
25. As a user, I want the removal warning to state that server-side files will be deleted and peer copies may remain, so that I understand what will and will not be erased.
26. As a user, I want folder removal to remove its Syncthing configuration and server-side files, so that the storage root and the server's managed folders stay aligned.
27. As a user, I want a failed add, share, or removal to report failure rather than success, so that I do not rely on a partially applied operation.
28. As a user, I want a brief explanation when further changes are blocked after an inconsistent operation, so that I know to contact the operator.
29. As a user, I want ordinary file additions and deletions inside my registered folder to continue syncing, so that normal Syncthing activity is not treated as drift.
30. As an admin, I want to view all registered users and their folders, peer devices, and relevant status, so that I can oversee the shared instance.
31. As an admin, I want to manage the folders and peer devices that I personally own, so that my admin role does not remove my ordinary user capabilities.
32. As an admin, I want to delete any user's folder after typing its ID, so that I can remove a folder when its owner cannot. Other users' resources remain read-only to me otherwise.
33. As an admin, I want the overview to omit full server paths and credentials, so that those sensitive values are not displayed unnecessarily.
34. As an operator, I want unknown-device requests dismissed and logged without permanently blacklisting the device ID, so that an explicit add by its eventual owner can still succeed.
35. As an operator, I want unsolicited folder offers from paired devices ignored and logged without blocking legitimate sharing with those devices, so that foreign folders cannot enter an unowned storage path.
36. As an operator, I want missing or unexpected folder directories to be logged as drift and affected users' writes blocked, so that the mismatch does not grow unnoticed.
37. As an operator, I want a partial operation recorded across restarts and the affected user's writes blocked until I reconcile it, so that restarting does not erase evidence of failure.
38. As an operator, I want loss of a person's PocketID access to leave their folders and files intact, so that identity changes cannot silently delete data.
39. As an operator, I want the wrapper to reject an incompatible Syncthing API version, so that an upgrade cannot silently invalidate authorization assumptions.
40. As an operator, I want an application Docker image build, so that I can run the wrapper alongside a separately managed Syncthing daemon.

## Implementation Decisions

- **Architecture:** Build a small Go application that serves the browser UI, manages PocketID OIDC login and application sessions, and calls the shared Syncthing daemon over its loopback-bound REST GUI/API. Hold the full-power API key only on the server side. Do not expose the daemon's stock GUI or a generic user-facing Syncthing REST proxy. The daemon must continue to accept normal peer sync connections independently of its loopback-only GUI/API.
- **Identity:** Implement a provider-agnostic OIDC authorization-code login with PKCE and logout; PocketID is the intended first provider, not a hardcoded dependency. Key users by the stable pair of OIDC issuer and subject, not name or email. Grant no user access based solely on browser-supplied identity fields. Create a user at first login by default; expose an application configuration option to disable registration. When disabled, reject unknown identities while retaining existing accounts. Document how to configure a PocketID client and manually test login when the application is ready.
- **Admin:** Read the admin identity allowlist from the application configuration file (intended to be supplied via a ConfigMap later). Match stable issuer-and-subject identities. An admin can view other users' names, folders, device names/IDs, and relevant stats, and delete any folder after typed-ID confirmation, but cannot otherwise edit another user's resources. Admins retain normal management of their own resources. Do not place secrets or the Syncthing API key in this configuration file.
- **Ownership:** Keep user records, folder ownership, device ownership, and operation/reconciliation state in an application-owned SQLite store. Each Syncthing folder and peer device has exactly one owner; reject duplicates and cross-owner sharing. Unowned Syncthing configuration is inaccessible to ordinary users, never silently adopted, and blocks affected writes pending operator repair.
- **Storage:** Configure a storage root separate from Syncthing's configuration directory. Assign each user a stable, opaque user storage area that does not change with their profile. Place each folder in one immediate subdirectory chosen by the user; accept a single validated name, not an arbitrary path or nested path. Keep full server paths out of the UI. Reject existing unregistered directories; prevent traversal or symlink-based escapes. Generate a stable Syncthing folder ID distinct from the user's name. A missing registered folder directory or unexpected unregistered folder directory is drift; normal changes to files within registered folders are not.
- **MVP folder behavior:** Provide folder add/remove, basic folder/device sharing, and relevant per-user status. New folders start unshared and use ordinary send-and-receive settings. Do not implement folder rename or advanced mode/versioning/ignore controls in this release. All writes use specifically authorized operations, not user-selected REST paths or arbitrary Syncthing configuration edits.
- **Peer workflow:** Let a user explicitly add a peer by device ID and display name, show the server's device ID and normal peer-side pairing instructions, and let them share server-created folders only with their own registered devices. A peer must reciprocally add the server and accept the offered folder; Syncthing folder IDs must match across both ends. A matched pending device may be paired only as part of the owner's explicit add flow. Automatically dismiss and log unknown pending device requests, without permanently blacklisting their IDs. Persistently ignore and log unsolicited folder offers from paired devices without blacklisting those devices or their legitimate shares. Do not show a global pending-request list or accept peer-initiated folders.
- **Removal:** Removing a device removes it from all of its owner's folder sharing lists and then removes its configuration and ownership; show the affected folders first. It never deletes files on the peer. Removing a folder requires typing its ID and explicitly warns that server-side data is permanently deleted while peer copies may remain. Remove or disable the folder's Syncthing synchronization before deleting its server-side files, so deletion is not propagated as ordinary synced file removal; remove the associated ownership only when the operation completes. Never claim that server removal deletes remote peer copies.
- **Failure and drift:** Syncthing configuration changes, SQLite updates, and filesystem operations are not one atomic transaction. Preserve an explicit, durable incomplete-operation state; check results and expected storage/config relationships, fail closed, and block further writes for the affected user until operator reconciliation. Return a brief action error to the browser and log detailed mismatches and rejected requests. Do not add a persistent user/admin drift banner or an in-app admin repair action in the MVP. Operator repair happens out of band.
- **Authorization and read filtering:** Allowlist each browser operation and scope both reads and writes by ownership; filter status, completion, statistics, and any event information so a user cannot infer another user's resources. Admin cross-user writes are limited to folder deletion; other mutations remain owner-only. No direct browser access to the daemon or its API key.
- **Compatibility and packaging:** Use documented Syncthing REST operations and check daemon version on startup, failing closed on an incompatible major version. Favor Go standard-library dependencies where practical, using maintained packages for OIDC and SQLite as needed. Provide a Dockerfile for the wrapper application; do not package Syncthing into this application image.

## Testing Decisions

- **Good tests** drive observable HTTP behavior across login, sessions, UI actions, and authorization, rather than asserting private handler calls or database internals. Negative cases must establish both that an operation is denied and that the other user's Syncthing configuration and files remain unchanged.
- **Primary seam:** Exercise the running application at its HTTP boundary with a local OIDC test issuer, simulated Syncthing REST endpoint, and temporary SQLite database and storage root. This is the one main seam for login, registration toggle, admin visibility, ownership, folder/device creation and sharing, deletion confirmations, filtering, request rejection, drift, and partial-operation recovery.
- **Compatibility seam:** Add a small smoke test against a real supported Syncthing daemon for pairing/configuration and removal behavior, daemon version checks, and the distinction between deleting configuration and deleting local data. It should verify API assumptions without replacing the primary user-flow tests.
- **Security scenarios:** Cover modified/forged session identity, cross-user resource IDs, duplicate device IDs, unknown OIDC subjects when registration is off, invalid or escaping folder names, symlinks, and filtered status/events. Include simultaneous/repeated writes and a restart after an interrupted operation where practical.
- **Deletion scenarios:** Cover typed-name confirmation, all device shares removed before device deletion, local files removed only after folder synchronization is disabled/removed, remote files not represented as deleted, and recoverable log-and-block behavior when any step fails.
- **Prior art:** The repo currently has no application code or test suite; there is no local test pattern to reuse. The upstream Syncthing API and ADR-0008 establish the integration behavior, not a test framework.

## Out of Scope

- Changes to the homelab repo, an ArgoCD Application, Kubernetes manifests, a Helm chart, ingress, PVC provisioning, peer-network exposure, or deployment configuration. After the application and Dockerfile work, draft homelab deployment; only later add a chart in this repo and migrate to it.
- A stock Syncthing GUI exposed to users; a generic REST proxy; a public client API; gateway-only OIDC; or a separate Syncthing daemon per user.
- Automatic acceptance of unknown devices or peer-initiated folders; a user-facing global pending-request list; cross-user shared ownership; user-selected absolute paths or nested folder paths.
- Advanced Syncthing settings, folder rename, user deletion/provisioning UI, admin write override, persistent drift banners, in-app drift repair, and external alert integration.
- Deleting data on external peer devices when a server folder or device is removed; automatically deleting a user's data when their PocketID access changes.

## Further Notes

- This spec incorporates the agreed design from the October 2026 interview and the proposed homelab ADR-0008. The ADR's deployment plan calls for plain manifests, but the agreed sequence defers all deployment work; a chart is a later phase after the application works. Reconcile the ADR's deployment wording when that phase begins.
- Folder removal deletes **server-side files**, unlike Syncthing's REST config deletion alone. Removing files inside a still-synchronized folder could propagate deletions to peers; the order and failure recovery are therefore essential acceptance criteria.
- Syncthing's unmatched device requests can be dismissed without permanently ignoring the ID; unsolicited folder offers from an already-paired device can be ignored for that device/folder pair. Neither path may silently assign ownership.
- The application must assume anyone with the daemon API key or execution inside its runtime boundary can bypass the wrapper's user separation. This design is intended for a trusted homelab, not adversarial multi-tenant isolation.
