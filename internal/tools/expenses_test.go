package tools

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

const expenseFixture = `{
  "id": 77, "postingDate": "2026-09-14", "status": "PART_PAID", "supplierReference": "INV-9",
  "supplier": {"id": 5, "companyName": "Example Supplier Ltd"},
  "amount": {"grossAmount": 120.00, "netAmount": 100.00, "vatAmount": 20.00}, "outstandingBalance": 70,
  "expenseLineItems": [{"id": 1, "expenseType": "ACCOUNTANCY", "vatType": "Standard", "description": "Bookkeeping", "amount": {"grossAmount": 120}}],
  "supplierPayments": [{"id": 300, "date": "2026-09-14", "amount": 50, "reconciled": false, "paymentTarget": {"paymentMethod": {"paymentMethodName": "EFT"}, "bankAccount": {"id": 11}}}],
  "attachments": [{"id": 0, "fileName": "receipt.jpg", "fileContents": null}],
  "editable": true, "deletable": false, "unknownField": "kept"
}`

func expenseTypesFixture() Obj {
	return Obj{"expenseGroups": []any{Obj{"groupName": "Office", "expenseTypes": []any{
		Obj{"expenseTypeName": "ACCOUNTANCY", "expenseTypeDisplayableName": "Accountancy", "defaultVatType": "Standard"},
		Obj{"expenseTypeName": "TELEPHONE", "expenseTypeDisplayableName": "Telephone", "defaultVatType": "Standard", "deprecated": true},
	}}}}
}

func TestListExpensesPagesAndSummarises(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/expenses", func(r crunchtest.Request, _ []string) (int, any) {
		page := r.Query.Get("page")
		return 200, Obj{"content": []any{jsonObj(t, expenseFixture)}, "totalElements": 3, "totalPages": 3, "last": page == "2", "number": page}
	})
	res := env.obj("crunch_list_expenses", map[string]any{"start_date": "2026-01-01", "has_attachment": false, "supplier_id": 5})
	q := env.mock.Last("GET").Query
	if q.Get("startDate") != "2026-01-01" || q.Get("attachment") != "false" || q.Get("supplierId") != "5" || q.Get("sort") != "postingDate,desc" || q.Get("size") != "50" {
		t.Errorf("query: %v", q)
	}
	if num(res["next_page"]) != 1 || num(res["total"]) != 3 {
		t.Errorf("paging: %v", res)
	}
	item := res["items"].([]any)[0].(Obj)
	if item["supplier"] != "Example Supplier Ltd" || num(item["gross"]) != 120 || num(item["outstanding"]) != 70 || item["paid_by"].([]any)[0] != "EFT" {
		t.Errorf("summary: %v", item)
	}

	all := env.obj("crunch_list_expenses", map[string]any{"all": true})
	if num(all["returned"]) != 3 || all["truncated"] != nil {
		t.Errorf("all pages: %v", all)
	}
	if env.mock.Last("GET").Query.Get("size") != "200" {
		t.Error("all=true should use 200 per page")
	}
	capped := env.obj("crunch_list_expenses", map[string]any{"all": true, "max_items": 2})
	if num(capped["returned"]) != 2 || capped["truncated"] != true {
		t.Errorf("max_items: %v", capped)
	}
}

