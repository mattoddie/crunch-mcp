package tools

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/crunch-mcp/internal/config"
	"github.com/mattoddie/crunch-mcp/internal/crunch"
	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

type testEnv struct {
	t       *testing.T
	mock    *crunchtest.Mock
	client  *crunch.Client
	session *mcp.ClientSession
}

// newEnv starts a mock Crunch and an MCP session with the given tool options (all tools by default).
func newEnv(t *testing.T, opts ...config.Tools) *testEnv {
	t.Helper()
	m := crunchtest.New()
	t.Cleanup(m.Server.Close)
	cfg := m.Config(filepath.Join(t.TempDir(), "tokens.json"))
	cfg.AccessToken = crunchtest.AccessToken
	client, err := crunch.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	o := config.AllTools()
	if len(opts) > 0 {
		o = opts[0]
	}
	return &testEnv{t: t, mock: m, client: client, session: connect(t, client, o)}
}

func connect(t *testing.T, client *crunch.Client, o config.Tools) *mcp.ClientSession {
	t.Helper()
	server := NewServer(client, o, nil)
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func (e *testEnv) raw(name string, args map[string]any) *mcp.CallToolResult {
	e.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	res, err := e.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// call runs a tool and decodes its JSON result, failing on a tool error.
func (e *testEnv) call(name string, args map[string]any) any {
	e.t.Helper()
	res := e.raw(name, args)
	if res.IsError {
		e.t.Fatalf("%s returned an error: %s", name, text(res))
	}
	var out any
	dec := json.NewDecoder(strings.NewReader(text(res)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		e.t.Fatalf("%s: bad JSON %q: %v", name, text(res), err)
	}
	return out
}

func (e *testEnv) obj(name string, args map[string]any) Obj {
	e.t.Helper()
	o, ok := e.call(name, args).(Obj)
	if !ok {
		e.t.Fatalf("%s: not an object", name)
	}
	return o
}

// callErr runs a tool that should fail and returns its error text.
func (e *testEnv) callErr(name string, args map[string]any) string {
	e.t.Helper()
	res := e.raw(name, args)
	if !res.IsError {
		e.t.Fatalf("%s: expected an error, got %s", name, text(res))
	}
	return text(res)
}

// asJSON normalises a value (e.g. a recorded request body) for comparison.
func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func num(v any) float64 {
	f, _ := crunch.Num(Obj{"v": v}, "v")
	return f
}

func toolNames(t *testing.T, session *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	out := map[string]*mcp.Tool{}
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		out[tool.Name] = tool
	}
	return out
}

// jsonObj parses a JSON literal for fixtures.
func jsonObj(t *testing.T, s string) Obj {
	t.Helper()
	var o Obj
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&o); err != nil {
		t.Fatal(err)
	}
	return o
}

// rejects reports whether the server refused the call, as a protocol error or a tool error.
func (e *testEnv) rejects(name string, args map[string]any) bool {
	e.t.Helper()
	res, err := e.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	return err != nil || res.IsError
}

func challengeOfURL(t *testing.T, authURL string) (challenge, state string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("code_challenge"), u.Query().Get("state")
}

// filesDir returns a temporary directory as CRUNCH_FILES_DIR would hold it (symlinks resolved).
func filesDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
