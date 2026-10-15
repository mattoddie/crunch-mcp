package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

// LineItem is one line of an expense.
type LineItem struct {
	ExpenseType string `json:"expense_type" jsonschema:"Expense type name, for example ACCOUNTANCY or TELEPHONE (see crunch_list_expense_types)"`
	Description string `json:"description,omitempty" jsonschema:"What was bought"`
	GrossAmount Money  `json:"gross_amount" jsonschema:"Total including VAT, for example 120.00"`
	NetAmount   Money  `json:"net_amount,omitempty" jsonschema:"Amount before VAT (normally worked out by Crunch from the VAT type)"`
	VATAmount   Money  `json:"vat_amount,omitempty" jsonschema:"VAT amount (normally worked out by Crunch from the VAT type)"`
	VATType     string `json:"vat_type,omitempty" jsonschema:"VAT type display name, for example Standard, Exempt or Outside the Scope (see crunch_list_vat_types). Defaults to the expense type's default VAT type."`
	EmployeeID  *int64 `json:"employee_id,omitempty" jsonschema:"Director or employee the line relates to (see crunch_list_persons)"`
}

// Payment records how an expense (or invoice) was paid.
type Payment struct {
	Date          Date   `json:"date" jsonschema:"Payment date (YYYY-MM-DD)"`
	Amount        Money  `json:"amount" jsonschema:"Amount paid, for example 120.00"`
	PaymentMethod string `json:"payment_method,omitempty" jsonschema:"Payment method name, for example EFT, CASH or DIRECTORS_EXPENSES (default EFT; see crunch_list_payment_methods)"`
	BankAccountID *int64 `json:"bank_account_id,omitempty" jsonschema:"Bank account the payment went through (see crunch_list_bank_accounts)"`
	CreditCardID  *int64 `json:"credit_card_id,omitempty" jsonschema:"Credit card the payment was made with (see crunch_list_credit_cards)"`
	PersonID      *int64 `json:"person_id,omitempty" jsonschema:"For DIRECTORS_EXPENSES: the director who paid (see crunch_list_persons)"`
}

func paymentTarget(method string, bankAccount, creditCard, person *int64) Obj {
	if method == "" {
		method = "EFT"
	}
	return set(Obj{"paymentMethod": Obj{"paymentMethodName": method}}, "bankAccount", ref(bankAccount), "creditCard", ref(creditCard), "person", ref(person))
}

func (p Payment) body() Obj {
	return Obj{"date": p.Date, "amount": p.Amount, "paymentTarget": paymentTarget(p.PaymentMethod, p.BankAccountID, p.CreditCardID, p.PersonID)}
}

// lineItemsBody builds expense lines, filling in each expense type's default VAT type when vat_type is left out.
func lineItemsBody(ctx context.Context, cl *crunch.Client, lines []LineItem) ([]Obj, error) {
	var defaults map[string]string
	for _, l := range lines {
		if l.VATType == "" && defaults == nil {
			var err error
			if defaults, err = expenseTypeDefaults(ctx, cl); err != nil {
				return nil, err
			}
		}
	}
	out := make([]Obj, 0, len(lines))
	for _, l := range lines {
		vat := l.VATType
		if vat == "" {
			vat = defaults[l.ExpenseType]
		}
		if vat == "" {
			return nil, fmt.Errorf("no vat_type given for the %s line, and Crunch has no default for that expense type; check crunch_list_expense_types", l.ExpenseType)
		}
		out = append(out, set(Obj{
			"expenseType": l.ExpenseType,
			"vatType":     vat,
			"amount":      set(Obj{}, "grossAmount", l.GrossAmount, "netAmount", l.NetAmount, "vatAmount", l.VATAmount),
		}, "description", l.Description, "employee", ref(l.EmployeeID)))
	}
	return out, nil
}

// SupplierRef chooses the supplier of an expense.
type SupplierRef struct {
	SupplierID     *int64 `json:"supplier_id,omitempty" jsonschema:"Supplier ID (see crunch_list_suppliers). Give this or supplier_name."`
	SupplierName   string `json:"supplier_name,omitempty" jsonschema:"Instead of supplier_id: the supplier's exact name"`
	CreateSupplier bool   `json:"create_supplier,omitempty" jsonschema:"With supplier_name: create the supplier if it doesn't exist yet (default false)"`
}

