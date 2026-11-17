package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

type vatSummaryArgs struct {
	StartDate Date `json:"from" jsonschema:"First day of the period (YYYY-MM-DD), for example the start of a VAT quarter"`
	EndDate   Date `json:"to" jsonschema:"Last day of the period (YYYY-MM-DD)"`
}

// maxVATInvoices caps how many invoices one VAT summary fetches in full (one request each).
const maxVATInvoices = 2000

const vatSummaryNote = "Built from the lines of the sales invoices (by issue date, drafts left out) and expenses (by expense date) recorded in Crunch. It is not a VAT return: " +
	"on the Flat Rate Scheme VAT due is a percentage of turnover, on cash accounting it follows payment dates, and credit notes are not deducted here (see credit_notes)."

// vatTotals adds up net, VAT and gross by VAT type, in whole pence.
type vatTotals struct {
	types map[string]*vatLine
}

type vatLine struct {
	net, vat, gross int64
	lines           int
}

func newVATTotals() *vatTotals { return &vatTotals{types: map[string]*vatLine{}} }

func (t *vatTotals) add(vatType string, net, vat, gross int64) {
	if vatType == "" {
		vatType = "(none)"
	}
	l := t.types[vatType]
	if l == nil {
		l = &vatLine{}
		t.types[vatType] = l
	}
	l.net += net
	l.vat += vat
	l.gross += gross
	l.lines++
}

func (t *vatTotals) result(countKey string, count int) Obj {
	names := make([]string, 0, len(t.types))
	for n := range t.types {
		names = append(names, n)
	}
	sort.Strings(names)
	var total vatLine
	rows := []Obj{}
	for _, n := range names {
		l := t.types[n]
		total.net += l.net
		total.vat += l.vat
		total.gross += l.gross
		rows = append(rows, Obj{"vat_type": n, "net": poundsFromPence(l.net), "vat": poundsFromPence(l.vat), "gross": poundsFromPence(l.gross), "lines": l.lines})
	}
	return Obj{countKey: count, "by_vat_type": rows, "total": Obj{"net": poundsFromPence(total.net), "vat": poundsFromPence(total.vat), "gross": poundsFromPence(total.gross)}, "vat_pence": total.vat}
}

// addInvoiceLines adds a sales invoice's lines. Each line's net is its unit price times its
// quantity and its VAT is vatAmountCharged. When the lines don't add up to the invoice's own
// net and VAT (for example a line without vatAmountCharged), an invoice with one VAT rate is
// counted from its totals instead, and one with several rates is reported for checking.
func addInvoiceLines(t *vatTotals, inv Obj) (ok bool) {
	items := crunch.List(inv, "items")
	invNet, _ := amountPence(inv, "netAmount")
	invVAT, _ := amountPence(inv, "vatAmount")
	type line struct {
		rate     string
		net, vat int64
	}
	var lines []line
	var sumNet, sumVAT int64
	rates := map[string]bool{}
	for _, it := range items {
		price, _ := crunch.Num(it, "priceExclVat")
		qty, hasQty := crunch.Num(it, "quantity")
		if !hasQty {
			qty = 1
		}
		vat, _ := amountPence(it, "vatAmountCharged")
		l := line{crunch.Str(it, "vatRate"), pence(price * qty), vat}
		lines = append(lines, l)
		sumNet += l.net
		sumVAT += l.vat
		rates[l.rate] = true
	}
	switch {
	case len(lines) > 0 && sumNet == invNet && sumVAT == invVAT:
		for _, l := range lines {
			t.add(l.rate, l.net, l.vat, l.net+l.vat)
		}
		return true
	case len(rates) <= 1:
		rate := ""
		for r := range rates {
			rate = r
		}
		t.add(rate, invNet, invVAT, invNet+invVAT)
		return true
	}
	for _, l := range lines {
		t.add(l.rate, l.net, l.vat, l.net+l.vat)
	}
	return false
}

