# 01: Run and package the wrapper

**What to build:** An operator can configure and launch the Go web application, see that it is alive, and build a wrapper-only Docker image. The app checks its Syncthing REST connection and refuses to serve against an incompatible daemon version; the daemon and its peer sync traffic remain separate from the wrapper image.

**Blocked by:** None (can start immediately).

**Status:** resolved

- [x] App starts with non-secret configuration plus separately supplied secrets, serves a minimal web page and health response, and never exposes the Syncthing API key to browsers or logs.
- [x] Startup verifies the Syncthing REST connection and fails closed on an incompatible major version or invalid required configuration.
- [x] Docker image builds and runs the application without bundling the Syncthing daemon; a running instance can connect to a separately launched daemon.
- [x] Module tests cover configuration validation, credential handling, REST authentication, version parsing, and failure cases; HTTP tests drive startup-visible behavior using a simulated Syncthing server.
- [x] A small real-Syncthing smoke check confirms the documented version/status calls and startup compatibility assumptions.
