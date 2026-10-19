# Configuration

crunch-mcp is configured entirely through environment variables. With Docker Compose, put them in `.env`. Copy [`example.env`](../example.env) to get started.

- [Crunch API and login](#crunch-api-and-login)
- [How login works](#how-login-works)
- [What the assistant may do](#what-the-assistant-may-do)
- [Turning on writes](#turning-on-writes)
- [MCP transport](#mcp-transport)
- [Docker secrets (`_FILE` variables)](#docker-secrets-_file-variables)
- [Choosing toolsets](#choosing-toolsets)
- [Example configurations](#example-configurations)

The server checks the configuration at startup. If something is wrong, it exits with a clear message such as `CRUNCH_ALLOW_DELETES=true also requires CRUNCH_ALLOW_WRITES=true`. If the container keeps restarting, check `docker compose logs`. The server starts without a login, though: tools then explain how to log in.

## Crunch API and login

| Variable | Default | Description |
|---|---|---|
| `CRUNCH_CLIENT_ID` | **required to log in** | The client ID Crunch gave you when it approved your API application. |
| `CRUNCH_CLIENT_SECRET` | | Only for *confidential* clients, which Crunch issues a secret. Leave it empty for *public* clients. With a secret, crunch-mcp authenticates to Crunch's token endpoint with HTTP Basic. Either way it uses PKCE. |
| `CRUNCH_REDIRECT_URI` | `http://127.0.0.1:8976/callback` | Where Crunch sends the browser after you click **Allow**. If your API account has a redirect URI registered with Crunch, this must match it exactly. A loopback address must use `http://`. A URI without a path, such as `http://127.0.0.1:8976`, is caught at `/`. |
| `CRUNCH_SCOPES` | follows the settings below | Space-separated OAuth scopes to ask for at login. The default is `read:all`, plus `create:all update:all` with writes, `delete:all` with deletes, and `offline_access` for a refresh token. Set it to, for example, `all offline_access` to ask for everything. |
| `CRUNCH_TOKEN_FILE` | `~/.config/crunch-mcp/tokens.json` (`/data/tokens.json` in the image) | Where tokens are saved. It must be writable and must survive restarts. In Docker, keep a volume mounted at `/data`. If `XDG_CONFIG_HOME` is set, the default is `$XDG_CONFIG_HOME/crunch-mcp/tokens.json`. |
| `CRUNCH_REFRESH_TOKEN` | | A refresh token to start from, instead of logging in. It's saved to the token file and replaced as it rotates. See [Starting from a refresh token](#starting-from-a-refresh-token). |
| `CRUNCH_ACCESS_TOKEN` | | A fixed access token, used until Crunch rejects it. Handy for a quick test. It lasts about an hour, and after that the server needs a refresh token or a login. |
| `CRUNCH_LOGIN_LISTEN` | the redirect URI's host and port, such as `127.0.0.1:8976` (`0.0.0.0:8976` in the image) | The address `crunch-mcp login` listens on to catch the redirect. The image sets `0.0.0.0:8976` so a published port reaches the container: publish it on loopback only, with `-p 127.0.0.1:8976:8976`. The listener only accepts the redirect for the login it started (the `state` must match), and stops when the login finishes. |
| `CRUNCH_API_URL` | `https://public-api.crunch.co.uk/v1` | The Crunch public API. You shouldn't need to change it. Must be `https://` (plain `http://` only for a loopback host, such as a local mock), without a user name or password in it. |
| `CRUNCH_AUTH_URL` | `https://oauth.crunch.co.uk` | The Crunch OAuth server (with `/authorize` and `/token`). You shouldn't need to change it. The same `https://` rule applies: the client secret and refresh tokens are sent here. |
| `CRUNCH_TIMEOUT_MS` | `30000` | Timeout for each request to Crunch, in milliseconds. |

## How login works

Crunch's public API uses the OAuth 2.0 **authorisation code** flow. In short:

1. crunch-mcp builds a URL on Crunch's OAuth server with your client ID, the scopes it wants and a PKCE challenge.
2. You open it in a browser, log in to Crunch and click **Allow**.
3. Crunch sends the browser to the redirect URI with a one-time **code**.
4. crunch-mcp swaps the code for an **access token** (valid for about an hour) and a **refresh token** (valid for 60 days), and saves them to the token file.
5. From then on, it uses the access token for API calls and quietly gets a new one with the refresh token when it runs out.

You do steps 2 and 3. There are three ways to start the flow and hand back the code:

| Method | How | Good for |
|---|---|---|
| `crunch-mcp login` on your computer | Run the binary in a terminal. It listens on `127.0.0.1:8976`, so the browser's redirect comes straight back to it. | Running the binary locally, or with stdio clients. |
| `crunch-mcp login` in Docker | `docker compose run --rm -p 127.0.0.1:8976:8976 crunch-mcp login`. The container listens on port 8976, published only on the Docker host's loopback address. If your browser is on another machine, the redirect page fails to load. Copy the URL from the address bar and paste it into the terminal. | The compose setup. |
| From the assistant | Ask it to *"log in to Crunch"*. It calls `crunch_auth_start`, which gives you the URL. After **Allow**, the redirect page fails to load (the running server doesn't listen on 8976). Paste the URL from the address bar into the chat, and it calls `crunch_auth_complete`. | Logging in, or switching account, without a terminal. The new tokens are used straight away. |

The `login` command waits up to 15 minutes, and a login started with `crunch_auth_start` must be finished within 15 minutes by the same running server.

> [!NOTE]
> A running server notices when the token file changes, so you can log in again with the `login` command without restarting it. Logging in with the assistant doesn't need a restart either.

You don't need to register the redirect URI with Crunch unless your API account already has one set. In that case, `CRUNCH_REDIRECT_URI` must match it exactly, or Crunch rejects the login with an `invalid_redirect_uri` error. If it isn't a loopback address, `crunch-mcp login` doesn't listen for it, and you paste the URL instead.

### Scopes

Crunch's scopes are either `all`, or one per kind of operation: `read:all`, `create:all`, `update:all` and `delete:all`. `offline_access` adds a refresh token, so the server stays logged in for more than an hour.

crunch-mcp asks only for what your settings need:

| Settings | Scopes requested |
|---|---|
| Read-only (default) | `read:all offline_access` |
| `CRUNCH_ALLOW_WRITES=true` | `read:all create:all update:all offline_access` |
| `+ CRUNCH_ALLOW_DELETES=true` | `read:all create:all update:all delete:all offline_access` |

Scopes are fixed when you log in. After you turn on writes or deletes, [log in again](#turning-on-writes) so the new login includes them. `crunch_auth_status` and `crunch-mcp status` show the scopes the current login has.

### Refresh tokens rotate

For API applications created since 14 January 2026, Crunch **rotates** refresh tokens. Every refresh returns a new refresh token, and if an old one is ever used again, Crunch treats it as stolen and **revokes the whole login**. crunch-mcp is built around this:

- It writes the new refresh token to the token file **before** it uses the new access token, so a crash or restart never loses it. The file is replaced atomically and is readable only by its owner (mode `0600`).
- It never runs two refreshes at once. Requests that arrive together wait for one refresh and share its result. Processes that share the token file (a server and `crunch-mcp login`, or two stdio sessions) take turns too: each holds an exclusive lock on `<token file>.lock` while it refreshes, and one that waited picks up the tokens the other saved instead of spending the old refresh token again.
- If it can't save the file (for example, a read-only filesystem without a volume), it keeps working from memory and warns in the log and in `crunch_auth_status`. A restart would then need a new login.

That leads to two rules:

- **The token file must persist.** In Docker, keep a volume at `/data`.
- **One token file per login.** Processes that share a token file on the same machine coordinate through the lock file (the lock may not work on network filesystems such as NFS). But two servers with *separate* token files that were started from the same refresh token or login will eventually reuse an old refresh token and get logged out. If you need servers on different machines, log each one in separately with its own token file.

A refresh token lasts 60 days. If the server isn't used for that long, or Crunch revokes the login, tools say so and you log in again.

### Starting from a refresh token

If you already have a refresh token, for example from another tool, set `CRUNCH_REFRESH_TOKEN` instead of logging in. The server saves it to the token file on first use and follows its rotations from there. The variable can stay set: the token file remembers which `CRUNCH_REFRESH_TOKEN` it descends from, so a stale value doesn't override newer tokens. If you put a *different* refresh token in the variable, it takes over from what's in the file.

Don't use the same refresh token anywhere else afterwards. Its first use by crunch-mcp makes it stale.

## What the assistant may do

| Variable | Default | Description |
|---|---|---|
| `CRUNCH_ALLOW_WRITES` | `false` | Register tools that create or change records: expenses, payments, attachments, invoices (including issuing them), clients, suppliers, recurring expenses, bank accounts and credit cards. |
| `CRUNCH_ALLOW_DELETES` | `false` | Also register tools that delete records or remove payments. Without it, updates (including `changes` and raw `PUT` requests) must keep a record's existing payments and attachments, because Crunch deletes whatever an update leaves out. Requires `CRUNCH_ALLOW_WRITES=true`. |
| `CRUNCH_ALLOW_EMAIL` | `false` | Also register `crunch_email_invoice`, which emails an invoice to a customer from Crunch, and allow turning on Crunch's payment reminders (`send_reminders`, or `sendReminders` in `extra`, `changes` or raw requests), which are emails too. Requires `CRUNCH_ALLOW_WRITES=true`. |
| `CRUNCH_FILES_DIR` | (unset: disabled) | A directory the server may read attachments from (`file_path`) and save downloads to (`save_to`). Paths are relative to it or absolute inside it, and may not lead out of it through `..` or symlinks. The token file, its lock and temporary files, and anything under `/run/secrets` are always refused. When unset, `file_path` and `save_to` are refused: send attachments as `content_base64` and use the downloaded content instead. In Docker, mount a folder for it, for example `./files:/files` with `CRUNCH_FILES_DIR=/files`. It applies at every access level, since saving a download is local, not a change in Crunch. |
| `CRUNCH_TOOLSETS` | `all` | Comma-separated list of [toolsets](#choosing-toolsets) to expose. |

Boolean variables accept `true`/`false`, `1`/`0`, `yes`/`no` and `on`/`off`. Anything else stops the server with an error.

That gives four access levels. The [tool reference](tools.md) labels every tool with the level it needs:

| Label | Registered when |
|---|---|
| 🟢 read | Always |
| 🟠 write | `CRUNCH_ALLOW_WRITES=true` |
| 🔴 delete | `CRUNCH_ALLOW_WRITES=true` and `CRUNCH_ALLOW_DELETES=true` |
| 🟣 email | `CRUNCH_ALLOW_WRITES=true` and `CRUNCH_ALLOW_EMAIL=true` |

Deletes and email are independent of each other: you can allow either without the other.

Tools that aren't allowed are **not registered at all**. The assistant can't see them, so it can't call them or be talked into calling them. Crunch also checks the login's scopes on every request, so a write fails with `403` if the login only has `read:all`.

## Turning on writes

1. Add the settings to `.env`, for example `CRUNCH_ALLOW_WRITES=true`.
2. Log in again so the login includes the extra scopes, and start the server with the new settings:

   ```sh
   docker compose stop
   docker compose run --rm -p 127.0.0.1:8976:8976 crunch-mcp login
   docker compose up -d
   ```

3. Reconnect or restart your MCP client so it fetches the new tool list.

`CRUNCH_ALLOW_EMAIL` doesn't need a new scope of its own, but it does need writes, so the same steps apply if writes were off before.

## MCP transport

| Variable | Default | Description |
|---|---|---|
| `MCP_TRANSPORT` | `http` in the Docker image (`stdio` for the binary) | `http` for Streamable HTTP, or `stdio`. |
| `MCP_HTTP_HOST` | `127.0.0.1` (`0.0.0.0` in the Docker image) | Address to listen on (HTTP only). The binary listens on loopback only unless you set this; the image listens on all interfaces so a published port reaches it. |
| `MCP_HTTP_PORT` | `8080` | Port to listen on (HTTP only). |
| `MCP_AUTH_TOKEN` | (required for http) | Every request to `/mcp` must send `Authorization: Bearer <token>`. The server refuses to start in http mode without it, or if it is shorter than 16 characters. Generate one with `openssl rand -hex 32`. |
| `MCP_ALLOW_UNAUTHENTICATED` | `false` | Start the http transport without `MCP_AUTH_TOKEN`. Only for a server that sits behind something else that authenticates every request. It also requires `MCP_ALLOWED_HOSTS`, because without a token the Host check is all that stops DNS rebinding; the server logs a warning at startup. |
| `MCP_ALLOWED_HOSTS` | | Comma-separated allow-list of `host` or `host:port` entries, such as `crunch-mcp.lan`. Requests with any other `Host` header are rejected, which protects against DNS-rebinding attacks from web pages in your browser. For the `Host` header, an entry without a port matches any port. Browser requests with an `Origin` header (including an empty one, or `null`) that isn't on the list are rejected too, so other websites can't call the tools. For `Origin`, an entry without a port matches only the scheme's default port (443 or 80); to allow a page on another port, list it as `host:port`, such as `localhost:5173`. Requests without an `Origin` (MCP clients that aren't browsers) are unaffected. |

The HTTP server exposes these endpoints:

| Path | Method | Purpose |
|---|---|---|
| `/mcp` | `POST` | The MCP endpoint (stateless Streamable HTTP, JSON responses). Other methods get `405`. The body must be JSON, and is limited to 16 MiB, enough for attachments sent as base64. |
| `/health`, `/healthz` | `GET` | Liveness check. Returns `{"status":"ok"}` and doesn't contact Crunch. |

## Docker secrets (`_FILE` variables)

Every variable can also be read from a file: set `<NAME>_FILE` to the file's path. Leading and trailing whitespace is trimmed. This works with [Docker secrets](https://docs.docker.com/compose/how-tos/use-secrets/) and Kubernetes secret volumes:

```yaml
services:
  crunch-mcp:
    image: ghcr.io/mattoddie/crunch-mcp:latest
    environment:
      CRUNCH_CLIENT_ID: your-client-id
      CRUNCH_CLIENT_SECRET_FILE: /run/secrets/crunch_client_secret
      MCP_AUTH_TOKEN_FILE: /run/secrets/mcp_auth_token
    secrets: [crunch_client_secret, mcp_auth_token]
    volumes: [crunch-data:/data]
    ports: ["8080:8080"]

secrets:
  crunch_client_secret:
    file: ./secrets/crunch_client_secret.txt
  mcp_auth_token:
    file: ./secrets/mcp_auth_token.txt

volumes:
  crunch-data:
```

If both `NAME` and `NAME_FILE` are set, the file wins. The tokens themselves don't belong in a secret: they change all the time, so they live in `CRUNCH_TOKEN_FILE` on a writable volume.

## Choosing toolsets

Every tool's name, description and argument schema is sent to the model, which uses up context. Exposing only what you need keeps the assistant faster and more focused:

| Toolset | Use it for |
|---|---|
| `account` | Login, company details and the business overview. **Always include it.** |
| `reference` | Expense types, VAT types, payment methods, countries, accounting periods, people. **Include it whenever you allow writes**, so the assistant can look up valid values. |
| `banking` | Bank accounts and credit cards, balances, recorded payments |
| `clients` | Customers you invoice |
| `suppliers` | Who you buy from |
| `expenses` | Expenses, payments and receipts |
| `recurring` | Recurring expenses |
| `invoices` | Sales invoices, payments received, PDFs |
| `raw` | Any API path. Handy, but gives broad access. |

The [tool reference](tools.md) lists the tools in each toolset. A read-only setup registers about half the tools. Some suggested sets:

| Goal | `CRUNCH_TOOLSETS` |
|---|---|
| Overview and reporting | `account,banking,expenses,invoices,clients,suppliers` |
| Recording receipts and bills | `account,reference,expenses,suppliers,banking` |
| Invoicing and credit control | `account,reference,invoices,clients,banking` |
| Everything (default) | `all` |

## Example configurations

**Read-only** (the safest setup):

```sh
CRUNCH_CLIENT_ID=your-client-id
MCP_AUTH_TOKEN=...
```

**Bookkeeping.** The assistant can record expenses, payments and invoices, but can't delete anything or email customers:

```sh
CRUNCH_CLIENT_ID=your-client-id
MCP_AUTH_TOKEN=...
CRUNCH_ALLOW_WRITES=true
CRUNCH_TOOLSETS=account,reference,banking,clients,suppliers,expenses,invoices
```

**Full control**, for a single trusted user:

```sh
CRUNCH_CLIENT_ID=your-client-id
CRUNCH_CLIENT_SECRET=...
MCP_AUTH_TOKEN=...
CRUNCH_ALLOW_WRITES=true
CRUNCH_ALLOW_DELETES=true
CRUNCH_ALLOW_EMAIL=true
```

**A quick test with an access token you already have** (no refresh, so it stops working after about an hour):

```sh
CRUNCH_ACCESS_TOKEN=...
MCP_AUTH_TOKEN=...
```
