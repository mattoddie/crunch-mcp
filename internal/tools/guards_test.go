package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattoddie/crunch-mcp/internal/config"
	"github.com/mattoddie/crunch-mcp/internal/crunch"
	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

func writesOnly() config.Tools {
	o := config.AllTools()
	o.AllowDeletes, o.AllowEmail = false, false
	return o
}

var oneItem = []any{map[string]any{"description": "Work", "price_excl_vat": 100, "vat_rate": "Standard"}}

func TestPaymentRemindersNeedEmail(t *testing.T) {
	env := newEnv(t, writesOnly())
	env.mock.On("GET", "/invoices/500", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, invoiceFixture) })
	create := func(extra map[string]any) map[string]any {
		args := map[string]any{"client_id": 42, "issued_date": "2026-09-05", "items": oneItem}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}
	for name, args := range map[string]map[string]any{
		"create send_reminders":    create(map[string]any{"send_reminders": true}),
		"create extra":             create(map[string]any{"extra": map[string]any{"payment": map[string]any{"paymentTerms": map[string]any{"SendReminders": true}}}}),
		"create extra, not a bool": create(map[string]any{"extra": map[string]any{"sendReminders": "yes"}}),
	} {
		if msg := env.callErr("crunch_create_invoice", args); !strings.Contains(msg, "CRUNCH_ALLOW_EMAIL") {
			t.Errorf("%s: %s", name, msg)
		}
	}
	for name, args := range map[string]map[string]any{
		"update send_reminders": {"invoice_id": 500, "send_reminders": true},
		"update changes":        {"invoice_id": 500, "changes": map[string]any{"payment": map[string]any{"paymentTerms": map[string]any{"send_reminders": true}}}},
	} {
		if msg := env.callErr("crunch_update_invoice", args); !strings.Contains(msg, "CRUNCH_ALLOW_EMAIL") {
			t.Errorf("%s: %s", name, msg)
		}
	}
	if msg := env.callErr("crunch_update_client", map[string]any{"client_id": 1, "extra": map[string]any{"sendReminders": true}}); !strings.Contains(msg, "CRUNCH_ALLOW_EMAIL") {
		t.Errorf("client extra: %s", msg)
	}
	if env.mock.Last("POST") != nil || env.mock.Last("PUT") != nil {
		t.Fatal("a refused request reached Crunch")
	}

	// Turning reminders off is fine.
	env.call("crunch_create_invoice", create(map[string]any{"send_reminders": false}))
	if got := asJSON(t, env.mock.Last("POST").Body.(Obj)["payment"]); got != `{"paymentTerms":{"sendReminders":false}}` {
		t.Errorf("reminders off: %s", got)
	}

	// The raw tool: turning reminders on is refused, keeping them on where they already are is not.
	raw := func(method, path string, body any) map[string]any {
		return map[string]any{"method": method, "path": path, "body": body}
	}
	if msg := env.callErr("crunch_api_request", raw("POST", "/invoices", map[string]any{"payment": map[string]any{"paymentTerms": map[string]any{"sendReminders": true}}})); !strings.Contains(msg, "CRUNCH_ALLOW_EMAIL") {
		t.Errorf("raw POST: %s", msg)
	}
	inv := jsonObj(t, invoiceFixture)
	inv["payment"] = Obj{"paymentTerms": Obj{"sendReminders": true}}
	if msg := env.callErr("crunch_api_request", raw("PUT", "/invoices/500", inv)); !strings.Contains(msg, "CRUNCH_ALLOW_EMAIL") {
		t.Errorf("raw PUT turning reminders on: %s", msg)
	}
	inv["id"] = 501
	env.mock.On("GET", "/invoices/501", crunchtest.JSON(inv))
	env.call("crunch_api_request", raw("PUT", "/invoices/501", inv))

	// With email allowed, all of it works.
	o := writesOnly()
	o.AllowEmail = true
	env = newEnv(t, o)
	env.mock.On("GET", "/invoices/500", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, invoiceFixture) })
	env.call("crunch_create_invoice", create(map[string]any{"send_reminders": true}))
	env.call("crunch_update_invoice", map[string]any{"invoice_id": 500, "send_reminders": true})
	if got := asJSON(t, env.mock.Last("PUT").Body.(Obj)["payment"]); got != `{"paymentTerms":{"sendReminders":true}}` {
		t.Errorf("reminders on: %s", got)
	}
}

