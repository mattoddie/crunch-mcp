<div align="center">

<img src="docs/assets/logo.svg" alt="crunch-mcp logo" width="112" height="112">

# crunch-mcp

**Talk to your books.**

An open-source [Model Context Protocol](https://modelcontextprotocol.io) server for **Crunch Accounting**, the UK online accounting service. It lets Claude and other AI assistants read and, if you allow it, update your expenses, receipts, invoices, clients, suppliers and bank accounts, all from one small Docker container.

[![CI](https://github.com/mattoddie/crunch-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/mattoddie/crunch-mcp/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mattoddie/crunch-mcp?sort=semver)](https://github.com/mattoddie/crunch-mcp/releases)
[![Container image](https://img.shields.io/badge/ghcr.io-mattoddie%2Fcrunch--mcp-2496ED?logo=docker&logoColor=white)](https://github.com/mattoddie/crunch-mcp/pkgs/container/crunch-mcp)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

**[crunch-mcp.mattoddie.dev](https://crunch-mcp.mattoddie.dev)**

[Quick start](#quick-start) · [Documentation](https://crunch-mcp.mattoddie.dev/docs/) · [Tool reference](docs/tools.md) · [Contributing](CONTRIBUTING.md)

</div>

---

> **"How's the business looking this month?"**
> *You have £18,240 across your two bank accounts and £312 on the company credit card. Three sales invoices are unpaid, £7,800 in total. INV-0042 to Example Client Ltd (£2,400) is 12 days overdue; the other two aren't due yet. On the spending side, four September expenses have no receipt attached and one bill from Example Hosting Ltd (£96) is still unpaid. Want me to draft a polite chaser for INV-0042, or list the expenses that are missing receipts?*

Ask about your business in plain English. The assistant picks from **70 tools** to look things up and, if you allow it, make changes. Some examples:

- 🩺 *"Give me a health check of the business."*
- 🧾 *"Here's a receipt for a train ticket to Leeds. Record it as an expense paid from the business account."*
- 📬 *"Which invoices are overdue? Draft a chasing email for each customer."*
- 📊 *"Summarise income and spending for the last quarter, with an estimate of the VAT."*
- 🔍 *"Which expenses this year have no receipt attached?"*
- 📝 *"Invoice Example Client Ltd for 5 days of consulting at £450 a day, on 30-day terms."*

## Highlights

- **🐳 Docker-first and tiny.** One image (`ghcr.io/mattoddie/crunch-mcp`) for `linux/amd64` and `linux/arm64`, about 21 MB. It's a single static Go binary on a distroless base, with no shell or package manager, running as a non-root user. It serves Streamable HTTP for a long-running service, or stdio for `docker run -i`. You can also run the binary on its own.
- **🛡️ Safe by default.** The server is read-only unless you turn on writes. Deleting needs a second switch, and emailing invoices to customers needs a third. Tools that aren't allowed aren't registered at all, and destructive tools, such as deletes and issuing an invoice, are labelled so your MCP client asks before running them.
- **🔑 Least-privilege login.** It logs in with Crunch's own OAuth 2.0 flow (with PKCE) and asks only for the scopes your settings need. Tokens never appear in tool output.
- **🔄 Stays logged in.** Crunch rotates refresh tokens, and reusing an old one revokes your login. crunch-mcp saves each new token before using it and never refreshes twice at once, so it keeps working across restarts.
- **✏️ Careful updates.** Changing an expense or invoice fetches the current record and changes only what you asked, so payments and attachments are never dropped by accident.
- **📎 Receipts and PDFs.** Attach receipts and timesheets, download receipts as images the assistant can read, and fetch invoice PDFs.
- **🎛️ Tunable.** Expose only the toolsets you need, which keeps the model's context small and focused.
- **🧪 Tested.** The test suite runs against a mock Crunch API and OAuth server on every build.

## Quick start

You need Docker, a Crunch account, Crunch API access and an MCP client such as [Claude Code](https://claude.com/claude-code).

**1. Get API access.** Apply for access to Crunch's public API through the application form linked from [Crunch's developer documentation](https://developer.crunch.co.uk/authentication-and-oauth/). Crunch sends you a **client ID**, and a client secret if you're set up as a confidential client. There's no sandbox, so a free Crunch account is a good place to try things. [Getting started](docs/getting-started.md#1-get-api-access) has the details.

**2. Set up the server.**

```sh
mkdir crunch-mcp && cd crunch-mcp
curl -fsSLO https://raw.githubusercontent.com/mattoddie/crunch-mcp/main/compose.yaml
curl -fsSL https://raw.githubusercontent.com/mattoddie/crunch-mcp/main/example.env -o .env
# Edit .env: set CRUNCH_CLIENT_ID (and CRUNCH_CLIENT_SECRET if you have one)
# and a random MCP_AUTH_TOKEN (e.g. `openssl rand -hex 32`)
```

**3. Log in to Crunch and start the server.**

```sh
docker compose run --rm -p 8976:8976 crunch-mcp login   # open the URL it prints and click Allow
docker compose up -d
```

If your browser isn't on the Docker host, the page after **Allow** won't load. That's fine: copy the URL from the address bar and paste it into the terminal.

**4. Connect your assistant.**

```sh
claude mcp add --transport http crunch http://<docker-host>:8080/mcp \
  --header "Authorization: Bearer <MCP_AUTH_TOKEN>"
```

Now ask *"How's my business doing?"* in Claude Code.

Using **Claude Desktop, VS Code, Cursor or Codex**? See [Connecting MCP clients](docs/clients.md). Prefer not to run a service? Use [stdio mode](docs/clients.md#stdio-mode) with `docker run -i` or the local binary.

## Turning on changes

Out of the box the assistant can look but not touch. To let it make changes, add these to `.env`:

```sh
CRUNCH_ALLOW_WRITES=true    # create and update expenses, invoices, clients, suppliers, payments, attachments…
CRUNCH_ALLOW_DELETES=true   # also allow deleting records and removing payments (needs writes)
CRUNCH_ALLOW_EMAIL=true     # also allow emailing invoices to customers (needs writes)
CRUNCH_TOOLSETS=account,reference,expenses,invoices,clients,suppliers   # optional: expose fewer tools
```

The login needs matching scopes, so **log in again** after turning on writes or deletes:

```sh
docker compose stop
docker compose run --rm -p 8976:8976 crunch-mcp login
docker compose up -d
```

Read [Security](docs/security.md) before turning on writes for your real books.

## What it can do

| Toolset | What's covered |
|---|---|
| `account` | Login status and the OAuth login flow, the logged-in user, company details (VAT number and scheme, addresses) and a one-call business overview |
| `reference` | The values Crunch expects: expense types, VAT types, payment methods, countries, open accounting periods, directors and employees |
| `banking` | Bank accounts and credit cards: balances, details, payments recorded against them, opening balances, default accounts, archiving |
| `clients` | Clients (the customers you invoice): list, look up, create, update and delete |
| `suppliers` | Suppliers: list, look up, create, update, archive and delete |
| `expenses` | Expenses: list and filter, totals, create with lines, payments and receipts, update, record and remove payments, attach and download receipts, delete |
| `recurring` | Recurring expenses that Crunch creates on a schedule: list, create, pause or change, delete |
| `invoices` | Sales invoices: list and filter, totals, create drafts, update, issue, email to the customer, record and remove payments, attachments, PDF download, delete |
| `raw` | An escape hatch that calls any Crunch public API endpoint |

See the **[tool reference](docs/tools.md)** for every tool and argument. Ready-made prompts like `business_health_check`, `record_receipt` and `chase_overdue_invoices` chain the tools together for common jobs.

## Documentation

The docs are also published at **[crunch-mcp.mattoddie.dev/docs](https://crunch-mcp.mattoddie.dev/docs/)**, with search.

| | |
|---|---|
| 🚀 [Getting started](docs/getting-started.md) | Step-by-step setup, from API access to first question |
| 🔌 [Connecting MCP clients](docs/clients.md) | Claude Code, Claude Desktop, VS Code, Cursor, Codex, stdio mode |
| ⚙️ [Configuration](docs/configuration.md) | Every environment variable, how login and tokens work, Docker secrets, toolsets |
| 🐳 [Deployment](docs/deployment.md) | Compose, image tags, upgrades, TLS reverse proxy, hardening, running without Docker |
| 💬 [Using crunch-mcp](docs/usage.md) | Example conversations, prompts, how changes, receipts and PDFs work |
| 🧰 [Tool reference](docs/tools.md) | Every tool and prompt, generated from the code |
| 🛡️ [Security](docs/security.md) | Threat model, least privilege, token storage, safe write setups |
| 📡 [Compatibility](docs/compatibility.md) | The Crunch API, its limits, and known gaps |
| 🩹 [Troubleshooting](docs/troubleshooting.md) | Common errors and how to fix them |
| 🛠️ [Development](docs/development.md) | Architecture, adding a tool, testing |

## Contributing

Contributions are very welcome, whether that's bug reports, docs or new tools. Read the [contributing guide](CONTRIBUTING.md) to get started, and please follow the [code of conduct](CODE_OF_CONDUCT.md). To report a vulnerability, see the [security policy](SECURITY.md). Please don't open a public issue for one.

## License

[MIT](LICENSE) © Matt Oddie

*crunch-mcp is an independent community project. It is not affiliated with, endorsed by or sponsored by Crunch (E-Crunch Ltd). "Crunch" is a trademark of E-Crunch Ltd.*
