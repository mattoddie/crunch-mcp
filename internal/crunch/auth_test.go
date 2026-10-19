package crunch_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattoddie/crunch-mcp/internal/config"
	"github.com/mattoddie/crunch-mcp/internal/crunch"
	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

func newClient(t *testing.T, m *crunchtest.Mock, edit func(c *config.Crunch)) (*crunch.Client, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "tokens.json")
	cfg := m.Config(file)
	if edit != nil {
		edit(&cfg)
	}
	c, err := crunch.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, file
}

func TestRefreshRotatesAndPersists(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	m.On("GET", "/user-details", crunchtest.JSON(map[string]any{"email": "a@b.c"}))
	c, file := newClient(t, m, func(c *config.Crunch) { c.RefreshToken = crunchtest.RefreshToken })

	var out map[string]any
	if err := c.Get(context.Background(), "/user-details", nil, &out); err != nil {
		t.Fatal(err)
	}
	saved, err := crunch.TokenStore{File: file}.Load()
	if err != nil || saved == nil {
		t.Fatalf("tokens not saved: %v", err)
	}
	if saved.RefreshToken == crunchtest.RefreshToken || saved.RefreshToken == "" || saved.AccessToken == "" {
		t.Errorf("rotated refresh token not saved: %+v", saved)
	}
	st, _ := os.Stat(file)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("token file mode %v", st.Mode().Perm())
	}

	// A new process starts from the file, not the (now spent) env token.
	c2, err := crunch.New(m.Config(file))
	if err != nil {
		t.Fatal(err)
	}
	m.RevokeAccess()
	if err := c2.Get(context.Background(), "/user-details", nil, &out); err != nil {
		t.Fatalf("second process: %v", err)
	}
	if m.TokenRequests != 2 {
		t.Errorf("token requests: %d", m.TokenRequests)
	}
}

func TestConcurrentRequestsShareOneRefresh(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	m.On("GET", "/user-details", crunchtest.JSON(map[string]any{}))
	c, _ := newClient(t, m, func(c *config.Crunch) { c.RefreshToken = crunchtest.RefreshToken })
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- c.Get(context.Background(), "/user-details", nil, nil)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if m.TokenRequests != 1 {
		t.Errorf("expected one refresh, got %d", m.TokenRequests)
	}
}

func TestRejectedAccessTokenTriggersRefresh(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	m.On("GET", "/user-details", crunchtest.JSON(map[string]any{}))
	c, _ := newClient(t, m, func(c *config.Crunch) { c.RefreshToken = crunchtest.RefreshToken })
	if err := c.Get(context.Background(), "/user-details", nil, nil); err != nil {
		t.Fatal(err)
	}
	m.RevokeAccess() // the API rejects the still-unexpired access token
	if err := c.Get(context.Background(), "/user-details", nil, nil); err != nil {
		t.Fatal(err)
	}
	if m.TokenRequests != 2 {
		t.Errorf("token requests: %d", m.TokenRequests)
	}
}

func TestInvalidRefreshTokenExplains(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	c, _ := newClient(t, m, func(c *config.Crunch) { c.RefreshToken = "spent" })
	err := c.Get(context.Background(), "/user-details", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") || !strings.Contains(err.Error(), "Log in again") {
		t.Fatalf("got %v", err)
	}
	if st := c.Auth.Status(); st["authenticated"] != false || st["error"] == nil {
		t.Errorf("status: %v", st)
	}
}

func TestNotLoggedIn(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	c, _ := newClient(t, m, nil)
	if err := c.Get(context.Background(), "/user-details", nil, nil); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("got %v", err)
	}
}

func TestConfidentialClientUsesBasicAuth(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	m.On("GET", "/user-details", crunchtest.JSON(map[string]any{}))
	c, _ := newClient(t, m, func(c *config.Crunch) { c.RefreshToken = crunchtest.RefreshToken; c.ClientSecret = "shh" })
	if err := c.Get(context.Background(), "/user-details", nil, nil); err != nil {
		t.Fatal(err)
	}
	if m.TokenBasicAuth[0] != crunchtest.ClientID || m.TokenForms[0].Get("client_id") != "" {
		t.Errorf("basic auth %q, form client_id %q", m.TokenBasicAuth[0], m.TokenForms[0].Get("client_id"))
	}
}