func registerVATSummary(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "reports", Name: "crunch_get_vat_summary", Title: "VAT summary",
		Description: "Net, VAT and gross by VAT type for a period, for sales (issued invoices by issue date) and purchases (expenses by expense date), with VAT on sales minus VAT on purchases. " +
			"Use it to sense-check a VAT return; it is not one (Crunch's API has no VAT returns, and the Flat Rate Scheme and cash accounting work VAT out differently). Reads each invoice in the period, so long periods take a while.",
	}, func(ctx context.Context, a vatSummaryArgs) (any, error) {
		if a.EndDate < a.StartDate {
			return nil, errors.New("to must not be before from")
		}
		invoices, truncInv, err := allPages(ctx, cl, "/invoices", nil, "issuedDate,desc")
		if err != nil {
			return nil, err
		}
		var ids []any
		var salesCredits int64
		for _, inv := range invoices {
			d := crunch.Str(inv, "issuedDate")
			if d < a.StartDate || d > a.EndDate || strings.EqualFold(crunch.Str(inv, "status"), "DRAFT") {
				continue
			}
			ids = append(ids, crunch.ID(inv, "id"))
			credits, _ := amountPence(inv, "totalCreditsAmount")
			salesCredits += credits
		}
		if len(ids) > maxVATInvoices {
			return nil, fmt.Errorf("the period has %d invoices, more than the %d this summary reads one by one; use a shorter period", len(ids), maxVATInvoices)
		}
		full, err := fetchEach(ctx, cl, ids)
		if err != nil {
			return nil, err
		}
		sales := newVATTotals()
		var check []any
		for i, inv := range full {
			if !addInvoiceLines(sales, inv) {
				check = append(check, ids[i])
			}
		}

		expenses, truncExp, err := allPages(ctx, cl, "/expenses", crunch.Query{"startDate": a.StartDate, "endDate": a.EndDate}, "postingDate,asc")
		if err != nil {
			return nil, err
		}
		purchases := newVATTotals()
		var purchaseCredits int64
		n := 0
		for _, e := range expenses {
			if d := crunch.Str(e, "postingDate"); d < a.StartDate || d > a.EndDate {
				continue
			}
			n++
			credits, _ := amountPence(e, "totalCreditsAmount")
			purchaseCredits += credits
			for _, l := range crunch.List(e, "expenseLineItems") {
				gross, _ := amountPence(l, "amount", "grossAmount")
				vat, _ := amountPence(l, "amount", "vatAmount")
				net, hasNet := amountPence(l, "amount", "netAmount")
				if !hasNet {
					net = gross - vat
				}
				purchases.add(crunch.Str(l, "vatType"), net, vat, gross)
			}
		}

		s, p := sales.result("invoices", len(full)), purchases.result("expenses", n)
		diff := s["vat_pence"].(int64) - p["vat_pence"].(int64)
		delete(s, "vat_pence")
		delete(p, "vat_pence")
		out := Obj{
			"from": a.StartDate, "to": a.EndDate, "sales": s, "purchases": p,
			"vat_on_sales_minus_purchases": poundsFromPence(diff), "note": vatSummaryNote,
		}
		if salesCredits != 0 || purchaseCredits != 0 {
			out["credit_notes"] = Obj{"sales_gross": poundsFromPence(salesCredits), "purchases_gross": poundsFromPence(purchaseCredits)}
		}
		if check != nil {
			out["check_invoices"] = Obj{"ids": check, "note": "These invoices have several VAT rates and their lines don't add up to the invoice's net and VAT, so their split by VAT type may be off. Check them with crunch_get_invoice."}
		}
		if truncInv || truncExp {
			out["warning"] = "Crunch returned more than 10000 invoices or expenses; only the first 10000 were read, so the totals may be incomplete."
		}
		return out, nil
	})
}

// fetchEach gets the invoices with the given ids, a few at a time.
func fetchEach(ctx context.Context, cl *crunch.Client, ids []any) ([]Obj, error) {
	out := make([]Obj, len(ids))
	errs := make([]error, len(ids))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			errs[i] = cl.Get(ctx, fmt.Sprintf("/invoices/%v", id), nil, &out[i])
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
