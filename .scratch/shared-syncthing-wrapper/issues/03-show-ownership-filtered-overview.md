# 03: Show an ownership-filtered overview

**What to build:** Signed-in users see only their own folders, peer devices, and relevant sync status in a web overview. Admins can additionally inspect all users' folders, devices, and relevant statistics without gaining write access to others' resources.

**Blocked by:** 02 — Sign in and establish user sessions.

**Status:** resolved

- [x] Ordinary users never see another user's folder, device, status, completion, statistics, or event details, including through guessed identifiers and browser requests.
- [x] Admins can inspect user names, folder and device names/IDs, and relevant status across users; the admin view does not expose full server paths or API credentials.
- [x] An admin's personal resources remain available in their own user view, while cross-user views remain read-only.
- [x] Unowned daemon configuration is not shown to ordinary users; all browser operations are specifically allowlisted rather than proxied as arbitrary Syncthing REST requests.
- [x] Module tests cover ownership joins, read filtering, and admin authorization; HTTP tests assert cross-user information is absent using a simulated daemon with several users' resources.
