package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

const invoiceFixture = `{
  "id": 500, "status": "ISSUED", "issuedDate": "2026-09-05", "dueDate": "2026-10-03", "currency": "POUND_STERLING",
  "customer": {"id": 42, "name": "Example Customer Ltd", "email": "accounts@example.com"},
  "netAmount": 100, "vatAmount": 20, "grossAmount": 120, "outstandingBalance": 120, "lineItemType": "SIMPLE",
  "items": [{"id": 1, "description": "Consulting", "priceExclVat": 100, "quantity": 1, "vatRate": "Standard"}],
  "payments": [{"id": 600, "amount": 20, "date": "2026-09-10", "reconciled": true, "paymentTarget": {"paymentMethod": {"paymentMethodName": "EFT"}}}],
  "attachments": [{"id": 0, "fileName": "timesheet.pdf"}],
  "bankAccount": {"id": 11, "displayableName": "Business"}, "deletable": false
}`

func TestCreateInvoiceBody(t *testing.T) {
	env := newEnv(t)
	res := env.obj("crunch_create_invoice", map[string]any{
		"client_id": 42, "issued_date": "2026-09-05", "bank_account_id": 11, "payment_terms_days": 28, "note": "Thanks!",
		"items": []any{map[string]any{"description": "Consulting services", "price_excl_vat": "100.00", "vat_rate": "Outside the Scope"}},
	})
	want := `{"attachments":[],"bankAccount":{"id":11},"currency":"POUND_STERLING","customer":{"id":42},"issuedDate":"2026-09-05",` +
		`"items":[{"description":"Consulting services","lineType":"S","priceExclVat":100.00,"quantity":1,"vatRate":"Outside the Scope"}],` +
		`"lineItemType":"SIMPLE","note":{"text":"Thanks!"},"payment":{"paymentTerms":{"days":28}}}`
	if got := asJSON(t, env.mock.Last("POST").Body); got != want {
		t.Errorf("body:\n got %s\nwant %s", got, want)
	}
	if num(res["id"]) != 9001 {
		t.Errorf("result: %v", res)
	}
	if msg := env.callErr("crunch_create_invoice", map[string]any{"client_id": 42, "issued_date": "2026-09-05", "items": []any{}}); !strings.Contains(msg, "at least one item") {
		t.Errorf("no items: %s", msg)
	}
}

func TestInvoicePayments(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/invoices/500", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, invoiceFixture) })
	env.call("crunch_add_invoice_payment", map[string]any{"invoice_id": 500, "date": "2026-09-30", "amount": "100.00"})
	body := env.mock.Last("PUT").Body.(Obj)
	payments := body["payments"].([]any)
	if len(payments) != 2 || asJSON(t, payments[1]) != `{"amount":100.00,"date":"2026-09-30","paymentTarget":{"bankAccount":{"id":11},"paymentMethod":{"paymentMethodName":"EFT"}}}` {
		t.Errorf("payments: %s", asJSON(t, payments))
	}
	if len(body["attachments"].([]any)) != 1 || len(body["items"].([]any)) != 1 {
		t.Error("existing attachments and items must be sent back")
	}
	if msg := env.callErr("crunch_remove_invoice_payment", map[string]any{"invoice_id": 500, "payment_id": 600}); !strings.Contains(msg, "reconciled") {
		t.Errorf("reconciled payment removal: %s", msg)
	}
}

func TestIssueAndEmailInvoice(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/invoices/500", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, invoiceFixture) })
	env.mock.On("POST", "/invoices/500/issue", func(crunchtest.Request, []string) (int, any) { return 200, jsonObj(t, invoiceFixture) })
	if res := env.obj("crunch_issue_invoice", map[string]any{"invoice_id": 500}); res["status"] != "ISSUED" {
		t.Errorf("issue: %v", res)
	}
	res := env.obj("crunch_email_invoice", map[string]any{"invoice_id": 500, "subject": "Invoice", "include_attachments": true})
	post := env.mock.Last("POST")
	if post.Path != "/invoices/500/email" || post.Query.Get("attachments") != "true" || asJSON(t, post.Body) != `{"emailAddresses":["accounts@example.com"],"subject":"Invoice"}` {
		t.Errorf("email request: %s %v %s", post.Path, post.Query, asJSON(t, post.Body))
	}
	if asJSON(t, res["sent_to"]) != `["accounts@example.com"]` {
		t.Errorf("result: %v", res)
	}
}

