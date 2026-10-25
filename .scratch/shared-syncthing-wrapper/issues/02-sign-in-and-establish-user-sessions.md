# 02: Sign in and establish user sessions

**What to build:** A person signs in through a generic standards-based OIDC provider, gains a session associated with their stable issuer-and-subject identity, and can log out. First login creates a user by default; the operator can disable registration and declare read-only admin identities in application configuration. Document a PocketID client configuration and hands-on login check for the intended homelab provider without making PocketID-specific assumptions in the app.

**Blocked by:** 01 — Run and package the wrapper.

**Status:** resolved

- [x] Provider-agnostic OIDC authorization-code login with PKCE and session validation works with a local test issuer; no issuer-specific claims or endpoints are hardcoded for PocketID.
- [x] First sign-in creates a user by default; when registration is disabled, existing users can sign in and unknown identities are denied without creating records.
- [x] A user stays the same after an email/name change; configurable admin identities match stable issuer and subject, and admins can still use their own account normally.
- [x] Login and logout protect browser session state; forged, expired, missing, or substituted credentials cannot impersonate another user.
- [x] Documentation explains how to configure a PocketID OIDC client, redirect URI, required application settings, and a manual login/logout test after the app is built; keep client secrets out of the non-secret configuration file.
- [x] Module tests cover identity mapping, registration toggle, admin matching, session validation, and logout; HTTP tests drive the full OIDC flow against a local test issuer.
