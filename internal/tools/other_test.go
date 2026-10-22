package tools

import (
	"strings"
	"testing"

	"github.com/mattoddie/crunch-mcp/internal/config"
	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

func TestOverview(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/company-details", crunchtest.JSON(Obj{"content": []any{Obj{
		"companyName": "Example Ltd", "companyNumber": "01234567", "vatRegistrationNumber": "GB123",
		"vatSchemeChanges":  []any{Obj{"dateFrom": "2020-01-01T00:00:00Z", "vatScheme": "FLAT_RATE", "industry": "IT"}},
		"registeredAddress": Obj{"addressLine1": "1 High St", "addressCity": "Leeds", "addressPostCode": "LS1 1AA"},
	}}}))
	env.mock.On("GET", "/banking/bank-accounts", crunchtest.JSON([]any{
		Obj{"id": 11, "displayName": "Current", "balanceDto": Obj{"balance": 1000.5}},
		Obj{"id": 12, "displayName": "Savings", "balanceDto": Obj{"balance": 500.25}},
		Obj{"id": 13, "displayName": "Old", "archived": true, "balanceDto": Obj{"balance": 99}},
	}))
	env.mock.On("GET", "/invoices/sales-invoice-totals", crunchtest.JSON(Obj{"salesTotal": 5000, "outstandingTotal": 1200}))
	// Credit cards and ledgers fail: the overview still returns what it has.
	res := env.obj("crunch_get_overview", nil)
	if num(res["total_bank_balance"]) != 1500.75 || len(res["bank_accounts"].([]any)) != 2 {
		t.Errorf("banks: %v", res)
	}
	company := res["company"].(Obj)
	if company["vat_scheme"] != "FLAT_RATE" || company["registered_address"] != "1 High St, Leeds, LS1 1AA" {
		t.Errorf("company: %v", company)
	}
	if len(res["errors"].([]any)) != 2 {
		t.Errorf("errors: %v", res["errors"])
	}
}

func TestAuthToolsLogIn(t *testing.T) {
	env := newEnv(t)
	start := env.obj("crunch_auth_start", nil)
	challenge, state := challengeOfURL(t, start["url"].(string))
	code := env.mock.IssueCode(challenge)
	env.mock.On("GET", "/user-details", crunchtest.JSON(Obj{"email": "me@example.com", "firstName": "Sam", "lastName": "Smith"}))
	res := env.obj("crunch_auth_complete", map[string]any{"redirected_url": "http://127.0.0.1:8976/callback?code=" + code + "&state=" + state})
	if res["logged_in"] != true || res["account"].(Obj)["email"] != "me@example.com" {
		t.Errorf("complete: %v", res)
	}
	st := env.obj("crunch_auth_status", nil)
	if st["source"] != "token file" || st["has_refresh_token"] != true || strings.Contains(asJSON(t, st), "access-") {
		t.Errorf("status (must not show tokens): %v", st)
	}
}

func TestClientsAndSuppliers(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/clients/list", crunchtest.JSON([]any{
		Obj{"id": 2, "name": "Zed Ltd"}, Obj{"id": 1, "name": "acme plc", "email": "a@acme.test", "billingAddress": Obj{"line1": "1 Road", "postCode": "AB1 2CD"}},
	}))
	res := env.obj("crunch_list_clients", map[string]any{"search": "ab1"})
	if num(res["total"]) != 1 || res["items"].([]any)[0].(Obj)["address"] != "1 Road, AB1 2CD" {
		t.Errorf("clients: %v", res)
	}
	all := env.obj("crunch_list_clients", nil)
	if all["items"].([]any)[0].(Obj)["name"] != "acme plc" {
		t.Error("clients should be sorted by name")
	}

	env.call("crunch_create_client", map[string]any{"name": "New Ltd", "payment_terms_days": 30, "billing_address": map[string]any{"line1": "1 A St", "line2": "Town", "country": "GB", "post_code": "X1"}})
	if got := asJSON(t, env.mock.Last("POST").Body); got != `{"billingAddress":{"country":"GB","line1":"1 A St","line2":"Town","postCode":"X1"},"name":"New Ltd","paymentTermsDays":30}` {
		t.Errorf("create client: %s", got)
	}

	env.mock.On("GET", "/clients/1", crunchtest.JSON(Obj{"id": 1, "name": "acme plc", "email": "old@acme.test", "billingAddress": Obj{"line1": "1 Road", "line2": "Town", "country": "GB"}}))
	env.call("crunch_update_client", map[string]any{"client_id": 1, "email": "new@acme.test"})
	if got := asJSON(t, env.mock.Last("PUT").Body); got != `{"billingAddress":{"country":"GB","line1":"1 Road","line2":"Town"},"email":"new@acme.test","id":1,"name":"acme plc"}` {
		t.Errorf("update client: %s", got)
	}

	env.mock.On("GET", "/expenses/suppliers/all", crunchtest.JSON(Obj{"content": []any{
		Obj{"id": 1, "companyName": "BT"}, Obj{"id": 2, "companyName": "Old Co", "archived": true},
	}}))
	if s := env.obj("crunch_list_suppliers", nil); num(s["total"]) != 1 {
		t.Errorf("archived suppliers should be hidden: %v", s)
	}
	if s := env.obj("crunch_list_suppliers", map[string]any{"include_archived": true}); num(s["total"]) != 2 {
		t.Errorf("include_archived: %v", s)
	}
	env.mock.On("GET", "/expenses/suppliers/1", crunchtest.JSON(Obj{"id": 1, "companyName": "BT", "country": "GB"}))
	env.call("crunch_update_supplier", map[string]any{"supplier_id": 1, "expense_type": "TELEPHONE", "archived": true})
	if got := asJSON(t, env.mock.Last("PUT").Body); got != `{"archived":true,"companyName":"BT","country":"GB","expenseType":{"expenseTypeName":"TELEPHONE"},"id":1}` {
		t.Errorf("update supplier: %s", got)
	}
}

