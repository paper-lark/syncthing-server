# Shared Syncthing

A shared Syncthing instance presents each person with only their own folders and peer devices, while an administrator can observe all users and their sync status.

## Language

**User**:
A person identified through PocketID who has their own collection of folders and peer devices in the shared instance. Their access begins on first sign-in.
_Avoid_: Syncthing user (Syncthing itself has no user accounts)

**Storage root**:
The server's designated storage area that contains each user's separate area.
_Avoid_: Syncthing configuration directory

**User storage area**:
The part of the storage root reserved for one user; that user's folders live only inside it.
_Avoid_: Shared folder

**Folder owner**:
The one user responsible for a folder in the shared instance and its files in their user storage area. A folder has exactly one owner.
_Avoid_: Shared owner

**Peer device owner**:
The one user responsible for an external Syncthing device paired with the shared instance. A peer device has exactly one owner.
_Avoid_: Shared device owner

**Admin**:
A user who can view all users, folders, and sync status and delete any folder, including another user's. An admin cannot otherwise change another user's resources.
_Avoid_: Superuser
