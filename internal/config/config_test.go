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
	if cfg.Transport != "stdio" || cfg.HTTP.Port != 8080 || cfg.HTTP.Host != "0.0.0.0" {
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
