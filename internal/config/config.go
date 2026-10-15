// Package config reads crunch-mcp's settings from environment variables.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Toolsets are groups of tools that can be switched on and off with CRUNCH_TOOLSETS.
var Toolsets = []string{"account", "reference", "banking", "clients", "suppliers", "expenses", "recurring", "invoices", "raw"}

// Crunch holds the API and OAuth settings.
type Crunch struct {
	// APIURL is the base URL of the public API, e.g. https://public-api.crunch.co.uk/v1.
	APIURL string
	// AuthURL is the base URL of the OAuth server (with /authorize and /token).
	AuthURL      string
	ClientID     string
	ClientSecret string // only confidential clients have one; public clients use PKCE
	RedirectURI  string // must be sent identically to /authorize and /token
	Scopes       string // space-separated, requested at login
	TokenFile    string // where tokens are saved; refresh tokens rotate, so they must persist
	RefreshToken string // seed refresh token, used when the token file hasn't seen it
	AccessToken  string // a fixed access token, used until it's rejected
	Timeout      time.Duration
}

// Tools controls which tools are registered.
type Tools struct {
	AllowWrites  bool
	AllowDeletes bool // requires AllowWrites
	AllowEmail   bool // requires AllowWrites
	Toolsets     map[string]bool
}

// AllTools enables every tool, for documentation and tests.
func AllTools() Tools {
	t := Tools{AllowWrites: true, AllowDeletes: true, AllowEmail: true, Toolsets: map[string]bool{}}
	for _, ts := range Toolsets {
		t.Toolsets[ts] = true
	}
	return t
}

// HTTP holds the Streamable HTTP transport settings.
type HTTP struct {
	Host         string
	Port         int
	AuthToken    string
	AllowedHosts []string
}

// Config is the whole configuration.
type Config struct {
	Crunch    Crunch
	Tools     Tools
	Transport string // "stdio" or "http"
	HTTP      HTTP
}

// Env looks up a variable; os.LookupEnv in production, a map in tests.
type Env func(string) (string, bool)