func TestCreateExpenseBuildsBody(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/expenses/expense-types/all", crunchtest.JSON(expenseTypesFixture()))
	env.mock.On("GET", "/expenses/suppliers", crunchtest.JSON(Obj{"content": []any{Obj{"id": 5, "companyName": "Example Supplier Ltd"}}}))
	img := base64.StdEncoding.EncodeToString([]byte("fake jpeg"))
	env.call("crunch_create_expense", map[string]any{
		"supplier_name": "Example Supplier Ltd",
		"posting_date":  "2026-09-14",
		"line_items":    []any{map[string]any{"expense_type": "ACCOUNTANCY", "description": "Monthly bookkeeping", "gross_amount": "120.00"}},
		"payments":      []any{map[string]any{"date": "2026-09-14", "amount": 120, "bank_account_id": 11}},
		"attachments":   []any{map[string]any{"file_name": "receipt.jpeg", "content_base64": img}},
	})
	post := env.mock.Last("POST")
	if post.Path != "/expenses" {
		t.Fatalf("posted to %s", post.Path)
	}
	want := `{"attachments":[{"fileContents":"data:image/jpeg;base64,` + img + `","fileName":"receipt.jpeg","id":0}],` +
		`"expenseLineItems":[{"amount":{"grossAmount":120.00},"description":"Monthly bookkeeping","expenseType":"ACCOUNTANCY","vatType":"Standard"}],` +
		`"postingDate":"2026-09-14","supplier":{"id":5},` +
		`"supplierPayments":[{"amount":120,"date":"2026-09-14","paymentTarget":{"bankAccount":{"id":11},"paymentMethod":{"paymentMethodName":"EFT"}}}]}`
	if got := asJSON(t, post.Body); got != want {
		t.Errorf("body:\n got %s\nwant %s", got, want)
	}
	found := false
	for _, r := range env.mock.Requests() {
		found = found || (r.Path == "/expenses/suppliers" && r.Query.Get("supplierName") == "Example Supplier Ltd")
	}
	if !found {
		t.Error("supplier not looked up by name")
	}
}

func TestCreateExpenseSupplierResolution(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/expenses/suppliers", crunchtest.JSON(Obj{"content": []any{}}))
	args := map[string]any{"supplier_name": "New Co", "posting_date": "2026-09-14", "line_items": []any{map[string]any{"expense_type": "TELEPHONE", "vat_type": "Standard", "gross_amount": 10}}}
	if msg := env.callErr("crunch_create_expense", args); !strings.Contains(msg, "create_supplier=true") {
		t.Errorf("missing supplier: %s", msg)
	}
	args["create_supplier"] = true
	env.call("crunch_create_expense", args)
	var created *crunchtest.Request
	for _, r := range env.mock.Requests() {
		if r.Method == "POST" && r.Path == "/expenses/suppliers" {
			created = &r
		}
	}
	if created == nil || asJSON(t, created.Body) != `{"companyName":"New Co","expenseType":{"expenseTypeName":"TELEPHONE"}}` {
		t.Errorf("supplier creation: %+v", created)
	}
	if asJSON(t, env.mock.Last("POST").Body.(Obj)["supplier"]) != `{"id":9001}` {
		t.Errorf("expense supplier: %v", env.mock.Last("POST").Body)
	}
}

func TestExpensePaymentsKeepExistingData(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/expenses/77", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, expenseFixture) })

	res := env.obj("crunch_add_expense_payment", map[string]any{"expense_id": 77, "date": "2026-09-20", "amount": "100", "payment_method": "DIRECTORS_EXPENSES", "person_id": 8})
	put := env.mock.Last("PUT")
	body := put.Body.(Obj)
	payments := body["supplierPayments"].([]any)
	if len(payments) != 2 || asJSON(t, payments[0]) != asJSON(t, jsonObj(t, expenseFixture)["supplierPayments"].([]any)[0]) {
		t.Errorf("existing payment changed: %v", payments)
	}
	if asJSON(t, payments[1]) != `{"amount":100,"date":"2026-09-20","paymentTarget":{"paymentMethod":{"paymentMethodName":"DIRECTORS_EXPENSES"},"person":{"id":8}}}` {
		t.Errorf("new payment: %s", asJSON(t, payments[1]))
	}
	if body["unknownField"] != "kept" || body["attachments"] == nil {
		t.Error("fields the server doesn't know about must survive updates")
	}
	if !strings.Contains(asJSON(t, res["warning"]), "more than the 70.00") {
		t.Errorf("overpayment warning: %v", res["warning"])
	}

	env.obj("crunch_remove_expense_payment", map[string]any{"expense_id": 77, "payment_id": 300})
	if p := env.mock.Last("PUT").Body.(Obj)["supplierPayments"].([]any); len(p) != 0 {
		t.Errorf("payment not removed: %v", p)
	}
	if msg := env.callErr("crunch_remove_expense_payment", map[string]any{"expense_id": 77, "payment_id": 1}); !strings.Contains(msg, "no payment 1") {
		t.Errorf("unknown payment: %s", msg)
	}
}

