# Compatibility

## The Crunch API

crunch-mcp talks to Crunch's **public REST API**, version 1, at `https://public-api.crunch.co.uk/v1`, and logs in through Crunch's OAuth server at `https://oauth.crunch.co.uk`. Crunch documents both on its [developer site](https://developer.crunch.co.uk).

| | Support |
|---|---|
| Crunch accounts with public API access | ✅ Any account you can log in to and have API access for. |
| Public OAuth clients (PKCE, no secret) | ✅ |
| Confidential OAuth clients (client secret) | ✅ The secret is sent with HTTP Basic, alongside PKCE. |
| Rotating refresh tokens (API applications created since 14 January 2026) | ✅ Handled. See [Refresh tokens rotate](configuration.md#refresh-tokens-rotate). |
| Non-rotating refresh tokens (older API applications) | ✅ The same refresh token is kept until it expires. |
| A Crunch sandbox or test environment | ❌ Crunch doesn't offer one. Test with a free Crunch account. |

The public API doesn't cover everything in the Crunch app, and crunch-mcp can only do what the API allows.

## Limits of the Crunch API

These come from Crunch's [FAQs](https://developer.crunch.co.uk/faqs/) and from using the API. They apply whatever crunch-mcp does:

- **Rate limit.** The API allows about 100 requests per second. crunch-mcp retries automatically when Crunch answers `429 Too Many Requests` or `503`, waiting as Crunch asks, so you're unlikely to notice.
- **Invoices are in pounds only.** The API only creates sales invoices in GBP.
- **New invoices only.** The API works with Crunch's new (beta) sales invoices, not the classic invoicing.
- **Undocumented endpoints are refused.** Calling an endpoint that isn't part of the public API, such as `/banking/statement-entries`, returns `403 Forbidden`. That's expected, and it means the data isn't available through the API.
- **Attachments must be `data:` URIs.** Crunch saves anything else as an empty file. crunch-mcp handles this for you. A zero-byte download means the file was uploaded raw by something else.
- **`500` errors on creates usually mean bad input.** Crunch often returns `500 Internal Server Error` when a required field is missing, or a VAT type, expense type or payment method isn't valid, instead of a clearer error. Check values with the `reference` tools.
- **Closed accounting years can't be edited**, and expenses can only be created in an open period (`crunch_list_accounting_periods`).
- **Reconciled payments can't be removed**, and reconciled expenses can't be changed or deleted.
- **Issued invoices are final.** They're corrected with credit notes in Crunch, not edited or deleted.

## Not covered

These parts of Crunch aren't available through the public API, or aren't covered by crunch-mcp yet:

- Bank statement entries, bank feeds and reconciliation (only payments recorded against an account are visible)
- VAT returns, Self Assessment, Corporation Tax and other filings
- Payroll, dividends and director's loan account details beyond what expenses show
- Credit notes, quotes and classic invoices
- Journals, reports and the accounts themselves (profit and loss, balance sheet)

If the public API gains an endpoint for something here, contributions are welcome. The `raw` tool can reach any public endpoint in the meantime.

## Running crunch-mcp

| Component | Requirement |
|---|---|
| Docker image | `linux/amd64` or `linux/arm64` (for example Raspberry Pi 4/5) |
| Go (to build without Docker) | 1.25 or later |
| Operating system for the binary | Linux and macOS. Windows should work, but isn't tested. |
| Network | Outbound HTTPS to `public-api.crunch.co.uk` and `oauth.crunch.co.uk` |
| MCP clients | Any client supporting Streamable HTTP or stdio. See [Connecting MCP clients](clients.md). |

The server is built on the official [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) and supports tools, prompts, tool annotations, and image and embedded-resource content in tool results.

## Help improve this page

Found an API limit, an error or a gap that isn't listed here? [Open an issue](https://github.com/mattoddie/crunch-mcp/issues/new/choose) or start a [discussion](https://github.com/mattoddie/crunch-mcp/discussions). Please remove any private or financial details first.
