# Troubleshooting

Start with the logs: `docker compose logs crunch-mcp`. Configuration errors stop the server at startup with a clear message. Crunch errors show up in the tool results the assistant sees, and you can usually ask it *"what error did you get?"*. To check the login, run `docker compose exec crunch-mcp /crunch-mcp status` or ask the assistant to call `crunch_auth_status`.

- [The container keeps restarting](#the-container-keeps-restarting)
- [The client can't connect to the server](#the-client-cant-connect-to-the-server)
- [Logging in](#logging-in)
- ["Not logged in" and expired logins](#not-logged-in-and-expired-logins)
- [Permission errors (`403`)](#permission-errors-403)
- [Errors when creating or changing records](#errors-when-creating-or-changing-records)
- [Attachments and downloads](#attachments-and-downloads)
- [Tools are missing](#tools-are-missing)
- [Still stuck?](#still-stuck)

## The container keeps restarting

Run `docker compose logs crunch-mcp`. The last line names the problem, for example:

| Message | Fix |
|---|---|
| `CRUNCH_ALLOW_DELETES=true also requires CRUNCH_ALLOW_WRITES=true` | Turn on writes too, or turn deletes off. The same applies to `CRUNCH_ALLOW_EMAIL`. |
| `CRUNCH_ALLOW_WRITES must be true or false, got "…"` | Fix the value. Booleans accept `true`/`false`, `1`/`0`, `yes`/`no` and `on`/`off`. |
| `unknown toolset "x" in CRUNCH_TOOLSETS; valid: …` | Fix the typo. The message lists the valid names. |
| `MCP_TRANSPORT must be stdio or http, got "…"` | Fix the value. |
| `MCP_AUTH_TOKEN must be at least 16 characters` | Use a longer random value: `openssl rand -hex 32`. |
| `MCP_ALLOW_UNAUTHENTICATED=true also requires MCP_ALLOWED_HOSTS …` | Set `MCP_ALLOWED_HOSTS` to the hostnames your clients (or the proxy in front) use, or set `MCP_AUTH_TOKEN` instead. |
| `MCP_AUTH_TOKEN is required for the http transport …` | Set `MCP_AUTH_TOKEN` to a long random value (`openssl rand -hex 32`) and give it to your MCP clients. Only if something in front of the server already authenticates every request, set `MCP_ALLOW_UNAUTHENTICATED=true` instead. |
| `CRUNCH_FILES_DIR (…) must be an existing directory` | Create the directory (in Docker, mount it), or unset the variable. |
| `CRUNCH_API_URL must use https:// (http:// is only allowed for localhost), got …` | The Crunch API and OAuth server must be reached over `https://`. Include the scheme, or remove the variable to use the default. The same applies to `CRUNCH_AUTH_URL`; `CRUNCH_REDIRECT_URI` may also use `http://`. |
| `… must not contain a user name or password` | Remove the `user:password@` part from the URL. |
| `MCP_HTTP_PORT must be a positive integer, got "…"` | Fix the value. The same applies to `CRUNCH_TIMEOUT_MS`. |
| `could not read CRUNCH_CLIENT_SECRET_FILE (…)` | The secret file isn't mounted where the variable points. |
| `the token file /data/tokens.json is not valid JSON; delete it and log in again` | The file was damaged or edited by hand. Delete it and [log in](getting-started.md#3-log-in-to-crunch) again. |
| `could not read the token file …: permission denied` | The container's user (UID `65532`) can't read `/data`. With a bind mount, `chown 65532:65532` the directory. |

## The client can't connect to the server

1. **Is it running?** `curl http://<docker-host>:8080/health` should return `{"status":"ok"}`.
2. **Right host?** The port is published on the machine running Docker. If Docker runs on another machine (for example with `DOCKER_HOST`), `localhost` won't work.
3. **Right path?** The endpoint is `/mcp`, so the URL is `http://<docker-host>:8080/mcp`.
4. **`401 Unauthorized`:** the client isn't sending `Authorization: Bearer <MCP_AUTH_TOKEN>`, or the token doesn't match. Watch for stray quotes or spaces in `.env`.
5. **`403 Host … is not allowed`** or **`403 Origin … is not allowed`:** add the hostname the client uses (or, for a browser-based client, the host of the page it runs on) to `MCP_ALLOWED_HOSTS`, or unset it. An `Origin` on a port other than 443 or 80, such as `http://localhost:5173`, must be listed with its port.
6. **Running the binary, and other machines can't connect:** it listens on `127.0.0.1` unless you set `MCP_HTTP_HOST`, for example to `0.0.0.0`.
7. **The client expects SSE:** the server is stateless and answers `POST` requests with JSON; other methods get `405 Method not allowed`. Make sure the client is set to *Streamable HTTP* (often called `http`), not `sse`.
8. **stdio clients show nothing:** check that the command has `-i` and not `-t`, and that `MCP_TRANSPORT=stdio` is set when using the image, which defaults to HTTP.

## Logging in

**`set CRUNCH_CLIENT_ID (from your Crunch API onboarding email) before logging in`**

Add `CRUNCH_CLIENT_ID` to `.env`. If you don't have one yet, see [Get API access](getting-started.md#1-get-api-access).

**The browser spins or shows an error after clicking Allow**

Crunch has sent the browser to the redirect URI (`http://127.0.0.1:8976/callback` by default), and nothing on the browser's machine is answering. That's normal when:

- Docker runs on another machine,
- you started the login with `crunch_auth_start` (the running server doesn't listen on port 8976), or
- you forgot `-p 127.0.0.1:8976:8976` on `docker compose run`.

The address bar already holds what crunch-mcp needs. Copy the whole URL (it contains `code=` and `state=`) and paste it into the terminal, or into the chat for `crunch_auth_complete`.

**`Could not listen on 127.0.0.1:8976 for the redirect (…); paste the URL instead.`**

Something else is using port 8976. Pasting the URL works fine. To use another port, set `CRUNCH_REDIRECT_URI`, for example to `http://127.0.0.1:8977/callback`, as long as your API account doesn't have a fixed redirect URI. In Docker, the container keeps listening on 8976, so publish it as `-p 127.0.0.1:8977:8976`.

**`This isn't the redirect for the login crunch-mcp is waiting for`**

The page that reached the login listener didn't carry the `state` of the login `crunch-mcp login` started, or it took longer than 15 minutes. Use the URL the command printed this time (not an old tab or bookmark), or start again.

**`crunch token request failed (HTTP 401): invalid_client …`**

Crunch didn't accept the client credentials.

- Check `CRUNCH_CLIENT_ID` is exactly what Crunch sent.
- **Public clients must not send a secret.** If Crunch didn't give you a client secret, leave `CRUNCH_CLIENT_SECRET` empty.
- **Confidential clients must.** If Crunch gave you a secret, set `CRUNCH_CLIENT_SECRET`.

**`crunch token request failed (…): invalid_redirect_uri …`, or an error page from Crunch before you can click Allow**

Your API account has a redirect URI registered, and `CRUNCH_REDIRECT_URI` doesn't match it exactly. Set `CRUNCH_REDIRECT_URI` to the registered value, character for character, including the scheme, port, path and any trailing slash.

**`crunch token request failed (HTTP 404) …`**

Crunch's token endpoint returns `404` when the request is malformed, for example with parameters in the query string instead of the form body. crunch-mcp always sends a form body, so check that `CRUNCH_AUTH_URL` is `https://oauth.crunch.co.uk` or unset.

**`this login link has expired or wasn't started by this server (the state doesn't match). Start again`**

The URL you pasted belongs to a different login attempt. Each attempt has its own `state`, and it's only known to the process that started it. Common causes: you pasted a URL from an earlier attempt, the server restarted between `crunch_auth_start` and `crunch_auth_complete`, or you started with `crunch-mcp login` and finished in the chat (or the other way round). Start again and use the newest URL.

**`crunch returned an error instead of a code: access_denied`**

You clicked **Deny**, or Crunch refused the requested scopes. Try again, and check the scopes your API account is allowed.

**`Warning: no refresh token was issued (is offline_access in CRUNCH_SCOPES?)`**

Without `offline_access`, the login only lasts about an hour. Unset `CRUNCH_SCOPES`, or add `offline_access` to it, and log in again.

## "Not logged in" and expired logins

**`not logged in to Crunch. Run crunch-mcp login (or docker compose run --rm -p 127.0.0.1:8976:8976 crunch-mcp login), or use the crunch_auth_start tool`**

The server has no tokens. Either nobody has logged in, or the token file isn't where the server looks:

- Log in as in [Getting started](getting-started.md#3-log-in-to-crunch).
- **You logged in, but the server still says this.** The login was saved somewhere the server doesn't look. Check that `login` ran with the same `CRUNCH_TOKEN_FILE` and, in Docker, the same volume (`docker compose run` uses the Compose volume, but a plain `docker run` needs `-v` with the same volume name).
- **It forgets the login after every restart.** The token file isn't on a persistent volume. Check that `compose.yaml` mounts a volume at `/data`, and for stdio, that you pass `-v crunch-data:/data` with the same volume name you logged in with.

**`… The refresh token was rejected: it may have expired (they last 60 days), been revoked, or been used already. Log in again …`**

The login has ended. Usual causes:

- Nothing used the server for 60 days.
- **Two servers or sessions shared one login.** One rotated the refresh token, the other then used the old one, and Crunch revoked the login. Check for another container, a stdio session, or an old copy of the token file. See [Refresh tokens rotate](configuration.md#refresh-tokens-rotate).
- The token file was restored from a backup.
- The access was revoked in Crunch.

Log in again. If you can't explain the cause, treat it as a possible leak of the token file. See [Token storage](security.md#token-storage).

**`could not save tokens to /data/tokens.json: … They will be lost on restart; mount a writable volume there`**

The server is working from memory because it can't write the token file. With the default read-only root filesystem, `/data` must be a volume. With a bind mount, make it writable by UID `65532`. Fix it before the next restart, or you'll need to log in again.

**`the access token in CRUNCH_ACCESS_TOKEN was rejected or has expired, and there is no refresh token. Log in with crunch-mcp login`**

Access tokens last about an hour. Remove `CRUNCH_ACCESS_TOKEN` and log in properly.

## Permission errors (`403`)

**`… failed (HTTP 403): … Crunch refused this request. The login may not include the create:all scope (…), or the endpoint isn't part of the public API.`**

Crunch understood the request but won't allow it. Two causes:

- **Missing scope.** You turned on writes or deletes after logging in, so the login still only has `read:all`. Log in again (see [Turning on writes](configuration.md#turning-on-writes)) and check the new scopes with `crunch_auth_status`.
- **Not a public endpoint.** Some endpoints the Crunch app uses, such as `/banking/statement-entries`, aren't part of the public API and always return `403`. This usually comes from the raw API tool. There's no workaround. See [Compatibility](compatibility.md#limits-of-the-crunch-api).

## Errors when creating or changing records

**`… failed (HTTP 500): … Crunch often returns 500 when a required field is missing or a value (VAT type, expense type, payment method) isn't valid; check them with the reference tools.`**

Despite the status code, this is almost always a problem with the request rather than an outage. Check:

- The **expense type** is a `name` from `crunch_list_expense_types`, such as `TRAVEL_AND_SUBSISTENCE`.
- The **VAT type** is a display name from `crunch_list_vat_types` with the right `for` (`expenses` or `invoices`).
- The **payment method** comes from `crunch_list_payment_methods`, and a bank account, credit card or director is given where the method needs one.
- The **supplier** or **client** exists (`crunch_list_suppliers`, `crunch_list_clients`).
- A client's **billing address** has `line1`, `line2` and `country`.

**`… failed (HTTP 400): …`** with field names

Crunch rejected specific fields. The message lists each one and why.

**Dates in closed periods, and reconciled records**

Crunch refuses to create or change expenses dated in a closed accounting year, and won't change or delete reconciled expenses or remove reconciled payments. Check `crunch_list_accounting_periods`, the `editable` and `deletable` fields from `crunch_get_expense`, and `deletable` from `crunch_get_invoice`. These have to be dealt with in Crunch itself.

**An invoice can't be changed or deleted**

Issued invoices are final. Correct them with a credit note in the Crunch app.

**`… failed (HTTP 429)`**

Crunch's rate limit is about 100 requests per second, and crunch-mcp retries a few times before giving up. If you see this, something is making a very large number of requests. Try again in a moment.

**`… could not reach the Crunch API: …` or timeouts**

- Check the server has outbound internet access, and that `public-api.crunch.co.uk` resolves inside the container.
- A single slow request can be given longer with `CRUNCH_TIMEOUT_MS`, for example `60000`.
- Check Crunch's own status if every request fails.

## Attachments and downloads

| Message or symptom | Fix |
|---|---|
| `… is larger than 10 MB` | crunch-mcp uploads files of up to 10 MB. Compress or resize the file. |
| `the content of … is not valid base64` | The client sent a damaged file. Attach it again, or use `file_path`. |
| `reading and saving local files is disabled …` | `file_path` and `save_to` need `CRUNCH_FILES_DIR`. Send the file as `content_base64` instead, or set `CRUNCH_FILES_DIR` to a folder for attachments and downloads. |
| `… is outside CRUNCH_FILES_DIR (…)` | Put the file in `CRUNCH_FILES_DIR` (or save there). Paths that lead out of it, through `..` or a symlink, are refused. |
| `refusing to use …: it holds credentials` | The path is the token file (or its lock or temporary files), or under `/run/secrets`. These are never read or written by tools. |
| `open …: no such file or directory` with `file_path` | The path is on the machine running crunch-mcp, not yours. In Docker, mount the folder into the container at `CRUNCH_FILES_DIR`. |
| `… already exists; choose another save_to` | `save_to` never overwrites. Choose another name or folder. |
| `save_to` fails with `read-only file system` or `permission denied` | Mount a writable folder in the container as `CRUNCH_FILES_DIR` and save there. |
| `payment reminders are emails Crunch sends to the customer, so turning on sendReminders needs CRUNCH_ALLOW_EMAIL=true` | Payment reminders email your customer, so they follow the email switch. Turn them on in Crunch yourself, or set `CRUNCH_ALLOW_EMAIL=true`. |
| `this update leaves out existing payments (id …)` (or `supplierPayments`, `attachments`) | Crunch deletes the payments and attachments an update doesn't send back, so without `CRUNCH_ALLOW_DELETES` they must be kept. Leave them out of `changes`, or send them back exactly as `crunch_get_*` with `raw=true` returns them. |
| `Too large to return inline (over 8 MB)` | Use `save_to` to save the file instead. |
| A downloaded attachment is empty (0 bytes) | It was uploaded to Crunch without a `data:` URI by another tool, and Crunch stored an empty file. Upload it again. |
| `an invoice can have at most 5 attachments` | Remove one in Crunch first, or combine files. |

## Tools are missing

Tools are only registered when they're allowed:

- Write tools need `CRUNCH_ALLOW_WRITES=true`. Delete tools also need `CRUNCH_ALLOW_DELETES=true`, and `crunch_email_invoice` also needs `CRUNCH_ALLOW_EMAIL=true`.
- Each tool's toolset must be listed in `CRUNCH_TOOLSETS`.
- Restart the container after changing `.env` with `docker compose up -d`, and reconnect or restart your MCP client so it fetches the new tool list.
- Check the first log line: it shows whether writes, deletes and email are enabled, and how many toolsets are loaded.

## Still stuck?

Search the [existing issues](https://github.com/mattoddie/crunch-mcp/issues) or [open a bug report](https://github.com/mattoddie/crunch-mcp/issues/new?template=bug_report.yml). Include the crunch-mcp version (the first log line), the transport, the tool involved and the exact error. **Remove tokens, client secrets, bank details, customer names and any other private or financial details first.**