// resolve turns supplier_id or supplier_name into a supplier reference, optionally creating the supplier.
func (s SupplierRef) resolve(ctx context.Context, cl *crunch.Client, expenseType string) (Obj, error) {
	if s.SupplierID != nil {
		return ref(s.SupplierID), nil
	}
	if s.SupplierName == "" {
		return nil, errors.New("give supplier_id or supplier_name")
	}
	found, err := findSuppliersByName(ctx, cl, s.SupplierName)
	if err != nil {
		return nil, err
	}
	var matches []Obj
	for _, m := range found {
		if !crunch.Bool(m, "archived") {
			matches = append(matches, m)
		}
	}
	switch {
	case len(matches) == 1:
		return Obj{"id": matches[0]["id"]}, nil
	case len(matches) > 1:
		var ids []string
		for _, m := range matches {
			ids = append(ids, crunch.Str(m, "id"))
		}
		return nil, fmt.Errorf("several suppliers are called %q (IDs %s); pass supplier_id instead", s.SupplierName, strings.Join(ids, ", "))
	case !s.CreateSupplier:
		return nil, fmt.Errorf("no supplier called exactly %q. Check crunch_list_suppliers (search), or pass create_supplier=true to create it", s.SupplierName)
	}
	body := Obj{"companyName": s.SupplierName}
	if expenseType != "" {
		body["expenseType"] = Obj{"expenseTypeName": expenseType}
	}
	var created Obj
	if err := cl.Post(ctx, "/expenses/suppliers", nil, body, &created); err != nil {
		return nil, err
	}
	return Obj{"id": created["id"]}, nil
}

// ExpenseInput is shared by one-off and recurring expenses.
type ExpenseInput struct {
	SupplierRef
	LineItems         []LineItem `json:"line_items" jsonschema:"One or more lines, each with its own expense type, VAT type and amount"`
	SupplierReference string     `json:"supplier_reference,omitempty" jsonschema:"The supplier's invoice or receipt number"`
	Note              string     `json:"note,omitempty" jsonschema:"Internal note"`
}

func (in ExpenseInput) body(ctx context.Context, cl *crunch.Client, postingDate string) (Obj, error) {
	if len(in.LineItems) == 0 {
		return nil, errors.New("give at least one line item")
	}
	supplier, err := in.resolve(ctx, cl, in.LineItems[0].ExpenseType)
	if err != nil {
		return nil, err
	}
	lines, err := lineItemsBody(ctx, cl, in.LineItems)
	if err != nil {
		return nil, err
	}
	return set(Obj{"supplier": supplier, "expenseLineItems": lines}, "postingDate", postingDate, "supplierReference", in.SupplierReference, "note", in.Note), nil
}

type listExpensesFilters struct {
	SupplierID       *int64 `json:"supplier_id,omitempty" jsonschema:"Only this supplier's expenses"`
	ExpenseType      string `json:"expense_type,omitempty" jsonschema:"Only this expense type, for example TRAVEL_AND_SUBSISTENCE"`
	HasAttachment    *bool  `json:"has_attachment,omitempty" jsonschema:"Only expenses with (true) or without (false) a receipt attached"`
	PaymentMethod    string `json:"payment_method,omitempty" jsonschema:"Only expenses paid this way, for example EFT"`
	StartDate        Date   `json:"start_date,omitempty" jsonschema:"Expenses dated on or after (YYYY-MM-DD)"`
	EndDate          Date   `json:"end_date,omitempty" jsonschema:"Expenses dated on or before (YYYY-MM-DD)"`
	PaymentStartDate Date   `json:"payment_start_date,omitempty" jsonschema:"Paid on or after (YYYY-MM-DD)"`
	PaymentEndDate   Date   `json:"payment_end_date,omitempty" jsonschema:"Paid on or before (YYYY-MM-DD)"`
}

func (f listExpensesFilters) query() crunch.Query {
	return crunch.Query{
		"supplierId": f.SupplierID, "expenseType": f.ExpenseType, "attachment": f.HasAttachment, "paymentMethod": f.PaymentMethod,
		"startDate": f.StartDate, "endDate": f.EndDate, "paymentStartDate": f.PaymentStartDate, "paymentEndDate": f.PaymentEndDate,
	}
}

