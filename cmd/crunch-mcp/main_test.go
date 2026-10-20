package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

func TestLoginCallback(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	client, err := crunch.New(m.Config(filepath.Join(t.TempDir(), "tokens.json")))
	if err != nil {
		t.Fatal(err)
	}
	start, err := client.Auth.StartLogin()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/callback", ""} {
		results := make(chan string, 1)
		mux := callbackMux(path, client.Auth, results) // "" must not panic
		get := func(target string) int {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
			return rec.Code
		}
		base := path
		if base == "" {
			base = "/"
		}
		for _, q := range []string{
			"code=abc",                              // no state
			"code=abc&state=forged",                 // someone else's
			"error=access_denied",                   // an error without a state is ignored
			"error=access_denied&state=forged",      // and so is one with the wrong state
			"state=" + url.QueryEscape(start.State), // neither code nor error
		} {
			if code := get(base + "?" + q); code != http.StatusBadRequest {
				t.Errorf("%s?%s: %d", base, q, code)
			}
		}
		if len(results) != 0 {
			t.Fatalf("%q: rejected callbacks were passed on: %v", path, <-results)
		}
		target := base + "?code=abc&state=" + url.QueryEscape(start.State)
		if code := get(target); code != http.StatusOK {
			t.Errorf("%q: valid callback: %d", path, code)
		}
		if got := <-results; got != target {
			t.Errorf("%q: result %q", path, got)
		}
	}
}

func TestOnlyServeNeedsTheAuthToken(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "http")
	t.Setenv("MCP_AUTH_TOKEN", "")
	t.Setenv("CRUNCH_TOKEN_FILE", filepath.Join(t.TempDir(), "tokens.json"))
	if _, _, err := load(true); err == nil || !strings.Contains(err.Error(), "MCP_AUTH_TOKEN is required") {
		t.Errorf("serve without a token: %v", err)
	}
	if _, _, err := load(false); err != nil {
		t.Errorf("login and status don't serve, so don't need a token: %v", err)
	}
}

func TestHealthcheckUsesTheListenAddress(t *testing.T) {
	for host, want := range map[string]string{"": "127.0.0.1", "0.0.0.0": "127.0.0.1", "::": "::1", "192.168.1.5": "192.168.1.5", "[fd00::1]": "fd00::1"} {
		if got := healthHost(host); got != want {
			t.Errorf("%q: %q, want %q", host, got, want)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	portFile := filepath.Join(t.TempDir(), "port")
	if err := os.WriteFile(portFile, []byte(u.Port()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_TRANSPORT", "http")
	t.Setenv("MCP_AUTH_TOKEN", strings.Repeat("t", 16))
	t.Setenv("MCP_HTTP_HOST", "0.0.0.0")
	t.Setenv("MCP_HTTP_PORT_FILE", portFile)
	if err := healthcheck(); err != nil {
		t.Errorf("healthcheck: %v", err)
	}
	t.Setenv("MCP_HTTP_PORT_FILE", "")
	t.Setenv("MCP_HTTP_PORT", "1")
	if err := healthcheck(); err == nil {
		t.Error("healthcheck of a closed port succeeded")
	}
}
