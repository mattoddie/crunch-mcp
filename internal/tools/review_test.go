package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattoddie/crunch-mcp/internal/config"
	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

// Spring parses ids leniently, so a raw path whose id isn't a plain number is refused:
// otherwise the payments, reminders and body-id checks, which only recognise plain ids,
// could be skipped for a path Crunch still reads as record 1.
func TestRawPathIDsMustBePlainNumbers(t *testing.T) {
	env := newEnv(t, writesOnly())
	for _, p := range []string{"/invoices/+1", "/invoices/0x1", "/invoices/1.json", "/invoices/1e3", "/invoices/1a", "/invoices/1~", "/invoices/1:2"} {
		for _, method := range []string{"GET", "PUT"} {
			if msg := env.callErr("crunch_api_request", map[string]any{"method": method, "path": p, "body": map[string]any{"reference": "x"}}); !strings.Contains(msg, "path must") {
				t.Errorf("%s %q: %s", method, p, msg)
			}
		}
	}
	// Writes outside Crunch's documented write endpoints are refused.
	for _, c := range []struct{ method, path string }{{"PUT", "/invoices/1/state"}, {"POST", "/expenses/77"}, {"PUT", "/company-details"}, {"POST", "/user-details"}} {
		if msg := env.callErr("crunch_api_request", map[string]any{"method": c.method, "path": c.path, "body": map[string]any{}}); !strings.Contains(msg, "not one of the write endpoints") {
			t.Errorf("%s %s: %s", c.method, c.path, msg)
		}
	}
	if len(env.writesSent()) != 0 {
		t.Fatalf("a refused request reached Crunch: %v", env.writesSent())
	}
	// Documented endpoints still work.
	env.call("crunch_api_request", map[string]any{"method": "POST", "path": "/clients", "body": map[string]any{"name": "Example Ltd"}})
	env.call("crunch_api_request", map[string]any{"method": "PUT", "path": "/banking/credit-cards/default/3"})
}

// Create tools refuse an id in extra (which could make Crunch replace an existing record),
// and every extra or changes is checked for send flags while email is off.
func TestCreateToolsRefuseIDsAndSendFlags(t *testing.T) {
	env := newEnv(t, writesOnly())
	env.mock.On("GET", "/expenses/77", crunchtest.JSON(jsonObj(t, expenseFixture)))
	line := []any{map[string]any{"expense_type": "TELEPHONE", "vat_type": "Standard", "gross_amount": 10}}
	address := map[string]any{"line1": "1 High St", "line2": "Town", "country": "GB"}
	creates := map[string]map[string]any{
		"crunch_create_invoice":           {"client_id": 42, "issued_date": "2026-09-05", "items": oneItem},
		"crunch_create_expense":           {"supplier_id": 5, "posting_date": "2026-09-14", "line_items": line},
		"crunch_create_recurring_expense": {"supplier_id": 5, "line_items": line, "recurrence": map[string]any{"frequency": "MONTHLY", "next_occurrence_date": "2026-10-01"}},
		"crunch_create_client":            {"name": "Example Ltd", "billing_address": address},
		"crunch_create_supplier":          {"company_name": "Example Supplier Ltd"},
		"crunch_create_bank_account":      {"display_name": "Current", "bank_name": "Example Bank"},
		"crunch_create_credit_card":       {"display_name": "Card"},
	}
	for name, args := range creates {
		for extra, want := range map[string]string{`{"id":500}`: "must not name an existing one", `{"Id":"500"}`: "must not name an existing one", `{"sendEmail":true}`: "CRUNCH_ALLOW_EMAIL"} {
			a := map[string]any{"extra": jsonObj(t, extra)}
			for k, v := range args {
				a[k] = v
			}
			if msg := env.callErr(name, a); !strings.Contains(msg, want) {
				t.Errorf("%s extra=%s: %s", name, extra, msg)
			}
		}
	}
	if msg := env.callErr("crunch_update_expense", map[string]any{"expense_id": 77, "changes": map[string]any{"sendToSupplier": true}}); !strings.Contains(msg, "CRUNCH_ALLOW_EMAIL") {
		t.Errorf("update changes: %s", msg)
	}
	if len(env.writesSent()) != 0 {
		t.Fatalf("a refused request reached Crunch: %v", env.writesSent())
	}
	// With email on, send flags are the caller's choice; an id on a create still isn't.
	env = newEnv(t)
	env.call("crunch_create_supplier", map[string]any{"company_name": "Example Supplier Ltd", "extra": map[string]any{"sendEmail": true}})
	if msg := env.callErr("crunch_create_supplier", map[string]any{"company_name": "Example Supplier Ltd", "extra": map[string]any{"id": 5}}); !strings.Contains(msg, "must not name") {
		t.Errorf("id with email on: %s", msg)
	}
}

