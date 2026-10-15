# Getting started

This guide takes you from nothing to asking your first question about your Crunch account. Once Crunch has given you API access, it takes about fifteen minutes.

- [What you need](#what-you-need)
- [1. Get API access](#1-get-api-access)
- [2. Set up the server](#2-set-up-the-server)
- [3. Log in to Crunch](#3-log-in-to-crunch)
- [4. Start the server](#4-start-the-server)
- [5. Connect your assistant](#5-connect-your-assistant)
- [6. Ask something](#6-ask-something)
- [Next steps](#next-steps)

## What you need

- **A Crunch account.** Any account you can log in to at [crunch.co.uk](https://www.crunch.co.uk) works.
- **Crunch API access**, which gives you a client ID. See the next step.
- **A machine that runs Docker.** This could be a NAS, a home server, a small VM or your laptop. The image supports both `amd64` and `arm64`. If you'd rather not use Docker, there's a single binary too. See [Running without Docker](deployment.md#running-without-docker).
- **A web browser** for logging in to Crunch once.
- **An MCP client**, such as [Claude Code](https://claude.com/claude-code), Claude Desktop, VS Code, Cursor or Codex.

## 1. Get API access

crunch-mcp talks to Crunch's **public API**, which uses OAuth 2.0: you approve crunch-mcp in your browser, and Crunch gives it tokens to act on your account. To take part, Crunch needs to know about your "app" first.

1. Read Crunch's [authentication and OAuth guide](https://developer.crunch.co.uk/authentication-and-oauth/) and [FAQs](https://developer.crunch.co.uk/faqs/). They link to the **API application form**.
2. Fill in the form. If it asks for a redirect URI, give `http://127.0.0.1:8976/callback`, which is what crunch-mcp uses by default.
3. Crunch replies with your credentials:
   - a **client ID**, which everyone gets, and
   - a **client secret**, but only if you're set up as a *confidential* client. *Public* clients have no secret.

crunch-mcp works with both. It always uses PKCE (a one-time code that proves the login was started by the same program that finishes it), and with a client secret it also authenticates to Crunch with it.

> [!NOTE]
> Crunch has no sandbox or test environment. Every request goes to a real Crunch account. If you want to experiment without touching your real books, sign up for a free Crunch account and use that.

## 2. Set up the server

Create a folder and download the compose file and the example settings:

```sh
mkdir crunch-mcp && cd crunch-mcp
curl -fsSLO https://raw.githubusercontent.com/mattoddie/crunch-mcp/main/compose.yaml
curl -fsSL https://raw.githubusercontent.com/mattoddie/crunch-mcp/main/example.env -o .env
```

Edit `.env` and set at least:

```sh
CRUNCH_CLIENT_ID=your-client-id
CRUNCH_CLIENT_SECRET=                    # only if Crunch gave you one; otherwise leave it empty
MCP_AUTH_TOKEN=a-long-random-string      # required; generate one with: openssl rand -hex 32
```

Leave everything else for now. The server starts **read-only**, which is the right way to begin.

The compose file keeps the login in a Docker volume called `crunch-data`, mounted at `/data`. Keep it: Crunch changes the refresh token every time it's used, so the server must be able to save the new one. See [How login works](configuration.md#how-login-works).

## 3. Log in to Crunch

Run the login command once:

```sh
docker compose run --rm -p 127.0.0.1:8976:8976 crunch-mcp login
```

It prints a long URL and waits:

```text
Open this URL in a browser, log in to Crunch and click Allow:

  https://oauth.crunch.co.uk/authorize?client_id=…

Scopes: read:all offline_access
Waiting for the redirect to http://127.0.0.1:8976/callback ...
If the browser shows an error page instead, copy the full URL from its address bar and paste it here:
>
```

Open the URL, log in to Crunch if asked, check the permissions and click **Allow**. What happens next depends on where your browser is:

- **The browser is on the Docker host.** Crunch sends it to `http://127.0.0.1:8976/callback`, the container catches it, and the page says *"Thanks!"*.
- **The browser is on another machine**, for example when Docker runs on a home server. The page after **Allow** fails to load, because nothing on *your* machine is listening on port 8976. That's expected. Copy the whole URL from the address bar (it starts with `http://127.0.0.1:8976/callback?code=`) and paste it into the terminal.

Either way, the command finishes with something like:

```text
Logged in to Crunch as Sam Smith (sam@example.com).
Tokens saved to /data/tokens.json (scope: read:all offline_access).
```

The authorisation code in that URL is single-use and only works with this login attempt, but don't share it anyway.

> [!TIP]
> There are two other ways to log in. You can run the `crunch-mcp` binary on your own computer with `crunch-mcp login`, or you can let the assistant do it with the `crunch_auth_start` and `crunch_auth_complete` tools: it gives you the URL, and you paste the redirected URL back into the chat. [How login works](configuration.md#how-login-works) explains all three.

## 4. Start the server

```sh
docker compose up -d
docker compose logs -f crunch-mcp
```

You should see lines like these:

```text
crunch-mcp 1.0.0: https://public-api.crunch.co.uk/v1, auth token file /data/tokens.json, writes disabled, toolsets 9, transport http
crunch-mcp listening on http://0.0.0.0:8080/mcp (bearer auth enabled)
```

If the first line says `not logged in`, go back to step 3. You can log in again at any time without restarting the server: it picks up the new tokens from the token file.

Check that it's up from another machine:

```sh
curl http://<docker-host>:8080/health
# {"status":"ok"}
```

> [!TIP]
> `/health` only shows that the server is running. It doesn't contact Crunch. To check the login, run `docker compose exec crunch-mcp /crunch-mcp status`, or ask the assistant to call `crunch_auth_status`. Step 6 is the real test.

## 5. Connect your assistant

For **Claude Code**:

```sh
claude mcp add --transport http crunch http://<docker-host>:8080/mcp \
  --header "Authorization: Bearer <MCP_AUTH_TOKEN>"
```

Run `claude mcp list` to check that `crunch` shows as connected. For other clients, see [Connecting MCP clients](clients.md).

## 6. Ask something

Start a new conversation and try:

- *"Give me an overview of my business in Crunch."*
- *"Which sales invoices are unpaid, and which are overdue?"*
- *"What did I spend on travel this year?"*

The assistant calls tools like `crunch_get_overview` and `crunch_list_invoices` and summarises the results. In Claude Code you can also run the built-in prompts as slash commands, for example `/mcp__crunch__business_health_check`.

If something goes wrong, the tool's error message usually says what to fix. [Troubleshooting](troubleshooting.md) covers the common errors.

## Next steps

- **Let it make changes.** Set `CRUNCH_ALLOW_WRITES=true` and log in again so the login includes the extra scopes. Read [Security](security.md) first, and see [Turning on writes](configuration.md#turning-on-writes).
- **Expose fewer tools** with `CRUNCH_TOOLSETS` for a faster, more focused assistant. See [Configuration](configuration.md#choosing-toolsets).
- **Put it behind HTTPS** if it's reachable outside your LAN. See [Deployment](deployment.md#tls-with-a-reverse-proxy).
- **Learn what it can do** in [Using crunch-mcp](usage.md) and the [tool reference](tools.md).
