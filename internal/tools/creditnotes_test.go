package tools

import (
	"testing"

	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

func TestListCreditNotes(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/invoices", crunchtest.JSON(Obj{"last": true, "content": []any{
		Obj{"id": 500, "status": "PAID", "totalCreditsAmount": 30},
		Obj{"id": 501, "status": "ISSUED", "totalCreditsAmount": 0},
	}}))
	env.mock.On("GET", "/invoices/500", crunchtest.JSON(Obj{"id": 500, "customer": Obj{"id": 42, "name": "Example Customer Ltd"}, "creditNotes": []any{
		Obj{"id": 1, "date": "2026-09-20", "state": "ISSUED", "amount": 10, "lineItems": []any{Obj{"itemIndex": 0, "description": "Discount", "amount": 10}}},
		Obj{"id": 2, "date": "2026-10-02", "state": "SETTLED", "amount": 20, "refunds": []any{Obj{"id": 9, "date": "2026-10-03", "amount": 20, "reconciled": true}}},
	}}))
	env.mock.On("GET", "/invoices/501", func(crunchtest.Request, []string) (int, any) {
		t.Error("fetched an invoice without credits")
		return 404, nil
	})
	env.mock.On("GET", "/expenses", crunchtest.JSON(Obj{"last": true, "content": []any{
		Obj{"id": 77, "supplier": Obj{"id": 5, "companyName": "Example Supplier Ltd"}, "creditNotes": []any{
			Obj{"id": 3, "date": "2026-09-25", "amount": Obj{"grossAmount": 12, "netAmount": 10, "vatAmount": 2}},
		}},
	}}))

	res := env.obj("crunch_list_credit_notes", nil)
	items := res["items"].([]any)
	if num(res["total"]) != 2 || num(items[0].(Obj)["id"]) != 2 || items[0].(Obj)["customer"] != "Example Customer Ltd" || len(items[0].(Obj)["refunds"].([]any)) != 1 {
		t.Errorf("sales: %v", res)
	}
	res = env.obj("crunch_list_credit_notes", map[string]any{"for": "all", "end_date": "2026-09-30"})
	got := asJSON(t, res["items"])
	want := `[{"expense_id":77,"gross":12,"id":3,"net":10,"date":"2026-09-25","supplier":"Example Supplier Ltd","supplier_id":5,"type":"purchase","vat":2},` +
		`{"amount":10,"customer":"Example Customer Ltd","customer_id":42,"date":"2026-09-20","id":1,"invoice_id":500,"lines":[{"amount":10,"description":"Discount"}],"state":"ISSUED","type":"sales"}]`
	if num(res["total"]) != 2 || got != asJSON(t, jsonArr(t, want)) {
		t.Errorf("all:\n got %s\nwant %s", got, want)
	}
	for _, r := range env.mock.Requests() {
		if r.Path == "/expenses" && r.Query.Get("endDate") != "2026-09-30" {
			t.Errorf("expense query: %v", r.Query)
		}
	}
}

func jsonArr(t *testing.T, s string) any {
	t.Helper()
	return jsonObj(t, `{"a":`+s+`}`)["a"]
}
