package tools

import (
	"strings"
	"testing"

	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

func TestVATSummary(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/invoices", crunchtest.JSON(Obj{"last": true, "content": []any{
		Obj{"id": 1, "status": "ISSUED", "issuedDate": "2026-07-05"},
		Obj{"id": 2, "status": "PAID", "issuedDate": "2026-09-30", "totalCreditsAmount": 12},
		Obj{"id": 3, "status": "ISSUED", "issuedDate": "2026-10-01"}, // after the period
		Obj{"id": 4, "status": "DRAFT", "issuedDate": "2026-08-01"},
		Obj{"id": 5, "status": "ISSUED", "issuedDate": "2026-08-15"},
	}}))
	invoices := map[string]Obj{
		// Two rates, lines that add up: split by line.
		"1": {"id": 1, "netAmount": 150, "vatAmount": 20, "items": []any{
			Obj{"priceExclVat": 50, "quantity": 2, "vatRate": "Standard", "vatAmountCharged": 20},
			Obj{"priceExclVat": 50, "quantity": 1, "vatRate": "Zero Rated", "vatAmountCharged": 0},
		}},
		// One rate, no line VAT: counted from the invoice totals.
		"2": {"id": 2, "netAmount": 99.99, "vatAmount": 20, "items": []any{Obj{"priceExclVat": 99.99, "vatRate": "Standard"}}},
		// Two rates that don't add up: reported for checking.
		"5": {"id": 5, "netAmount": 30, "vatAmount": 4, "items": []any{
			Obj{"priceExclVat": 10, "quantity": 1, "vatRate": "Standard"}, Obj{"priceExclVat": 20, "quantity": 1, "vatRate": "Reduced"},
		}},
	}
	env.mock.On("GET", `/invoices/(\d+)`, func(_ crunchtest.Request, m []string) (int, any) {
		if inv, ok := invoices[m[1]]; ok {
			return 200, inv
		}
		t.Errorf("fetched invoice %s", m[1])
		return 404, nil
	})
	env.mock.On("GET", "/expenses", func(r crunchtest.Request, _ []string) (int, any) {
		if r.Query.Get("startDate") != "2026-07-01" || r.Query.Get("endDate") != "2026-09-30" {
			t.Errorf("expense query: %v", r.Query)
		}
		return 200, Obj{"last": true, "content": []any{
			Obj{"id": 77, "postingDate": "2026-08-01", "totalCreditsAmount": 6, "expenseLineItems": []any{
				Obj{"vatType": "Standard", "amount": Obj{"grossAmount": 120, "netAmount": 100, "vatAmount": 20}},
				Obj{"vatType": "Exempt", "amount": Obj{"grossAmount": 30.5, "vatAmount": 0}},
			}},
		}}
	})
	res := env.obj("crunch_get_vat_summary", map[string]any{"from": "2026-07-01", "to": "2026-09-30"})
	sales := res["sales"].(Obj)
	wantSales := `[{"gross":20,"lines":1,"net":20,"vat":0,"vat_type":"Reduced"},{"gross":249.99,"lines":3,"net":209.99,"vat":40,"vat_type":"Standard"},{"gross":50,"lines":1,"net":50,"vat":0,"vat_type":"Zero Rated"}]`
	if got := asJSON(t, sales["by_vat_type"]); got != wantSales || num(sales["invoices"]) != 3 {
		t.Errorf("sales:\n got %s\nwant %s", got, wantSales)
	}
	if got := asJSON(t, sales["total"]); got != `{"gross":319.99,"net":279.99,"vat":40}` {
		t.Errorf("sales total: %s", got)
	}
	purchases := res["purchases"].(Obj)
	if got := asJSON(t, purchases["by_vat_type"]); got != `[{"gross":30.5,"lines":1,"net":30.5,"vat":0,"vat_type":"Exempt"},{"gross":120,"lines":1,"net":100,"vat":20,"vat_type":"Standard"}]` {
		t.Errorf("purchases: %s", got)
	}
	if num(res["vat_on_sales_minus_purchases"]) != 20 {
		t.Errorf("difference: %v", res["vat_on_sales_minus_purchases"])
	}
	if asJSON(t, res["check_invoices"].(Obj)["ids"]) != `[5]` || asJSON(t, res["credit_notes"]) != `{"purchases_gross":6,"sales_gross":12}` {
		t.Errorf("checks and credits: %v %v", res["check_invoices"], res["credit_notes"])
	}
	if !strings.Contains(res["note"].(string), "not a VAT return") {
		t.Errorf("note: %v", res["note"])
	}
	if msg := env.callErr("crunch_get_vat_summary", map[string]any{"from": "2026-09-30", "to": "2026-07-01"}); !strings.Contains(msg, "to must not") {
		t.Errorf("reversed dates: %s", msg)
	}
}
