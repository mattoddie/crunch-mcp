# Contributing to crunch-mcp

Thanks for wanting to help! crunch-mcp is a community project, and every kind of contribution is welcome: bug reports, notes on how the Crunch API behaves, documentation fixes, new tools and code review.

By taking part you agree to follow the [code of conduct](CODE_OF_CONDUCT.md).

- [Ways to contribute](#ways-to-contribute)
- [Development setup](#development-setup)
- [Making a change](#making-a-change)
- [Pull request guidelines](#pull-request-guidelines)
- [Commit messages](#commit-messages)
- [Adding or changing tools](#adding-or-changing-tools)
- [Releases](#releases)
- [Getting help](#getting-help)

## Ways to contribute

- **Report a bug.** Use the [bug report form](https://github.com/mattoddie/crunch-mcp/issues/new?template=bug_report.yml). Include the crunch-mcp version, the tool involved and the exact error, and strip out tokens, client secrets, bank details, customer names and any other private or financial details.
- **Share what you've learned about the Crunch API.** The public API has quirks, such as fields that must be present or values it silently ignores. A note in an issue or [discussion](https://github.com/mattoddie/crunch-mcp/discussions) saying *"creating X fails with 500 unless Y is set"* is genuinely useful.
- **Suggest a feature.** Open a [feature request](https://github.com/mattoddie/crunch-mcp/issues/new?template=feature_request.yml). If you know the API endpoint involved, from [Crunch's API documentation](https://developer.crunch.co.uk), include it. It's often most of the work.
- **Improve the docs.** Typos, unclear steps and missing examples are all fair game. Small docs fixes can go straight to a pull request.
- **Write code.** Look for issues labelled [`good first issue`](https://github.com/mattoddie/crunch-mcp/labels/good%20first%20issue) or [`help wanted`](https://github.com/mattoddie/crunch-mcp/labels/help%20wanted). For anything big, please open an issue first so we can agree on the approach before you invest time.
- **Report a vulnerability privately.** See [SECURITY.md](SECURITY.md). Please don't open a public issue for it.

## Development setup

You need **Go 1.25+** and, to build the image, **Docker**.

```sh
git clone https://github.com/<you>/crunch-mcp.git   # your fork
cd crunch-mcp
make test
```

`make test` runs the whole suite against a built-in mock Crunch API and OAuth server in a few seconds. You don't need Crunch API access or a Crunch account to contribute.

See [docs/development.md](docs/development.md) for the architecture, the mock and a walkthrough of adding a tool.

## Making a change

1. **Fork** the repository and create a branch from `main`, for example `git checkout -b feat/credit-notes`.
2. **Make your change** with tests. Keep it focused: one feature or fix per pull request.
3. **Run the checks** that CI runs:

   ```sh
   make test         # go test ./...
   make lint         # gofmt and go vet
   make docs-check   # docs/tools.md matches the code
   make image        # optional: the image builds and the tests pass inside it
   ```

4. **Update the docs.** If you changed tools, run `make docs`. Update `README.md` or `docs/` if behaviour changed, and add a line under **Unreleased** in [CHANGELOG.md](CHANGELOG.md) for anything users will notice.
5. **Open a pull request** against `main` with a [Conventional Commits](#commit-messages) title, and fill in the template.

## Pull request guidelines

- CI must be green. It runs the tests, lint and the generated-docs check, builds the image for amd64 and arm64, and smoke-tests the container.
- Match the existing style: `gofmt`, small focused functions, errors that tell the user what to do, and comments that explain *why* rather than *what*.
- **No new dependencies without discussion.** The project deliberately stays small. The only direct dependencies are the MCP Go SDK and `jsonschema-go`.
- Never include real tokens, client secrets, bank details, company names, customer or supplier names, or other financial or personal data in code, tests, fixtures or screenshots. Use obviously fake values like `Example Ltd`, `sam@example.com` and `01234567`.
- Maintainers may push small fixes to your branch or squash commits when merging.
- Label suggestions (`bug`, `enhancement`, `new-tool`, `documentation`, `breaking`, …) are welcome. Labels sort the auto-generated release notes.

## Commit messages

Commits and pull request titles follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/). Pull requests are squash-merged, so the PR title becomes the commit on `main`, and a check fails the pull request if its title doesn't match.

- **Format:** `<type>(<optional scope>)<optional !>: <description>`, for example `feat: add credit note tools`, `fix(invoices): keep attachments when updating an invoice`, `docs: explain the TLS choice` or `build(deps): bump golang from 1.27 to 1.28`.
- **Types:** `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `build` (dependencies, Dockerfile and toolchain), `ci` (workflows), `chore` (anything else, such as `chore(release): v1.2.0`), `revert` and `style`.
- **Description:** imperative mood, lower-case first word and no trailing full stop. Keep the whole subject to 72 characters or fewer.
- **Breaking changes:** add `!` after the type or scope **and** a `BREAKING CHANGE: …` footer in the body or PR description, and label the pull request `breaking`.
- **Body:** explain **why** when it isn't obvious, and reference issues with `Fixes #123`.

## Adding or changing tools

The full guide is in [docs/development.md](docs/development.md#adding-a-tool). The short version:

- Put the tool in the right toolset file under `internal/tools/`, using `add[In]()` with a `Def` and an input struct whose `json` and `jsonschema` tags define the arguments.
- Set its `Gate` correctly: `Write` for anything that creates or changes records, `Delete` for anything that deletes records or removes payments, and `Email` for anything that sends email or otherwise reaches people outside Crunch. Set `Destructive` for writes that can't be undone, such as issuing an invoice. **This gating is the project's main safety feature, so reviewers check it carefully.**
- Updates fetch the current record and change only the fields passed. Never send back a record with payments or attachments missing.
- Use `Money` for amounts and `YYYY-MM-DD` dates. Point descriptions at the `reference` tools for valid values.
- Return compact summaries, offer `raw: true`, and never return tokens.
- Write the description for an AI model: what the tool does, side effects, where IDs come from, and Crunch's own limits.
- Add tests against the mock, then run `make docs`.

Changing an existing tool's name or arguments in an incompatible way, or moving it to a less restrictive gate, is a **breaking change**. Call it out in the PR and the changelog.

## Releases

Releases are made by maintainers by publishing a GitHub release. That triggers the workflow that builds and pushes the Docker image, attaches the Claude Desktop bundles and, for stable releases, publishes to the MCP Registry. The changelog update for a release goes in a small pull request titled `chore(release): vX.Y.Z`. See [docs/releasing.md](docs/releasing.md).

## Getting help

Ask in [GitHub Discussions](https://github.com/mattoddie/crunch-mcp/discussions), or comment on the issue you're working on. No question is too small.