type listExpensesArgs struct {
	listExpensesFilters
	PageArgs
	Raw bool `json:"raw,omitempty" jsonschema:"Return the full objects from the API instead of compact summaries (larger)"`
}

type expenseIDArgs struct {
	ExpenseID int64 `json:"expense_id" jsonschema:"Expense ID (see crunch_list_expenses)"`
}

type getExpenseArgs struct {
	ExpenseID int64 `json:"expense_id" jsonschema:"Expense ID (see crunch_list_expenses)"`
	Raw       bool  `json:"raw,omitempty" jsonschema:"Return the API object unchanged (the base for hand-made updates)"`
}

type expensesTotalArgs struct {
	Status string `json:"status" jsonschema:"Expense status to total, as Crunch names it in the status field of expenses (see crunch_list_expenses)"`
	listExpensesFilters
}

type createExpenseArgs struct {
	ExpenseInput
	PostingDate Date           `json:"posting_date" jsonschema:"Date of the expense or receipt (YYYY-MM-DD); must be in an open accounting period"`
	Payments    []Payment      `json:"payments,omitempty" jsonschema:"How it was paid; leave out to record it as unpaid"`
	Attachments []Attachment   `json:"attachments,omitempty" jsonschema:"Receipts or invoices to attach"`
	Extra       map[string]any `json:"extra,omitempty" jsonschema:"Additional raw ExpenseDto fields, using Crunch's own field names (advanced). Merged last."`
}

type updateExpenseArgs struct {
	ExpenseID         int64          `json:"expense_id" jsonschema:"Expense ID (see crunch_list_expenses)"`
	SupplierID        *int64         `json:"supplier_id,omitempty" jsonschema:"Move the expense to this supplier"`
	PostingDate       Date           `json:"posting_date,omitempty" jsonschema:"New date (YYYY-MM-DD)"`
	SupplierReference *string        `json:"supplier_reference,omitempty" jsonschema:"New supplier reference"`
	Note              *string        `json:"note,omitempty" jsonschema:"New internal note"`
	LineItems         []LineItem     `json:"line_items,omitempty" jsonschema:"Replacement lines (all existing lines are replaced)"`
	Changes           map[string]any `json:"changes,omitempty" jsonschema:"Raw ExpenseDto fields to merge into the current expense (advanced)"`
}

type addExpensePaymentArgs struct {
	ExpenseID int64 `json:"expense_id" jsonschema:"Expense ID (see crunch_list_expenses)"`
	Payment
}

type removeExpensePaymentArgs struct {
	ExpenseID int64  `json:"expense_id" jsonschema:"Expense ID (see crunch_list_expenses)"`
	PaymentID *int64 `json:"payment_id,omitempty" jsonschema:"Payment to remove; omit to remove all of the expense's payments"`
}

type addAttachmentArgs struct {
	ExpenseID   int64        `json:"expense_id" jsonschema:"Expense ID (see crunch_list_expenses)"`
	Attachments []Attachment `json:"attachments" jsonschema:"Files to attach"`
}

type downloadExpenseAttachmentArgs struct {
	ExpenseID    int64  `json:"expense_id" jsonschema:"Expense ID (see crunch_list_expenses)"`
	AttachmentID int64  `json:"attachment_id" jsonschema:"Attachment ID within the expense (0, 1, 2...; see crunch_get_expense)"`
	FileName     string `json:"file_name,omitempty" jsonschema:"Attachment file name (looked up when omitted)"`
	SaveTo       string `json:"save_to,omitempty" jsonschema:"Also save the file on the machine running this server: a directory (the file keeps its name) or a full path. Existing files are never overwritten."`
}