func TestUpdatesKeepPaymentsAndAttachmentsWithoutDeletes(t *testing.T) {
	env := newEnv(t, writesOnly())
	env.mock.On("GET", "/invoices/500", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, invoiceFixture) })
	env.mock.On("GET", "/expenses/77", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, expenseFixture) })
	env.mock.On("GET", "/expenses/recurring-expenses/8", crunchtest.JSON(Obj{"id": 8, "supplierPayments": []any{Obj{"id": 1, "date": "2026-11-01"}}}))
	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"crunch_update_invoice", map[string]any{"invoice_id": 500, "changes": map[string]any{"payments": []any{}}}, "payments (id 600)"},
		{"crunch_update_invoice", map[string]any{"invoice_id": 500, "changes": map[string]any{"attachments": nil}}, "attachments (id 0)"},
		{"crunch_update_invoice", map[string]any{"invoice_id": 500, "changes": map[string]any{"Payments": []any{}}}, `field name "payments"`},
		{"crunch_update_expense", map[string]any{"expense_id": 77, "changes": map[string]any{"supplierPayments": []any{}, "attachments": []any{}}}, "CRUNCH_ALLOW_DELETES"},
		{"crunch_update_recurring_expense", map[string]any{"recurring_expense_id": 8, "changes": map[string]any{"supplierPayments": []any{}}}, "supplierPayments (id 1)"},
		{"crunch_api_request", map[string]any{"method": "PUT", "path": "/invoices/500", "body": map[string]any{"reference": "x"}}, "payments (id 600)"},
		{"crunch_api_request", map[string]any{"method": "PUT", "path": "/expenses/77/", "body": map[string]any{"supplierPayments": []any{Obj{"id": 300}}}}, "attachments (id 0)"},
		{"crunch_api_request", map[string]any{"method": "PUT", "path": "/invoices/500", "body": []any{}}, "JSON object"},
	} {
		if msg := env.callErr(c.tool, c.args); !strings.Contains(msg, c.want) {
			t.Errorf("%s %v: %s", c.tool, c.args, msg)
		}
	}
	if env.mock.Last("PUT") != nil {
		t.Fatal("a refused update reached Crunch")
	}

	// Ordinary updates send the existing entries back, so they pass.
	env.call("crunch_update_invoice", map[string]any{"invoice_id": 500, "reference": "PO-1"})
	env.call("crunch_update_expense", map[string]any{"expense_id": 77, "note": "ok"})
	env.call("crunch_api_request", map[string]any{"method": "PUT", "path": "/invoices/500", "body": jsonObj(t, invoiceFixture)})
	// Adding a payment keeps the old ones too.
	env.call("crunch_update_invoice", map[string]any{"invoice_id": 500, "changes": map[string]any{"payments": []any{Obj{"id": 600}, Obj{"amount": 5}}}})

	// With deletes allowed, dropping entries is the caller's choice.
	env = newEnv(t)
	env.mock.On("GET", "/invoices/500", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, invoiceFixture) })
	env.call("crunch_update_invoice", map[string]any{"invoice_id": 500, "changes": map[string]any{"payments": []any{}}})
	env.call("crunch_api_request", map[string]any{"method": "PUT", "path": "/invoices/500", "body": map[string]any{"reference": "x"}})
}

