package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg, err := Load(MapEnv(map[string]string{"HOME": "/home/x"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Transport != "stdio" || cfg.HTTP.Port != 8080 || cfg.HTTP.Host != "127.0.0.1" || cfg.Tools.FilesDir != "" {
		t.Errorf("transport defaults: %+v %+v", cfg.Transport, cfg.HTTP)
	}
	c := cfg.Crunch
	if c.APIURL != "https://public-api.crunch.co.uk/v1" || c.AuthURL != "https://oauth.crunch.co.uk" {
		t.Errorf("URLs: %s %s", c.APIURL, c.AuthURL)
	}
	if c.RedirectURI != "http://127.0.0.1:8976/callback" || c.Timeout != 30*time.Second {
		t.Errorf("redirect/timeout: %s %s", c.RedirectURI, c.Timeout)
	}
	if c.TokenFile != filepath.Join("/home/x", ".config", "crunch-mcp", "tokens.json") {
		t.Errorf("token file: %s", c.TokenFile)
	}
	if c.Scopes != "read:all offline_access" {
		t.Errorf("read-only scopes: %q", c.Scopes)
	}
	if cfg.Tools.AllowWrites || cfg.Tools.AllowDeletes || cfg.Tools.AllowEmail || len(cfg.Tools.Toolsets) != len(Toolsets) {
		t.Errorf("tools: %+v", cfg.Tools)
	}
}

func TestScopesFollowWriteSettings(t *testing.T) {
	cfg, err := Load(MapEnv(map[string]string{"CRUNCH_ALLOW_WRITES": "true", "CRUNCH_ALLOW_DELETES": "yes"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Crunch.Scopes != "read:all create:all update:all delete:all offline_access" {
		t.Errorf("scopes: %q", cfg.Crunch.Scopes)
	}
	cfg, _ = Load(MapEnv(map[string]string{"CRUNCH_SCOPES": "all offline_access"}))
	if cfg.Crunch.Scopes != "all offline_access" {
		t.Errorf("override: %q", cfg.Crunch.Scopes)
	}
}

func TestValidation(t *testing.T) {
	for env, want := range map[string]string{
		"CRUNCH_ALLOW_DELETES=true":           "requires CRUNCH_ALLOW_WRITES",
		"CRUNCH_ALLOW_EMAIL=true":             "requires CRUNCH_ALLOW_WRITES",
		"CRUNCH_ALLOW_WRITES=maybe":           "must be true or false",
		"MCP_TRANSPORT=sse":                   "stdio or http",
		"CRUNCH_TOOLSETS=expenses,nope":       `unknown toolset "nope"`,
		"CRUNCH_API_URL=ftp://x":              "https://",
		"CRUNCH_TIMEOUT_MS=-1":                "positive integer",
		"MCP_HTTP_PORT=http":                  "positive integer",
		"CRUNCH_CLIENT_SECRET_FILE=/nonexist": "CRUNCH_CLIENT_SECRET_FILE",
	} {
		k, v, _ := strings.Cut(env, "=")
		_, err := Load(MapEnv(map[string]string{k: v}))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", env, err, want)
		}
	}
}

func TestToolsetsAndSecretsFromFiles(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(MapEnv(map[string]string{
		"CRUNCH_TOOLSETS":           " Expenses, invoices ",
		"CRUNCH_CLIENT_SECRET_FILE": secret,
		"MCP_ALLOWED_HOSTS":         "MCP.example.com, localhost:8080",
		"XDG_CONFIG_HOME":           "/xdg",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tools.Toolsets) != 2 || !cfg.Tools.Toolsets["expenses"] || !cfg.Tools.Toolsets["invoices"] {
		t.Errorf("toolsets: %v", cfg.Tools.Toolsets)
	}
	if cfg.Crunch.ClientSecret != "s3cret" {
		t.Errorf("secret from file: %q", cfg.Crunch.ClientSecret)
	}
	if strings.Join(cfg.HTTP.AllowedHosts, ",") != "mcp.example.com,localhost:8080" {
		t.Errorf("allowed hosts: %v", cfg.HTTP.AllowedHosts)
	}
	if cfg.Crunch.TokenFile != "/xdg/crunch-mcp/tokens.json" {
		t.Errorf("token file: %s", cfg.Crunch.TokenFile)
	}
}

func TestHTTPNeedsAuthToken(t *testing.T) {
	base := map[string]string{"MCP_TRANSPORT": "http"}
	if _, err := Load(MapEnv(base)); err == nil || !strings.Contains(err.Error(), "MCP_AUTH_TOKEN is required") {
		t.Fatalf("http without a token: got %v", err)
	}
	base["MCP_ALLOW_UNAUTHENTICATED"] = "true"
	if _, err := Load(MapEnv(base)); err != nil {
		t.Fatalf("explicit opt-out: %v", err)
	}
	base["MCP_ALLOW_UNAUTHENTICATED"] = "maybe"
	if _, err := Load(MapEnv(base)); err == nil || !strings.Contains(err.Error(), "must be true or false") {
		t.Fatalf("bad opt-out value: %v", err)
	}
	delete(base, "MCP_ALLOW_UNAUTHENTICATED")
	base["MCP_AUTH_TOKEN"] = "secret-token"
	if _, err := Load(MapEnv(base)); err != nil {
		t.Fatalf("with a token: %v", err)
	}
	// stdio has no network listener, so it needs no token.
	if _, err := Load(MapEnv(map[string]string{"MCP_TRANSPORT": "stdio"})); err != nil {
		t.Fatalf("stdio: %v", err)
	}
}

func TestFilesDir(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	cfg, err := Load(MapEnv(map[string]string{"CRUNCH_FILES_DIR": link}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.FilesDir != real {
		t.Errorf("files dir: %q, want the resolved %q", cfg.Tools.FilesDir, real)
	}
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{filepath.Join(dir, "missing"), file} {
		if _, err := Load(MapEnv(map[string]string{"CRUNCH_FILES_DIR": bad})); err == nil || !strings.Contains(err.Error(), "existing directory") {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if _, err := filesDir("/"); err != nil {
		t.Errorf("/: %v", err)
	}
	if !Within("/run/secrets/x", SecretsDir) || !Within("/run/secrets", SecretsDir) || Within("/run/secretsx", SecretsDir) || !Within("/a", "/") {
		t.Error("Within")
	}
}