func TestListInvoicesByStatusAndTotals(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/invoices", crunchtest.JSON(Obj{"content": []any{
		Obj{"id": 1, "invoiceNumber": "INV-1", "status": "PAID", "grossAmount": 10},
		Obj{"id": 2, "invoiceNumber": "INV-2", "status": "ISSUED", "grossAmount": 20, "outstandingBalance": 20, "customerName": "Acme"},
	}, "last": true, "totalElements": 2}))
	res := env.obj("crunch_list_invoices", map[string]any{"status": "issued", "client_id": 42})
	items := res["items"].([]any)
	if len(items) != 1 || items[0].(Obj)["number"] != "INV-2" || env.mock.Last("GET").Query.Get("clientId") != "42" {
		t.Errorf("filtered: %v", res)
	}

	env.mock.On("GET", "/invoices/sales-invoice-totals", crunchtest.JSON(Obj{"salesTotal": 1000, "outstandingTotal": 250}))
	env.mock.On("GET", "/invoices/filtered-totals", crunchtest.JSON(Obj{"invoicedSum": 300, "owedToYouSum": 100}))
	if tot := env.obj("crunch_get_invoice_totals", nil); num(tot["outstanding"]) != 250 {
		t.Errorf("totals: %v", tot)
	}
	tot := env.obj("crunch_get_invoice_totals", map[string]any{"issued_from_date": "2026-04-01", "issued_to_date": "2027-03-31"})
	if q := env.mock.Last("GET").Query; q.Get("issuedDateStart") != "2026-04-01" || q.Get("issuedDateEnd") != "2027-03-31" || num(tot["owed_to_you"]) != 100 {
		t.Errorf("filtered totals: %v %v", q, tot)
	}
}

func TestDownloadInvoicePDF(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/invoices/500/pdf", func(crunchtest.Request, []string) (int, any) { return 200, []byte("%PDF-1.7 fake") })
	dir := t.TempDir()
	res := env.raw("crunch_download_invoice_pdf", map[string]any{"invoice_id": 500, "save_to": dir})
	if res.IsError {
		t.Fatal(text(res))
	}
	r, ok := res.Content[1].(*mcp.EmbeddedResource)
	if !ok || r.Resource.MIMEType != "application/pdf" || string(r.Resource.Blob) != "%PDF-1.7 fake" || r.Resource.URI != "crunch://invoices/500/pdf" {
		t.Errorf("resource: %#v", res.Content[1])
	}
	if b, err := os.ReadFile(filepath.Join(dir, "invoice-500.pdf")); err != nil || string(b) != "%PDF-1.7 fake" {
		t.Errorf("saved file: %v", err)
	}
	// Never overwrites.
	if msg := env.callErr("crunch_download_invoice_pdf", map[string]any{"invoice_id": 500, "save_to": dir}); !strings.Contains(msg, "already exists") {
		t.Errorf("overwrite: %s", msg)
	}
}

func TestInvoiceAttachmentLimit(t *testing.T) {
	env := newEnv(t)
	inv := jsonObj(t, invoiceFixture)
	inv["attachments"] = []any{Obj{"id": 0}, Obj{"id": 1}, Obj{"id": 2}, Obj{"id": 3}, Obj{"id": 4}}
	env.mock.On("GET", "/invoices/500", crunchtest.JSON(inv))
	msg := env.callErr("crunch_add_invoice_attachment", map[string]any{"invoice_id": 500, "attachments": []any{map[string]any{"file_name": "a.txt", "content_base64": "YQ=="}}})
	if !strings.Contains(msg, "at most 5") {
		t.Errorf("limit: %s", msg)
	}
}