func TestRawPathsMustBeCanonical(t *testing.T) {
	env := newEnv(t, writesOnly())
	for _, p := range []string{
		"/invoices/1/email/.", "/invoices/1/email;x", "/invoices/1//email", "/invoices/1/./email", "/invoices/x/../1/email",
		"/invoices/1/%65mail", "/invoices/1/email%2F", "/invoices/1/email\x00", "/invoices/1/email\n", "/invoices/1/ email",
		"/invoices/1/email//", "/invoices/1/émail", "//invoices/1/email", "/v1//invoices/1/email",
	} {
		if msg := env.callErr("crunch_api_request", map[string]any{"method": "POST", "path": p}); !strings.Contains(msg, "path must") {
			t.Errorf("%q: %s", p, msg)
		}
	}
	for _, p := range []string{"/invoices/1/email", "/invoices/1/email/", "/Invoices/1/EMAIL", "/v1/invoices/1/email", "/invoices/1/send-reminder"} {
		if msg := env.callErr("crunch_api_request", map[string]any{"method": "POST", "path": p}); !strings.Contains(msg, "CRUNCH_ALLOW_EMAIL") {
			t.Errorf("%q: %s", p, msg)
		}
	}
	if env.mock.Last("") != nil {
		t.Fatal("a refused request reached Crunch")
	}
	// Crunch's own camelCase paths still work.
	env.mock.On("GET", "/banking/credit-cards/3/openingBalance", crunchtest.JSON(Obj{"openingBalance": "100"}))
	env.call("crunch_api_request", map[string]any{"path": "/banking/credit-cards/3/openingBalance"})
}

