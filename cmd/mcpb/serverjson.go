package main

// server.json describes crunch-mcp to the official MCP Registry. The committed
// file carries the placeholder version 0.0.0-dev; the release workflow stamps
// the real version, image tag and bundle hashes into a copy and publishes that.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const devVersion = "0.0.0-dev"

func serverJSONCmd(args []string) error {
	fs := flag.NewFlagSet("server-json", flag.ExitOnError)
	check := fs.Bool("check", false, "check server.json against the bundle manifest and the Dockerfile instead of stamping it")
	file := fs.String("f", "server.json", "server.json to read")
	manifest := fs.String("manifest", "mcpb/manifest.json", "bundle manifest (with -check)")
	dockerfile := fs.String("dockerfile", "Dockerfile", "Dockerfile (with -check)")
	version := fs.String("version", "", "version to stamp (X.Y.Z, without the v)")
	dist := fs.String("dist", "dist", "directory with the release's .mcpb bundles")
	out := fs.String("o", "", "the stamped server.json to write")
	_ = fs.Parse(args)

	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	s, err := parseServerJSON(data)
	if err != nil {
		return err
	}
	if *check {
		m, err := os.ReadFile(*manifest)
		if err != nil {
			return err
		}
		df, err := os.ReadFile(*dockerfile)
		if err != nil {
			return err
		}
		if err := checkServerJSON(s, data, m, df); err != nil {
			return fmt.Errorf("%s: %w", *file, err)
		}
		fmt.Printf("%s matches %s and %s.\n", *file, *manifest, *dockerfile)
		return nil
	}
	if *version == "" || *out == "" {
		return errors.New("server-json needs -check, or -version and -o\n\n" + usage)
	}
	if err := stamp(s, *version, *dist); err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(format(s)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("Wrote %s for %s.\n", *out, *version)
	return nil
}

func parseServerJSON(data []byte) (*object, error) {
	v, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("server.json: %w", err)
	}
	s, ok := v.(*object)
	if !ok {
		return nil, errors.New("server.json: not a JSON object")
	}
	return s, nil
}

// packages returns the server's packages of one registry type.
func packages(s *object, registryType string) []*object {
	list, _ := s.get("packages").([]any)
	var out []*object
	for _, p := range list {
		if o, ok := p.(*object); ok && str(o.get("registryType")) == registryType {
			out = append(out, o)
		}
	}
	return out
}

// bundleName matches a bundle's file name: crunch-mcp-<version>-<os>-<arch>.mcpb.
var bundleName = regexp.MustCompile(`^crunch-mcp-(.+)-([a-z0-9]+)-([a-z0-9]+)\.mcpb$`)

// checkServerJSON keeps server.json in step with what the release ships: the
// committed placeholder version, the Dockerfile's ownership label, and the
// container's environment variables, which mirror the bundle's settings.
func checkServerJSON(s *object, raw, manifestData, dockerfile []byte) error {
	if got := format(s) + "\n"; got != string(raw) {
		return errors.New("is not formatted like the release workflow writes it (two-space indent, no HTML escaping)")
	}
	name := str(s.get("name"))
	if name != "io.github.mattoddie/crunch-mcp" {
		return fmt.Errorf("name is %q, want io.github.mattoddie/crunch-mcp", name)
	}
	if d := str(s.get("description")); d == "" || utf8.RuneCountInString(d) > 100 {
		return fmt.Errorf("description must be 1 to 100 characters (it has %d)", utf8.RuneCountInString(d))
	}
	if v := str(s.get("version")); v != devVersion {
		return fmt.Errorf("version is %q; keep the placeholder %s, the release workflow stamps the real one", v, devVersion)
	}
	label := fmt.Sprintf("io.modelcontextprotocol.server.name=%q", name)
	if !strings.Contains(string(dockerfile), label) {
		return fmt.Errorf("the Dockerfile has no LABEL %s, which the registry needs to verify the image", label)
	}

	oci := packages(s, "oci")
	if len(oci) != 1 {
		return fmt.Errorf("want one oci package, found %d", len(oci))
	}
	if id, want := str(oci[0].get("identifier")), "ghcr.io/mattoddie/crunch-mcp:"+devVersion; id != want {
		return fmt.Errorf("oci identifier is %q, want %q", id, want)
	}
	bundles := packages(s, "mcpb")
	if len(bundles) == 0 {
		return errors.New("no mcpb packages")
	}
	for _, p := range bundles {
		file := path.Base(str(p.get("identifier")))
		if m := bundleName.FindStringSubmatch(file); m == nil || m[1] != devVersion {
			return fmt.Errorf("mcpb identifier %q doesn't end in crunch-mcp-%s-<os>-<arch>.mcpb", str(p.get("identifier")), devVersion)
		}
	}

	manifest, err := parseManifest(manifestData)
	if err != nil {
		return err
	}
	return checkEnv(oci[0], manifest)
}

