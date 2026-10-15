# Changelog

All notable changes to this project are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For crunch-mcp, the "public API" is the set of tool names, their arguments, the prompts and the configuration variables. Renaming or removing any of these is a breaking change.

## [Unreleased]

This release contains security fixes and several **breaking changes** to defaults; see below before upgrading.

### Security

- **Breaking:** the http transport refuses to start without `MCP_AUTH_TOKEN`. Set `MCP_ALLOW_UNAUTHENTICATED=true` only when something in front of the server authenticates every request. `login` and `status` don't need the token.
- **Breaking:** the binary's `MCP_HTTP_HOST` defaults to `127.0.0.1` instead of `0.0.0.0`. The Docker image still sets `0.0.0.0`.
- With `MCP_ALLOWED_HOSTS` set, browser requests whose `Origin` isn't on the list are refused.
- **Breaking:** turning on payment reminders (`send_reminders: true`, or a truthy `sendReminders` in `extra`, `changes` or a raw request body, in any letter case) needs `CRUNCH_ALLOW_EMAIL=true`, because Crunch emails them to the customer. The raw API tool also refuses any path containing `email` or `remind` without it.
- **Breaking:** without `CRUNCH_ALLOW_DELETES`, updates can no longer drop existing payments, supplier payments or attachments (Crunch deletes whatever an update leaves out). This applies to `changes` on `crunch_update_invoice`, `crunch_update_expense` and `crunch_update_recurring_expense`, and to raw `PUT` requests, which are checked against the current record.
- **Breaking:** the raw API tool only accepts canonical paths: no `.` or `..` segments, `//`, `%`, `;`, `\`, spaces, control characters or non-ASCII characters. Access checks match the canonical, lower-cased path, so a path such as `/invoices/1/email/.` can no longer get past the email switch.
- **Breaking:** attachment `file_path` and download `save_to` are disabled unless `CRUNCH_FILES_DIR` is set, and then only work inside that directory, after resolving `..` and symlinks. The token file, its lock and temporary files, and anything under `/run/secrets` are always refused. Before, they could read and write any file the server's user could.
- Refreshes are serialised across processes that share a token file, with an exclusive lock on `<token file>.lock`; a process that waited uses the tokens the other one saved. Two processes refreshing at once could spend the same rotating refresh token and get the whole login revoked.
- `crunch-mcp login` only accepts a redirect carrying the `state` of the login it started, and ignores other requests, including `error=` responses without a valid state. A pasted URL whose state doesn't match is refused too.
- The docs and `compose.yaml` publish the login port on loopback only (`-p 127.0.0.1:8976:8976`).
- Builds use Go 1.26.9 (`toolchain go1.26.9` in `go.mod`), which fixes standard library vulnerabilities reported by `govulncheck`. Release images are built with the latest `golang:1.26`.

### Changed

- New settings: `MCP_ALLOW_UNAUTHENTICATED` and `CRUNCH_FILES_DIR`.
- The http server logs a warning when it runs without bearer auth.

### Fixed

- Requests over 4 MiB were rejected by the MCP SDK's own body limit, so attachments near the documented 10 MB failed over HTTP. The SDK now uses the server's 16 MiB limit.
- `crunch-mcp login` no longer panics when `CRUNCH_REDIRECT_URI` has no path (such as `http://127.0.0.1:8976`); the redirect is caught at `/`.

## [0.1.0] - 2026-10-15

First release. It hasn't yet been tested against a live Crunch account, so expect rough edges, and please [report](https://github.com/mattoddie/crunch-mcp/issues/new/choose) anything Crunch rejects.

### Added

- MCP server for Crunch Accounting with 70 tools in 9 toolsets: account, reference, banking, clients, suppliers, expenses, recurring, invoices and raw.
- Six built-in prompts: `business_health_check`, `chase_overdue_invoices`, `record_receipt`, `expense_review`, `create_invoice` and `period_summary`.
- OAuth 2.0 login with PKCE for public and confidential clients, from a terminal (`crunch-mcp login`, which catches the redirect or accepts a pasted URL) or from the assistant (`crunch_auth_start` and `crunch_auth_complete`). `CRUNCH_REFRESH_TOKEN` and `CRUNCH_ACCESS_TOKEN` as alternatives.
- Least-privilege scopes derived from the settings, overridable with `CRUNCH_SCOPES`.
- Safe handling of Crunch's rotating refresh tokens: tokens are saved atomically to `CRUNCH_TOKEN_FILE` (mode `0600`) before use, refreshes are never run concurrently, and a running server picks up a new login written to the token file by another process.
- Four access levels: read-only by default, `CRUNCH_ALLOW_WRITES`, `CRUNCH_ALLOW_DELETES` and `CRUNCH_ALLOW_EMAIL`. `CRUNCH_TOOLSETS` limits the tools exposed. Tools are annotated as read-only, destructive or open-world.
- Read-modify-write updates that keep existing payments and attachments.
- Attachments from base64, `data:` URIs or file paths, and downloads of receipts (inline images) and invoice PDFs (embedded resources), with optional `save_to`.
- Automatic retries when Crunch rate-limits requests, and error messages with hints for common Crunch errors.
- Streamable HTTP (stateless) and stdio transports, with bearer-token auth and a `Host` header allow-list for HTTP.
- `serve`, `login`, `status`, `healthcheck` and `version` commands.
- `_FILE` variants of every variable for Docker secrets.
- Multi-arch distroless Docker image (`linux/amd64`, `linux/arm64`) published to GHCR on each release, with SBOM and provenance attestation.
- Documentation, including a tool reference generated from the code.

[Unreleased]: https://github.com/mattoddie/crunch-mcp/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/mattoddie/crunch-mcp/releases/tag/v0.1.0