func TestLocalFilesStayInFilesDir(t *testing.T) {
	// Disabled without CRUNCH_FILES_DIR.
	env := newEnv(t)
	env.mock.On("GET", "/invoices/500", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, invoiceFixture) })
	env.mock.On("GET", "/invoices/500/pdf", func(crunchtest.Request, []string) (int, any) { return 200, []byte("%PDF") })
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	attach := func(p string) map[string]any {
		return map[string]any{"invoice_id": 500, "attachments": []any{map[string]any{"file_name": "a.txt", "file_path": p}}}
	}
	if msg := env.callErr("crunch_add_invoice_attachment", attach(outside)); !strings.Contains(msg, "CRUNCH_FILES_DIR") {
		t.Errorf("file_path without a files dir: %s", msg)
	}
	if msg := env.callErr("crunch_download_invoice_pdf", map[string]any{"invoice_id": 500, "save_to": t.TempDir()}); !strings.Contains(msg, "CRUNCH_FILES_DIR") {
		t.Errorf("save_to without a files dir: %s", msg)
	}

	// With it, only paths inside it work.
	dir := filesDir(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	o := config.AllTools()
	o.FilesDir = dir
	env = newEnv(t, o)
	env.mock.On("GET", "/invoices/500", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, invoiceFixture) })
	env.mock.On("GET", "/invoices/500/pdf", func(crunchtest.Request, []string) (int, any) { return 200, []byte("%PDF") })
	for _, p := range []string{"a.txt", filepath.Join(dir, "a.txt")} {
		env.call("crunch_add_invoice_attachment", attach(p))
		files := env.mock.Last("PUT").Body.(Obj)["attachments"].([]any)
		if got := files[len(files)-1].(Obj)["fileContents"]; got != "data:text/plain;base64,aGk=" {
			t.Errorf("%s: %v", p, got)
		}
	}
	for _, p := range []string{outside, "../secret.txt", "link.txt", "out/secret.txt", filepath.Join(dir, "..", "x")} {
		if msg := env.callErr("crunch_add_invoice_attachment", attach(p)); !strings.Contains(msg, "outside CRUNCH_FILES_DIR") {
			t.Errorf("file_path %s: %s", p, msg)
		}
		if msg := env.callErr("crunch_download_invoice_pdf", map[string]any{"invoice_id": 500, "save_to": p}); !strings.Contains(msg, "outside CRUNCH_FILES_DIR") {
			t.Errorf("save_to %s: %s", p, msg)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "secret" {
		t.Error("the file outside was changed")
	}
	env.call("crunch_download_invoice_pdf", map[string]any{"invoice_id": 500, "save_to": "sub.pdf"})
	if b, err := os.ReadFile(filepath.Join(dir, "sub.pdf")); err != nil || string(b) != "%PDF" {
		t.Errorf("saved: %v", err)
	}
}

func TestLocalFilesNeverTouchCredentials(t *testing.T) {
	dir := filesDir(t)
	tokenFile := filepath.Join(dir, "tokens.json")
	if err := os.WriteFile(tokenFile, []byte(`{"refresh_token":"fake"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tokenFile, filepath.Join(dir, "innocent.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(tokenFile, filepath.Join(dir, "hardlink.txt")); err != nil {
		t.Fatal(err)
	}
	m := crunchtest.New()
	defer m.Server.Close()
	client, err := crunch.New(m.Config(tokenFile))
	if err != nil {
		t.Fatal(err)
	}
	c := &Context{Client: client, Tools: config.Tools{FilesDir: dir}}
	for _, p := range []string{"tokens.json", "innocent.txt", "hardlink.txt", tokenFile} {
		if _, err := c.readLocalFile(p); err == nil || !strings.Contains(err.Error(), "credentials") {
			t.Errorf("read %s: %v", p, err)
		}
	}
	for _, p := range []string{"tokens.json.lock", "tokens.json.1.tmp"} {
		if _, err := c.saveLocalFile(p, "x", []byte("x")); err == nil || !strings.Contains(err.Error(), "credentials") {
			t.Errorf("save %s: %v", p, err)
		}
	}
	// A download whose name is the token file's, saved into the directory, is refused too.
	if _, err := c.saveLocalFile(".", "tokens.json.lock", []byte("x")); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Errorf("save into dir: %v", err)
	}
	root := &Context{Tools: config.Tools{FilesDir: "/"}}
	if _, err := root.filesPath("/run/secrets/crunch_client_secret"); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Errorf("/run/secrets: %v", err)
	}
}

// changes, extra and raw bodies come from the model: they must not point an update
// at a different record from the one fetched and checked at the path.
func TestUpdatesStayOnTheRecordInThePath(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/invoices/500", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, invoiceFixture) })
	env.mock.On("GET", "/expenses/77", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, expenseFixture) })
	env.mock.On("GET", "/expenses/recurring-expenses/8", crunchtest.JSON(Obj{"id": 8, "recurringEvent": Obj{"id": 3, "enabled": true}}))
	env.mock.On("GET", "/clients/4", crunchtest.JSON(Obj{"id": 4, "name": "Example Ltd"}))
	for _, c := range []struct {
		tool, path string
		args       map[string]any
		want       string
	}{
		{"crunch_update_invoice", "/invoices/500", map[string]any{"invoice_id": 500, "changes": map[string]any{"id": 501, "ID": 502}}, "500"},
		{"crunch_update_expense", "/expenses/77", map[string]any{"expense_id": 77, "changes": map[string]any{"id": 78}}, "77"},
		{"crunch_update_recurring_expense", "/expenses/recurring-expenses/8", map[string]any{"recurring_expense_id": 8, "changes": map[string]any{"id": 9, "recurringEvent": map[string]any{"id": 4}}}, "8"},
		{"crunch_update_client", "/clients/4", map[string]any{"client_id": 4, "extra": map[string]any{"id": 5}}, "4"},
	} {
		env.call(c.tool, c.args)
		put := env.mock.Last("PUT")
		body := put.Body.(Obj)
		if put.Path != c.path || fmt.Sprint(body["id"]) != c.want || body["ID"] != nil {
			t.Errorf("%s: PUT %s with id %v (%v)", c.tool, put.Path, body["id"], body)
		}
		if ev, ok := body["recurringEvent"].(Obj); ok && fmt.Sprint(ev["id"]) != "3" {
			t.Errorf("%s: recurringEvent id %v", c.tool, ev["id"])
		}
	}

	env.mock.Reset()
	env.mock.On("GET", "/invoices/500", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, invoiceFixture) })
	for _, body := range []map[string]any{{"id": 501}, {"Id": "501"}} {
		if msg := env.callErr("crunch_api_request", map[string]any{"method": "PUT", "path": "/invoices/500", "body": body}); !strings.Contains(msg, "doesn't match the record in the path") {
			t.Errorf("raw PUT %v: %s", body, msg)
		}
	}
	if env.mock.Last("PUT") != nil {
		t.Fatal("a raw PUT for another record reached Crunch")
	}
	env.call("crunch_api_request", map[string]any{"method": "PUT", "path": "/invoices/500", "body": jsonObj(t, invoiceFixture)})
}
