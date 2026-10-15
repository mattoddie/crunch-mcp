# Security policy

crunch-mcp has access to company accounts, bank balances and customer data, so security reports are taken seriously and handled as a priority.

## Supported versions

Security fixes go into the latest release. Please upgrade to the newest version before reporting.

| Version | Supported |
|---|---|
| Latest release | ✅ |
| Older releases | ❌ |

## Reporting a vulnerability

**Please don't report security vulnerabilities in public issues, discussions or pull requests.**

Report them privately through GitHub's **[private vulnerability reporting](https://github.com/mattoddie/crunch-mcp/security/advisories/new)**: go to the repository's **Security** tab and click **Report a vulnerability**.

Please include:

- A description of the issue and its impact
- Steps to reproduce, or a proof of concept
- The affected version(s) and configuration, such as transport, write/delete/email settings and toolsets
- Any suggested fix, if you have one

Please don't include real tokens, client secrets or financial data. Use a test account.

## What to expect

- An acknowledgement within **3 working days**.
- An initial assessment within **7 days**, including whether the report is accepted and a rough timeline.
- A fix released as soon as practical, with a GitHub security advisory and credit to you, unless you'd rather stay anonymous.

Please give us a reasonable amount of time to fix the issue before you disclose it publicly.

## Scope

In scope:

- Bypassing `MCP_AUTH_TOKEN`, `MCP_ALLOWED_HOSTS`, or the write, delete, email or toolset gating. Examples would be performing a write or delete when it's disabled, or emailing an invoice through the raw API tool without `CRUNCH_ALLOW_EMAIL`.
- Leaking access or refresh tokens, the client secret, or the PKCE verifier, through tool output, logs or files
- Weaknesses in the OAuth flow, such as accepting a code from a login this server didn't start, or handling rotating refresh tokens in a way that loses or exposes them
- Reading or writing files outside what `file_path` and `save_to` are meant to allow
- Vulnerabilities in the Docker image or the release pipeline

Out of scope:

- Issues that need an attacker who already holds a valid `MCP_AUTH_TOKEN` and uses only the tools that configuration allows. That's intended behaviour.
- Prompt injection that leads the model to call tools the configuration allows, for example through a supplier name or a receipt. This is a known limitation of AI assistants. [docs/security.md](docs/security.md) explains how to limit the impact.
- Anything the Crunch login's scopes allow. Choosing settings and scopes is the operator's job.
- Vulnerabilities in Crunch itself or its API. Report those to Crunch directly.

For guidance on deploying crunch-mcp securely, see [docs/security.md](docs/security.md).