// MapEnv adapts a map for tests.
func MapEnv(m map[string]string) Env {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// read returns a trimmed variable, honouring the <NAME>_FILE convention for Docker secrets.
func read(env Env, name string) (string, error) {
	if file, ok := env(name + "_FILE"); ok && file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("could not read %s_FILE (%s): %w", name, file, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	v, _ := env(name)
	return strings.TrimSpace(v), nil
}

func boolValue(name, v string, fallback bool) (bool, error) {
	if v == "" {
		return fallback, nil
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("%s must be true or false, got %q", name, v)
}

func positiveInt(name, v string, fallback int) (int, error) {
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", name, v)
	}
	return n, nil
}

func checkURL(name, v string) (string, error) {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%s is not a valid URL: %s", name, v)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("%s must use https:// (or http://), got %s", name, v)
	}
	return strings.TrimRight(v, "/"), nil
}

// DefaultScopes are the least-privilege scopes for the enabled tools, plus offline_access for a refresh token.
func DefaultScopes(allowWrites, allowDeletes bool) string {
	scopes := []string{"read:all"}
	if allowWrites {
		scopes = append(scopes, "create:all", "update:all")
	}
	if allowDeletes {
		scopes = append(scopes, "delete:all")
	}
	return strings.Join(append(scopes, "offline_access"), " ")
}

func defaultTokenFile(env Env) string {
	if dir, ok := env("XDG_CONFIG_HOME"); ok && dir != "" {
		return filepath.Join(dir, "crunch-mcp", "tokens.json")
	}
	home, _ := env("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".config", "crunch-mcp", "tokens.json")
}

// Load builds the configuration from the environment.
func Load(env Env) (*Config, error) {
	var firstErr error
	get := func(name string) string {
		v, err := read(env, name)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return v
	}
	or := func(v, fallback string) string {
		if v == "" {
			return fallback
		}
		return v
	}

	cfg := &Config{Transport: or(get("MCP_TRANSPORT"), "stdio")}
	if cfg.Transport != "stdio" && cfg.Transport != "http" {
		return nil, fmt.Errorf("MCP_TRANSPORT must be stdio or http, got %q", cfg.Transport)
	}

	var err error
	t := &cfg.Tools
	if t.AllowWrites, err = boolValue("CRUNCH_ALLOW_WRITES", get("CRUNCH_ALLOW_WRITES"), false); err != nil {
		return nil, err
	}
	if t.AllowDeletes, err = boolValue("CRUNCH_ALLOW_DELETES", get("CRUNCH_ALLOW_DELETES"), false); err != nil {
		return nil, err
	}
	if t.AllowEmail, err = boolValue("CRUNCH_ALLOW_EMAIL", get("CRUNCH_ALLOW_EMAIL"), false); err != nil {
		return nil, err
	}
	if t.AllowDeletes && !t.AllowWrites {
		return nil, fmt.Errorf("CRUNCH_ALLOW_DELETES=true also requires CRUNCH_ALLOW_WRITES=true")
	}
	if t.AllowEmail && !t.AllowWrites {
		return nil, fmt.Errorf("CRUNCH_ALLOW_EMAIL=true also requires CRUNCH_ALLOW_WRITES=true")
	}
	t.Toolsets = map[string]bool{}
	for _, name := range strings.Split(or(get("CRUNCH_TOOLSETS"), "all"), ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		switch {
		case name == "":
		case name == "all":
			for _, ts := range Toolsets {
				t.Toolsets[ts] = true
			}
		case contains(Toolsets, name):
			t.Toolsets[name] = true
		default:
			return nil, fmt.Errorf("unknown toolset %q in CRUNCH_TOOLSETS; valid: all, %s", name, strings.Join(Toolsets, ", "))
		}
	}

	c := &cfg.Crunch
	if c.APIURL, err = checkURL("CRUNCH_API_URL", or(get("CRUNCH_API_URL"), "https://public-api.crunch.co.uk/v1")); err != nil {
		return nil, err
	}
	if c.AuthURL, err = checkURL("CRUNCH_AUTH_URL", or(get("CRUNCH_AUTH_URL"), "https://oauth.crunch.co.uk")); err != nil {
		return nil, err
	}
	if c.RedirectURI, err = checkURL("CRUNCH_REDIRECT_URI", or(get("CRUNCH_REDIRECT_URI"), "http://127.0.0.1:8976/callback")); err != nil {
		return nil, err
	}
	c.ClientID = get("CRUNCH_CLIENT_ID")
	c.ClientSecret = get("CRUNCH_CLIENT_SECRET")
	c.Scopes = or(get("CRUNCH_SCOPES"), DefaultScopes(t.AllowWrites, t.AllowDeletes))
	c.TokenFile = or(get("CRUNCH_TOKEN_FILE"), defaultTokenFile(env))
	c.RefreshToken = get("CRUNCH_REFRESH_TOKEN")
	c.AccessToken = get("CRUNCH_ACCESS_TOKEN")
	ms, err := positiveInt("CRUNCH_TIMEOUT_MS", get("CRUNCH_TIMEOUT_MS"), 30000)
	if err != nil {
		return nil, err
	}
	c.Timeout = time.Duration(ms) * time.Millisecond

	h := &cfg.HTTP
	h.Host = or(get("MCP_HTTP_HOST"), "0.0.0.0")
	if h.Port, err = positiveInt("MCP_HTTP_PORT", get("MCP_HTTP_PORT"), 8080); err != nil {
		return nil, err
	}
	h.AuthToken = get("MCP_AUTH_TOKEN")
	for _, host := range strings.Split(get("MCP_ALLOWED_HOSTS"), ",") {
		if host = strings.TrimSpace(host); host != "" {
			h.AllowedHosts = append(h.AllowedHosts, strings.ToLower(host))
		}
	}
	return cfg, firstErr
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
