# Releasing

Releases are driven by **GitHub releases**. Publishing a release with a tag like `v1.2.3` triggers the [Release workflow](../.github/workflows/release.yml). That workflow builds the Docker image for `linux/amd64` and `linux/arm64` and pushes it to `ghcr.io/mattoddie/crunch-mcp`, then builds the Claude Desktop extension bundles (`.mcpb`) and attaches them to the release. For stable releases it finally publishes the new version to the [official MCP Registry](https://registry.modelcontextprotocol.io/v0/servers?search=io.github.mattoddie/crunch-mcp). Nothing is published by merging to `main` alone.

- [Versioning](#versioning)
- [Release checklist](#release-checklist)
- [Pre-releases](#pre-releases)
- [What the workflow does](#what-the-workflow-does)
- [The MCP Registry](#the-mcp-registry)
- [If something goes wrong](#if-something-goes-wrong)
- [Repository setup](#repository-setup)

## Versioning

The project follows [Semantic Versioning](https://semver.org). Its public API is the set of **tool names, their arguments, prompts and the environment variables**:

| Change | Bump | Examples |
|---|---|---|
| Breaking | **major** (`2.0.0`) | Renaming or removing a tool, argument, prompt or variable; moving a tool to a less restrictive gate; changing a default so that more is allowed |
| New features | **minor** (`1.3.0`) | New tools, toolsets, arguments or prompts |
| Fixes | **patch** (`1.2.4`) | Bug fixes, compatibility fixes, docs, dependency updates |

While the version is `0.x`, minor bumps may contain breaking changes. Call them out in the release notes.

The version comes **from the tag**. There's no version file in the repository: builds default to `0.0.0-dev`, and release builds stamp the tag's version into `internal/tools.Version` with `-ldflags`. The server reports it to MCP clients, prints it on its first log line, and `crunch-mcp version` prints it.

## Release checklist

1. **Check `main` is green** on [CI](https://github.com/mattoddie/crunch-mcp/actions/workflows/ci.yml).
2. **Update the changelog.** In a small PR titled `chore(release): v1.2.0`, move the **Unreleased** entries in [`CHANGELOG.md`](../CHANGELOG.md) under a new heading, `## [1.2.0] - YYYY-MM-DD`, and update the comparison links at the bottom. Fill in the final tool and prompt counts from the top of [`docs/tools.md`](tools.md). Merge it.
3. **Publish the release.** Use either the CLI or the web UI.

   With the CLI:

   ```sh
   gh release create v1.2.0 --target main --title "v1.2.0" --generate-notes
   ```

   Or in the web UI: **Releases → Draft a new release → Choose a tag → type `v1.2.0` → Create new tag on publish**, target `main`. Click **Generate release notes**, edit them as needed, then **Publish release**.

4. **Watch the workflow.** Open **Actions → Release**. It takes a few minutes. When it finishes, the release notes gain a *Container image* section with the pull command, digest and tags, and a *Claude Desktop* section listing the six `.mcpb` bundles attached to the release.
5. **Check the image:** `docker pull ghcr.io/mattoddie/crunch-mcp:1.2.0`, then `docker run --rm ghcr.io/mattoddie/crunch-mcp:1.2.0 version`.
6. **Check a bundle:** download the one for your computer from the release and install it in Claude Desktop (**Settings → Extensions**), or unzip it and check the manifest with `npx @anthropic-ai/mcpb@2.1.2 validate manifest.json`.
7. **Check the registry** (stable releases): the *Publish to the MCP Registry* job is green, and `curl -s https://registry.modelcontextprotocol.io/v0/servers/io.github.mattoddie%2Fcrunch-mcp/versions/latest | jq .server.version` prints the new version.

Release notes are generated from merged PR titles and grouped by label. The groups are set in [`.github/release.yml`](../.github/release.yml). Label PRs `breaking`, `enhancement`, `new-tool`, `bug`, `compatibility`, `documentation` or `dependencies` to sort them, or `skip-changelog` to leave a PR out.

## Pre-releases

To test a release candidate, use a pre-release tag and tick **Set as a pre-release**:

```sh
gh release create v1.3.0-rc.1 --target main --prerelease --generate-notes
```

Pre-releases are published only under their exact version (`ghcr.io/mattoddie/crunch-mcp:1.3.0-rc.1`). They never move `latest`, `1` or `1.3`. They aren't published to the MCP Registry either.

## What the workflow does

| Step | Detail |
|---|---|
| Validate the tag | Must match `vMAJOR.MINOR.PATCH` with an optional `-prerelease` suffix, otherwise the workflow fails before building. |
| Build | `docker buildx` for `linux/amd64` and `linux/arm64`, from scratch with no build cache. The Go build cross-compiles, so no emulation is needed for the binary. The test suite runs inside the build, so failing tests mean no image. |
| Tag | `X.Y.Z` always. For stable releases also `X.Y`, `X` (from `1.0.0` on) and `latest`. |
| Push | To `ghcr.io/mattoddie/crunch-mcp` using the workflow's `GITHUB_TOKEN`. No personal tokens are needed. |
| Supply chain | Attaches an SBOM and SLSA provenance to the image, and pushes a signed build provenance attestation (`gh attestation verify`). |
| Release notes | Appends the pull command, digest and tags. |
| MCP Bundles | After the image, `make mcpb VERSION=X.Y.Z` builds a bundle per platform (macOS, Windows and Linux, each for amd64 and arm64), validates each manifest with `@anthropic-ai/mcpb`, attests them, uploads them as release assets and appends a *Claude Desktop* section to the release notes. Re-running it replaces the assets and doesn't add the section twice. |
| MCP Registry | Stable releases only, after the image and the bundles. Stamps the version, image tag and the published bundles' SHA-256 hashes into a copy of `server.json`, then publishes it with a pinned, checksum-verified `mcp-publisher`, logging in with GitHub OIDC. See [The MCP Registry](#the-mcp-registry). |

## The MCP Registry

[`server.json`](../server.json) describes crunch-mcp to the [official MCP Registry](https://registry.modelcontextprotocol.io) as `io.github.mattoddie/crunch-mcp`. It offers two kinds of package:

- **The Docker image** (`ghcr.io/mattoddie/crunch-mcp:X.Y.Z`) over stdio. The image serves HTTP by default, so the entry fixes `MCP_TRANSPORT=stdio`, mounts the `crunch-data` volume for the token file, and lists the same settings as the extension bundle.
- **The six extension bundles**, one per platform, with their SHA-256 hashes. The registry has no field for a package's operating system or architecture, so the platform is in each bundle's file name. The image comes first, so a client that just takes the first package gets the one that runs everywhere.

The committed file keeps the placeholder version `0.0.0-dev`, like the bundle manifest, because there's no version file in the repository. The release job stamps the real version, image tag, bundle URLs and hashes into a copy with `make server-json VERSION=X.Y.Z`, which hashes the bundles in `dist/`. CI keeps the file honest on every pull request:

- `make docs-check` runs `go run ./cmd/mcpb server-json -check`. It fails if the image's environment variables drift from the bundle manifest's (names, fixed values, required, secret, boolean and default), if the description is over 100 characters, or if the Dockerfile loses the `io.modelcontextprotocol.server.name` label the registry uses to check that the image belongs to the server.
- The *MCP Bundles* job stamps a copy for the CI bundles and runs `mcp-publisher validate` on both files. The *Docker image* job checks the label on the built image.

When you add a setting, add it to `mcpb/manifest.json` and to the image's `environmentVariables` in `server.json` together. When you add a bundle platform, add its package to `server.json` too: stamping fails if a bundle in `dist/` isn't listed, or a listed one is missing.

### The registry job

The job runs for stable releases only, after the image and bundle jobs. It downloads the bundles from the release, so the hashes are of the files people actually download, stamps `server.json`, installs `mcp-publisher` (pinned in the [Makefile](../Makefile) and checked against the release's SHA-256), logs in with GitHub OIDC and publishes. Only this job gets `id-token: write` for the registry login. No secrets are needed: a workflow in a `mattoddie` repository may publish under `io.github.mattoddie/`.

A published version can't be changed or published again. So:

- **Re-running the job is safe.** It first asks the registry whether this version is already there, and skips the publish if it is. If it failed before publishing (say the registry was down), re-run it from **Actions → Release → Re-run failed jobs**.
- **Don't rebuild the bundles after the version is in the registry.** The registry entry pins their hashes, and clients refuse a bundle that doesn't match.
- **To fix a wrong entry,** publish the next patch release. To withdraw a bad version, mark it with `mcp-publisher status --status deprecated` (or `deleted`) and a `--message`, after logging in as below.

To update `mcp-publisher`, change `MCP_PUBLISHER_VERSION` and the `MCP_PUBLISHER_SHA256_*` lines in the Makefile, taking the hashes from `registry_<version>_checksums.txt` on its [release page](https://github.com/modelcontextprotocol/registry/releases).

### Publishing by hand

If the job can't publish, an owner of the `mattoddie` GitHub organisation can do the same from a checkout of the release tag:

```sh
git checkout v1.2.0
rm -rf dist && gh release download v1.2.0 --pattern '*.mcpb' --dir dist
make server-json VERSION=1.2.0 mcp-publisher   # writes dist/server.json; the checksum check needs Linux or macOS
.build/mcp-publisher/mcp-publisher validate dist/server.json
.build/mcp-publisher/mcp-publisher login github   # opens a device login in the browser
.build/mcp-publisher/mcp-publisher publish dist/server.json
.build/mcp-publisher/mcp-publisher logout
```

## If something goes wrong

- **The workflow failed** for a transient reason: re-run it from the Actions tab. It's safe to re-run, and it pushes the same tags again.
- **The MCP Registry job failed:** see [The registry job](#the-registry-job) for re-running it, and [Publishing by hand](#publishing-by-hand) if that doesn't work.
- **The release was wrong** (bad code or wrong tag): don't move or reuse a published tag, because people may already have pulled it. Fix the problem on `main` and publish the next patch version. You can delete the bad GitHub release and mark it as such in the changelog, but leave the image tag alone.
- **Wrong tag format** (e.g. `1.2.0` without the `v`): delete the release and tag (`gh release delete 1.2.0 --cleanup-tag`) and publish again with the right tag.

## Repository setup

### Rulesets

Two rulesets protect the repository. You can view and edit them under **Settings → Rules → Rulesets**, and that's where to create them if you're setting up a fresh copy.

**Protect main** (the default branch):

- Changes land only through pull requests. Nobody can push directly, force-push or delete the branch, and there are no bypasses.
- The CI checks must pass (tests, lint, the generated-docs check and the Docker image build), and the branch must be up to date with `main`.
- Review threads must be resolved before merging. The only merge method is squash.
- No approving review is required yet, because GitHub doesn't let authors approve their own pull requests and the project has a single maintainer. Raise this when there are more maintainers.

**Protect release tags** (`v*`):

- Only repository admins can create, move or delete version tags, so only maintainers can publish releases. Published versions can't be re-pointed.

> [!NOTE]
> Required checks are matched by job name. If you rename a job in [`ci.yml`](../.github/workflows/ci.yml), update the *Protect main* ruleset in the same change. Otherwise pull requests wait forever for a check that never reports.

### Container package visibility

New GHCR packages normally start private, and GitHub doesn't offer an API to change this. A package linked to a public repository usually becomes public automatically. If it starts private, open the package page (**Your profile → Packages → crunch-mcp → Package settings**) and choose **Change visibility → Public** under **Danger Zone**.

The package links to the repository automatically through the image's `org.opencontainers.image.source` label, which makes it show in the repository sidebar.
