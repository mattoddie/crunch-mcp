package tools

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

// Reports are read-only views built from the invoices and expenses Crunch returns. The public
// API has no report endpoints, so they page through the records and add them up here, in
// whole pence so the totals are exact.

// agedBuckets are the age bands, in order. For debtors the age is days past the due date
// (current = not yet due); for creditors it's days since the expense date, because Crunch's
// expenses have no due date.
var (
	debtorBuckets   = []string{"current", "1_30", "31_60", "61_90", "over_90"}
	creditorBuckets = []string{"0_30", "31_60", "61_90", "over_90"}
)

func debtorBucket(days int) string {
	switch {
	case days <= 0:
		return "current"
	case days <= 30:
		return "1_30"
	case days <= 60:
		return "31_60"
	case days <= 90:
		return "61_90"
	}
	return "over_90"
}

func creditorBucket(days int) string {
	switch {
	case days <= 30:
		return "0_30"
	case days <= 60:
		return "31_60"
	case days <= 90:
		return "61_90"
	}
	return "over_90"
}

func poundsFromPence(p int64) float64 { return float64(p) / 100 }

// amountPence reads an amount from an API object in whole pence.
func amountPence(o any, path ...string) (int64, bool) {
	f, ok := crunch.Num(o, path...)
	return pence(f), ok
}

// agedTotals adds up outstanding amounts by age band, overall and per counterparty.
type agedTotals struct {
	buckets []string
	total   map[string]int64
	count   map[string]int
	parties map[string]*agedParty
}

type agedParty struct {
	name    string
	id      any
	total   int64
	buckets map[string]int64
}

func newAgedTotals(buckets []string) *agedTotals {
	return &agedTotals{buckets: buckets, total: map[string]int64{}, count: map[string]int{}, parties: map[string]*agedParty{}}
}

func (t *agedTotals) add(bucket, party string, partyID any, amount int64) {
	t.total[bucket] += amount
	t.count[bucket]++
	key := party + "\x00" + crunch.Str(Obj{"id": partyID}, "id")
	p := t.parties[key]
	if p == nil {
		p = &agedParty{name: party, id: partyID, buckets: map[string]int64{}}
		t.parties[key] = p
	}
	p.total += amount
	p.buckets[bucket] += amount
}

func (t *agedTotals) result(asOf, partyLabel string, items []Obj, truncated bool) Obj {
	var grand int64
	buckets := Obj{}
	for _, b := range t.buckets {
		grand += t.total[b]
		buckets[b] = Obj{"amount": poundsFromPence(t.total[b]), "count": t.count[b]}
	}
	parties := make([]*agedParty, 0, len(t.parties))
	for _, p := range t.parties {
		parties = append(parties, p)
	}
	sort.Slice(parties, func(i, j int) bool {
		if parties[i].total != parties[j].total {
			return parties[i].total > parties[j].total
		}
		return parties[i].name < parties[j].name
	})
	var byParty []Obj
	for _, p := range parties {
		row := Obj{partyLabel: p.name, "total": poundsFromPence(p.total)}
		if p.id != nil {
			row[partyLabel+"_id"] = p.id
		}
		for _, b := range t.buckets {
			if p.buckets[b] != 0 {
				row[b] = poundsFromPence(p.buckets[b])
			}
		}
		byParty = append(byParty, row)
	}
	out := Obj{"as_of": asOf, "total_outstanding": poundsFromPence(grand), "buckets": buckets, "by_" + partyLabel: byParty}
	if byParty == nil {
		out["by_"+partyLabel] = []Obj{}
	}
	if items != nil {
		out["items"] = items
	}
	if truncated {
		out["note"] = "Crunch returned more records than this report reads (10000); the totals only cover the first 10000."
	}
	return out
}

type agedArgs struct {
	AsOf    Date `json:"as_of_date,omitempty" jsonschema:"Age the balances as of this date (YYYY-MM-DD; default today)"`
	Details bool `json:"details,omitempty" jsonschema:"Also list each outstanding record with its age (default false: totals only)"`
}

func (a agedArgs) asOf() (time.Time, string, error) {
	if a.AsOf == "" {
		now := time.Now()
		d := now.Format(time.DateOnly)
		t, _ := time.Parse(time.DateOnly, d)
		return t, d, nil
	}
	t, err := time.Parse(time.DateOnly, a.AsOf)
	if err != nil {
		return time.Time{}, "", errors.New("as_of_date must be a date like 2026-09-30")
	}
	return t, a.AsOf, nil
}

// daysBetween is the whole days from date (YYYY-MM-DD) to asOf; ok is false for a bad date.
func daysBetween(date string, asOf time.Time) (int, bool) {
	if len(date) > len(time.DateOnly) {
		date = date[:len(time.DateOnly)]
	}
	d, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return 0, false
	}
	return int(asOf.Sub(d).Hours() / 24), true
}