func TestNewEnvRefreshTokenTakesOver(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	file := filepath.Join(t.TempDir(), "tokens.json")
	if err := (crunch.TokenStore{File: file}).Save(&crunch.Tokens{RefreshToken: "old", Seed: "something-else"}); err != nil {
		t.Fatal(err)
	}
	cfg := m.Config(file)
	cfg.RefreshToken = crunchtest.RefreshToken
	c, err := crunch.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.On("GET", "/user-details", crunchtest.JSON(map[string]any{}))
	if err := c.Get(context.Background(), "/user-details", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := m.TokenForms[0].Get("refresh_token"); got != crunchtest.RefreshToken {
		t.Errorf("refreshed with %q", got)
	}
}

func challengeOf(t *testing.T, authURL string) (string, string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("client_id") != crunchtest.ClientID {
		t.Errorf("authorize URL: %s", authURL)
	}
	return q.Get("code_challenge"), q.Get("state")
}

func TestLoginFlow(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	c, file := newClient(t, m, nil)
	start, err := c.Auth.StartLogin()
	if err != nil {
		t.Fatal(err)
	}
	challenge, state := challengeOf(t, start.URL)
	code := m.IssueCode(challenge)
	// Crunch's docs show the code appended with "&"; accept that form too.
	res, err := c.Auth.CompleteLogin(context.Background(), "http://127.0.0.1:8976/callback&code="+code+"&state="+state)
	if err != nil {
		t.Fatal(err)
	}
	if res["logged_in"] != true || res["has_refresh_token"] != true {
		t.Errorf("result: %v", res)
	}
	form := m.TokenForms[0]
	if form.Get("redirect_uri") != "http://127.0.0.1:8976/callback" || form.Get("code_verifier") == "" {
		t.Errorf("token form: %v", form)
	}
	sum := sha256.Sum256([]byte(form.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
		t.Error("verifier doesn't match challenge")
	}
	if saved, _ := (crunch.TokenStore{File: file}).Load(); saved == nil || saved.RefreshToken == "" {
		t.Error("tokens not saved")
	}
	// The state is single-use.
	if _, err := c.Auth.CompleteLogin(context.Background(), "http://x/callback?code="+code+"&state="+state); err == nil || !strings.Contains(err.Error(), "state") {
		t.Errorf("reused state: %v", err)
	}
}

func TestLoginWithBareCodeAndErrors(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	c, _ := newClient(t, m, nil)
	if _, err := c.Auth.CompleteLogin(context.Background(), "abc"); err == nil || !strings.Contains(err.Error(), "no login in progress") {
		t.Errorf("no pending login: %v", err)
	}
	start, _ := c.Auth.StartLogin()
	challenge, _ := challengeOf(t, start.URL)
	if _, err := c.Auth.CompleteLogin(context.Background(), "http://x/cb?error=access_denied&error_description=User+said+no"); err == nil || !strings.Contains(err.Error(), "User said no") {
		t.Errorf("denied: %v", err)
	}
	if _, err := c.Auth.CompleteLogin(context.Background(), m.IssueCode(challenge)); err != nil {
		t.Errorf("bare code: %v", err)
	}
}

func TestStaticAccessToken(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	m.On("GET", "/user-details", crunchtest.JSON(map[string]any{}))
	c, _ := newClient(t, m, func(c *config.Crunch) { c.AccessToken = crunchtest.AccessToken })
	if err := c.Get(context.Background(), "/user-details", nil, nil); err != nil {
		t.Fatal(err)
	}
	m.RevokeAccess()
	if err := c.Get(context.Background(), "/user-details", nil, nil); err == nil || !strings.Contains(err.Error(), "CRUNCH_ACCESS_TOKEN") {
		t.Errorf("got %v", err)
	}
}

func TestPicksUpLoginFromAnotherProcess(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	m.On("GET", "/user-details", crunchtest.JSON(map[string]any{}))
	server, file := newClient(t, m, func(c *config.Crunch) { c.RefreshToken = crunchtest.RefreshToken })
	if err := server.Get(context.Background(), "/user-details", nil, nil); err != nil {
		t.Fatal(err)
	}

	// `crunch-mcp login` in another process writes fresh tokens to the same file.
	cli, err := crunch.New(m.Config(file))
	if err != nil {
		t.Fatal(err)
	}
	start, _ := cli.Auth.StartLogin()
	challenge, state := challengeOf(t, start.URL)
	if _, err := cli.Auth.CompleteLogin(context.Background(), "http://x/cb?code="+m.IssueCode(challenge)+"&state="+state); err != nil {
		t.Fatal(err)
	}
	saved, _ := crunch.TokenStore{File: file}.Load()

	// The running server must use the new login, and refresh with the new refresh token.
	m.RevokeAccess()
	if err := server.Get(context.Background(), "/user-details", nil, nil); err != nil {
		t.Fatalf("server didn't pick up the new login: %v", err)
	}
	if got := m.TokenForms[len(m.TokenForms)-1].Get("refresh_token"); got != saved.RefreshToken {
		t.Errorf("refreshed with %q, want the new login's %q", got, saved.RefreshToken)
	}
}

// Two processes sharing a token file (simulated by two clients, whose flocks
// conflict just as separate processes' do) must not both spend the same
// rotating refresh token: Crunch would revoke the grant.
func TestRefreshIsSerialisedAcrossProcesses(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	m.On("GET", "/user-details", crunchtest.JSON(map[string]any{}))
	m.TokenDelay = 200 * time.Millisecond
	seed := func(c *config.Crunch) { c.RefreshToken = crunchtest.RefreshToken }
	a, file := newClient(t, m, seed)
	cfg := m.Config(file)
	seed(&cfg)
	b, err := crunch.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, c := range []*crunch.Client{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- c.Get(context.Background(), "/user-details", nil, nil)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("request: %v", err)
		}
	}
	if m.TokenRequests != 1 {
		t.Errorf("token requests: %d, want 1 (the second process should reuse the first one's tokens)", m.TokenRequests)
	}
	if _, err := os.Stat(file + ".lock"); err != nil {
		t.Errorf("lock file: %v", err)
	}
}

func TestPendingStateAndErrorsNeedAValidState(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	c, _ := newClient(t, m, nil)
	start, _ := c.Auth.StartLogin()
	if !c.Auth.PendingState(start.State) || c.Auth.PendingState("") || c.Auth.PendingState("forged") {
		t.Error("PendingState")
	}
	// An error with a state this server didn't issue is not trusted.
	if _, err := c.Auth.CompleteLogin(context.Background(), "http://x/cb?error=access_denied&state=forged"); err == nil || !strings.Contains(err.Error(), "state doesn't match") {
		t.Errorf("forged error: %v", err)
	}
	if _, err := c.Auth.CompleteLogin(context.Background(), "http://x/cb?code=abc&state=forged"); err == nil || !strings.Contains(err.Error(), "state doesn't match") {
		t.Errorf("forged state: %v", err)
	}
	if !c.Auth.PendingState(start.State) {
		t.Error("a forged callback must not use up the real login")
	}
}

// A tool call cancelled while its refresh is in flight must not lose the rotated
// refresh token: Crunch has already spent the old one, and reusing it revokes the grant.
func TestCancelledRefreshStillSavesRotatedToken(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	m.On("GET", "/user-details", crunchtest.JSON(map[string]any{}))
	m.TokenDelay = 300 * time.Millisecond
	c, file := newClient(t, m, func(c *config.Crunch) { c.RefreshToken = crunchtest.RefreshToken })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = c.Get(ctx, "/user-details", nil, nil) // fails: the caller gave up
	saved, err := crunch.TokenStore{File: file}.Load()
	if err != nil || saved == nil || saved.RefreshToken == crunchtest.RefreshToken {
		t.Fatalf("rotated refresh token not saved: %+v %v", saved, err)
	}
	m.TokenDelay = 0
	m.RevokeAccess()
	if err := c.Get(context.Background(), "/user-details", nil, nil); err != nil {
		t.Errorf("next request: %v", err)
	}
}
