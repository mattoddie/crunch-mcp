package tools

import (
	"context"
	"errors"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

// Recurrence is the schedule of a recurring expense. Values are Crunch's own names.
type Recurrence struct {
	Frequency           string `json:"frequency" jsonschema:"How often, as Crunch names it in recurringEventFrequency (for example MONTHLY); check an existing recurring expense if unsure"`
	NextOccurrenceDate  Date   `json:"next_occurrence_date" jsonschema:"Date of the next (first) expense to create (YYYY-MM-DD)"`
	EndType             string `json:"end_type,omitempty" jsonschema:"How the schedule ends, as Crunch names it in recurringEventEndType (never, on a date, or after a number of occurrences)"`
	EndDate             Date   `json:"end_date,omitempty" jsonschema:"Last date, when it ends on a date (YYYY-MM-DD)"`
	OccurrenceCount     *int   `json:"occurrence_count,omitempty" jsonschema:"Number of expenses to create, when it ends after a count"`
	FrequencyOccurrence string `json:"frequency_occurrence,omitempty" jsonschema:"Crunch's frequencyOccurrence setting, if your schedule needs it"`
	Description         string `json:"description,omitempty" jsonschema:"Description of the schedule"`
	Enabled             *bool  `json:"enabled,omitempty" jsonschema:"Whether the schedule is active (default true)"`
}

func (r Recurrence) body() Obj {
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return set(Obj{"enabled": enabled},
		"recurringEventFrequency", r.Frequency, "nextOccurrenceDate", r.NextOccurrenceDate, "recurringEventEndType", r.EndType,
		"endDate", r.EndDate, "occurrenceCount", r.OccurrenceCount, "frequencyOccurrence", r.FrequencyOccurrence, "description", r.Description)
}

type listRecurringArgs struct {
	PageArgs
	Raw bool `json:"raw,omitempty" jsonschema:"Return the full objects from the API instead of compact summaries (larger)"`
}

type recurringIDArgs struct {
	RecurringExpenseID int64 `json:"recurring_expense_id" jsonschema:"Recurring expense ID (see crunch_list_recurring_expenses)"`
}

type createRecurringArgs struct {
	ExpenseInput
	PostingDate   Date           `json:"posting_date,omitempty" jsonschema:"Date on the template expense (defaults to the first occurrence)"`
	Recurrence    Recurrence     `json:"recurrence" jsonschema:"The schedule"`
	PaymentMethod string         `json:"payment_method,omitempty" jsonschema:"Payment method for the created expenses, for example EFT"`
	BankAccountID *int64         `json:"bank_account_id,omitempty" jsonschema:"Bank account the payments come from"`
	Extra         map[string]any `json:"extra,omitempty" jsonschema:"Additional raw ExpenseDto fields, using Crunch's own field names (advanced). Merged last."`
}

type updateRecurringArgs struct {
	RecurringExpenseID int64          `json:"recurring_expense_id" jsonschema:"Recurring expense ID (see crunch_list_recurring_expenses)"`
	LineItems          []LineItem     `json:"line_items,omitempty" jsonschema:"Replacement lines (all existing lines are replaced)"`
	Enabled            *bool          `json:"enabled,omitempty" jsonschema:"Pause (false) or resume (true) the schedule"`
	NextOccurrenceDate Date           `json:"next_occurrence_date,omitempty" jsonschema:"New next date (YYYY-MM-DD)"`
	EndDate            Date           `json:"end_date,omitempty" jsonschema:"New end date (YYYY-MM-DD)"`
	SupplierReference  *string        `json:"supplier_reference,omitempty" jsonschema:"New supplier reference"`
	Note               *string        `json:"note,omitempty" jsonschema:"New internal note"`
	Changes            map[string]any `json:"changes,omitempty" jsonschema:"Raw ExpenseDto fields to merge in, for example {\"recurringEvent\": {\"recurringEventFrequency\": \"...\"}} (advanced)"`
}

func registerRecurring(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "recurring", Name: "crunch_list_recurring_expenses", Title: "List recurring expenses",
		Description: "List recurring expenses (regular bills such as rent, phone or software subscriptions that Crunch creates automatically) with supplier, amount, status and next date.",
	}, func(ctx context.Context, a listRecurringArgs) (any, error) {
		var mapFn func(Obj) Obj
		if !a.Raw {
			mapFn = crunch.SummarizeRecurringExpense
		}
		return fetchPaged(ctx, cl, "/expenses/recurring-expenses", nil, a.PageArgs, "", mapFn)
	})

	add(c, Def{
		Toolset: "recurring", Name: "crunch_get_recurring_expense", Title: "Get recurring expense",
		Description: "Get a recurring expense in full: the template expense (supplier, lines, payment) and its schedule (recurringEvent).",
	}, func(ctx context.Context, a recurringIDArgs) (any, error) {
		var out any
		return out, cl.Get(ctx, path("/expenses/recurring-expenses/%d", a.RecurringExpenseID), nil, &out)
	})

	add(c, Def{
		Toolset: "recurring", Name: "crunch_create_recurring_expense", Title: "Create recurring expense",
		Description: "Set up a recurring expense: a template expense (supplier, lines, optional payment method) that Crunch creates automatically on a schedule. " +
			"Schedule values are Crunch's own; look at an existing one with crunch_get_recurring_expense if you're unsure of the names.",
		Gate: Write,
	}, func(ctx context.Context, a createRecurringArgs) (any, error) {
		date := a.PostingDate
		if date == "" {
			date = a.Recurrence.NextOccurrenceDate
		}
		body, err := a.body(ctx, cl, date)
		if err != nil {
			return nil, err
		}
		body["recurringEvent"] = a.Recurrence.body()
		if a.PaymentMethod != "" || a.BankAccountID != nil {
			body["supplierPayments"] = []Obj{{"date": date, "paymentTarget": paymentTarget(a.PaymentMethod, a.BankAccountID, nil, nil)}}
		}
		var out any
		return out, cl.Post(ctx, "/expenses/recurring-expenses", nil, merge(body, a.Extra), &out)
	})

	add(c, Def{
		Toolset: "recurring", Name: "crunch_update_recurring_expense", Title: "Update recurring expense",
		Description: "Change a recurring expense: its lines, its schedule (for example pause it with enabled=false, or change the next or end date), or any field via `changes`. Only what you pass changes. Expenses already created aren't affected.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a updateRecurringArgs) (any, error) {
		p := path("/expenses/recurring-expenses/%d", a.RecurringExpenseID)
		var current Obj
		if err := cl.Get(ctx, p, nil, &current); err != nil {
			return nil, err
		}
		patch := Obj{}
		if len(a.LineItems) > 0 {
			lines, err := lineItemsBody(ctx, cl, a.LineItems)
			if err != nil {
				return nil, err
			}
			patch["expenseLineItems"] = lines
		}
		if a.SupplierReference != nil {
			patch["supplierReference"] = *a.SupplierReference
		}
		if a.Note != nil {
			patch["note"] = *a.Note
		}
		if event := set(Obj{}, "enabled", a.Enabled, "nextOccurrenceDate", a.NextOccurrenceDate, "endDate", a.EndDate); len(event) > 0 {
			patch["recurringEvent"] = event
		}
		if len(patch) == 0 && len(a.Changes) == 0 {
			return nil, errors.New("nothing to change")
		}
		body := deepMerge(deepMerge(current, patch), a.Changes)
		if err := c.checkKeepsEntries(current, body); err != nil {
			return nil, err
		}
		var out any
		return out, cl.Put(ctx, p, nil, body, &out)
	})

	add(c, Def{
		Toolset: "recurring", Name: "crunch_delete_recurring_expense", Title: "Delete recurring expense",
		Description: "Delete a recurring expense so no more expenses are created from it. Expenses it already created are kept. To pause it instead, use crunch_update_recurring_expense with enabled=false.",
		Gate:        Delete,
	}, func(ctx context.Context, a recurringIDArgs) (any, error) {
		if err := cl.Delete(ctx, path("/expenses/recurring-expenses/%d", a.RecurringExpenseID), nil, nil); err != nil {
			return nil, err
		}
		return Obj{"recurring_expense_id": a.RecurringExpenseID, "deleted": true}, nil
	})
}
