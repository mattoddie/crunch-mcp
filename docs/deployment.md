# Deployment

- [Docker Compose](#docker-compose)
- [The token volume](#the-token-volume)
- [Commands](#commands)
- [Image tags](#image-tags)
- [Upgrading](#upgrading)
- [Building from source](#building-from-source)
- [TLS with a reverse proxy](#tls-with-a-reverse-proxy)
- [Hardening](#hardening)
- [Running without Docker](#running-without-docker)

## Docker Compose

The repository's [`compose.yaml`](../compose.yaml) is a good starting point:

```yaml
services:
  crunch-mcp:
    image: ghcr.io/mattoddie/crunch-mcp:latest
    container_name: crunch-mcp
    restart: unless-stopped
    env_file: .env
    ports:
      - "8080:8080"
    volumes:
      - crunch-data:/data
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]

volumes:
  crunch-data:
```

```sh
docker compose run --rm -p 127.0.0.1:8976:8976 crunch-mcp login   # once, to log in to Crunch (login port on loopback only)
docker compose up -d          # start
docker compose logs -f        # follow logs
docker compose ps             # shows "healthy" once the health check passes
docker compose down           # stop (the volume, and so the login, is kept)
```

The image has a built-in health check that runs `crunch-mcp healthcheck` every 30 seconds. The image has no shell or `curl`, so the binary checks its own `/health` endpoint. It only shows that the server is running, not that Crunch is reachable or the login is valid.

To listen on one interface only, such as a LAN address or localhost when you use a reverse proxy, change the port mapping to `"192.168.1.20:8080:8080"` or `"127.0.0.1:8080:8080"`. The server won't start without `MCP_AUTH_TOKEN` (see [MCP transport](configuration.md#mcp-transport)).

To attach files by `file_path` or save downloads with `save_to`, mount a folder for them and point `CRUNCH_FILES_DIR` at it, for example `- ./files:/files` under `volumes` and `CRUNCH_FILES_DIR=/files` in `.env`. Make it writable by UID `65532` for `save_to`. Without it, both are refused.

## The token volume

The server keeps its Crunch login in `/data/tokens.json`. Crunch replaces the refresh token every time it's used, so this file changes about once an hour while the server is busy, and it must survive restarts and upgrades. See [Refresh tokens rotate](configuration.md#refresh-tokens-rotate).

- Keep the named volume (`crunch-data`) or a bind mount at `/data`. The root filesystem is read-only, so `/data` is the only place the server can write.
- If you use a bind mount, make the directory writable by the container's user, UID `65532`: `mkdir data && sudo chown 65532:65532 data`.
- `docker compose down` keeps the volume. `docker compose down -v` deletes it, and with it the login.
- Back up the volume if you like, but don't **restore** an old copy: its refresh token has been used since, and using it again makes Crunch revoke the login. Log in again instead.
- Don't copy the token file to another server. Containers that share the volume on one Docker host take turns to refresh through a lock file, but one server per login is simplest. See [Refresh tokens rotate](configuration.md#refresh-tokens-rotate).

## Commands

The image's entrypoint is the `crunch-mcp` binary, so you can run any of its commands with `docker compose run --rm crunch-mcp <command>`, or `docker compose exec crunch-mcp /crunch-mcp <command>` while it's running:

| Command | What it does |
|---|---|
| `serve` (the default) | Serves MCP over stdio, or Streamable HTTP when `MCP_TRANSPORT=http` |
| `login` | Logs in to Crunch in a browser and saves the tokens to `CRUNCH_TOKEN_FILE`. In Docker, add `-p 127.0.0.1:8976:8976` |
| `status` | Prints the login status as JSON: where the tokens come from, the scopes, when the access token expires and whether there's a refresh token. Never prints the tokens. |
| `healthcheck` | Exits with `0` if the HTTP server answers `/health`. It reads the same settings as `serve` and connects to `MCP_HTTP_HOST` and `MCP_HTTP_PORT` (to loopback when the host is empty, `0.0.0.0` or `::`). Used by the image's `HEALTHCHECK`. |
| `version` | Prints the version |

## Image tags

Images are published to the GitHub Container Registry at **`ghcr.io/mattoddie/crunch-mcp`** for `linux/amd64` and `linux/arm64`. Each [GitHub release](https://github.com/mattoddie/crunch-mcp/releases) publishes these tags:

| Tag | Example | Moves? |
|---|---|---|
| `X.Y.Z` | `1.4.2` | Never. Pin this for full reproducibility. |
| `X.Y` | `1.4` | Moves to the latest patch release (bug fixes). |
| `X` | `1` | Moves to the latest minor release (new features, no breaking changes). Not published for `0.x`. |
| `latest` | | Moves to the newest stable release. |

Pre-releases, such as `1.5.0-rc.1`, are only published under their exact version, never as `latest`.

The image is built `FROM gcr.io/distroless/static-debian13:nonroot`. It contains the static `crunch-mcp` binary, CA certificates and time zone data, and nothing else: no shell and no package manager. It runs as the non-root user `65532` and is about 21 MB.

Every image includes an SBOM and build provenance attestation. To verify that an image was built by this repository's release workflow:

```sh
gh attestation verify oci://ghcr.io/mattoddie/crunch-mcp:1.4.2 --owner mattoddie
```

## Upgrading

```sh
docker compose pull
docker compose up -d
```

The login carries over in the volume. Check the [changelog](../CHANGELOG.md) or the release notes before upgrading across a major version. After an upgrade, reconnect or restart your MCP client so it fetches the new tool list.

## Building from source

```sh
git clone https://github.com/mattoddie/crunch-mcp.git
cd crunch-mcp
docker build -t crunch-mcp .        # or: make image
```

The tests run as part of the image build, so a broken build never produces an image. The build stage cross-compiles, so building for another architecture doesn't need emulation. To use a local build with Compose, replace `image:` in `compose.yaml` with `build: .` and run `docker compose up -d --build`.

## TLS with a reverse proxy

The server speaks plain HTTP. If it's reachable from outside a trusted network, put a TLS-terminating reverse proxy in front of it and keep `MCP_AUTH_TOKEN` set.

**Caddy**, which gets certificates automatically:

```text
crunch-mcp.example.com {
    reverse_proxy crunch-mcp:8080
}
```

**nginx:**

```nginx
server {
    listen 443 ssl;
    server_name crunch-mcp.example.com;
    ssl_certificate     /etc/ssl/certs/crunch-mcp.pem;
    ssl_certificate_key /etc/ssl/private/crunch-mcp.key;
    client_max_body_size 16m;      # attachments arrive as base64 in tool arguments

    location /mcp {
        proxy_pass http://crunch-mcp:8080;
        proxy_set_header Host $host;
        proxy_read_timeout 120s;
    }
}
```

**Traefik** labels:

```yaml
services:
  crunch-mcp:
    image: ghcr.io/mattoddie/crunch-mcp:latest
    env_file: .env
    volumes: [crunch-data:/data]
    labels:
      - traefik.enable=true
      - traefik.http.routers.crunch-mcp.rule=Host(`crunch-mcp.example.com`)
      - traefik.http.routers.crunch-mcp.tls.certresolver=letsencrypt
      - traefik.http.services.crunch-mcp.loadbalancer.server.port=8080
```

When the server sits behind a proxy, don't publish its port. Set `MCP_ALLOWED_HOSTS=crunch-mcp.example.com` so it only accepts requests for that hostname.

Most tools make one or a few Crunch requests, each limited by `CRUNCH_TIMEOUT_MS` (30 seconds by default). List tools with `all: true` fetch page after page, so give the proxy a read timeout of a minute or two.

> [!WARNING]
> Don't expose crunch-mcp directly to the internet. Anyone with the `MCP_AUTH_TOKEN` can use your Crunch account with whatever your settings allow. For remote access, a VPN (WireGuard, Tailscale) is safer than a public endpoint.

## Hardening

The image already runs as a non-root user with nothing in it but the binary, and the default `compose.yaml` already sets `read_only`, `cap_drop: [ALL]` and `no-new-privileges`. You can lock it down further:

```yaml
services:
  crunch-mcp:
    image: ghcr.io/mattoddie/crunch-mcp:1.4.2   # pinned
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    mem_limit: 128m
    pids_limit: 100
    env_file: .env
    volumes: [crunch-data:/data]
    ports: ["127.0.0.1:8080:8080"]
```

The server only needs outbound HTTPS to `public-api.crunch.co.uk` and `oauth.crunch.co.uk`. See [Security](security.md) for choosing settings and toolsets.

## Running without Docker

Docker is the easiest way to run crunch-mcp as a service, but the server is a single static binary with no dependencies. You need [Go](https://go.dev/dl/) 1.25 or later to build it.

With `go install`:

```sh
go install github.com/mattoddie/crunch-mcp/cmd/crunch-mcp@latest
```

The binary lands in `$(go env GOPATH)/bin`. Built this way it reports its version as `0.0.0-dev`.

Or from a clone, which also stamps the version:

```sh
git clone https://github.com/mattoddie/crunch-mcp.git
cd crunch-mcp
make build VERSION=1.2.3      # writes bin/crunch-mcp
```

Then:

```sh
export CRUNCH_CLIENT_ID=your-client-id
crunch-mcp login              # once; opens nothing itself, so copy the URL into a browser
crunch-mcp                    # stdio, for an MCP client to start
MCP_TRANSPORT=http MCP_AUTH_TOKEN=... crunch-mcp   # or a long-running HTTP server on 127.0.0.1:8080
                                                   # (set MCP_HTTP_HOST=0.0.0.0 for other machines)
```

Without Docker, the token file defaults to `~/.config/crunch-mcp/tokens.json` (or `$XDG_CONFIG_HOME/crunch-mcp/tokens.json`). To run it as a service, a systemd unit that sets these variables in an `EnvironmentFile` and runs `crunch-mcp` as an unprivileged user works well.
