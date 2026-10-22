// Package config reads crunch-mcp's settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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
	// FilesDir is the only directory that attachment file_path and download save_to may use
	// (absolute, symlinks resolved). Empty disables both.
	FilesDir string
	// AuditLog is where calls of tools that change things are logged (CRUNCH_AUDIT_LOG):
	// a file path, AuditLogStderr, or "" for no log.
	AuditLog string
}

// AuditLogStderr is the CRUNCH_AUDIT_LOG value that writes the audit log to standard error.
const AuditLogStderr = "stderr"

// AllTools enables every tool, for documentation and tests.
func AllTools() Tools {
	t := Tools{AllowWrites: true, AllowDeletes: true, AllowEmail: true, Toolsets: map[string]bool{}}
	for _, ts := range Toolsets {
		t.Toolsets[ts] = true
	}
	return t
}

// MinAuthTokenLength is the shortest MCP_AUTH_TOKEN accepted.
const MinAuthTokenLength = 16

// HTTP holds the Streamable HTTP transport settings.
type HTTP struct {
	Host                 string
	Port                 int
	AuthToken            string
	AllowUnauthenticated bool
	// AllowedHosts are host or host:port entries checked against the Host and Origin headers.
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

// placeholder matches a ${user_config.<key>} reference that an MCP Bundle host such as
// Claude Desktop passed through unchanged because the user left an optional setting empty.
// It means "not set", not a value.
var placeholder = regexp.MustCompile(`^\$\{user_config\.[^}]*\}$`)

// lookup returns a trimmed variable, or "" when it is unset, blank or an unfilled bundle placeholder.
func lookup(env Env, name string) string {
	v, _ := env(name)
	v = strings.TrimSpace(v)
	if placeholder.MatchString(v) {
		return ""
	}
	return v
}

// read returns a trimmed variable (empty when unset, blank or an unfilled bundle
// placeholder), honouring the <NAME>_FILE convention for Docker secrets.
func read(env Env, name string) (string, error) {
	if file := lookup(env, name+"_FILE"); file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("could not read %s_FILE (%s): %w", name, file, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return lookup(env, name), nil
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

// checkURL validates a configured URL. Credentials in it are refused (they would be sent
// with every request and show up in logs). With cloud set, plain http:// is only accepted
// for loopback hosts, such as a local mock: the Crunch API and OAuth server carry bearer
// tokens and the client secret, which must not cross a network in cleartext.
func checkURL(name, v string, cloud bool) (string, error) {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%s is not a valid URL: %s", name, v)
	}
	if u.User != nil {
		return "", fmt.Errorf("%s must not contain a user name or password", name)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && (!cloud || isLoopback(u.Hostname())):
	case cloud:
		return "", fmt.Errorf("%s must use https:// (http:// is only allowed for localhost), got %s", name, v)
	default:
		return "", fmt.Errorf("%s must use https:// (or http://), got %s", name, v)
	}
	return strings.TrimRight(v, "/"), nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
	if c.APIURL, err = checkURL("CRUNCH_API_URL", or(get("CRUNCH_API_URL"), "https://public-api.crunch.co.uk/v1"), true); err != nil {
		return nil, err
	}
	if c.AuthURL, err = checkURL("CRUNCH_AUTH_URL", or(get("CRUNCH_AUTH_URL"), "https://oauth.crunch.co.uk"), true); err != nil {
		return nil, err
	}
	if c.RedirectURI, err = checkURL("CRUNCH_REDIRECT_URI", or(get("CRUNCH_REDIRECT_URI"), "http://127.0.0.1:8976/callback"), false); err != nil {
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
	h.Host = or(get("MCP_HTTP_HOST"), "127.0.0.1")
	if h.Port, err = positiveInt("MCP_HTTP_PORT", get("MCP_HTTP_PORT"), 8080); err != nil {
		return nil, err
	}
	h.AuthToken = get("MCP_AUTH_TOKEN")
	for _, host := range strings.Split(get("MCP_ALLOWED_HOSTS"), ",") {
		if host = strings.TrimSpace(host); host != "" {
			h.AllowedHosts = append(h.AllowedHosts, strings.ToLower(host))
		}
	}
	unauth, err := boolValue("MCP_ALLOW_UNAUTHENTICATED", get("MCP_ALLOW_UNAUTHENTICATED"), false)
	if err != nil {
		return nil, err
	}
	// An HTTP server without a token would hand every enabled tool to anyone who can reach the port.
	h.AllowUnauthenticated = unauth
	if cfg.Transport == "http" {
		switch {
		case h.AuthToken == "" && !unauth:
			return nil, errors.New("MCP_AUTH_TOKEN is required for the http transport (generate one with: openssl rand -hex 32); set MCP_ALLOW_UNAUTHENTICATED=true only behind something else that authenticates")
		case h.AuthToken != "" && len(h.AuthToken) < MinAuthTokenLength:
			return nil, fmt.Errorf("MCP_AUTH_TOKEN must be at least %d characters (generate one with: openssl rand -hex 32)", MinAuthTokenLength)
		case h.AuthToken == "" && len(h.AllowedHosts) == 0:
			// Without a token, nothing stops a web page the user visits from reaching the
			// server through DNS rebinding (its own hostname resolving to this address),
			// except checking the Host header against a list.
			return nil, errors.New("MCP_ALLOW_UNAUTHENTICATED=true also requires MCP_ALLOWED_HOSTS (the hostnames clients use), so web pages can't reach the server through DNS rebinding")
		}
	}

	if t.FilesDir, err = filesDir(get("CRUNCH_FILES_DIR")); err != nil {
		return nil, err
	}
	t.AuditLog = get("CRUNCH_AUDIT_LOG")
	if err := CheckAuditLog(t.AuditLog); err != nil {
		return nil, err
	}
	return cfg, firstErr
}

// SecretsDir is where Docker and Kubernetes mount secrets. Tools never read or write files there.
const SecretsDir = "/run/secrets"

// filesDir resolves CRUNCH_FILES_DIR to an absolute path without symlinks. It must be an
// existing directory, and not /run/secrets or inside it.
func filesDir(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("CRUNCH_FILES_DIR (%s): %w", dir, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("CRUNCH_FILES_DIR (%s) must be an existing directory: %w", dir, err)
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return "", fmt.Errorf("CRUNCH_FILES_DIR (%s) must be an existing directory", dir)
	}
	if Within(real, SecretsDir) {
		return "", fmt.Errorf("CRUNCH_FILES_DIR (%s) must not be inside %s", dir, SecretsDir)
	}
	return real, nil
}

// Within reports whether path is dir or inside it. Both must be clean and absolute.
func Within(path, dir string) bool {
	if path == dir {
		return true
	}
	if !strings.HasSuffix(dir, string(filepath.Separator)) {
		dir += string(filepath.Separator)
	}
	return strings.HasPrefix(path, dir)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// AuditLogMode is the permission an audit log file is created with: only the server's
// user can read it, since it records what was changed in the accounts and when.
const AuditLogMode = 0o600

// OpenAuditLog opens an audit log file for appending, creating it if needed.
func OpenAuditLog(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, AuditLogMode)
}

// CheckAuditLog checks that CRUNCH_AUDIT_LOG can be written, so a mistake is reported
// at startup rather than on the first write.
func CheckAuditLog(path string) error {
	if path == "" || path == AuditLogStderr {
		return nil
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("CRUNCH_AUDIT_LOG must be an absolute path or stderr, got %q", path)
	}
	if Within(filepath.Clean(path), SecretsDir) {
		return fmt.Errorf("CRUNCH_AUDIT_LOG must not be inside %s", SecretsDir)
	}
	f, err := OpenAuditLog(path)
	if err != nil {
		return fmt.Errorf("CRUNCH_AUDIT_LOG: cannot write to %s: %w. Its directory must exist and be writable by the server (in Docker, mount a writable directory; the image runs as UID 65532)", path, err)
	}
	return f.Close()
}