// Bank and card numbers stay out of the audit log, and card numbers are masked in summaries.
func TestCardAndBankNumbersAreProtected(t *testing.T) {
	got := asJSON(t, redact(map[string]any{
		"card_number": "4111111111111111", "extra": map[string]any{"cardNumber": "4111", "accountNumber": "12345678", "sortCode": "12-34-56", "IBAN": "GB00EXAMPLE"},
		"account_number": "12345678", "sort_code": "12-34-56", "iban": "GB00", "display_name": "Card",
	}))
	want := `{"account_number":"[redacted]","card_number":"[redacted]","display_name":"Card","extra":{"IBAN":"[redacted]","accountNumber":"[redacted]","cardNumber":"[redacted]","sortCode":"[redacted]"},"iban":"[redacted]","sort_code":"[redacted]"}`
	if got != want {
		t.Errorf("redacted:\n got %s\nwant %s", got, want)
	}

	env := newEnv(t)
	cards := crunchtest.JSON([]any{Obj{"id": 3, "displayName": "Card", "cardNumber": "4111 1111 1111 1234"}, Obj{"id": 4, "cardNumber": "5678"}})
	env.mock.On("GET", "/banking/credit-cards/cards/false", cards)
	list := asJSON(t, env.call("crunch_list_credit_cards", nil))
	if strings.Contains(list, "4111") || !strings.Contains(list, `"card_number":"**** 1234"`) || !strings.Contains(list, `"card_number":"5678"`) {
		t.Errorf("list: %s", list)
	}
	if overview := asJSON(t, env.call("crunch_get_overview", nil)); strings.Contains(overview, "4111") || !strings.Contains(overview, "**** 1234") {
		t.Errorf("overview: %s", overview)
	}
}

// Setting a credit card's opening balance creates one only when Crunch says there is none.
func TestOpeningBalanceOnlyCreatesOn404(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/banking/credit-cards/3/openingBalance", func(crunchtest.Request, []string) (int, any) { return 500, Obj{"message": "boom"} })
	env.callErr("crunch_set_credit_card_opening_balance", map[string]any{"credit_card_id": 3, "opening_balance": 100})
	if len(env.writesSent()) != 0 {
		t.Fatalf("posted after a failed lookup: %v", env.writesSent())
	}
	env = newEnv(t) // unmatched GETs are 404s
	if res := env.obj("crunch_set_credit_card_opening_balance", map[string]any{"credit_card_id": 3, "opening_balance": 100}); res["created"] != true {
		t.Errorf("404: %v", res)
	}
	if sent := env.writesSent(); len(sent) != 1 || sent[0] != "POST /banking/credit-cards/postOpeningBalance/3" {
		t.Errorf("sent %v", sent)
	}
}

