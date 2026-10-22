package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readRepoFile reads a file from the repository root, skipping inside the
// Docker build, which only copies the Go sources.
func readRepoFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../..", name))
	if os.IsNotExist(err) {
		t.Skipf("%s is not available", name)
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestServerJSONMatchesManifest(t *testing.T) {
	raw := readRepoFile(t, "server.json")
	manifest := readRepoFile(t, "mcpb/manifest.json")
	dockerfile := readRepoFile(t, "Dockerfile")
	s, err := parseServerJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkServerJSON(s, raw, manifest, dockerfile); err != nil {
		t.Fatal(err)
	}

	// Drift is caught: a variable the bundle doesn't set, one it sets that's
	// missing, and settings whose flags disagree.
	mutate := func(name string, edit func(env []any) []any, want string) {
		t.Run(name, func(t *testing.T) {
			s, _ := parseServerJSON(raw)
			oci := packages(s, "oci")[0]
			list, _ := oci.get("environmentVariables").([]any)
			oci.set("environmentVariables", edit(list))
			err := checkEnv(oci, mustManifest(t, manifest))
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("error %v, want one mentioning %q", err, want)
			}
		})
	}
	find := func(env []any, name string) *object {
		for _, e := range env {
			if o := e.(*object); str(o.get("name")) == name {
				return o
			}
		}
		t.Fatalf("no %s", name)
		return nil
	}
	mutate("extra", func(env []any) []any {
		return append(env, newObject().set("name", "CRUNCH_NEW").set("description", "x"))
	}, "CRUNCH_NEW isn't set by the bundle manifest")
	mutate("missing", func(env []any) []any { return env[:len(env)-1] }, "CRUNCH_AUDIT_LOG is set by the bundle manifest but missing")
	mutate("secret", func(env []any) []any {
		find(env, "CRUNCH_CLIENT_SECRET").set("isSecret", false)
		return env
	}, "CRUNCH_CLIENT_SECRET: isSecret is false")
	mutate("default", func(env []any) []any {
		find(env, "CRUNCH_ALLOW_WRITES").set("default", "true")
		return env
	}, `CRUNCH_ALLOW_WRITES: default is "true"`)
	mutate("fixed value", func(env []any) []any {
		find(env, "MCP_TRANSPORT").set("value", "http")
		return env
	}, `MCP_TRANSPORT has value "http"`)

	if err := checkServerJSON(s, raw, manifest, []byte("FROM scratch\n")); err == nil {
		t.Error("a Dockerfile without the registry label was accepted")
	}
}

func mustManifest(t *testing.T, data []byte) *object {
	t.Helper()
	m, err := parseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestStamp(t *testing.T) {
	const input = `{"name":"io.github.mattoddie/crunch-mcp","version":"0.0.0-dev",
		"repository":{"url":"https://github.com/mattoddie/crunch-mcp","source":"github"},
		"packages":[
		{"registryType":"oci","identifier":"ghcr.io/mattoddie/crunch-mcp:0.0.0-dev","transport":{"type":"stdio"}},
		{"registryType":"mcpb","identifier":"https://github.com/mattoddie/crunch-mcp/releases/download/v0.0.0-dev/crunch-mcp-0.0.0-dev-linux-amd64.mcpb","version":"0.0.0-dev","fileSha256":"0000","transport":{"type":"stdio"}},
		{"registryType":"mcpb","identifier":"https://github.com/mattoddie/crunch-mcp/releases/download/v0.0.0-dev/crunch-mcp-0.0.0-dev-windows-arm64.mcpb","fileSha256":"0000","transport":{"type":"stdio"}}]}`
	dist := t.TempDir()
	writeFile(t, filepath.Join(dist, "crunch-mcp-1.2.3-linux-amd64.mcpb"), "abc")
	writeFile(t, filepath.Join(dist, "crunch-mcp-1.2.3-windows-arm64.mcpb"), "")
	writeFile(t, filepath.Join(dist, "crunch-mcp-1.2.2-darwin-arm64.mcpb"), "old release")

	s, _ := parseServerJSON([]byte(input))
	if err := stamp(s, "1.2.3", dist); err != nil {
		t.Fatal(err)
	}
	if v := s.get("version"); v != "1.2.3" {
		t.Errorf("version %v", v)
	}
	if id := packages(s, "oci")[0].get("identifier"); id != "ghcr.io/mattoddie/crunch-mcp:1.2.3" {
		t.Errorf("oci identifier %v", id)
	}
	b := packages(s, "mcpb")
	for i, want := range []struct{ id, sum string }{
		{"https://github.com/mattoddie/crunch-mcp/releases/download/v1.2.3/crunch-mcp-1.2.3-linux-amd64.mcpb", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"https://github.com/mattoddie/crunch-mcp/releases/download/v1.2.3/crunch-mcp-1.2.3-windows-arm64.mcpb", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	} {
		if id := b[i].get("identifier"); id != want.id {
			t.Errorf("bundle %d identifier %v", i, id)
		}
		if sum := b[i].get("fileSha256"); sum != want.sum {
			t.Errorf("bundle %d sha256 %v", i, sum)
		}
	}
	if v := b[0].get("version"); v != "1.2.3" {
		t.Errorf("bundle version %v", v)
	}
	if b[1].has("version") {
		t.Error("stamping added a version to a bundle without one")
	}

	// A bundle in dist that server.json doesn't list, or the other way round, fails.
	writeFile(t, filepath.Join(dist, "crunch-mcp-1.2.3-darwin-arm64.mcpb"), "new platform")
	s, _ = parseServerJSON([]byte(input))
	if err := stamp(s, "1.2.3", dist); err == nil || !strings.Contains(err.Error(), "darwin-arm64.mcpb isn't listed") {
		t.Errorf("unlisted bundle: %v", err)
	}
	s, _ = parseServerJSON([]byte(input))
	if err := stamp(s, "1.2.4", dist); err == nil || !strings.Contains(err.Error(), "isn't in") {
		t.Errorf("missing bundle: %v", err)
	}
	s, _ = parseServerJSON([]byte(input))
	if err := stamp(s, "v1.2.3", dist); err == nil {
		t.Error("a version with a v was accepted")
	}
}