func summarizeExpenseFull(e Obj) Obj {
	out := crunch.SummarizeExpense(e)
	out["lines"] = mapItems(crunch.List(e, "expenseLineItems"), func(l Obj) Obj {
		return crunch.Compact(Obj{
			"id": crunch.ID(l, "id"), "type": crunch.Str(l, "expenseType"), "description": crunch.Str(l, "description"), "vat_type": crunch.Str(l, "vatType"),
			"gross": crunch.Money(l, "amount", "grossAmount"), "net": crunch.Money(l, "amount", "netAmount"), "vat": crunch.Money(l, "amount", "vatAmount"),
			"employee": crunch.Str(l, "employee", "displayableName"),
		})
	})
	out["payments"] = mapItems(crunch.List(e, "supplierPayments"), func(p Obj) Obj {
		return crunch.Compact(Obj{
			"id": crunch.ID(p, "id"), "date": crunch.Str(p, "date"), "amount": crunch.Money(p, "amount"),
			"method":       crunch.Str(p, "paymentTarget", "paymentMethod", "paymentMethodName"),
			"bank_account": firstNonEmpty(crunch.Str(p, "paymentTarget", "bankAccount", "displayableName"), crunch.Str(p, "paymentTarget", "bankAccount", "id")),
			"credit_card":  firstNonEmpty(crunch.Str(p, "paymentTarget", "creditCard", "displayableName"), crunch.Str(p, "paymentTarget", "creditCard", "id")),
			"person":       crunch.Str(p, "paymentTarget", "person", "displayableName"),
			"reconciled":   crunch.Bool(p, "reconciled"),
		})
	})
	out["attachment_files"] = mapItems(crunch.List(e, "attachments"), func(f Obj) Obj {
		return Obj{"id": crunch.ID(f, "id"), "file_name": crunch.Str(f, "fileName")}
	})
	out["credit_notes"] = e["creditNotes"]
	out["editable"] = e["editable"]
	out["deletable"] = e["deletable"]
	out["period_open"] = e["expenseLedgerIsOpen"]
	out["recharged_to"] = crunch.Str(e, "rechargedClient", "name")
	return compactKeepFalse(out)
}