// allPages fetches every page of a paged list, up to maxPagedItems.
func allPages(ctx context.Context, cl *crunch.Client, path string, q crunch.Query, sort string) ([]Obj, bool, error) {
	max := maxPagedItems
	res, items, err := fetchPages(ctx, cl, path, q, PageArgs{All: true, MaxItems: &max}, sort)
	if err != nil {
		return nil, false, err
	}
	return items, res["truncated"] == true, nil
}

func registerReports(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "reports", Name: "crunch_get_aged_debtors", Title: "Aged debtors",
		Description: "Who owes the business money and for how long: the outstanding balance of every issued sales invoice, grouped by days past its due date (current, 1-30, 31-60, 61-90 and over 90 days) and by customer, largest first. " +
			"Drafts are left out. Reads every invoice, so it can take a few seconds on a large account. details=true also lists each invoice.",
	}, func(ctx context.Context, a agedArgs) (any, error) {
		asOf, asOfStr, err := a.asOf()
		if err != nil {
			return nil, err
		}
		invoices, truncated, err := allPages(ctx, cl, "/invoices", nil, "issuedDate,desc")
		if err != nil {
			return nil, err
		}
		t := newAgedTotals(debtorBuckets)
		var items []Obj
		if a.Details {
			items = []Obj{}
		}
		for _, inv := range invoices {
			outstanding, _ := amountPence(inv, "outstandingBalance")
			if outstanding == 0 || strings.EqualFold(crunch.Str(inv, "status"), "DRAFT") {
				continue
			}
			due := crunch.Str(inv, "dueDate")
			if due == "" {
				due = crunch.Str(inv, "issuedDate")
			}
			if issued, ok := daysBetween(crunch.Str(inv, "issuedDate"), asOf); ok && issued < 0 {
				continue // issued after the report date
			}
			days, _ := daysBetween(due, asOf)
			bucket := debtorBucket(days)
			t.add(bucket, crunch.Str(inv, "customerName"), crunch.ID(inv, "customerId"), outstanding)
			if a.Details {
				items = append(items, crunch.Compact(Obj{
					"id": crunch.ID(inv, "id"), "number": crunch.Str(inv, "invoiceNumber"), "customer": crunch.Str(inv, "customerName"),
					"issued": crunch.Str(inv, "issuedDate"), "due": due, "days_overdue": max(days, 0), "bucket": bucket,
					"outstanding": poundsFromPence(outstanding),
				}))
			}
		}
		sortAged(items, "days_overdue")
		return t.result(asOfStr, "customer", items, truncated), nil
	})

	add(c, Def{
		Toolset: "reports", Name: "crunch_get_aged_creditors", Title: "Aged creditors",
		Description: "What the business owes suppliers and for how long: the outstanding balance of every unpaid or part-paid expense, grouped by age (0-30, 31-60, 61-90 and over 90 days since the expense date, since Crunch's expenses have no due date) and by supplier, largest first. " +
			"Reads every expense up to the report date, so it can take a few seconds on a large account. details=true also lists each expense.",
	}, func(ctx context.Context, a agedArgs) (any, error) {
		asOf, asOfStr, err := a.asOf()
		if err != nil {
			return nil, err
		}
		expenses, truncated, err := allPages(ctx, cl, "/expenses", crunch.Query{"endDate": asOfStr}, "postingDate,desc")
		if err != nil {
			return nil, err
		}
		t := newAgedTotals(creditorBuckets)
		var items []Obj
		if a.Details {
			items = []Obj{}
		}
		for _, e := range expenses {
			outstanding, _ := amountPence(e, "outstandingBalance")
			if outstanding == 0 {
				continue
			}
			days, ok := daysBetween(crunch.Str(e, "postingDate"), asOf)
			if ok && days < 0 {
				continue
			}
			bucket := creditorBucket(days)
			t.add(bucket, crunch.Str(e, "supplier", "companyName"), crunch.ID(e, "supplier", "id"), outstanding)
			if a.Details {
				items = append(items, crunch.Compact(Obj{
					"id": crunch.ID(e, "id"), "supplier": crunch.Str(e, "supplier", "companyName"), "reference": crunch.Str(e, "supplierReference"),
					"date": crunch.Str(e, "postingDate"), "age_days": days, "bucket": bucket, "outstanding": poundsFromPence(outstanding),
				}))
			}
		}
		sortAged(items, "age_days")
		return t.result(asOfStr, "supplier", items, truncated), nil
	})

	registerVATSummary(c)
	registerCreditNotes(c)
}

// sortAged puts the oldest records first.
func sortAged(items []Obj, key string) {
	sort.SliceStable(items, func(i, j int) bool {
		a, _ := items[i][key].(int)
		b, _ := items[j][key].(int)
		return a > b
	})
}
