# Development

This page is for people working on crunch-mcp itself. Start with [CONTRIBUTING.md](../CONTRIBUTING.md) for the workflow, and use this page for how the code fits together.

- [Setup](#setup)
- [Project layout](#project-layout)
- [How a request flows](#how-a-request-flows)
- [Adding a tool](#adding-a-tool)
- [Tool design guidelines](#tool-design-guidelines)
- [Testing](#testing)
- [Running against a real Crunch account](#running-against-a-real-crunch-account)
- [Docs that are generated](#docs-that-are-generated)
- [The website](#the-website)

## Setup

You need Go 1.25 or later, and Docker for building the image.

```sh
git clone https://github.com/mattoddie/crunch-mcp.git
cd crunch-mcp
make test           # runs all tests against a mock Crunch API and OAuth server
```

| Target | What it does |
|---|---|
| `make build` | Build a static binary to `bin/crunch-mcp`. Pass `VERSION=1.2.3` to stamp a version. |
| `make test` | `go test ./...` |
| `make lint` | Fail if any file needs `gofmt`, then run `go vet ./...` |
| `make docs` | Regenerate [`docs/tools.md`](tools.md) from the tool definitions |
| `make docs-check` | Fail if `docs/tools.md` or the bundle manifest is out of date, or `server.json` has drifted from the manifest (CI runs this) |
| `make image` | Build the Docker image as `crunch-mcp:$(VERSION)` |
| `make server-json` | Write `dist/server.json`, the MCP Registry entry for `VERSION`, with the hashes of the bundles in `dist/` |
| `make mcp-publisher` | Download the pinned `mcp-publisher` to `.build/mcp-publisher/` and check its SHA-256 |

The project deliberately has only two direct dependencies: the official MCP Go SDK, [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk), and [`github.com/google/jsonschema-go`](https://github.com/google/jsonschema-go), which the SDK uses for input schemas. Everything else is the standard library. Please discuss before adding a dependency.

## Project layout

```text
cmd/
├── crunch-mcp/          # the binary: serve, login, status, healthcheck and version commands
├── gen-tool-docs/       # writes docs/tools.md from the registered tools and prompts
└── mcpb/                # keeps mcpb/manifest.json in sync, packs the Claude Desktop bundles, checks and stamps server.json
mcpb/                    # the bundle manifest (tools and prompts generated) and icon
server.json              # the MCP Registry entry (version stamped at release)
internal/
├── config/              # environment variables → typed Config (validation and default scopes live here)
├── crunch/
│   ├── auth.go          # OAuth 2.0: PKCE login, the token file, serialised refreshes of rotating tokens
│   ├── client.go        # Client: bearer auth, refresh on 401, retries on 429/503, errors with hints
│   ├── dryrun.go        # Call: records the requests a tool call sends, stops them in a dry run, diffs updates
│   ├── format.go        # helpers to read API objects and build compact summaries
│   └── crunchtest/      # an in-process fake Crunch API and OAuth server for tests
├── httpserver/          # stateless Streamable HTTP, bearer auth, Host allow-list, /health
└── tools/
    ├── tools.go         # the registration framework: Def, Gate, add[In](), Money, Date, schemas
    ├── server.go        # NewServer(): server instructions, registers every toolset and the prompts
    ├── guard.go         # dry_run and the audit log for tools that change things
    ├── list.go          # the list arguments (search, fields, limit) and {total, returned, items}
    ├── helpers.go       # paging, read-modify-write merging, attachments, downloads
    ├── prompts.go       # the built-in MCP prompts
    └── <toolset>.go     # one file per toolset: account, reference, banking, clients, suppliers, …
```

## How a request flows

1. An MCP client calls a tool, for example `crunch_update_client {client_id: 1, email: "new@example.com"}`.
2. The MCP SDK validates the arguments against the tool's JSON Schema, which was inferred from its Go input struct, and decodes them into that struct.
3. The handler registered by `add()` runs. For an update, it fetches the current record, merges in only what was passed, and sends the result back. For a tool that changes things, `guard.go` wraps the call. `add()` gives the tool the `dry_run` argument, and a server middleware (which runs before validation, so refused calls are seen too) puts a `crunch.Call` in the context. The client records every request that changes something in it, and in a dry run stops the first one instead of sending it; GETs, and POSTs that only read, still go through. The handler's result is then replaced by the preview, and the middleware writes the audit log entry (`CRUNCH_AUDIT_LOG`).
4. `crunch.Client` gets an access token from `Auth` (refreshing it first if it's about to expire), sends the request, and unwraps the JSON. On `401` it refreshes once and retries. On `429` or `503` it waits and retries up to three times. Other failures become a `crunch.Error` with Crunch's message and a hint for `401`, `403` and `500`.
5. The handler returns a compact summary (built with `crunch/format.go`), or a `Result` for files.
6. `add()` serialises the value to indented JSON text. A returned error becomes `isError: true` with the message, so the model can see what went wrong and adjust.

`Auth` is where the care goes. Crunch rotates refresh tokens and revokes the login if an old one is reused, so `Refresh()` holds a mutex, re-checks whether another caller has already refreshed, and `accept()` saves the new tokens to disk before handing out the new access token.

## Adding a tool

Tools are defined with the generic `add[In]()` in the file for their toolset. Here's a complete example from `clients.go`:

```go
type updateClientArgs struct {
	ClientID       int64    `json:"client_id" jsonschema:"Client (customer) ID (see crunch_list_clients)"`
	Name           string   `json:"name,omitempty" jsonschema:"New name"`
	BillingAddress *Address `json:"billing_address,omitempty" jsonschema:"New billing address (replaces the whole address)"`
	clientFields
}

add(c, Def{
	Toolset: "clients", Name: "crunch_update_client", Title: "Update client",
	Description: "Change a client's details. Only the fields you pass change; the rest are kept (a new billing_address replaces the whole address).",
	Gate:        Write, Idempotent: true,
}, func(ctx context.Context, a updateClientArgs) (any, error) {
	p := path("/clients/%d", a.ClientID)
	var current Obj
	if err := cl.Get(ctx, p, nil, &current); err != nil {
		return nil, err
	}
	merged := deepMerge(current, a.body(a.Name))
	if a.BillingAddress != nil {
		merged["billingAddress"] = a.BillingAddress.body()
	}
	var out any
	return out, cl.Put(ctx, p, nil, merge(merged, a.Extra), &out)
})
```

### The input struct

The input struct **is** the tool's argument schema:

- The `json` tag names the argument. Use `snake_case`.
- `omitempty` makes it optional. Without it, the argument is required.
- The `jsonschema` tag is the argument's description, which the model reads. Every argument needs one, and a test checks this.
- Use pointers (`*int`, `*bool`, `*string`) for optional values where zero means something, so "not given" and "set to zero" differ.
- `Money` takes an amount as a number (`120`) or a decimal string (`"120.00"`) and sends it to Crunch as an exact JSON number. Use it for every amount.
- `Date` is a `YYYY-MM-DD` string. Any string argument named `date`, `*_date` or `date_*` gets a date pattern in its schema automatically.
- Embedded structs, such as `PageArgs` or `clientFields` above, add their fields to the tool.
- Tools without arguments take `NoArgs`.

### The `Def`

| Field | Effect |
|---|---|
| `Toolset` | Required. The tool is only registered when this toolset is enabled. |
| `Name`, `Title`, `Description` | The tool's name (`crunch_<verb>_<thing>`), a short human title, and the description the model reads. |
| `Gate` | `Read` (the default, always registered), `Write` (needs `CRUNCH_ALLOW_WRITES`), `Delete` (also needs `CRUNCH_ALLOW_DELETES`) or `Email` (also needs `CRUNCH_ALLOW_EMAIL`). |
| `ReadOnly` | Overrides the `readOnlyHint` annotation, which defaults to `Gate == Read`. |
| `Destructive` | Overrides the `destructiveHint` annotation for non-read-only tools, which defaults to `Gate == Delete`. Set it to `true` for writes that can't be undone, such as issuing an invoice. |
| `Idempotent` | Repeating the call with the same arguments has no further effect. Sets `idempotentHint`. |
| `OpenWorld` | The tool reaches outside Crunch, such as emailing a customer. Sets `openWorldHint`. |
| `Enums` | Restricts string arguments to fixed values, keyed by argument name, for example `{"for": {"expenses", "invoices"}}`. |
| `Writes`, `Reads` | For a `Read`-gated tool that can still change things (the raw API tool with writes on): `Writes` gives it `dry_run` and audit logging like a write tool, and `Reads(args)` reports which calls only read, so they stay out of the audit log. Tools with any other gate get `dry_run` and audit logging automatically. |

### Helpers

| Helper | Use it for |
|---|---|
| `ListArgs` and `a.result(items)` | Every list tool: embed `ListArgs` for `search`, `fields` and `limit`, summarise the items, and return `a.result(items)` for a consistent `{total, returned, items}`. |
| `PageArgs` and `fetchPaged()` | List tools over Crunch's paged endpoints: `page`, `size`, `sort`, and `all` with `max_items`. Pass the tool's `ListArgs` too; they apply to the items fetched. |
| `deepMerge()` | Read-modify-write updates: merge the changes into the record fetched from Crunch. |
| `set()` | Build a request body, skipping empty strings and nil pointers. |
| `merge()` | Apply `extra` or `changes` (raw Crunch fields) last, so they override typed arguments. |
| `ref()` | Turn an optional ID into Crunch's `{"id": …}` reference. |
| `Attachment` and `attachmentsBody()` | Uploads from base64, a `data:` URI or a file path, sent as the `data:` URIs Crunch requires. |
| `fileResult()` | Downloads: images inline, other files as embedded resources, and `save_to` without overwriting. |
| `guards.go` | Checks that apply the switches inside request bodies: `checkReminders()` (payment reminders need email), `checkKeepsEntries()` (updates keep payments and attachments without deletes), and `readLocalFile()`/`saveLocalFile()` (files stay inside `CRUNCH_FILES_DIR`). Use them in any new tool that takes raw fields, updates a record with payments or attachments, or touches local files. |

### Checklist for a new tool

1. Put it in the right `internal/tools/<toolset>.go`. If it needs a new toolset, add it to `Toolsets` in `internal/config/config.go`, describe it in `toolsetInfo` in `cmd/gen-tool-docs/main.go`, and register it in `NewServer()`.
2. Set its `Gate` correctly, and set `Destructive` deliberately.
3. For updates, fetch the current record and merge, so nothing you didn't mean to change is sent back missing. Crunch deletes invoice payments and attachments that are left out of a `PUT`.
4. Return a **compact summary** and offer `raw: true` for the full object. Never return tokens.
5. Write descriptions for the model: what the tool does, when to use it, which tool supplies each ID (*"see crunch_list_clients"*), units, side effects (*"this is final"*), and Crunch's own limits.
6. Add tests in `internal/tools/<toolset>_test.go` using the mock.
7. Run `make docs` and commit the updated `docs/tools.md`. If it's a notable feature, update the README table and [CHANGELOG](../CHANGELOG.md).

## Tool design guidelines

- **Fewer, broader tools beat many narrow ones.** One update tool with optional fields is easier for a model than a tool per field.
- **Never surprise.** Updates only change what was asked. Destructive actions are flagged. Deletes and email are behind their own switches.
- **Point the model at valid values.** Crunch is strict about expense types, VAT types and payment methods. Descriptions should name the `reference` tool that lists them.
- **Errors should teach.** Return messages that tell the model, or the user, how to fix the call: *"give at least one item"*. `crunch.Error` already adds hints for `401`, `403` and `500`.
- **Respect Crunch's own rules.** Don't work around a `403`, a closed period or a reconciled payment; report it.

## Testing

Tests use Go's `testing` package against `crunchtest.Mock` (`internal/crunch/crunchtest`), an `httptest` server that imitates both the Crunch API (under `/v1`) and its OAuth server (under `/oauth`):

- Every API request needs a valid bearer token. `crunchtest.AccessToken` and `crunchtest.RefreshToken` are valid from the start.
- The OAuth server implements the authorisation code flow with PKCE and **rotating** refresh tokens: each refresh token works once, just like Crunch. `IssueCode(challenge)` simulates the user clicking **Allow**, and `RevokeAccess()` expires every access token.
- `On(method, pattern, handler)` adds a route. The pattern is a regular expression matched against the whole path after `/v1`, and later routes win. `crunchtest.JSON(v)` is a handler that returns `v`. Return `[]byte` for raw file content.
- Unmatched `GET`s return `404`. Unmatched `POST` and `PUT` requests echo their body back with an `id`, and unmatched `DELETE`s succeed, so simple write tools can be tested by inspecting what was sent.
- `Requests()` and `Last(method)` show what the server sent, with JSON bodies decoded.

`newEnv(t)` in `helpers_test.go` starts the mock, a client pointed at it and an in-memory MCP session with **every** tool enabled. `env.obj()` and `env.call()` run a tool and decode its JSON (failing on a tool error), and `env.callErr()` returns a tool's error text. A typical test:

```go
func TestUpdateClientKeepsOtherFields(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/clients/1", crunchtest.JSON(Obj{
		"id": 1, "name": "Example Ltd", "email": "old@example.com",
		"billingAddress": Obj{"line1": "1 Road", "line2": "Town", "country": "GB"},
	}))
	env.call("crunch_update_client", map[string]any{"client_id": 1, "email": "new@example.com"})
	got := asJSON(t, env.mock.Last("PUT").Body)
	want := `{"billingAddress":{"country":"GB","line1":"1 Road","line2":"Town"},"email":"new@example.com","id":1,"name":"Example Ltd"}`
	if got != want {
		t.Errorf("update client sent %s", got)
	}
}
```

To test that a tool is gated, pass different options: `newEnv(t, opts)` or `connect(t, env.client, opts)` with a `config.Tools`, and check the tool isn't listed with `toolNames()`. `TestGatesControlWhichToolsExist` already checks every tool against its `Gate`, and `TestSchemasDescribeEveryArgument` checks that every argument has a description.

Run one package or test with `go test ./internal/tools -run TestUpdateClient -v`.

## Running against a real Crunch account

There's no Crunch sandbox, so test against a **free Crunch account** you've set up for the purpose, never your real books. Your API application needs to be approved first. See [Getting started](getting-started.md#1-get-api-access).

With the [MCP Inspector](https://github.com/modelcontextprotocol/inspector):

```sh
make build
export CRUNCH_CLIENT_ID=your-client-id CRUNCH_TOKEN_FILE=$PWD/tokens.json
./bin/crunch-mcp login
npx @modelcontextprotocol/inspector ./bin/crunch-mcp
```

Or build the image and point Claude Code at it:

```sh
make image
docker run --rm -it -p 127.0.0.1:8976:8976 -v crunch-dev:/data --env-file .env crunch-mcp:0.0.0-dev login
docker run --rm -p 8080:8080 -v crunch-dev:/data --env-file .env crunch-mcp:0.0.0-dev
claude mcp add --transport http crunch-dev http://localhost:8080/mcp --header "Authorization: Bearer $MCP_AUTH_TOKEN"
```

`tokens.json` and `.env` are in `.gitignore`. Keep it that way, and never paste real tokens, client secrets or financial data into issues, tests or fixtures.

## Docs that are generated

[`docs/tools.md`](tools.md) is generated by `cmd/gen-tool-docs`. The program builds the server with every tool enabled, records each tool's toolset and gate as it's registered, reads the tools and prompts back over MCP, and writes the reference. Don't edit `docs/tools.md` by hand. Change the tool's title, description or `jsonschema` tags and run `make docs`. CI runs `make docs-check` and fails if the committed file is stale.

The `tools` and `prompts` in [`mcpb/manifest.json`](../mcpb/manifest.json) are generated too, by `go run ./cmd/mcpb manifest` (part of `make docs`), from the server started with the bundle's default settings. Edit the rest of the manifest (settings, descriptions) by hand. `make mcpb VERSION=1.2.3` builds a bundle per platform in `dist/`; CI validates them with `npx @anthropic-ai/mcpb validate` and smoke-tests the Linux one over stdio. [`server.json`](../server.json), the MCP Registry entry, is edited by hand; `go run ./cmd/mcpb server-json -check` (part of `make docs-check`) keeps its environment variables in step with the manifest. See [The MCP Registry](releasing.md#the-mcp-registry).

## The website

The site at [crunch-mcp.mattoddie.dev](https://crunch-mcp.mattoddie.dev) lives in `website/`. It has a hand-built landing page plus these docs rendered with Astro Starlight. It has its own `package.json`, so its dependencies never reach the server or the Docker image.

The Markdown in `docs/` is the single source: the site copies it in at build time. It turns each page's first `# Heading` into the page title, drops hand-written "Contents" lists, rewrites links between `.md` files to site URLs and converts GitHub alerts (`> [!NOTE]`) to Starlight asides. So write docs as normal GitHub Markdown, start each page with a `# Title`, and link between pages with relative `.md` links.

To preview the site locally:

```sh
cd website
npm ci
npm run dev
```