// compactKeepFalse is Compact without dropping false booleans, which matter for editable/deletable.
func compactKeepFalse(o Obj) Obj {
	keep := Obj{}
	for k, v := range o {
		if b, ok := v.(bool); ok && !b {
			keep[k] = false
		}
	}
	return merge(crunch.Compact(o), keep)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func registerExpenses(c *Context) {
	cl := c.Client

	getExpense := func(ctx context.Context, id int64) (Obj, error) {
		var e Obj
		return e, cl.Get(ctx, path("/expenses/%d", id), nil, &e)
	}
	putExpense := func(ctx context.Context, id int64, body Obj) (Obj, error) {
		var e Obj
		return e, cl.Put(ctx, path("/expenses/%d", id), nil, body, &e)
	}

	add(c, Def{
		Toolset: "expenses", Name: "crunch_list_expenses", Title: "List expenses",
		Description: "List expenses (purchases, receipts and bills), newest first, filtered by supplier, expense type, date, payment date, payment method or whether a receipt is attached. Each result shows supplier, amounts, status, outstanding balance and payment method. Paged (up to 200 per page); use all=true to fetch every page.",
	}, func(ctx context.Context, a listExpensesArgs) (any, error) {
		var mapFn func(Obj) Obj
		if !a.Raw {
			mapFn = crunch.SummarizeExpense
		}
		return fetchPaged(ctx, cl, "/expenses", a.query(), a.PageArgs, "postingDate,desc", mapFn)
	})

	add(c, Def{
		Toolset: "expenses", Name: "crunch_get_expense", Title: "Get expense",
		Description: "Get one expense: its lines (types, VAT, amounts), payments, attachments, credit notes, and whether it can still be edited or deleted. raw=true returns the API object.",
	}, func(ctx context.Context, a getExpenseArgs) (any, error) {
		e, err := getExpense(ctx, a.ExpenseID)
		if err != nil || a.Raw {
			return e, err
		}
		return summarizeExpenseFull(e), nil
	})

	add(c, Def{
		Toolset: "expenses", Name: "crunch_get_expenses_total", Title: "Get expenses total",
		Description: "Total the expenses matching the filters (gross amount, plus under- and over-paid amounts) without listing them. Handy for \"how much did I spend on travel this year?\".",
	}, func(ctx context.Context, a expensesTotalArgs) (any, error) {
		q := a.query()
		q["status"] = a.Status
		var res Obj
		if err := cl.Get(ctx, "/expenses/total", q, &res); err != nil {
			return nil, err
		}
		return crunch.Compact(Obj{"gross": crunch.Money(res, "grossAmount"), "underpaid": crunch.Money(res, "underpaid"), "overpaid": crunch.Money(res, "overpaid")}), nil
	})

	add(c, Def{
		Toolset: "expenses", Name: "crunch_create_expense", Title: "Create expense",
		Description: "Record an expense: who it was from, the date, one or more lines (expense type, VAT type, gross amount), and optionally how it was paid and the receipt. " +
			"Look up valid values first: crunch_list_expense_types, crunch_list_vat_types, crunch_list_payment_methods, crunch_list_bank_accounts. The date must be in an open accounting period.",
		Gate: Write,
	}, func(ctx context.Context, a createExpenseArgs) (any, error) {
		body, err := a.body(ctx, cl, a.PostingDate)
		if err != nil {
			return nil, err
		}
		if len(a.Payments) > 0 {
			var payments []Obj
			for _, p := range a.Payments {
				payments = append(payments, p.body())
			}
			body["supplierPayments"] = payments
		}
		if len(a.Attachments) > 0 {
			if body["attachments"], err = attachmentsBody(a.Attachments, 0); err != nil {
				return nil, err
			}
		}
		var created Obj
		if err := cl.Post(ctx, "/expenses", nil, merge(body, a.Extra), &created); err != nil {
			return nil, err
		}
		return summarizeExpenseFull(created), nil
	})

	add(c, Def{
		Toolset: "expenses", Name: "crunch_update_expense", Title: "Update expense",
		Description: "Change an expense. The current expense is fetched and only what you pass changes: posting_date, supplier, reference, note, or line_items (which replace all lines). " +
			"Payments and attachments are kept as they are; use crunch_add_expense_payment, crunch_remove_expense_payment and crunch_add_expense_attachment for those. Reconciled expenses and closed years can't be changed.",
		Gate: Write, Idempotent: true,
	}, func(ctx context.Context, a updateExpenseArgs) (any, error) {
		current, err := getExpense(ctx, a.ExpenseID)
		if err != nil {
			return nil, err
		}
		patch := set(Obj{}, "supplier", ref(a.SupplierID), "postingDate", a.PostingDate)
		if a.SupplierReference != nil {
			patch["supplierReference"] = *a.SupplierReference
		}
		if a.Note != nil {
			patch["note"] = *a.Note
		}
		if len(a.LineItems) > 0 {
			if patch["expenseLineItems"], err = lineItemsBody(ctx, cl, a.LineItems); err != nil {
				return nil, err
			}
		}
		if len(patch) == 0 && len(a.Changes) == 0 {
			return nil, errors.New("nothing to change")
		}
		updated, err := putExpense(ctx, a.ExpenseID, deepMerge(deepMerge(current, patch), a.Changes))
		if err != nil {
			return nil, err
		}
		return summarizeExpenseFull(updated), nil
	})

	add(c, Def{
		Toolset: "expenses", Name: "crunch_add_expense_payment", Title: "Record expense payment",
		Description: "Record that an expense was paid (fully or partly): date, amount, payment method and the bank account, credit card or director it was paid from. Existing payments are kept.",
		Gate:        Write,
	}, func(ctx context.Context, a addExpensePaymentArgs) (any, error) {
		current, err := getExpense(ctx, a.ExpenseID)
		if err != nil {
			return nil, err
		}
		payments, _ := current["supplierPayments"].([]any)
		current["supplierPayments"] = append(payments, a.Payment.body())
		outstanding, hasOutstanding := crunch.Num(current, "outstandingBalance")
		updated, err := putExpense(ctx, a.ExpenseID, current)
		if err != nil {
			return nil, err
		}
		out := summarizeExpenseFull(updated)
		if hasOutstanding && a.Amount.Float() > outstanding+0.005 {
			out["warning"] = fmt.Sprintf("The payment is more than the %.2f that was outstanding.", outstanding)
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "expenses", Name: "crunch_remove_expense_payment", Title: "Remove expense payment",
		Description: "Remove a payment from an expense, leaving the expense itself (it becomes unpaid or part-paid). Reconciled payments can't be removed. See crunch_get_expense for payment IDs.",
		Gate:        Delete,
	}, func(ctx context.Context, a removeExpensePaymentArgs) (any, error) {
		current, err := getExpense(ctx, a.ExpenseID)
		if err != nil {
			return nil, err
		}
		payments := crunch.List(current, "supplierPayments")
		if len(payments) == 0 {
			return nil, errors.New("the expense has no payments")
		}
		keep, removed := []any{}, []Obj{}
		for _, p := range payments {
			if a.PaymentID == nil || crunch.ID(p, "id") == *a.PaymentID {
				removed = append(removed, p)
			} else {
				keep = append(keep, p)
			}
		}
		if len(removed) == 0 {
			return nil, fmt.Errorf("the expense has no payment %d", *a.PaymentID)
		}
		for _, p := range removed {
			if crunch.Bool(p, "reconciled") {
				return nil, errors.New("reconciled payments can't be removed; unreconcile them in Crunch first")
			}
		}
		current["supplierPayments"] = keep
		updated, err := putExpense(ctx, a.ExpenseID, current)
		if err != nil {
			return nil, err
		}
		return Obj{
			"removed": mapItems(removed, func(p Obj) Obj {
				return crunch.Compact(Obj{"id": crunch.ID(p, "id"), "date": crunch.Str(p, "date"), "amount": crunch.Money(p, "amount")})
			}),
			"expense": summarizeExpenseFull(updated),
		}, nil
	})

	add(c, Def{
		Toolset: "expenses", Name: "crunch_add_expense_attachment", Title: "Attach receipt to expense",
		Description: "Attach one or more files (receipts, invoices) to an existing expense. Existing attachments are kept.",
		Gate:        Write,
	}, func(ctx context.Context, a addAttachmentArgs) (any, error) {
		if len(a.Attachments) == 0 {
			return nil, errors.New("give at least one attachment")
		}
		current, err := getExpense(ctx, a.ExpenseID)
		if err != nil {
			return nil, err
		}
		existing, _ := current["attachments"].([]any)
		next := int64(0)
		for _, f := range crunch.Objs(existing) {
			if id, ok := crunch.ID(f, "id").(int64); ok && id >= next {
				next = id + 1
			}
		}
		files, err := attachmentsBody(a.Attachments, next)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			existing = append(existing, f)
		}
		current["attachments"] = existing
		updated, err := putExpense(ctx, a.ExpenseID, current)
		if err != nil {
			return nil, err
		}
		return Obj{"expense_id": a.ExpenseID, "attachments": mapItems(crunch.List(updated, "attachments"), func(f Obj) Obj {
			return Obj{"id": crunch.ID(f, "id"), "file_name": crunch.Str(f, "fileName")}
		})}, nil
	})

	add(c, Def{
		Toolset: "expenses", Name: "crunch_download_expense_attachment", Title: "Download expense attachment",
		Description: "Download a receipt or other file attached to an expense. Images are returned so you can read them; other files (PDFs) come back as an embedded resource. Attachment IDs and names come from crunch_get_expense.",
	}, func(ctx context.Context, a downloadExpenseAttachmentArgs) (any, error) {
		name := a.FileName
		if name == "" {
			e, err := getExpense(ctx, a.ExpenseID)
			if err != nil {
				return nil, err
			}
			for _, f := range crunch.List(e, "attachments") {
				if crunch.ID(f, "id") == a.AttachmentID {
					name = crunch.Str(f, "fileName")
				}
			}
			if name == "" {
				return nil, fmt.Errorf("expense %d has no attachment %d", a.ExpenseID, a.AttachmentID)
			}
		}
		f, err := cl.GetFile(ctx, "/expenses/attachments", crunch.Query{"expenseId": a.ExpenseID, "attachmentId": a.AttachmentID, "fileName": name})
		if err != nil {
			return nil, err
		}
		if len(f.Data) == 0 {
			return nil, errors.New("the attachment is empty; it may have been uploaded without a data: URI prefix")
		}
		return fileResult(f, name, fmt.Sprintf("crunch://expenses/%d/attachments/%d", a.ExpenseID, a.AttachmentID), a.SaveTo)
	})

	add(c, Def{
		Toolset: "expenses", Name: "crunch_delete_expense", Title: "Delete expense",
		Description: "Permanently delete an expense with its lines, payments and attachments. Not possible for reconciled expenses or those in a closed year.",
		Gate:        Delete,
	}, func(ctx context.Context, a expenseIDArgs) (any, error) {
		if err := cl.Delete(ctx, path("/expenses/%d", a.ExpenseID), nil, nil); err != nil {
			return nil, err
		}
		return Obj{"expense_id": a.ExpenseID, "deleted": true}, nil
	})
}
