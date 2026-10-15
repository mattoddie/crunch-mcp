package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
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