func TestBanking(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/banking/bank-accounts/accounts/(true|false)", func(_ crunchtest.Request, m []string) (int, any) {
		return 200, []any{Obj{"id": 11, "displayName": "Current", "archived": m[1] == "true", "unmatchedEntriesAmount": 4}}
	})
	res := env.obj("crunch_list_bank_accounts", map[string]any{"status": "archived"})
	if env.mock.Last("GET").Path != "/banking/bank-accounts/accounts/true" || num(res["items"].([]any)[0].(Obj)["unmatched_entries"]) != 4 {
		t.Errorf("bank accounts: %v", res)
	}

	env.mock.On("GET", "/banking/bank-accounts/11/payments", crunchtest.JSON(Obj{"content": []any{Obj{"id": 1, "amount": -50, "amountType": "DEBIT", "paymentType": "SUPPLIER_PAYMENT"}}, "last": true}))
	pay := env.obj("crunch_list_bank_account_payments", map[string]any{"bank_account_id": 11, "reconciled": false})
	if q := env.mock.Last("GET").Query; q.Get("reconciled") != "false" || q.Get("sort") != "date,desc" || pay["items"].([]any)[0].(Obj)["type"] != "SUPPLIER_PAYMENT" {
		t.Errorf("payments: %v %v", q, pay)
	}

	env.call("crunch_archive_bank_account", map[string]any{"bank_account_id": 11})
	if p := env.mock.Last("PUT"); p.Path != "/banking/bank-accounts/archive/11" || p.Query.Get("archive") != "true" {
		t.Errorf("archive: %v", p)
	}

	// Opening balance: create when none exists, update when one does.
	env.call("crunch_set_credit_card_opening_balance", map[string]any{"credit_card_id": 3, "opening_balance": 250})
	if p := env.mock.Last("POST"); p.Path != "/banking/credit-cards/postOpeningBalance/3" || p.Query.Get("accountBalance") != "250" {
		t.Errorf("post opening balance: %v", p)
	}
	env.mock.On("GET", "/banking/credit-cards/3/openingBalance", crunchtest.JSON(Obj{"openingBalance": "100", "editable": true}))
	res = env.obj("crunch_set_credit_card_opening_balance", map[string]any{"credit_card_id": 3, "opening_balance": "300.50"})
	if p := env.mock.Last("PUT"); p.Path != "/banking/credit-cards/updateOpeningBalance/3" || p.Query.Get("accountBalance") != "300.50" || num(res["previous"]) != 100 {
		t.Errorf("update opening balance: %v %v", p, res)
	}

	env.mock.On("POST", "/banking/bank-accounts/opening-balances", crunchtest.JSON(Obj{"openingBalances": []any{Obj{"accountId": 11, "openingBalance": "12.30", "editable": false}}}))
	ob := env.call("crunch_get_opening_balances", map[string]any{"account_ids": []any{11}}).([]any)
	if asJSON(t, env.mock.Last("POST").Body) != `{"accountIds":[11]}` || num(ob[0].(Obj)["opening_balance"]) != 12.3 || ob[0].(Obj)["editable"] != false {
		t.Errorf("opening balances: %v", ob)
	}
}