func TestUpdateExpenseMergesChanges(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/expenses/77", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, expenseFixture) })
	env.call("crunch_update_expense", map[string]any{"expense_id": 77, "note": "", "posting_date": "2026-09-15", "changes": map[string]any{"supplier": map[string]any{"companyName": "Renamed"}}})
	body := env.mock.Last("PUT").Body.(Obj)
	if body["postingDate"] != "2026-09-15" || body["note"] != "" || asJSON(t, body["supplier"]) != `{"companyName":"Renamed","id":5}` || len(body["supplierPayments"].([]any)) != 1 {
		t.Errorf("merged body: %s", asJSON(t, body))
	}
	if msg := env.callErr("crunch_update_expense", map[string]any{"expense_id": 77}); !strings.Contains(msg, "nothing to change") {
		t.Errorf("empty update: %s", msg)
	}
}

func TestExpenseAttachments(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/expenses/77", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, expenseFixture) })
	env.call("crunch_add_expense_attachment", map[string]any{"expense_id": 77, "attachments": []any{map[string]any{"file_name": "b.pdf", "content_base64": "data:application/pdf;base64,JVBERi0="}}})
	files := env.mock.Last("PUT").Body.(Obj)["attachments"].([]any)
	if len(files) != 2 || asJSON(t, files[1]) != `{"fileContents":"data:application/pdf;base64,JVBERi0=","fileName":"b.pdf","id":1}` {
		t.Errorf("attachments: %s", asJSON(t, files))
	}
	if msg := env.callErr("crunch_add_expense_attachment", map[string]any{"expense_id": 77, "attachments": []any{map[string]any{"file_name": "x.png", "content_base64": "not base64!"}}}); !strings.Contains(msg, "not valid base64") {
		t.Errorf("bad base64: %s", msg)
	}

	env.mock.On("GET", "/expenses/attachments", func(r crunchtest.Request, _ []string) (int, any) {
		if r.Query.Get("fileName") != "receipt.jpg" || r.Query.Get("attachmentId") != "0" {
			return 400, Obj{"message": "bad query"}
		}
		return 200, []byte("JPEGDATA")
	})
	res := env.raw("crunch_download_expense_attachment", map[string]any{"expense_id": 77, "attachment_id": 0})
	if res.IsError || len(res.Content) != 2 {
		t.Fatalf("download: %s", text(res))
	}
	img, ok := res.Content[1].(*mcp.ImageContent)
	if !ok || img.MIMEType != "image/jpeg" || string(img.Data) != "JPEGDATA" {
		t.Errorf("image content: %#v", res.Content[1])
	}
}

func TestExpensesTotalAndTypes(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/expenses/total", crunchtest.JSON(Obj{"grossAmount": 1234.5, "underpaid": 0, "overpaid": 10}))
	res := env.obj("crunch_get_expenses_total", map[string]any{"status": "PAID", "expense_type": "TRAVEL"})
	if q := env.mock.Last("GET").Query; q.Get("status") != "PAID" || q.Get("expenseType") != "TRAVEL" {
		t.Errorf("query: %v", q)
	}
	if num(res["gross"]) != 1234.5 || num(res["overpaid"]) != 10 {
		t.Errorf("totals: %v", res)
	}

	env.mock.On("GET", "/expenses/expense-types", crunchtest.JSON(expenseTypesFixture()))
	types := env.obj("crunch_list_expense_types", map[string]any{"search": "tele"})
	if num(types["total"]) != 1 || types["items"].([]any)[0].(Obj)["name"] != "TELEPHONE" {
		t.Errorf("types: %v", types)
	}
	if env.mock.Last("GET").Query.Get("excludeDeprecated") != "true" {
		t.Error("deprecated types should be excluded by default")
	}
}
