package tools

import (
	"strings"
	"testing"

	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

func TestAgedDebtors(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/invoices", crunchtest.JSON(Obj{"last": true, "totalElements": 6, "content": []any{
		Obj{"id": 1, "invoiceNumber": "INV-1", "customerName": "Example Customer Ltd", "customerId": 42, "status": "ISSUED", "issuedDate": "2026-08-01", "dueDate": "2026-08-31", "outstandingBalance": 100.10},
		Obj{"id": 2, "invoiceNumber": "INV-2", "customerName": "Example Customer Ltd", "customerId": 42, "status": "PART_PAID", "issuedDate": "2026-09-01", "dueDate": "2026-10-01", "outstandingBalance": 50.20},
		Obj{"id": 3, "invoiceNumber": "INV-3", "customerName": "Other Customer Ltd", "customerId": 43, "status": "ISSUED", "issuedDate": "2026-09-20", "dueDate": "2026-10-20", "outstandingBalance": 200},
		Obj{"id": 4, "invoiceNumber": "INV-4", "customerName": "Other Customer Ltd", "customerId": 43, "status": "PAID", "issuedDate": "2026-05-01", "dueDate": "2026-05-31", "outstandingBalance": 0},
		Obj{"id": 5, "customerName": "Other Customer Ltd", "customerId": 43, "status": "DRAFT", "issuedDate": "2026-09-01", "outstandingBalance": 999},
		Obj{"id": 6, "invoiceNumber": "INV-6", "customerName": "Old Customer Ltd", "customerId": 44, "status": "ISSUED", "issuedDate": "2026-04-01", "dueDate": "2026-05-01", "outstandingBalance": 0.3},
	}}))
	res := env.obj("crunch_get_aged_debtors", map[string]any{"as_of_date": "2026-10-15", "details": true})
	if num(res["total_outstanding"]) != 350.6 {
		t.Errorf("total: %v", res["total_outstanding"])
	}
	want := `{"1_30":{"amount":50.2,"count":1},"31_60":{"amount":100.1,"count":1},"61_90":{"amount":0,"count":0},"current":{"amount":200,"count":1},"over_90":{"amount":0.3,"count":1}}`
	if got := asJSON(t, res["buckets"]); got != want {
		t.Errorf("buckets:\n got %s\nwant %s", got, want)
	}
	byCustomer := res["by_customer"].([]any)
	if len(byCustomer) != 3 || byCustomer[0].(Obj)["customer"] != "Other Customer Ltd" || num(byCustomer[1].(Obj)["total"]) != 150.3 {
		t.Errorf("by_customer: %v", byCustomer)
	}
	items := res["items"].([]any)
	if len(items) != 4 || items[0].(Obj)["number"] != "INV-6" || num(items[0].(Obj)["days_overdue"]) != 167 {
		t.Errorf("items: %v", items)
	}
	if strings.Contains(asJSON(t, res), "999") {
		t.Error("a draft was counted")
	}
	// Without details, only the totals.
	if _, ok := env.obj("crunch_get_aged_debtors", map[string]any{"as_of_date": "2026-10-15"})["items"]; ok {
		t.Error("items without details")
	}
}

func TestAgedCreditors(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/expenses", func(r crunchtest.Request, _ []string) (int, any) {
		if r.Query.Get("endDate") != "2026-10-15" {
			t.Errorf("endDate: %v", r.Query)
		}
		return 200, Obj{"last": true, "content": []any{
			Obj{"id": 77, "postingDate": "2026-10-01", "supplier": Obj{"id": 5, "companyName": "Example Supplier Ltd"}, "outstandingBalance": 70},
			Obj{"id": 78, "postingDate": "2026-07-01", "supplier": Obj{"id": 5, "companyName": "Example Supplier Ltd"}, "outstandingBalance": 30.5},
			Obj{"id": 79, "postingDate": "2026-09-01", "supplier": Obj{"id": 6, "companyName": "Paid Supplier Ltd"}, "outstandingBalance": 0},
		}}
	})
	res := env.obj("crunch_get_aged_creditors", map[string]any{"as_of_date": "2026-10-15", "details": true})
	want := `{"0_30":{"amount":70,"count":1},"31_60":{"amount":0,"count":0},"61_90":{"amount":0,"count":0},"over_90":{"amount":30.5,"count":1}}`
	if got := asJSON(t, res["buckets"]); got != want || num(res["total_outstanding"]) != 100.5 {
		t.Errorf("buckets: %s total %v", got, res["total_outstanding"])
	}
	if s := res["by_supplier"].([]any); len(s) != 1 || num(s[0].(Obj)["total"]) != 100.5 || num(s[0].(Obj)["supplier_id"]) != 5 {
		t.Errorf("by_supplier: %v", s)
	}
	if items := res["items"].([]any); len(items) != 2 || num(items[0].(Obj)["id"]) != 78 {
		t.Errorf("items: %v", items)
	}
	if msg := env.callErr("crunch_get_aged_creditors", map[string]any{"as_of_date": "2026-13-01"}); !strings.Contains(msg, "as_of_date") {
		t.Errorf("bad date: %s", msg)
	}
}
