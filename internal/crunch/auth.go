package crunch

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mattoddie/crunch-mcp/internal/config"
)

// Tokens is what the token file holds.
type Tokens struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// ExpiresAt is when the access token expires (Unix milliseconds).
	ExpiresAt  int64  `json:"expires_at,omitempty"`
	Scope      string `json:"scope,omitempty"`
	ObtainedAt string `json:"obtained_at,omitempty"`
	// Seed fingerprints the CRUNCH_REFRESH_TOKEN these tokens descend from, so a new one in the env takes over.
	Seed string `json:"seed,omitempty"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type pendingLogin struct {
	verifier string
	created  time.Time
}

const (
	expiryMargin    = time.Minute // refresh a little before the access token expires
	pendingLoginTTL = 15 * time.Minute
)

// ErrNotLoggedIn is returned when there are no credentials at all.
var ErrNotLoggedIn = errors.New("not logged in to Crunch. Run `crunch-mcp login` (or `docker compose run --rm -p 127.0.0.1:8976:8976 crunch-mcp login`), or use the crunch_auth_start tool")

func fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// TokenStore reads and writes the token file atomically, readable only by its owner.
type TokenStore struct{ File string }

// Load returns nil when the file doesn't exist yet.
func (s TokenStore) Load() (*Tokens, error) {
	b, err := os.ReadFile(s.File)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the token file %s: %w", s.File, err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil, nil
	}
	var t Tokens
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("the token file %s is not valid JSON; delete it and log in again", s.File)
	}
	return &t, nil
}

// Save writes the tokens via a temporary file and rename, so a crash never leaves a half-written file.
func (s TokenStore) Save(t *Tokens) error {
	if err := os.MkdirAll(filepath.Dir(s.File), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(t, "", "  ")
	tmp := fmt.Sprintf("%s.%d.tmp", s.File, os.Getpid())
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.File)
}

// Auth handles OAuth 2.0 for the Crunch API: the authorisation code flow
// with PKCE to log in, and refresh tokens to stay logged in.
//
// Crunch rotates refresh tokens: each refresh returns a new one and
// invalidates the old, and reusing an old one revokes the whole grant. So the
// newest refresh token is written to the token file before the new access
// token is used, and refreshes never run concurrently.
type Auth struct {
	cfg   config.Crunch
	http  *http.Client
	Store TokenStore

	mu             sync.Mutex // guards the fields below
	tokens         *Tokens
	pending        map[string]pendingLogin
	lastError      string
	persistWarning string
	staticRejected bool      // CRUNCH_ACCESS_TOKEN is used until the API rejects it
	fileMod        time.Time // modification time of the token file when last read or written

	refreshMu sync.Mutex // serialises refreshes within this process; lockTokens does so across processes
	lockWarn  sync.Once
}

// NewAuth loads saved tokens, letting a new CRUNCH_REFRESH_TOKEN take over.
func NewAuth(cfg config.Crunch, client *http.Client) (*Auth, error) {
	a := &Auth{cfg: cfg, http: client, Store: TokenStore{File: cfg.TokenFile}, pending: map[string]pendingLogin{}}
	saved, err := a.Store.Load()
	if err != nil {
		return nil, err
	}
	if cfg.RefreshToken != "" {
		seed := fingerprint(cfg.RefreshToken)
		if saved == nil || saved.RefreshToken == "" || saved.Seed != seed {
			saved = &Tokens{RefreshToken: cfg.RefreshToken, Seed: seed}
		}
	}
	a.tokens = saved
	a.fileMod = a.modTime()
	return a, nil
}

func (a *Auth) modTime() time.Time {
	st, err := os.Stat(a.Store.File)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// reloadLocked picks up tokens written by another process, such as
// `crunch-mcp login` run while the server is up, so the server neither keeps
// using an old login nor overwrites the new one. Callers hold a.mu.
func (a *Auth) reloadLocked() {
	m := a.modTime()
	if m.IsZero() || m.Equal(a.fileMod) {
		return
	}
	a.fileMod = m
	t, err := a.Store.Load()
	if err != nil || t == nil || (t.AccessToken == "" && t.RefreshToken == "") {
		return
	}
	a.tokens = t
	a.lastError = ""
	a.staticRejected = a.cfg.AccessToken != "" // a newer login replaces a fixed access token
}

// lockTokens takes the cross-process lock on the token file (<file>.lock). If the lock
// file can't be created, for example on a read-only volume, it carries on without it.
func (a *Auth) lockTokens(ctx context.Context) (func(), error) {
	err := os.MkdirAll(filepath.Dir(a.Store.File), 0o700)
	if err == nil {
		var unlock func()
		if unlock, err = lockFile(ctx, a.Store.File+".lock"); err == nil {
			return unlock, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("waiting for another crunch-mcp process to finish refreshing the tokens: %w", ctx.Err())
		}
	}
	a.lockWarn.Do(func() {
		log.Printf("crunch-mcp: could not lock %s.lock (%v); don't run other crunch-mcp processes with the same token file", a.Store.File, err)
	})
	return func() {}, nil
}

// HasCredentials reports whether any credentials are available (they may still be expired or revoked).
func (a *Auth) HasCredentials() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.AccessToken != "" || (a.tokens != nil && (a.tokens.AccessToken != "" || a.tokens.RefreshToken != ""))
}

// Status describes the login without revealing any tokens.
func (a *Auth) Status() map[string]any {
	has := a.HasCredentials()
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tokens
	if t == nil {
		t = &Tokens{}
	}
	source := "none"
	switch {
	case a.cfg.AccessToken != "" && !a.staticRejected:
		source = "CRUNCH_ACCESS_TOKEN"
	case t.RefreshToken != "" || t.AccessToken != "":
		source = "token file"
	}
	clientType := "public (PKCE)"
	if a.cfg.ClientSecret != "" {
		clientType = "confidential"
	}
	out := map[string]any{
		"authenticated":        has && a.lastError == "",
		"client_id_configured": a.cfg.ClientID != "",
		"client_type":          clientType,
		"source":               source,
		"token_file":           a.cfg.TokenFile,
		"requested_scopes":     a.cfg.Scopes,
		"has_refresh_token":    t.RefreshToken != "",
		"access_token_valid":   t.AccessToken != "" && t.ExpiresAt > time.Now().UnixMilli(),
	}
	if t.Scope != "" {
		out["scope"] = t.Scope
	}
	if t.ExpiresAt > 0 {
		out["access_token_expires"] = time.UnixMilli(t.ExpiresAt).UTC().Format(time.RFC3339)
	}
	if t.ObtainedAt != "" {
		out["last_login_or_refresh"] = t.ObtainedAt
	}
	if a.lastError != "" {
		out["error"] = a.lastError
	}
	if a.persistWarning != "" {
		out["warning"] = a.persistWarning
	}
	return out
}

// AccessToken returns a valid access token, refreshing it first when it has expired or is about to.
func (a *Auth) AccessToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	a.reloadLocked()
	if a.cfg.AccessToken != "" && !a.staticRejected {
		a.mu.Unlock()
		return a.cfg.AccessToken, nil
	}
	if t := a.tokens; t != nil && t.AccessToken != "" && (t.ExpiresAt == 0 || time.UnixMilli(t.ExpiresAt).Add(-expiryMargin).After(time.Now())) {
		a.mu.Unlock()
		return t.AccessToken, nil
	}
	a.mu.Unlock()
	return a.Refresh(ctx, "")
}

// Refresh gets a new access token with the refresh token. rejected is the
// access token the API just refused (if any); when another caller has already
// replaced it, that newer token is returned instead of refreshing again,
// because a rotated refresh token can only be used once.
func (a *Auth) Refresh(ctx context.Context, rejected string) (string, error) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	// Another process (a second server, or `crunch-mcp login`) may share the token file. Holding
	// the file lock from reading the tokens until the rotated ones are saved means only one of
	// them ever spends a given refresh token; the others pick up its new tokens just below.
	unlock, err := a.lockTokens(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()

	a.mu.Lock()
	a.reloadLocked()
	if a.cfg.AccessToken != "" && rejected == a.cfg.AccessToken {
		a.staticRejected = true
	}
	t := a.tokens
	if t != nil && t.AccessToken != "" && t.AccessToken != rejected && time.UnixMilli(t.ExpiresAt).Add(-expiryMargin).After(time.Now()) {
		a.mu.Unlock()
		return t.AccessToken, nil
	}
	if t == nil || t.RefreshToken == "" {
		a.mu.Unlock()
		if a.cfg.AccessToken != "" {
			return "", errors.New("the access token in CRUNCH_ACCESS_TOKEN was rejected or has expired, and there is no refresh token. Log in with `crunch-mcp login`")
		}
		return "", ErrNotLoggedIn
	}
	refreshToken, seed, scope := t.RefreshToken, t.Seed, t.Scope
	a.mu.Unlock()

	if a.cfg.ClientID == "" {
		return "", errors.New("CRUNCH_CLIENT_ID is required to refresh the access token")
	}
	res, err := a.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
	if err != nil {
		a.mu.Lock()
		a.lastError = err.Error()
		a.mu.Unlock()
		return "", err
	}
	// The old refresh token is kept only if Crunch didn't rotate it.
	if res.RefreshToken == "" {
		res.RefreshToken = refreshToken
	}
	if res.Scope == "" {
		res.Scope = scope
	}
	return a.accept(res, seed), nil
}

// LoginStart is what StartLogin returns.
type LoginStart struct {
	URL         string `json:"url"`
	State       string `json:"state"`
	RedirectURI string `json:"redirect_uri"`
	Scopes      string `json:"scopes"`
}

// StartLogin builds the URL the user opens to authorise this app, and remembers the PKCE verifier for it.
func (a *Auth) StartLogin() (*LoginStart, error) {
	if a.cfg.ClientID == "" {
		return nil, errors.New("set CRUNCH_CLIENT_ID (from your Crunch API onboarding email) before logging in")
	}
	verifier := randomString(48)
	sum := sha256.Sum256([]byte(verifier))
	state := randomString(18)
	a.mu.Lock()
	for s, p := range a.pending {
		if time.Since(p.created) > pendingLoginTTL {
			delete(a.pending, s)
		}
	}
	a.pending[state] = pendingLogin{verifier: verifier, created: time.Now()}
	a.mu.Unlock()
	q := url.Values{
		"client_id":             {a.cfg.ClientID},
		"redirect_uri":          {a.cfg.RedirectURI},
		"response_type":         {"code"},
		"scope":                 {a.cfg.Scopes},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	return &LoginStart{URL: a.cfg.AuthURL + "/authorize?" + q.Encode(), State: state, RedirectURI: a.cfg.RedirectURI, Scopes: a.cfg.Scopes}, nil
}

// PendingState reports whether state belongs to a login started with StartLogin
// that hasn't expired or been completed yet. It doesn't use the state up.
func (a *Auth) PendingState(state string) bool {
	if state == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.pending[state]
	return ok && time.Since(p.created) <= pendingLoginTTL
}

var urlish = regexp.MustCompile(`(?i)^https?://|^/|code=`)

