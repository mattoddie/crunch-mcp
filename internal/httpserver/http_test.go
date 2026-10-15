package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattoddie/crunch-mcp/internal/config"
	"github.com/mattoddie/crunch-mcp/internal/crunch"
	"github.com/mattoddie/crunch-mcp/internal/tools"
)

func newServer(t *testing.T, cfg config.HTTP) *httptest.Server {
	t.Helper()
	client, err := crunch.New(config.Crunch{APIURL: "http://invalid", AuthURL: "http://invalid", TokenFile: t.TempDir() + "/t.json", Timeout: 1e9})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Handler(cfg, tools.NewServer(client, config.AllTools(), nil)))
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, url, token, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

const listTools = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

func TestBearerAuthAndToolsList(t *testing.T) {
	srv := newServer(t, config.HTTP{AuthToken: "s3cret"})
	if res := post(t, srv.URL+"/mcp", "", listTools); res.StatusCode != 401 || res.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("no token: %d", res.StatusCode)
	}
	if res := post(t, srv.URL+"/mcp", "wrong", listTools); res.StatusCode != 401 {
		t.Errorf("wrong token: %d", res.StatusCode)
	}
	res := post(t, srv.URL+"/mcp", "s3cret", listTools)
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var out struct {
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Result.Tools) < 60 {
		t.Errorf("tools: %d", len(out.Result.Tools))
	}
}

func TestHealthAndNotFound(t *testing.T) {
	srv := newServer(t, config.HTTP{AuthToken: "x"})
	res, err := http.Get(srv.URL + "/health")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("health: %v %v", res, err)
	}
	res.Body.Close()
	res, _ = http.Get(srv.URL + "/other")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("other: %d", res.StatusCode)
	}
}

func TestAllowedHosts(t *testing.T) {
	srv := newServer(t, config.HTTP{AllowedHosts: []string{"mcp.example.com"}})
	if res := post(t, srv.URL+"/mcp", "", listTools); res.StatusCode != 403 {
		t.Errorf("unlisted host: %d", res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(listTools))
	req.Host = "mcp.example.com:443"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("listed host: %d", res.StatusCode)
	}
}

func TestOriginMustBeAllowed(t *testing.T) {
	srv := newServer(t, config.HTTP{AllowedHosts: []string{"mcp.example.com", "app.example.com"}})
	send := func(origin string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(listTools))
		req.Host = "mcp.example.com"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for origin, want := range map[string]int{
		"":                                 200, // not a browser
		"https://app.example.com":          200,
		"https://MCP.example.com:8443":     200,
		"https://evil.example.net":         403,
		"null":                             403,
		"https://mcp.example.com.evil.net": 403,
	} {
		if got := send(origin); got != want {
			t.Errorf("Origin %q: %d, want %d", origin, got, want)
		}
	}
}

func TestLargeRequestBodies(t *testing.T) {
	srv := newServer(t, config.HTTP{AuthToken: "x"})
	body := func(n int) string {
		return `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"pad":"` + strings.Repeat("a", n) + `"}}}`
	}
	// A 10 MB attachment is about 13.4 MB as base64; the SDK's own 4 MiB default must not apply.
	if res := post(t, srv.URL+"/mcp", "x", body(14<<20)); res.StatusCode != 200 {
		t.Errorf("14 MiB request: %d", res.StatusCode)
	}
	if res := post(t, srv.URL+"/mcp", "x", body(17<<20)); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("17 MiB request: %d", res.StatusCode)
	}
}
