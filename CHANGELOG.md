# Changelog

All notable changes to this project are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For crunch-mcp, the "public API" is the set of tool names, their arguments, the prompts and the configuration variables. Renaming or removing any of these is a breaking change.

## [Unreleased]

## [0.3.0] - 2026-10-20

This release contains security fixes from a review, and some **breaking changes** to configuration and tools; see below before upgrading.

### Security

- A token refresh is no longer cut short when the tool call that triggered it is cancelled. Before, a cancellation after Crunch had rotated the refresh token lost the new one, and the next refresh reused the spent token, which makes Crunch revoke the whole login.
- POST requests (creating expenses, recording payments, issuing or emailing invoices) are no longer retried when Crunch returns `503`, which could do them twice. They are still retried on `429`.
- Updates always send the id of the record in their path: an `id` in `changes` or `extra` is overridden (as is a recurring expense's `recurringEvent` id), and a raw `PUT` whose body names a different record is refused. Before, the model could point an update at a record the keep-payments checks never looked at.
- **Breaking:** `CRUNCH_API_URL` and `CRUNCH_AUTH_URL` must use `https://` (plain `http://` only for loopback hosts), and no configured URL may contain `user:password@`. Redirects from the Crunch API or OAuth server are no longer followed, so tokens and the client secret can't be sent elsewhere.
- **Breaking:** `MCP_ALLOW_UNAUTHENTICATED=true` now also requires `MCP_ALLOWED_HOSTS`, because without a token the Host check is the only protection against DNS rebinding.
- **Breaking:** `MCP_AUTH_TOKEN` must be at least 16 characters.
- **Breaking:** with `MCP_ALLOWED_HOSTS` set, an `Origin` entry without a port only matches the scheme's default port (443 or 80); list `host:port` to allow a page on another port, such as `localhost:5173`. An `Origin` header that is present but empty is now checked (and refused) like any other. `Host` entries without a port still match any port.
- `/mcp` only accepts `POST` (`405` otherwise) with a JSON body. The HTTP server has read and idle timeouts.
- **Breaking:** `crunch_auth_complete` needs the login's `state`: paste the whole redirected URL, or pass the code with the new `state` argument. A bare code was used with the most recent login, so a code from someone else's authorisation could complete it.
- The raw API tool applies the email switch to query parameters (such as `sendReminders=true`, or anything named like `email` or `remind`) and to flags such as `sendEmail: true` in bodies, and a `POST` to a record path must keep its payments and attachments, like a `PUT`.
- With `CRUNCH_FILES_DIR` set, the download tools are no longer annotated read-only, because `save_to` writes files.
- **Breaking:** `crunch_remove_expense_payment` needs `payment_id`, or `all: true` to remove every payment. Leaving `payment_id` out used to remove all of them.
- A refresh token that Crunch rejected isn't sent again on every call; after other refresh failures, refreshes wait 10 seconds.
- API responses over 64 MiB fail instead of being read in full (or, for downloads, silently truncated), and `max_items` is capped at 10,000.
- Error text from Crunch is stripped of control and formatting characters and quoted as coming from the API before it reaches the model.
- Builds use Go 1.27 (`toolchain go1.27.2`), the Docker base images are pinned by digest, and CI runs `govulncheck`.

### Changed

- `crunch-mcp healthcheck` reads the same settings as `serve` (including `_FILE` variants) and connects to `MCP_HTTP_HOST`, or to loopback when the server listens on every interface.

## [0.2.0] - 2026-10-15

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

[Unreleased]: https://github.com/mattoddie/crunch-mcp/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/mattoddie/crunch-mcp/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/mattoddie/crunch-mcp/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/mattoddie/crunch-mcp/releases/tag/v0.1.0