// A bad line is reported before create_supplier makes a supplier, and a dry run shows both requests.
func TestCreateExpenseChecksLinesBeforeCreatingSupplier(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/expenses/suppliers", crunchtest.JSON(Obj{"content": []any{}}))
	env.mock.On("GET", "/expenses/expense-types/all", crunchtest.JSON(expenseTypesFixture()))
	args := map[string]any{"supplier_name": "New Co", "create_supplier": true, "posting_date": "2026-09-14",
		"line_items": []any{map[string]any{"expense_type": "NO_SUCH_TYPE", "gross_amount": 10}}}
	if msg := env.callErr("crunch_create_expense", args); !strings.Contains(msg, "no vat_type") {
		t.Errorf("bad line: %s", msg)
	}
	if len(env.writesSent()) != 0 {
		t.Fatalf("created something for an expense that failed: %v", env.writesSent())
	}
	args["line_items"] = []any{map[string]any{"expense_type": "ACCOUNTANCY", "gross_amount": 10}}
	args["dry_run"] = true
	res := env.obj("crunch_create_expense", args)
	if len(env.writesSent()) != 0 {
		t.Fatalf("a dry run sent %v", env.writesSent())
	}
	if w := res["would_send"].(Obj); w["path"] != "/expenses/suppliers" {
		t.Errorf("would_send: %v", res)
	}
	then := res["then_would_send"].([]any)
	if len(then) != 1 || then[0].(Obj)["path"] != "/expenses" || !strings.Contains(asJSON(t, then[0]), `"companyName":"New Co"`) || !strings.Contains(asJSON(t, then[0]), `"vatType":"Standard"`) {
		t.Errorf("then_would_send: %v", then)
	}
}

func TestCompanyVATSchemeWithPlainDates(t *testing.T) {
	got := summarizeCompany(Obj{"vatSchemeChanges": []any{
		Obj{"dateFrom": "2019-01-01", "dateTo": "2020-12-31", "vatScheme": "STANDARD"},
		Obj{"dateFrom": "2021-01-01", "vatScheme": "FLAT_RATE"},
		Obj{"dateFrom": "2999-01-01", "vatScheme": "CASH"},
	}})
	if got["vat_scheme"] != "FLAT_RATE" {
		t.Errorf("vat_scheme: %v", got)
	}
}

// Logging in can switch the server to another Crunch account, so it is audited, without the code.
func TestAuthToolsAreAudited(t *testing.T) {
	file := filepath.Join(t.TempDir(), "audit.jsonl")
	o := config.AllTools()
	o.AuditLog = file
	env := newEnv(t, o)
	start := env.obj("crunch_start_auth", nil)
	challenge, state := challengeOfURL(t, start["url"].(string))
	code := env.mock.IssueCode(challenge)
	env.call("crunch_complete_auth", map[string]any{"redirected_url": "http://127.0.0.1:8976/callback?code=" + code + "&state=" + state})
	entries := readAudit(t, file)
	if len(entries) != 2 || entries[0]["tool"] != "crunch_start_auth" || entries[1]["tool"] != "crunch_complete_auth" {
		t.Fatalf("entries: %v", entries)
	}
	b, _ := os.ReadFile(file)
	if strings.Contains(string(b), code) || strings.Contains(string(b), state) {
		t.Errorf("the audit log holds the authorisation code or state: %s", b)
	}
}

func TestMoneyAndQuantityPrecision(t *testing.T) {
	env := newEnv(t)
	create := func(price, qty any) bool {
		item := map[string]any{"description": "Work", "price_excl_vat": price, "vat_rate": "Standard"}
		if qty != nil {
			item["quantity"] = qty
		}
		return env.rejects("crunch_create_invoice", map[string]any{"client_id": 42, "issued_date": "2026-09-05", "items": []any{item}})
	}
	for _, bad := range []any{"1.234", "1e3", 1e21, "1.", ".5", "+1", "0x10"} {
		if !create(bad, nil) {
			t.Errorf("price %v accepted", bad)
		}
	}
	for _, bad := range []any{"1.23456", "2e2"} {
		if !create(100, bad) {
			t.Errorf("quantity %v accepted", bad)
		}
	}
	if create("100.50", "1.2345") || create(-10, 2) {
		t.Error("valid amounts refused")
	}
	if got := asJSON(t, env.mock.Last("POST").Body.(Obj)["items"]); !strings.Contains(got, `"priceExclVat":-10`) {
		t.Errorf("items: %s", got)
	}
}