// checkEnv compares the image's environment variables with the bundle's: the
// same names, fixed values where the bundle fixes them, and the same required,
// secret, boolean and default flags as the bundle's settings.
func checkEnv(oci, manifest *object) error {
	menv := manifest.child("server").child("mcp_config").child("env")
	if menv == nil {
		return errors.New("the manifest has no server.mcp_config.env")
	}
	userConfig := manifest.child("user_config")
	list, _ := oci.get("environmentVariables").([]any)
	seen := map[string]bool{}
	var errs []error
	for _, e := range list {
		v, _ := e.(*object)
		name := str(v.get("name"))
		seen[name] = true
		if !menv.has(name) {
			errs = append(errs, fmt.Errorf("%s isn't set by the bundle manifest", name))
			continue
		}
		if str(v.get("description")) == "" {
			errs = append(errs, fmt.Errorf("%s has no description", name))
		}
		mval := str(menv.get(name))
		ref := userConfigRef.FindStringSubmatch(mval)
		if ref == nil {
			if got := str(v.get("value")); got != mval {
				errs = append(errs, fmt.Errorf("%s has value %q, the bundle sets %q", name, got, mval))
			}
			continue
		}
		if v.has("value") {
			errs = append(errs, fmt.Errorf("%s has a fixed value, but the bundle lets the user set it", name))
		}
		setting := userConfig.child(ref[1])
		if setting == nil {
			errs = append(errs, fmt.Errorf("%s: the manifest has no user_config.%s", name, ref[1]))
			continue
		}
		flags := []struct{ ours, theirs string }{{"isRequired", "required"}, {"isSecret", "sensitive"}}
		for _, f := range flags {
			if got, want := v.get(f.ours) == true, setting.get(f.theirs) == true; got != want {
				errs = append(errs, fmt.Errorf("%s: %s is %t, but user_config.%s.%s is %t", name, f.ours, got, ref[1], f.theirs, want))
			}
		}
		if got, want := str(v.get("format")) == "boolean", str(setting.get("type")) == "boolean"; got != want {
			errs = append(errs, fmt.Errorf("%s: format boolean is %t, but user_config.%s is a %s", name, got, ref[1], str(setting.get("type"))))
		}
		if got, want := str(v.get("default")), str(setting.get("default")); got != want {
			errs = append(errs, fmt.Errorf("%s: default is %q, but user_config.%s defaults to %q", name, got, ref[1], want))
		}
	}
	for _, name := range menv.keys {
		if !seen[name] {
			errs = append(errs, fmt.Errorf("%s is set by the bundle manifest but missing from the oci package", name))
		}
	}
	return errors.Join(errs...)
}

// stamp sets the release's version, image tag and bundle download URLs and
// SHA-256 hashes. Every bundle in dist must be listed and every listed bundle
// must be in dist, so a new platform can't be left out silently.
func stamp(s *object, version, dist string) error {
	if !semver.MatchString(version) {
		return fmt.Errorf("version %q is not a semantic version like 1.2.3", version)
	}
	s.set("version", version)
	oci := packages(s, "oci")
	if len(oci) != 1 {
		return fmt.Errorf("want one oci package, found %d", len(oci))
	}
	id := str(oci[0].get("identifier"))
	repo, _, ok := strings.Cut(id, ":")
	if !ok {
		return fmt.Errorf("oci identifier %q has no tag", id)
	}
	oci[0].set("identifier", repo+":"+version)

	base := strings.TrimSuffix(str(s.child("repository").get("url")), "/")
	if base == "" {
		return errors.New("server.json has no repository.url")
	}
	found, err := filepath.Glob(filepath.Join(dist, "crunch-mcp-"+version+"-*.mcpb"))
	if err != nil {
		return err
	}
	unused := map[string]bool{}
	for _, f := range found {
		unused[filepath.Base(f)] = true
	}
	for _, p := range packages(s, "mcpb") {
		m := bundleName.FindStringSubmatch(path.Base(str(p.get("identifier"))))
		if m == nil {
			return fmt.Errorf("can't tell the platform of mcpb identifier %q", str(p.get("identifier")))
		}
		file := fmt.Sprintf("crunch-mcp-%s-%s-%s.mcpb", version, m[2], m[3])
		if !unused[file] {
			return fmt.Errorf("%s is listed in server.json but isn't in %s", file, dist)
		}
		delete(unused, file)
		sum, err := sha256File(filepath.Join(dist, file))
		if err != nil {
			return err
		}
		p.set("identifier", fmt.Sprintf("%s/releases/download/v%s/%s", base, version, file))
		if p.has("version") {
			p.set("version", version)
		}
		p.set("fileSha256", sum)
	}
	if len(unused) > 0 {
		names := make([]string, 0, len(unused))
		for f := range unused {
			names = append(names, f)
		}
		slices.Sort(names)
		return fmt.Errorf("%s isn't listed in server.json; add a package for it", strings.Join(names, ", "))
	}
	return nil
}

func sha256File(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
