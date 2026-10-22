package tools

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattoddie/crunch-mcp/internal/config"
	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

// writesSent returns the recorded requests that could change something.
func (e *testEnv) writesSent() []string {
	var out []string
	for _, r := range e.mock.Requests() {
		if r.Method != "GET" {
			out = append(out, r.Method+" "+r.Path)
		}
	}
	return out
}

func TestDryRunArgumentOnlyOnToolsThatChangeThings(t *testing.T) {
	env := newEnv(t)
	for name, tool := range toolNames(t, env.session) {
		props, _ := tool.InputSchema.(map[string]any)["properties"].(map[string]any)
		_, has := props["dry_run"]
		if want := !tool.Annotations.ReadOnlyHint && !strings.HasPrefix(name, "crunch_auth_") && !strings.HasPrefix(name, "crunch_download_"); has != want {
			t.Errorf("%s: dry_run argument %v, want %v", name, has, want)
		}
	}
	ro := config.AllTools()
	ro.AllowWrites, ro.AllowDeletes, ro.AllowEmail = false, false, false
	raw := toolNames(t, connect(t, env.client, ro))["crunch_api_request"]
	if _, has := raw.InputSchema.(map[string]any)["properties"].(map[string]any)["dry_run"]; has {
		t.Error("the read-only raw tool should not take dry_run")
	}
}

func TestDryRunUpdateShowsChangedFields(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/expenses/77", crunchtest.JSON(jsonObj(t, expenseFixture)))
	res := env.obj("crunch_update_expense", map[string]any{"expense_id": 77, "note": "Paid by card", "posting_date": "2026-09-15", "dry_run": true})
	if sent := env.writesSent(); len(sent) != 0 {
		t.Fatalf("a dry run sent %v", sent)
	}
	if res["dry_run"] != true || asJSON(t, res["would_send"]) != `{"method":"PUT","path":"/expenses/77"}` {
		t.Errorf("would_send: %v", res)
	}
	want := `{"added":{"note":"Paid by card"},"changed":{"postingDate":{"from":"2026-09-14","to":"2026-09-15"}}}`
	if got := asJSON(t, res["changes"]); got != want {
		t.Errorf("changes:\n got %s\nwant %s", got, want)
	}
	if !strings.Contains(res["note"].(string), "Nothing was changed") {
		t.Errorf("note: %v", res["note"])
	}
}

func TestDryRunCreateShowsBody(t *testing.T) {
	env := newEnv(t)
	big := strings.Repeat("QUJD", 200)
	env.mock.On("GET", "/expenses/expense-types/all", crunchtest.JSON(expenseTypesFixture()))
	res := env.obj("crunch_create_expense", map[string]any{
		"supplier_id": 5, "posting_date": "2026-09-14", "dry_run": true,
		"line_items":  []any{map[string]any{"expense_type": "ACCOUNTANCY", "gross_amount": 120}},
		"attachments": []any{map[string]any{"file_name": "r.png", "content_base64": big}},
	})
	if sent := env.writesSent(); len(sent) != 0 {
		t.Fatalf("a dry run sent %v", sent)
	}
	send := res["would_send"].(Obj)
	if send["method"] != "POST" || send["path"] != "/expenses" {
		t.Errorf("would_send: %v", send)
	}
	body := asJSON(t, send["body"])
	if !strings.Contains(body, `"vatType":"Standard"`) || !strings.Contains(body, "image/png file, 800 characters of base64") || strings.Contains(body, big) {
		t.Errorf("body: %s", body)
	}
}

func TestDryRunValidatesFirst(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/expenses/77", crunchtest.JSON(jsonObj(t, expenseFixture)))
	if msg := env.callErr("crunch_update_expense", map[string]any{"expense_id": 77, "dry_run": true}); !strings.Contains(msg, "nothing to change") {
		t.Errorf("error: %s", msg)
	}
	// The delete switch still applies: dropping payments is refused, not previewed.
	o := config.AllTools()
	o.AllowDeletes = false
	env = newEnv(t, o)
	env.mock.On("GET", "/expenses/77", crunchtest.JSON(jsonObj(t, expenseFixture)))
	if msg := env.callErr("crunch_update_expense", map[string]any{"expense_id": 77, "changes": map[string]any{"supplierPayments": []any{}}, "dry_run": true}); !strings.Contains(msg, "CRUNCH_ALLOW_DELETES") {
		t.Errorf("error: %s", msg)
	}
}

