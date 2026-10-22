# Connecting MCP clients

crunch-mcp supports both standard MCP transports:

| Transport | How it runs | Best for |
|---|---|---|
| **Streamable HTTP** (the default in the image) | One long-running container. Clients connect to `http://<host>:8080/mcp`. | Most setups. One server can be shared by several clients, sessions and machines. |
| **stdio** (the default for the binary) | The client starts the server for each session, with `docker run -i` or the local `crunch-mcp` binary. | A single machine and a single session at a time, no network service, or clients that only support stdio. |

The HTTP endpoint is **stateless**: each request is handled on its own, so you can restart the container at any time. When `MCP_AUTH_TOKEN` is set, every request must send `Authorization: Bearer <token>`.

In the examples below, replace `<docker-host>` with the machine running the container and `<token>` with your `MCP_AUTH_TOKEN`. The server is called `crunch` in every example. If you use a different name, the prompt slash commands change to match.

- [Claude Code](#claude-code)
- [Claude Desktop](#claude-desktop)
- [VS Code (GitHub Copilot)](#vs-code-github-copilot)
- [Cursor](#cursor)
- [OpenAI Codex CLI](#openai-codex-cli)
- [Other clients](#other-clients)
- [stdio mode](#stdio-mode)

## Claude Code

```sh
claude mcp add --transport http crunch http://<docker-host>:8080/mcp \
  --header "Authorization: Bearer <token>"
```

Add `--scope user` to make it available in every project, or `--scope project` to save it in a `.mcp.json` you can share. Don't commit the token: in `.mcp.json`, reference an environment variable instead:

```json
{
  "mcpServers": {
    "crunch": {
      "type": "http",
      "url": "http://<docker-host>:8080/mcp",
      "headers": { "Authorization": "Bearer ${CRUNCH_MCP_TOKEN}" }
    }
  }
}
```

Run `claude mcp list` to check the connection, and `/mcp` inside Claude Code to see the tools. The built-in prompts are available as slash commands, such as `/mcp__crunch__business_health_check` or `/mcp__crunch__period_summary 2026-07-01 2026-09-30`.

To use stdio instead, with Docker or the local binary:

```sh
# Docker
claude mcp add crunch -- docker run -i --rm \
  -e MCP_TRANSPORT=stdio -e CRUNCH_CLIENT_ID=your-client-id \
  -v crunch-data:/data ghcr.io/mattoddie/crunch-mcp:latest

# The local binary (see "Running without Docker" in the deployment guide)
claude mcp add crunch -e CRUNCH_CLIENT_ID=your-client-id -- crunch-mcp
```

Read the notes under [stdio mode](#stdio-mode) first, especially about running more than one session at a time.

## Claude Desktop

The easiest way is the **extension bundle**: a single `.mcpb` file that installs crunch-mcp into Claude Desktop with a settings form. It needs no Docker, no service and no `MCP_AUTH_TOKEN`, because Claude Desktop runs the server itself over stdio.

### Install the extension

1. Open the [latest release](https://github.com/mattoddie/crunch-mcp/releases/latest) and download the bundle for your computer:

   | Computer | Bundle |
   |---|---|
   | Mac with Apple silicon (M1 and later) | `crunch-mcp-<version>-darwin-arm64.mcpb` |
   | Mac with an Intel processor | `crunch-mcp-<version>-darwin-amd64.mcpb` |
   | Windows on x64 (most PCs) | `crunch-mcp-<version>-windows-amd64.mcpb` |
   | Windows on Arm | `crunch-mcp-<version>-windows-arm64.mcpb` |

   There are Linux bundles too (`linux-amd64`, `linux-arm64`) for other clients that install MCP Bundles.

2. Double-click the file, or drag it onto **Settings → Extensions** in Claude Desktop, and click **Install**.
3. Fill in the settings and turn the extension on. The tools appear under the tools (🔨) menu, and the prompts under the **+** menu. You can allow or block each tool separately there.
4. Ask Claude to *"log in to Crunch"*. It uses `crunch_auth_start` and gives you a link. After you click **Allow**, the page it lands on fails to load: paste its address into the chat and Claude finishes the login with `crunch_auth_complete`. The tokens are saved in `~/.config/crunch-mcp/tokens.json`.

To update, install the bundle from a newer release the same way.

### Settings

| Setting | Variable | What to enter |
|---|---|---|
| **Client ID** (required) | `CRUNCH_CLIENT_ID` | The client ID from your Crunch API onboarding. See [Getting started](getting-started.md#1-get-api-access). |
| **Client secret** | `CRUNCH_CLIENT_SECRET` | Only for confidential clients. Leave empty for a public client. |
| **Redirect URI** | `CRUNCH_REDIRECT_URI` | Only if your API account has a redirect URI registered with Crunch. Empty means `http://127.0.0.1:8976/callback`. |
| **Refresh token** | `CRUNCH_REFRESH_TOKEN` | Optional: start from a refresh token instead of logging in. Don't use it anywhere else afterwards. |
| **Allow changes** | `CRUNCH_ALLOW_WRITES` | Off by default, which keeps the assistant read-only. Read [Security](security.md) before turning it on, and log in again afterwards so the login includes the extra scopes. |
| **Allow deletes** | `CRUNCH_ALLOW_DELETES` | Also allows deleting records and removing payments. Needs **Allow changes**. |
| **Allow emailing customers** | `CRUNCH_ALLOW_EMAIL` | Also allows emailing invoices and turning on payment reminders. Needs **Allow changes**. |
| **Toolsets** | `CRUNCH_TOOLSETS` | Optional, such as `account,reference,expenses,suppliers,banking`. Empty means all, which is 31 KB of the model's context read-only and up to 79 KB with everything allowed. See [Keeping the tool list small](configuration.md#keeping-the-tool-list-small). |
| **Files folder** | `CRUNCH_FILES_DIR` | Optional folder Claude may read receipts from (`file_path`) and save downloads to (`save_to`). Off when empty. |
| **Audit log file** | `CRUNCH_AUDIT_LOG` | Optional full path to a file, such as `/Users/you/crunch-mcp-audit.jsonl`, that records every change Claude makes or previews. The folder must exist; the file is created. `stderr` writes the entries to the extension's log instead. Off when empty. See [Audit log](configuration.md#audit-log). |

Settings you leave empty use their defaults, as described in [Configuration](configuration.md).

### Without the extension

You can also add crunch-mcp to Claude Desktop's configuration file yourself, using [stdio mode](#stdio-mode). Open **Settings → Developer → Edit Config** and add:


```json
{
  "mcpServers": {
    "crunch": {
      "command": "docker",
      "args": [
        "run", "-i", "--rm",
        "-e", "MCP_TRANSPORT=stdio",
        "-e", "CRUNCH_CLIENT_ID", "-e", "CRUNCH_CLIENT_SECRET",
        "-v", "crunch-data:/data",
        "ghcr.io/mattoddie/crunch-mcp:latest"
      ],
      "env": {
        "CRUNCH_CLIENT_ID": "your-client-id",
        "CRUNCH_CLIENT_SECRET": ""
      }
    }
  }
}
```

Or, with the local binary:

```json
{
  "mcpServers": {
    "crunch": {
      "command": "/usr/local/bin/crunch-mcp",
      "env": { "CRUNCH_CLIENT_ID": "your-client-id" }
    }
  }
}
```

Restart Claude Desktop. The tools appear under the tools (🔨) menu, and the prompts appear under **+ → Add from crunch**. If you haven't logged in yet, ask Claude to *"log in to Crunch"*: it uses `crunch_auth_start` and `crunch_auth_complete`, and you paste the redirected URL back into the chat.

To turn on writes, add `"-e", "CRUNCH_ALLOW_WRITES=true"` to `args` (or `"CRUNCH_ALLOW_WRITES": "true"` to `env` for the binary), then log in again so the login includes the extra scopes.

## VS Code (GitHub Copilot)

Create `.vscode/mcp.json` in your workspace, or run **MCP: Add Server** from the command palette:

```json
{
  "inputs": [
    { "id": "crunch-token", "type": "promptString", "description": "crunch-mcp token", "password": true }
  ],
  "servers": {
    "crunch": {
      "type": "http",
      "url": "http://<docker-host>:8080/mcp",
      "headers": { "Authorization": "Bearer ${input:crunch-token}" }
    }
  }
}
```

VS Code asks for the token once and stores it securely. Use the tools from Copilot Chat in **Agent** mode.

## Cursor

Add to `~/.cursor/mcp.json` for all projects, or `.cursor/mcp.json` for one project:

```json
{
  "mcpServers": {
    "crunch": {
      "url": "http://<docker-host>:8080/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

## OpenAI Codex CLI

Add to `~/.codex/config.toml`:

```toml
[mcp_servers.crunch]
url = "http://<docker-host>:8080/mcp"
bearer_token_env_var = "CRUNCH_MCP_TOKEN"
```

Then `export CRUNCH_MCP_TOKEN=<token>` before running `codex`.

## Other clients

Any client that supports MCP **Streamable HTTP** works. Point it at `http://<docker-host>:8080/mcp` and send the bearer token header.

Clients that install servers from the [official MCP Registry](https://registry.modelcontextprotocol.io/v0/servers?search=io.github.mattoddie/crunch-mcp) can find crunch-mcp there as `io.github.mattoddie/crunch-mcp`. The entry offers the Docker image in [stdio mode](#stdio-mode), with the `crunch-data` token volume and the same settings as the [extension bundle](#claude-desktop), and the bundles themselves.

If your client only supports stdio, you have two options. You can use [stdio mode](#stdio-mode), or you can keep the shared HTTP server and connect through a stdio-to-HTTP bridge such as [`mcp-remote`](https://www.npmjs.com/package/mcp-remote):

```json
{
  "command": "npx",
  "args": ["-y", "mcp-remote", "http://<docker-host>:8080/mcp", "--header", "Authorization: Bearer <token>"]
}
```

Some clients limit how many tools they accept, or slow down with a lot of tools. If yours does, narrow `CRUNCH_TOOLSETS`. See [Configuration](configuration.md#choosing-toolsets).

## stdio mode

In stdio mode the MCP client runs the server itself and talks to it over stdin/stdout. No port is opened, and no `MCP_AUTH_TOKEN` is needed because only the client can reach the server.

With Docker, the general shape is:

```sh
docker run -i --rm \
  -e MCP_TRANSPORT=stdio \
  -e CRUNCH_CLIENT_ID=your-client-id \
  -v crunch-data:/data \
  ghcr.io/mattoddie/crunch-mcp:latest
```

To log in for stdio use, run the login command against the same volume once:

```sh
docker run -it --rm -p 127.0.0.1:8976:8976 \
  -e CRUNCH_CLIENT_ID=your-client-id \
  -v crunch-data:/data \
  ghcr.io/mattoddie/crunch-mcp:latest login
```

With the local binary, run `crunch-mcp login` with the same environment instead. The tokens go to `~/.config/crunch-mcp/tokens.json`. You can also log in from the chat with the `crunch_auth_start` tool.

Tips:

- **Always mount the token volume** (`-v crunch-data:/data`). Without it, each session starts with an empty `/data`, the login is lost when the container exits, and a rotated refresh token is thrown away.
- **Use the same volume name everywhere.** Docker Compose prefixes volume names with the project name, so the volume from the [compose setup](deployment.md#docker-compose) is usually `crunch-mcp_crunch-data`, not `crunch-data`. Run `docker volume ls` to check.
- **Share one token file between sessions.** Each stdio session is a separate server process. Sessions that use the same token file (the same volume, or the same `~/.config/crunch-mcp/tokens.json`) take turns to refresh, using a lock file next to it, and pick up each other's new tokens. Sessions with *separate* token files that started from the same login can't coordinate, and Crunch revokes the whole login when an old refresh token is reused. If you use several clients at once, the [HTTP server](#claude-code) is still the simplest option.
- **Keep secrets out of the client config.** Pass `-e NAME` without a value so Docker copies the variable from the client's environment, as in the Claude Desktop example. You can also use `--env-file /path/to/crunch.env`.
- **Always pass `-i` and never `-t`** when serving. A TTY corrupts the MCP message stream. (The `login` command is the exception: it needs `-it` so you can paste the URL.)
- **Docker on another machine** works too. Set `DOCKER_HOST` in the client's `env`. The volume lives on that machine.
- **Pin a version** (for example `:1.2.3`) so a new `latest` doesn't surprise you. See [Deployment](deployment.md#image-tags).