func TestRecurringExpenses(t *testing.T) {
	env := newEnv(t)
	env.call("crunch_create_recurring_expense", map[string]any{
		"supplier_id":     5,
		"line_items":      []any{map[string]any{"expense_type": "TELEPHONE", "vat_type": "Standard", "gross_amount": 30}},
		"recurrence":      map[string]any{"frequency": "MONTHLY", "next_occurrence_date": "2026-11-01"},
		"bank_account_id": 11,
	})
	body := env.mock.Last("POST").Body.(Obj)
	if asJSON(t, body["recurringEvent"]) != `{"enabled":true,"nextOccurrenceDate":"2026-11-01","recurringEventFrequency":"MONTHLY"}` || body["postingDate"] != "2026-11-01" {
		t.Errorf("recurring body: %s", asJSON(t, body))
	}
	if asJSON(t, body["supplierPayments"]) != `[{"date":"2026-11-01","paymentTarget":{"bankAccount":{"id":11},"paymentMethod":{"paymentMethodName":"EFT"}}}]` {
		t.Errorf("payment template: %s", asJSON(t, body["supplierPayments"]))
	}
	env.mock.On("GET", "/expenses/recurring-expenses/8", crunchtest.JSON(Obj{"id": 8, "recurringEvent": Obj{"enabled": true, "recurringEventFrequency": "MONTHLY"}}))
	env.call("crunch_update_recurring_expense", map[string]any{"recurring_expense_id": 8, "enabled": false})
	if got := asJSON(t, env.mock.Last("PUT").Body); got != `{"id":8,"recurringEvent":{"enabled":false,"recurringEventFrequency":"MONTHLY"}}` {
		t.Errorf("pause: %s", got)
	}
}

func TestRawRequestGates(t *testing.T) {
	ro := config.AllTools()
	ro.AllowWrites, ro.AllowDeletes, ro.AllowEmail = false, false, false
	env := newEnv(t, ro)
	env.mock.On("GET", "/expenses/vat-types", crunchtest.JSON(Obj{"vatTypes": []any{}}))
	if res := env.obj("crunch_api_request", map[string]any{"path": "/v1/expenses/vat-types"}); res["vatTypes"] == nil {
		t.Errorf("GET: %v", res)
	}
	if msg := env.callErr("crunch_api_request", map[string]any{"method": "POST", "path": "/expenses", "body": map[string]any{}}); !strings.Contains(msg, "CRUNCH_ALLOW_WRITES") {
		t.Errorf("write blocked: %s", msg)
	}
	env.mock.On("POST", "/banking/bank-accounts/opening-balances", crunchtest.JSON(Obj{}))
	env.call("crunch_api_request", map[string]any{"method": "POST", "path": "/banking/bank-accounts/opening-balances", "body": map[string]any{"accountIds": []any{1}}})
	for _, p := range []string{"/expenses/../user-details", "expenses", "/expenses?x=1", "/expenses/%2e%2e/x"} {
		if msg := env.callErr("crunch_api_request", map[string]any{"path": p}); !strings.Contains(msg, "path must") && !strings.Contains(msg, "percent") {
			t.Errorf("%s: %s", p, msg)
		}
	}

	w := config.AllTools()
	w.AllowDeletes, w.AllowEmail = false, false
	env = newEnv(t, w)
	if msg := env.callErr("crunch_api_request", map[string]any{"method": "DELETE", "path": "/expenses/1"}); !strings.Contains(msg, "CRUNCH_ALLOW_DELETES") {
		t.Errorf("delete blocked: %s", msg)
	}
	if msg := env.callErr("crunch_api_request", map[string]any{"method": "POST", "path": "/invoices/1/email"}); !strings.Contains(msg, "CRUNCH_ALLOW_EMAIL") {
		t.Errorf("email blocked: %s", msg)
	}
}

func TestReferenceTools(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/expenses/vat-types", crunchtest.JSON(Obj{"vatTypes": []any{Obj{"name": "Standard", "vatPercent": 20}}}))
	env.mock.On("GET", "/invoices/vat-types", crunchtest.JSON(Obj{"vatTypes": []any{Obj{"displayName": "Exempt", "vatPercent": 0}}}))
	vat := env.obj("crunch_list_vat_types", nil)
	if asJSON(t, vat) != `{"expenses":[{"name":"Standard","percent":20}],"invoices":[{"name":"Exempt","percent":0}]}` {
		t.Errorf("vat types: %s", asJSON(t, vat))
	}
	env.mock.On("GET", "/invoices/payment-methods", crunchtest.JSON(Obj{"paymentMethods": []any{Obj{"paymentMethodName": "EFT"}}}))
	env.call("crunch_list_payment_methods", map[string]any{"for": "invoices"})
	if env.mock.Last("GET").Query.Get("direction") != "INCOMING" {
		t.Error("invoice payment methods need a direction")
	}
	env.mock.On("GET", "/expenses/persons", crunchtest.JSON([]any{Obj{"id": 8, "forename": "Sam", "surname": "Smith"}}))
	people := env.obj("crunch_list_persons", nil)["items"].([]any)
	if people[0].(Obj)["name"] != "Sam Smith" || env.mock.Last("GET").Query.Get("date") == "" {
		t.Errorf("persons: %v", people)
	}
	if !env.rejects("crunch_list_vat_types", map[string]any{"for": "nonsense"}) {
		t.Error("enum not enforced")
	}
}
