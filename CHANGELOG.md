# Changelog

All notable changes to this project are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For crunch-mcp, the "public API" is the set of tool names, their arguments, the prompts and the configuration variables. Renaming or removing any of these is a breaking change.

## [Unreleased]

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
