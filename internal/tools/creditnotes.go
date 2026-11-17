package tools

import (
	"context"
	"sort"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

// Crunch's public API has no credit note endpoints: credit notes are only returned inside
// the invoices (SalesInvoiceDto.creditNotes) and expenses (ExpenseDto.creditNotes) they
// belong to. So they can be listed, but not created; that's done in the Crunch app.

type listCreditNotesArgs struct {
	For       string `json:"for,omitempty" jsonschema:"invoices (default: credit notes you issued against sales invoices), expenses (credit notes from suppliers) or all"`
	StartDate Date   `json:"from,omitempty" jsonschema:"Only credit notes dated on or after (YYYY-MM-DD)"`
	EndDate   Date   `json:"to,omitempty" jsonschema:"Only credit notes dated on or before (YYYY-MM-DD)"`
	ListArgs
}

func (a listCreditNotesArgs) inRange(date string) bool {
	return (a.StartDate == "" || date >= a.StartDate) && (a.EndDate == "" || date <= a.EndDate)
}

func summarizeRefunds(cn Obj) any {
	return nonEmpty(mapItems(crunch.List(cn, "refunds"), func(p Obj) Obj {
		return crunch.Compact(Obj{"id": crunch.ID(p, "id"), "date": crunch.Str(p, "date"), "amount": crunch.Money(p, "amount"), "reconciled": crunch.Bool(p, "reconciled")})
	}))
}

func nonEmpty(items []Obj) any {
	if len(items) == 0 {
		return nil
	}
	return items
}

func registerCreditNotes(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "reports", Name: "crunch_list_credit_notes", Title: "List credit notes",
		Description: "List credit notes: those issued against your sales invoices (for=invoices, the default), those from suppliers against expenses (for=expenses), or both (for=all), newest first, with the invoice or expense they belong to, amount, state and refunds. " +
			"Crunch's public API can't create credit notes; do that in the Crunch app. Reads the invoices that have credits one by one, so it can take a few seconds.",
		Enums: map[string][]string{"for": {"invoices", "expenses", "all"}},
	}, func(ctx context.Context, a listCreditNotesArgs) (any, error) {
		kind := a.For
		if kind == "" {
			kind = "invoices"
		}
		var notes []Obj
		var truncated bool
		if kind == "invoices" || kind == "all" {
			invoices, trunc, err := allPages(ctx, cl, "/invoices", nil, "issuedDate,desc")
			if err != nil {
				return nil, err
			}
			truncated = truncated || trunc
			var ids []any
			for _, inv := range invoices {
				if credits, _ := amountPence(inv, "totalCreditsAmount"); credits != 0 {
					ids = append(ids, crunch.ID(inv, "id"))
				}
			}
			if len(ids) > maxVATInvoices {
				ids = ids[:maxVATInvoices]
				truncated = true
			}
			full, err := fetchEach(ctx, cl, ids)
			if err != nil {
				return nil, err
			}
			for _, inv := range full {
				for _, cn := range crunch.List(inv, "creditNotes") {
					if !a.inRange(crunch.Str(cn, "date")) {
						continue
					}
					notes = append(notes, crunch.Compact(Obj{
						"type": "sales", "id": crunch.ID(cn, "id"), "date": crunch.Str(cn, "date"), "state": crunch.Str(cn, "state"),
						"amount":     crunch.Money(cn, "amount"),
						"invoice_id": crunch.ID(inv, "id"), "customer": crunch.Str(inv, "customer", "name"), "customer_id": crunch.ID(inv, "customer", "id"),
						"lines": nonEmpty(mapItems(crunch.List(cn, "lineItems"), func(l Obj) Obj {
							return crunch.Compact(Obj{"description": crunch.Str(l, "description"), "amount": crunch.Money(l, "amount")})
						})),
						"refunds": summarizeRefunds(cn),
					}))
				}
			}
		}
		if kind == "expenses" || kind == "all" {
			// A supplier's credit note is dated on or after its expense, so only the end date narrows the expenses fetched.
			expenses, trunc, err := allPages(ctx, cl, "/expenses", crunch.Query{"endDate": a.EndDate}, "postingDate,desc")
			if err != nil {
				return nil, err
			}
			truncated = truncated || trunc
			for _, e := range expenses {
				for _, cn := range crunch.List(e, "creditNotes") {
					if !a.inRange(crunch.Str(cn, "date")) {
						continue
					}
					notes = append(notes, crunch.Compact(Obj{
						"type": "purchase", "id": crunch.ID(cn, "id"), "date": crunch.Str(cn, "date"),
						"gross": crunch.Money(cn, "amount", "grossAmount"), "net": crunch.Money(cn, "amount", "netAmount"), "vat": crunch.Money(cn, "amount", "vatAmount"),
						"expense_id": crunch.ID(e, "id"), "supplier": crunch.Str(e, "supplier", "companyName"), "supplier_id": crunch.ID(e, "supplier", "id"),
						"lines": nonEmpty(mapItems(crunch.List(cn, "lineItems"), func(l Obj) Obj {
							return crunch.Compact(Obj{"description": crunch.Str(l, "description"), "gross": crunch.Money(l, "amount", "grossAmount")})
						})),
						"refunds": summarizeRefunds(cn),
					}))
				}
			}
		}
		sort.SliceStable(notes, func(i, j int) bool { return crunch.Str(notes[i], "date") > crunch.Str(notes[j], "date") })
		if notes == nil {
			notes = []Obj{}
		}
		out := a.result(notes)
		if truncated {
			out["note"] = "Crunch returned more records than this tool reads; the list may be incomplete. Narrow it with to."
		}
		return out, nil
	})
}
