package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mattoddie/crunch-mcp/internal/config"
)

// The access switches are enforced in the request bodies too, because Crunch has
// side doors: payment reminders are emails, and an update deletes the payments
// and attachments it leaves out.

// ---------------------------------------------------------------- email

const remindersDisabled = "payment reminders are emails Crunch sends to the customer, so turning on sendReminders needs CRUNCH_ALLOW_EMAIL=true"

// remindersOn reports whether v sets Crunch's sendReminders flag (in any letter
// case, at any depth) to anything other than false.
func remindersOn(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if normKey(k) == "sendreminders" {
				if b, ok := val.(bool); !ok || b {
					return true
				}
				continue
			}
			if remindersOn(val) {
				return true
			}
		}
	case []any:
		for _, val := range x {
			if remindersOn(val) {
				return true
			}
		}
	case []Obj:
		for _, val := range x {
			if remindersOn(val) {
				return true
			}
		}
	}
	return false
}

func normKey(k string) string {
	return strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(k))
}

// checkReminders refuses raw fields that turn on payment reminders unless email is allowed.
func (c *Context) checkReminders(raw any) error {
	if !c.AllowEmail && remindersOn(raw) {
		return errors.New(remindersDisabled)
	}
	return nil
}

// ---------------------------------------------------------------- deletes

// keptLists are the lists Crunch replaces wholesale on an update, deleting any entry left out.
var keptLists = []string{"payments", "supplierPayments", "customerPayments", "attachments"}

// checkKeepsEntries refuses an update body that would drop existing payments or
// attachments of current, unless deletes are allowed. A list left out of the
// body counts as dropping its entries.
func (c *Context) checkKeepsEntries(current, body Obj) error {
	if c.AllowDeletes {
		return nil
	}
	for k := range body {
		for _, name := range keptLists {
			if k != name && strings.EqualFold(k, name) {
				return fmt.Errorf("use Crunch's field name %q, not %q", name, k)
			}
		}
	}
	for _, name := range keptLists {
		have := entryIDs(current[name])
		if len(have) == 0 {
			continue
		}
		kept := map[string]bool{}
		for _, id := range entryIDs(body[name]) {
			kept[id] = true
		}
		var missing []string
		for _, id := range have {
			if !kept[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return fmt.Errorf("this update leaves out existing %s (id %s), and Crunch deletes whatever an update leaves out. Delete access is disabled (CRUNCH_ALLOW_DELETES=false): send the existing entries back as the API returned them", name, strings.Join(missing, ", "))
		}
	}
	return nil
}

// entryIDs returns the ids of the objects in a JSON list (from the API or built here).
func entryIDs(list any) []string {
	if list == nil {
		return nil
	}
	b, err := json.Marshal(list)
	if err != nil {
		return nil
	}
	var items []any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&items) != nil {
		return nil
	}
	var ids []string
	for _, it := range items {
		if o, ok := it.(map[string]any); ok && o["id"] != nil {
			ids = append(ids, fmt.Sprint(o["id"]))
		}
	}
	return ids
}

// ---------------------------------------------------------------- local files

// filesPath checks that p (absolute, or relative to CRUNCH_FILES_DIR) is inside
// CRUNCH_FILES_DIR once symlinks are resolved, and isn't the token file or a
// secret. It returns the path relative to CRUNCH_FILES_DIR.
func (c *Context) filesPath(p string) (string, error) {
	if c.FilesDir == "" {
		return "", errors.New("reading and saving local files is disabled. Send attachments as content_base64, or set CRUNCH_FILES_DIR to a directory this server may use for file_path and save_to")
	}
	if p == "" {
		return "", errors.New("empty path")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.FilesDir, p)
	}
	real := resolveExisting(filepath.Clean(p))
	if !config.Within(real, c.FilesDir) {
		return "", fmt.Errorf("%s is outside CRUNCH_FILES_DIR (%s)", p, c.FilesDir)
	}
	if config.Within(real, config.SecretsDir) || c.isTokenPath(real) {
		return "", fmt.Errorf("refusing to use %s: it holds credentials", p)
	}
	return filepath.Rel(c.FilesDir, real)
}

// resolveExisting resolves symlinks in the longest existing prefix of p.
func resolveExisting(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(resolveExisting(parent), filepath.Base(p))
}

// isTokenPath reports whether p is the token file or one of its lock and temporary files.
func (c *Context) isTokenPath(p string) bool {
	if c.Client == nil || c.Client.Auth == nil {
		return false
	}
	tf, err := filepath.Abs(c.Client.Auth.Store.File)
	if err != nil {
		return false
	}
	tf = resolveExisting(tf)
	return p == tf || strings.HasPrefix(p, tf+".")
}

func (c *Context) isTokenFile(st os.FileInfo) bool {
	if c.Client == nil || c.Client.Auth == nil {
		return false
	}
	tst, err := os.Stat(c.Client.Auth.Store.File)
	return err == nil && os.SameFile(st, tst)
}

// readLocalFile reads an attachment's file_path from CRUNCH_FILES_DIR.
func (c *Context) readLocalFile(p string) ([]byte, error) {
	rel, err := c.filesPath(p)
	if err != nil {
		return nil, err
	}
	// os.Root refuses anything that escapes the directory, even if a symlink changes after the check above.
	root, err := os.OpenRoot(c.FilesDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	if c.isTokenFile(st) {
		return nil, fmt.Errorf("refusing to use %s: it holds credentials", p)
	}
	if st.Size() > maxUploadBytes {
		return nil, fmt.Errorf("%s is larger than 10 MB", p)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxUploadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxUploadBytes {
		return nil, fmt.Errorf("%s is larger than 10 MB", p)
	}
	return data, nil
}

// saveLocalFile writes a downloaded file to saveTo (a directory or a file path inside
// CRUNCH_FILES_DIR) without overwriting anything, and returns where it went.
func (c *Context) saveLocalFile(saveTo, name string, data []byte) (string, error) {
	rel, err := c.filesPath(saveTo)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(c.FilesDir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if st, err := root.Stat(rel); err == nil && st.IsDir() {
		base := filepath.Base(name)
		if base == "." || base == ".." || base == string(filepath.Separator) {
			base = "download"
		}
		if rel, err = c.filesPath(filepath.Join(c.FilesDir, rel, base)); err != nil {
			return "", err
		}
	}
	target := filepath.Join(c.FilesDir, rel)
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("%s already exists; choose another save_to", target)
		}
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	return target, f.Close()
}