func TestDryRunDeleteAndRaw(t *testing.T) {
	env := newEnv(t)
	res := env.obj("crunch_delete_client", map[string]any{"client_id": 4, "dry_run": true})
	if asJSON(t, res["would_send"]) != `{"method":"DELETE","path":"/clients/4"}` {
		t.Errorf("delete: %v", res)
	}
	// A POST that only reads goes through in a dry run.
	env.mock.On("POST", "/banking/bank-accounts/opening-balances", crunchtest.JSON(Obj{"openingBalances": []any{}}))
	res = env.obj("crunch_api_request", map[string]any{"method": "POST", "path": "/banking/bank-accounts/opening-balances", "body": map[string]any{"accountIds": []any{1}}, "dry_run": true})
	if res["would_send"] != nil || res["result"] == nil {
		t.Errorf("read POST: %v", res)
	}
	res = env.obj("crunch_api_request", map[string]any{"method": "PUT", "path": "/banking/bank-accounts/default/3", "query": map[string]any{"x": "1"}, "dry_run": true})
	if asJSON(t, res["would_send"]) != `{"method":"PUT","path":"/banking/bank-accounts/default/3","query":"x=1"}` {
		t.Errorf("raw PUT: %v", res)
	}
	if sent := env.writesSent(); len(sent) != 1 || sent[0] != "POST /banking/bank-accounts/opening-balances" {
		t.Errorf("sent: %v", sent)
	}
}

func readAudit(t *testing.T, file string) []Obj {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var out []Obj
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var o Obj
		if err := json.Unmarshal(line, &o); err != nil {
			t.Fatalf("bad audit line %q: %v", line, err)
		}
		out = append(out, o)
	}
	return out
}

func TestAuditLog(t *testing.T) {
	file := filepath.Join(t.TempDir(), "audit.jsonl")
	o := config.AllTools()
	o.AuditLog = file
	env := newEnv(t, o)
	env.mock.On("GET", "/clients/list", crunchtest.JSON([]any{}))
	env.mock.On("GET", "/expenses/77", crunchtest.JSON(jsonObj(t, expenseFixture)))
	big := strings.Repeat("QUJD", 200)

	env.call("crunch_list_clients", nil)                                    // read: not logged
	env.call("crunch_api_request", map[string]any{"path": "/clients/list"}) // raw GET: not logged
	env.call("crunch_create_client", map[string]any{"name": "Example Ltd", "billing_address": map[string]any{"line1": "1 High St", "line2": "Town", "country": "GB"}})
	env.call("crunch_update_expense", map[string]any{"expense_id": 77, "note": "x", "dry_run": true})
	env.callErr("crunch_add_expense_attachment", map[string]any{"expense_id": 77, "attachments": []any{map[string]any{"file_name": "a.png", "content_base64": big + "!"}}})
	env.rejects("crunch_delete_expense", map[string]any{"expense_id": "not a number"})

	entries := readAudit(t, file)
	if len(entries) != 4 {
		t.Fatalf("want 4 entries, got %d: %v", len(entries), entries)
	}
	if e := entries[0]; e["tool"] != "crunch_create_client" || e["outcome"] != "ok" || e["dry_run"] != false || asJSON(t, e["requests"]) != `[{"method":"POST","path":"/clients","status":200}]` {
		t.Errorf("create: %v", e)
	}
	if e := entries[1]; e["dry_run"] != true || asJSON(t, e["previewed"]) != `{"method":"PUT","path":"/expenses/77"}` || asJSON(t, e["requests"]) != `[]` {
		t.Errorf("dry run: %v", e)
	}
	if e := entries[2]; e["outcome"] != "error" || !strings.Contains(e["error"].(string), "base64") || strings.Contains(asJSON(t, e), big) {
		t.Errorf("error: %v", e)
	}
	if e := entries[3]; e["tool"] != "crunch_delete_expense" || e["outcome"] != "error" {
		t.Errorf("invalid input: %v", e)
	}
	if st, err := os.Stat(file); err != nil || st.Mode().Perm() != config.AuditLogMode {
		t.Errorf("mode: %v %v", st.Mode(), err)
	}
}

func TestAuditLogRedactsSecrets(t *testing.T) {
	got := asJSON(t, redact(map[string]any{"client_secret": "s3cret", "nested": map[string]any{"access_token": "t"}, "name": "Example Ltd", "empty_token": ""}))
	if got != `{"client_secret":"[redacted]","empty_token":"","name":"Example Ltd","nested":{"access_token":"[redacted]"}}` {
		t.Errorf("redacted: %s", got)
	}
}