// CompleteLogin finishes a login started with StartLogin. It accepts the
// full URL the browser was redirected to (preferred, as it carries the state)
// or just the code.
func (a *Auth) CompleteLogin(ctx context.Context, redirectedURLOrCode string) (map[string]any, error) {
	input := strings.TrimSpace(redirectedURLOrCode)
	var code, state string
	if urlish.MatchString(input) {
		// Crunch's docs show the code appended with "&" even when the redirect URI has no query, so parse leniently.
		query := input
		if i := strings.Index(input, "?"); i >= 0 {
			query = input[i+1:]
		} else if i := strings.Index(input, "&"); i >= 0 {
			query = input[i+1:]
		}
		query, _, _ = strings.Cut(query, "#")
		params, _ := url.ParseQuery(query)
		state = params.Get("state")
		if state != "" && !a.PendingState(state) {
			return nil, errors.New("this login link has expired or wasn't started by this server (the state doesn't match). Start again")
		}
		if e := params.Get("error"); e != "" {
			if d := params.Get("error_description"); d != "" {
				e += " (" + d + ")"
			}
			return nil, fmt.Errorf("crunch returned an error instead of a code: %s", e)
		}
		code = params.Get("code")
	} else {
		code = input
	}
	if code == "" {
		return nil, errors.New("no authorisation code found. Paste the whole URL your browser was sent to after you clicked Allow")
	}

	a.mu.Lock()
	var p pendingLogin
	var found bool
	if state != "" {
		p, found = a.pending[state]
		delete(a.pending, state)
		if !found {
			a.mu.Unlock()
			return nil, errors.New("this login link has expired or wasn't started by this server (the state doesn't match). Start again")
		}
	} else {
		// Only a bare code: use the most recent login attempt.
		var latest string
		for s, x := range a.pending {
			if !found || x.created.After(p.created) {
				latest, p, found = s, x, true
			}
		}
		if !found {
			a.mu.Unlock()
			return nil, errors.New("there is no login in progress. Start one first")
		}
		delete(a.pending, latest)
	}
	a.mu.Unlock()
	if time.Since(p.created) > pendingLoginTTL {
		return nil, errors.New("this login attempt has expired. Start again")
	}

	res, err := a.tokenRequest(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {a.cfg.RedirectURI},
		"code_verifier": {p.verifier},
	})
	if err != nil {
		return nil, err
	}
	seed := ""
	if a.cfg.RefreshToken != "" {
		seed = fingerprint(a.cfg.RefreshToken)
	}
	// Wait for any refresh in another process to finish, so it can't overwrite this login.
	if unlock, err := a.lockTokens(ctx); err == nil {
		defer unlock()
	}
	a.accept(res, seed)
	a.mu.Lock()
	a.staticRejected = true // a fresh login replaces any fixed access token
	a.mu.Unlock()
	st := a.Status()
	out := map[string]any{"logged_in": true, "has_refresh_token": st["has_refresh_token"], "token_file": st["token_file"]}
	for _, k := range []string{"scope", "warning"} {
		if v, ok := st[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

// accept stores a token response, persisting it before the new access token is handed out.
func (a *Auth) accept(res *tokenResponse, seed string) string {
	t := &Tokens{
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		Scope:        res.Scope,
		ObtainedAt:   time.Now().UTC().Format(time.RFC3339),
		Seed:         seed,
	}
	if res.ExpiresIn > 0 {
		t.ExpiresAt = time.Now().Add(time.Duration(res.ExpiresIn) * time.Second).UnixMilli()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens = t
	a.lastError = ""
	if err := a.Store.Save(t); err != nil {
		// Carry on with the in-memory tokens, but a restart would need a new login.
		a.persistWarning = fmt.Sprintf("could not save tokens to %s: %v. They will be lost on restart; mount a writable volume there", a.cfg.TokenFile, err)
		log.Printf("crunch-mcp: %s", a.persistWarning)
	} else {
		a.persistWarning = ""
		a.fileMod = a.modTime()
	}
	return t.AccessToken
}

func (a *Auth) tokenRequest(ctx context.Context, form url.Values) (*tokenResponse, error) {
	if a.cfg.ClientID == "" {
		return nil, errors.New("CRUNCH_CLIENT_ID is not set")
	}
	if a.cfg.ClientSecret == "" {
		form.Set("client_id", a.cfg.ClientID)
	}
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.AuthURL+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if a.cfg.ClientSecret != "" {
		// Confidential clients: HTTP Basic, and then client_id/secret must not also be in the body.
		req.SetBasicAuth(a.cfg.ClientID, a.cfg.ClientSecret)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the Crunch OAuth server at %s: %w", a.cfg.AuthURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &e)
		reason := strings.Trim(e.Error+": "+e.Description, ": ")
		if reason == "" {
			reason = truncate(strings.TrimSpace(string(body)), 200)
		}
		if reason == "" {
			reason = resp.Status
		}
		hint := ""
		switch {
		case form.Get("grant_type") == "refresh_token" && regexp.MustCompile(`(?i)invalid_grant|invalid_token|expired|revoked`).MatchString(reason):
			hint = " The refresh token was rejected: it may have expired (they last 60 days), been revoked, or been used already. Log in again with `crunch-mcp login` or the crunch_auth_start tool."
		case strings.Contains(strings.ToLower(reason), "invalid_client"):
			hint = " Check CRUNCH_CLIENT_ID, and set CRUNCH_CLIENT_SECRET only if you were issued one (confidential client)."
		case strings.Contains(strings.ToLower(reason), "redirect"):
			hint = fmt.Sprintf(" Check that CRUNCH_REDIRECT_URI (%s) matches any redirect URI set on your Crunch API account.", a.cfg.RedirectURI)
		}
		return nil, fmt.Errorf("crunch token request failed (HTTP %d): %s.%s", resp.StatusCode, reason, hint)
	}
	var res tokenResponse
	if err := json.Unmarshal(body, &res); err != nil || res.AccessToken == "" {
		return nil, errors.New("the token response had no access_token")
	}
	return &res, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
