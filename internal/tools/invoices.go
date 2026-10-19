package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

const maxInvoiceAttachments = 5

// InvoiceItem is one line of a sales invoice.
type InvoiceItem struct {
	Description  string `json:"description" jsonschema:"What was sold, for example Consulting services"`
	PriceExclVAT Money  `json:"price_excl_vat" jsonschema:"Unit price before VAT, for example 100.00"`
	Quantity     Money  `json:"quantity,omitempty" jsonschema:"Quantity (default 1)"`
	VATRate      string `json:"vat_rate" jsonschema:"VAT type display name, for example Standard or Outside the Scope (see crunch_list_vat_types for=invoices)"`
	LineType     string `json:"line_type,omitempty" jsonschema:"Crunch's lineType (default S)"`
}

func (it InvoiceItem) body() Obj {
	qty := it.Quantity
	if qty == "" {
		qty = "1"
	}
	lineType := it.LineType
	if lineType == "" {
		lineType = "S"
	}
	return Obj{"description": it.Description, "priceExclVat": it.PriceExclVAT, "quantity": qty, "vatRate": it.VATRate, "lineType": lineType}
}

func itemsBody(items []InvoiceItem) []Obj {
	out := make([]Obj, len(items))
	for i, it := range items {
		out[i] = it.body()
	}
	return out
}

type listInvoicesArgs struct {
	ClientID *int64 `json:"client_id,omitempty" jsonschema:"Only this client's invoices (see crunch_list_clients)"`
	Status   string `json:"status,omitempty" jsonschema:"Only invoices with this status, as shown in the status field (for example DRAFT); fetches all pages and filters them here"`
	PageArgs
	Raw bool `json:"raw,omitempty" jsonschema:"Return the full objects from the API instead of compact summaries (larger)"`
}

type invoiceIDArgs struct {
	InvoiceID int64 `json:"invoice_id" jsonschema:"Invoice ID (see crunch_list_invoices); not the invoice number"`
}

type getInvoiceArgs struct {
	InvoiceID int64 `json:"invoice_id" jsonschema:"Invoice ID (see crunch_list_invoices); not the invoice number"`
	Raw       bool  `json:"raw,omitempty" jsonschema:"Return the API object unchanged"`
}

type invoiceTotalsArgs struct {
	ClientID      *int64 `json:"client_id,omitempty" jsonschema:"Only this client's invoices"`
	Status        string `json:"status,omitempty" jsonschema:"Only invoices with this status, as Crunch names it"`
	IssuedFrom    Date   `json:"issued_from_date,omitempty" jsonschema:"Issued on or after (YYYY-MM-DD)"`
	IssuedTo      Date   `json:"issued_to_date,omitempty" jsonschema:"Issued on or before (YYYY-MM-DD)"`
	DueFrom       Date   `json:"due_from_date,omitempty" jsonschema:"Due on or after (YYYY-MM-DD)"`
	DueTo         Date   `json:"due_to_date,omitempty" jsonschema:"Due on or before (YYYY-MM-DD)"`
	Recurring     *bool  `json:"recurring,omitempty" jsonschema:"Only recurring (true) or one-off (false) invoices"`
	InvoiceNumber string `json:"invoice_number,omitempty" jsonschema:"Only this invoice number"`
}

func (a invoiceTotalsArgs) filtered() bool {
	return a.ClientID != nil || a.Status != "" || a.IssuedFrom != "" || a.IssuedTo != "" || a.DueFrom != "" || a.DueTo != "" || a.Recurring != nil || a.InvoiceNumber != ""
}

type downloadInvoicePDFArgs struct {
	InvoiceID int64  `json:"invoice_id" jsonschema:"Invoice ID (see crunch_list_invoices)"`
	SaveTo    string `json:"save_to,omitempty" jsonschema:"Also save the file in the server's CRUNCH_FILES_DIR: a directory (the file keeps its name) or a file path, absolute or relative to it. Existing files are never overwritten. Only works when the server sets CRUNCH_FILES_DIR"`
}

type downloadInvoiceAttachmentArgs struct {
	InvoiceID    int64  `json:"invoice_id" jsonschema:"Invoice ID (see crunch_list_invoices)"`
	AttachmentID int64  `json:"attachment_id" jsonschema:"Attachment ID within the invoice (see crunch_get_invoice)"`
	FileName     string `json:"file_name,omitempty" jsonschema:"Attachment file name (looked up when omitted)"`
	SaveTo       string `json:"save_to,omitempty" jsonschema:"Also save the file in the server's CRUNCH_FILES_DIR: a directory (the file keeps its name) or a file path, absolute or relative to it. Existing files are never overwritten. Only works when the server sets CRUNCH_FILES_DIR"`
}

type createInvoiceArgs struct {
	ClientID         int64          `json:"client_id" jsonschema:"Customer to invoice (see crunch_list_clients; create one with crunch_create_client)"`
	IssuedDate       Date           `json:"issued_date" jsonschema:"Invoice date (YYYY-MM-DD)"`
	DueDate          Date           `json:"due_date,omitempty" jsonschema:"Due date (YYYY-MM-DD); worked out from the payment terms when omitted"`
	Items            []InvoiceItem  `json:"items" jsonschema:"Invoice lines"`
	BankAccountID    *int64         `json:"bank_account_id,omitempty" jsonschema:"Bank account the customer should pay into, shown on the invoice (see crunch_list_bank_accounts)"`
	PaymentTermsDays *int           `json:"payment_terms_days,omitempty" jsonschema:"Payment terms in days, for example 28"`
	SendReminders    *bool          `json:"send_reminders,omitempty" jsonschema:"Have Crunch email the customer payment reminders (true needs CRUNCH_ALLOW_EMAIL=true)"`
	Reference        string         `json:"reference,omitempty" jsonschema:"Your reference"`
	PurchaseOrder    string         `json:"purchase_order,omitempty" jsonschema:"The customer's purchase order number"`
	Note             string         `json:"note,omitempty" jsonschema:"Note printed on the invoice (see crunch_get_default_invoice_note)"`
	LineItemType     string         `json:"line_item_type,omitempty" jsonschema:"SIMPLE (default) or DETAILED lines"`
	Attachments      []Attachment   `json:"attachments,omitempty" jsonschema:"Up to 5 files to attach"`
	Extra            map[string]any `json:"extra,omitempty" jsonschema:"Additional raw SalesInvoiceDto fields, using Crunch's own field names (advanced). Merged last."`
}

type updateInvoiceArgs struct {
	InvoiceID        int64          `json:"invoice_id" jsonschema:"Invoice ID (see crunch_list_invoices)"`
	ClientID         *int64         `json:"client_id,omitempty" jsonschema:"Move the invoice to this customer"`
	IssuedDate       Date           `json:"issued_date,omitempty" jsonschema:"New invoice date (YYYY-MM-DD)"`
	DueDate          Date           `json:"due_date,omitempty" jsonschema:"New due date (YYYY-MM-DD)"`
	Items            []InvoiceItem  `json:"items,omitempty" jsonschema:"Replacement lines (all existing lines are replaced)"`
	BankAccountID    *int64         `json:"bank_account_id,omitempty" jsonschema:"Bank account shown on the invoice"`
	PaymentTermsDays *int           `json:"payment_terms_days,omitempty" jsonschema:"Payment terms in days"`
	SendReminders    *bool          `json:"send_reminders,omitempty" jsonschema:"Have Crunch email the customer payment reminders (true needs CRUNCH_ALLOW_EMAIL=true)"`
	Reference        *string        `json:"reference,omitempty" jsonschema:"New reference"`
	PurchaseOrder    *string        `json:"purchase_order,omitempty" jsonschema:"New purchase order number"`
	Note             *string        `json:"note,omitempty" jsonschema:"New note printed on the invoice"`
	Changes          map[string]any `json:"changes,omitempty" jsonschema:"Raw SalesInvoiceDto fields to merge into the current invoice (advanced)"`
}

type invoicePaymentArgs struct {
	InvoiceID     int64  `json:"invoice_id" jsonschema:"Invoice ID (see crunch_list_invoices)"`
	Date          Date   `json:"date" jsonschema:"Date the customer paid (YYYY-MM-DD)"`
	Amount        Money  `json:"amount" jsonschema:"Amount received, for example 120.00"`
	PaymentMethod string `json:"payment_method,omitempty" jsonschema:"Payment method name (default EFT; see crunch_list_payment_methods for=invoices)"`
	BankAccountID *int64 `json:"bank_account_id,omitempty" jsonschema:"Bank account the money went into (defaults to the invoice's bank account)"`
}

type removeInvoicePaymentArgs struct {
	InvoiceID int64 `json:"invoice_id" jsonschema:"Invoice ID (see crunch_list_invoices)"`
	PaymentID int64 `json:"payment_id" jsonschema:"Payment to remove (see crunch_get_invoice)"`
}

type invoiceAttachmentArgs struct {
	InvoiceID   int64        `json:"invoice_id" jsonschema:"Invoice ID (see crunch_list_invoices)"`
	Attachments []Attachment `json:"attachments" jsonschema:"Files to attach (an invoice can have up to 5)"`
}

type emailInvoiceArgs struct {
	InvoiceID          int64    `json:"invoice_id" jsonschema:"Invoice ID (see crunch_list_invoices)"`
	EmailAddresses     []string `json:"email_addresses,omitempty" jsonschema:"Recipients (default: the customer's email address)"`
	Subject            string   `json:"subject,omitempty" jsonschema:"Email subject"`
	Message            string   `json:"message,omitempty" jsonschema:"Email message"`
	IncludeAttachments bool     `json:"include_attachments,omitempty" jsonschema:"Also send the invoice's attachments (default false)"`
}

func summarizeInvoiceFull(i Obj) Obj {
	out := crunch.Compact(Obj{
		"id":                 crunch.ID(i, "id"),
		"customer":           crunch.Str(i, "customer", "name"),
		"customer_id":        crunch.ID(i, "customer", "id"),
		"reference":          crunch.Str(i, "reference"),
		"purchase_order":     crunch.Str(i, "purchaseOrder"),
		"issued":             crunch.Str(i, "issuedDate"),
		"due":                crunch.Str(i, "dueDate"),
		"status":             crunch.Str(i, "status"),
		"net":                crunch.Money(i, "netAmount"),
		"vat":                crunch.Money(i, "vatAmount"),
		"gross":              crunch.Money(i, "grossAmount"),
		"outstanding":        crunch.Money(i, "outstandingBalance"),
		"line_item_type":     crunch.Str(i, "lineItemType"),
		"payment_terms_days": crunch.Get(i, "payment", "paymentTerms", "days"),
		"bank_account":       firstNonEmpty(crunch.Str(i, "bankAccount", "displayableName"), crunch.Str(i, "bankAccount", "id")),
		"note":               crunch.Str(i, "note", "text"),
		"recurring":          crunch.Bool(i, "recurring"),
		"items": mapItems(crunch.List(i, "items"), func(it Obj) Obj {
			return crunch.Compact(Obj{
				"id": crunch.ID(it, "id"), "description": crunch.Str(it, "description"), "quantity": crunch.Get(it, "quantity"),
				"price_excl_vat": crunch.Money(it, "priceExclVat"), "vat_rate": crunch.Str(it, "vatRate"), "amount": crunch.Money(it, "amount"),
			})
		}),
		"payments": mapItems(crunch.List(i, "payments"), func(p Obj) Obj {
			return crunch.Compact(Obj{
				"id": crunch.ID(p, "id"), "date": crunch.Str(p, "date"), "amount": crunch.Money(p, "amount"),
				"method": crunch.Str(p, "paymentTarget", "paymentMethod", "paymentMethodName"), "reconciled": crunch.Bool(p, "reconciled"),
			})
		}),
		"credit_notes": crunch.List(i, "creditNotes"),
		"attachments": mapItems(crunch.List(i, "attachments"), func(f Obj) Obj {
			return Obj{"id": crunch.ID(f, "id"), "file_name": crunch.Str(f, "fileName")}
		}),
	})
	if v, ok := i["deletable"].(bool); ok {
		out["deletable"] = v
	}
	return out
}

func registerInvoices(c *Context) {
	cl := c.Client

	getInvoice := func(ctx context.Context, id int64) (Obj, error) {
		var i Obj
		return i, cl.Get(ctx, path("/invoices/%d", id), nil, &i)
	}
	putInvoice := func(ctx context.Context, id int64, body Obj) (Obj, error) {
		var i Obj
		return i, cl.Put(ctx, path("/invoices/%d", id), nil, pinID(body, id), &i)
	}

	add(c, Def{
		Toolset: "invoices", Name: "crunch_list_invoices", Title: "List invoices",
		Description: "List sales invoices, newest first: number, customer, dates, status, amounts and outstanding balance. Filter by client, or by status (which fetches every page and filters here). Paged (up to 200 per page); use all=true to fetch every page.",
	}, func(ctx context.Context, a listInvoicesArgs) (any, error) {
		mapFn := crunch.SummarizeInvoiceState
		if a.Raw {
			mapFn = nil
		}
		if a.Status == "" {
			return fetchPaged(ctx, cl, "/invoices", crunch.Query{"clientId": a.ClientID}, a.PageArgs, "issuedDate,desc", mapFn)
		}
		pa := a.PageArgs
		pa.All = true
		res, err := fetchPaged(ctx, cl, "/invoices", crunch.Query{"clientId": a.ClientID}, pa, "issuedDate,desc", nil)
		if err != nil {
			return nil, err
		}
		var items []Obj
		for _, it := range res["items"].([]Obj) {
			if strings.EqualFold(crunch.Str(it, "status"), a.Status) {
				if mapFn != nil {
					it = mapFn(it)
				}
				items = append(items, it)
			}
		}
		if items == nil {
			items = []Obj{}
		}
		out := Obj{"total": len(items), "returned": len(items), "items": items}
		if res["truncated"] != nil {
			out["note"] = res["note"]
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_get_invoice", Title: "Get invoice",
		Description: "Get one sales invoice: customer, dates, lines, VAT, payments received, credit notes, attachments and status. raw=true returns the API object.",
	}, func(ctx context.Context, a getInvoiceArgs) (any, error) {
		i, err := getInvoice(ctx, a.InvoiceID)
		if err != nil || a.Raw {
			return i, err
		}
		return summarizeInvoiceFull(i), nil
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_get_invoice_status", Title: "Get invoice status",
		Description: "Get an invoice's current state (status, number, amounts, outstanding balance, due date), which is lighter than crunch_get_invoice.",
	}, func(ctx context.Context, a invoiceIDArgs) (any, error) {
		var s Obj
		if err := cl.Get(ctx, path("/invoices/%d/state", a.InvoiceID), nil, &s); err != nil {
			return nil, err
		}
		return crunch.SummarizeInvoiceState(s), nil
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_get_invoice_totals", Title: "Get invoice totals",
		Description: "Get sales invoice totals: with no filters, everything invoiced and still outstanding; with filters (client, status, issue or due dates, invoice number, recurring), the amount invoiced, owed to you and owed by you for the matching invoices.",
	}, func(ctx context.Context, a invoiceTotalsArgs) (any, error) {
		var res Obj
		if !a.filtered() {
			if err := cl.Get(ctx, "/invoices/sales-invoice-totals", nil, &res); err != nil {
				return nil, err
			}
			return crunch.Compact(Obj{"total_invoiced": crunch.Money(res, "salesTotal"), "outstanding": crunch.Money(res, "outstandingTotal")}), nil
		}
		q := crunch.Query{
			"clientId": a.ClientID, "status": a.Status, "issuedDateStart": a.IssuedFrom, "issuedDateEnd": a.IssuedTo,
			"dueDateStart": a.DueFrom, "dueDateEnd": a.DueTo, "recurring": a.Recurring, "invoiceNumber": a.InvoiceNumber,
		}
		if err := cl.Get(ctx, "/invoices/filtered-totals", q, &res); err != nil {
			return nil, err
		}
		return crunch.Compact(Obj{"invoiced": crunch.Money(res, "invoicedSum"), "owed_to_you": crunch.Money(res, "owedToYouSum"), "owed_by_you": crunch.Money(res, "owedByYouSum")}), nil
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_get_default_invoice_note", Title: "Get default invoice note",
		Description: "Get the default note Crunch prints on new sales invoices (often payment instructions).",
	}, func(ctx context.Context, _ NoArgs) (any, error) {
		var out any
		return out, cl.Get(ctx, "/invoices/default-note", nil, &out)
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_download_invoice_pdf", Title: "Download invoice PDF",
		Description: "Download a sales invoice as a PDF, as Crunch renders it for the customer. Returned as an embedded resource; use save_to to also write it to disk (when the server sets CRUNCH_FILES_DIR).",
		ReadOnly:    c.downloadReadOnly(),
	}, func(ctx context.Context, a downloadInvoicePDFArgs) (any, error) {
		f, err := cl.GetFile(ctx, path("/invoices/%d/pdf", a.InvoiceID), nil)
		if err != nil {
			return nil, err
		}
		if f.ContentType == "application/octet-stream" || f.ContentType == "" {
			f.ContentType = "application/pdf"
		}
		return fileResult(c, f, fmt.Sprintf("invoice-%d.pdf", a.InvoiceID), fmt.Sprintf("crunch://invoices/%d/pdf", a.InvoiceID), a.SaveTo)
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_download_invoice_attachment", Title: "Download invoice attachment",
		Description: "Download a file attached to a sales invoice. Attachment IDs and names come from crunch_get_invoice.",
		ReadOnly:    c.downloadReadOnly(),
	}, func(ctx context.Context, a downloadInvoiceAttachmentArgs) (any, error) {
		name := a.FileName
		if name == "" {
			i, err := getInvoice(ctx, a.InvoiceID)
			if err != nil {
				return nil, err
			}
			for _, f := range crunch.List(i, "attachments") {
				if crunch.ID(f, "id") == a.AttachmentID {
					name = crunch.Str(f, "fileName")
				}
			}
			if name == "" {
				return nil, fmt.Errorf("invoice %d has no attachment %d", a.InvoiceID, a.AttachmentID)
			}
		}
		f, err := cl.GetFile(ctx, "/invoices/attachments", crunch.Query{"invoiceId": a.InvoiceID, "attachmentId": a.AttachmentID, "fileName": name})
		if err != nil {
			return nil, err
		}
		return fileResult(c, f, name, fmt.Sprintf("crunch://invoices/%d/attachments/%d", a.InvoiceID, a.AttachmentID), a.SaveTo)
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_create_invoice", Title: "Create invoice",
		Description: "Create a draft sales invoice for a client: date, lines (description, unit price excluding VAT, quantity, VAT rate), payment terms and the bank account to be paid into. " +
			"Crunch's API only supports GBP. The invoice stays a draft until crunch_issue_invoice. Look up the client (crunch_list_clients), VAT rates (crunch_list_vat_types for=invoices) and bank account first.",
		Gate:  Write,
		Enums: map[string][]string{"line_item_type": {"SIMPLE", "DETAILED"}},
	}, func(ctx context.Context, a createInvoiceArgs) (any, error) {
		if len(a.Items) == 0 {
			return nil, errors.New("give at least one item")
		}
		if len(a.Attachments) > maxInvoiceAttachments {
			return nil, fmt.Errorf("an invoice can have at most %d attachments", maxInvoiceAttachments)
		}
		if a.SendReminders != nil && *a.SendReminders && !c.AllowEmail {
			return nil, errors.New(remindersDisabled)
		}
		if err := c.checkReminders(a.Extra); err != nil {
			return nil, err
		}
		lineType := a.LineItemType
		if lineType == "" {
			lineType = "SIMPLE"
		}
		body := set(Obj{
			"customer":     Obj{"id": a.ClientID},
			"currency":     "POUND_STERLING",
			"issuedDate":   a.IssuedDate,
			"lineItemType": lineType,
			"items":        itemsBody(a.Items),
			"attachments":  []Obj{},
		}, "dueDate", a.DueDate, "bankAccount", ref(a.BankAccountID), "reference", a.Reference, "purchaseOrder", a.PurchaseOrder)
		if terms := set(Obj{}, "days", a.PaymentTermsDays, "sendReminders", a.SendReminders); len(terms) > 0 {
			body["payment"] = Obj{"paymentTerms": terms}
		}
		if a.Note != "" {
			body["note"] = Obj{"text": a.Note}
		}
		if len(a.Attachments) > 0 {
			files, err := attachmentsBody(c, a.Attachments, 0)
			if err != nil {
				return nil, err
			}
			body["attachments"] = files
		}
		var created Obj
		if err := cl.Post(ctx, "/invoices", nil, merge(body, a.Extra), &created); err != nil {
			return nil, err
		}
		return summarizeInvoiceFull(created), nil
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_update_invoice", Title: "Update invoice",
		Description: "Change a sales invoice. The current invoice is fetched and only what you pass changes; existing payments and attachments are kept. `items` replaces all lines. Issued invoices may be restricted by Crunch.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a updateInvoiceArgs) (any, error) {
		if a.SendReminders != nil && *a.SendReminders && !c.AllowEmail {
			return nil, errors.New(remindersDisabled)
		}
		if err := c.checkReminders(a.Changes); err != nil {
			return nil, err
		}
		current, err := getInvoice(ctx, a.InvoiceID)
		if err != nil {
			return nil, err
		}
		patch := set(Obj{}, "customer", ref(a.ClientID), "issuedDate", a.IssuedDate, "dueDate", a.DueDate, "bankAccount", ref(a.BankAccountID))
		if len(a.Items) > 0 {
			patch["items"] = itemsBody(a.Items)
		}
		if a.Reference != nil {
			patch["reference"] = *a.Reference
		}
		if a.PurchaseOrder != nil {
			patch["purchaseOrder"] = *a.PurchaseOrder
		}
		if a.Note != nil {
			patch["note"] = Obj{"text": *a.Note}
		}
		if terms := set(Obj{}, "days", a.PaymentTermsDays, "sendReminders", a.SendReminders); len(terms) > 0 {
			patch["payment"] = Obj{"paymentTerms": terms}
		}
		if len(patch) == 0 && len(a.Changes) == 0 {
			return nil, errors.New("nothing to change")
		}
		body := deepMerge(deepMerge(current, patch), a.Changes)
		if err := c.checkKeepsEntries(current, body); err != nil {
			return nil, err
		}
		updated, err := putInvoice(ctx, a.InvoiceID, body)
		if err != nil {
			return nil, err
		}
		return summarizeInvoiceFull(updated), nil
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_issue_invoice", Title: "Issue invoice",
		Description: "Issue a draft sales invoice: it gets its invoice number and is posted to your accounts. This is final (correct mistakes afterwards with a credit note in Crunch). It does not email the customer; see crunch_email_invoice.",
		Gate:        Write, Destructive: boolPtr(true),
	}, func(ctx context.Context, a invoiceIDArgs) (any, error) {
		var issued Obj
		if err := cl.Post(ctx, path("/invoices/%d/issue", a.InvoiceID), nil, nil, &issued); err != nil {
			return nil, err
		}
		return summarizeInvoiceFull(issued), nil
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_email_invoice", Title: "Email invoice to customer",
		Description: "Email an issued sales invoice (as a PDF) to the customer from Crunch. This sends a real email: confirm the recipients, subject and message with the user first.",
		Gate:        Email, Destructive: boolPtr(false), OpenWorld: true,
	}, func(ctx context.Context, a emailInvoiceArgs) (any, error) {
		recipients := a.EmailAddresses
		if len(recipients) == 0 {
			i, err := getInvoice(ctx, a.InvoiceID)
			if err != nil {
				return nil, err
			}
			if e := crunch.Str(i, "customer", "email"); e != "" {
				recipients = []string{e}
			} else {
				return nil, errors.New("the customer has no email address; pass email_addresses")
			}
		}
		body := set(Obj{"emailAddresses": recipients}, "subject", a.Subject, "message", a.Message)
		if err := cl.Post(ctx, path("/invoices/%d/email", a.InvoiceID), crunch.Query{"attachments": a.IncludeAttachments}, body, nil); err != nil {
			return nil, err
		}
		return Obj{"invoice_id": a.InvoiceID, "sent_to": recipients, "sent": true}, nil
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_add_invoice_payment", Title: "Record invoice payment",
		Description: "Record a payment received from a customer against an issued invoice (full or part payment). This records the payment but doesn't reconcile the matching bank statement entry. Existing payments are kept.",
		Gate:        Write,
	}, func(ctx context.Context, a invoicePaymentArgs) (any, error) {
		current, err := getInvoice(ctx, a.InvoiceID)
		if err != nil {
			return nil, err
		}
		bank := a.BankAccountID
		if bank == nil {
			if id, ok := crunch.ID(current, "bankAccount", "id").(int64); ok {
				bank = &id
			}
		}
		payments, _ := current["payments"].([]any)
		// New payments are sent without an id; existing ones exactly as returned, or Crunch deletes them.
		current["payments"] = append(payments, Obj{"amount": a.Amount, "date": a.Date, "paymentTarget": paymentTarget(a.PaymentMethod, bank, nil, nil)})
		outstanding, hasOutstanding := crunch.Num(current, "outstandingBalance")
		updated, err := putInvoice(ctx, a.InvoiceID, current)
		if err != nil {
			return nil, err
		}
		out := summarizeInvoiceFull(updated)
		if hasOutstanding && a.Amount.Float() > outstanding+0.005 {
			out["warning"] = fmt.Sprintf("The payment is more than the %.2f that was outstanding.", outstanding)
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_remove_invoice_payment", Title: "Remove invoice payment",
		Description: "Remove a recorded customer payment from an invoice (for example one entered by mistake). Reconciled payments can't be removed.",
		Gate:        Delete,
	}, func(ctx context.Context, a removeInvoicePaymentArgs) (any, error) {
		current, err := getInvoice(ctx, a.InvoiceID)
		if err != nil {
			return nil, err
		}
		var keep []any
		var removed Obj
		for _, p := range crunch.List(current, "payments") {
			if crunch.ID(p, "id") == a.PaymentID {
				removed = p
			} else {
				keep = append(keep, p)
			}
		}
		if removed == nil {
			return nil, fmt.Errorf("invoice %d has no payment %d", a.InvoiceID, a.PaymentID)
		}
		if crunch.Bool(removed, "reconciled") {
			return nil, errors.New("reconciled payments can't be removed; unreconcile it in Crunch first")
		}
		if keep == nil {
			keep = []any{}
		}
		current["payments"] = keep
		updated, err := putInvoice(ctx, a.InvoiceID, current)
		if err != nil {
			return nil, err
		}
		return Obj{"removed": crunch.Compact(Obj{"id": a.PaymentID, "date": crunch.Str(removed, "date"), "amount": crunch.Money(removed, "amount")}), "invoice": summarizeInvoiceFull(updated)}, nil
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_add_invoice_attachment", Title: "Attach file to invoice",
		Description: "Attach files (for example timesheets) to a sales invoice. Existing attachments are kept; an invoice can have up to 5.",
		Gate:        Write,
	}, func(ctx context.Context, a invoiceAttachmentArgs) (any, error) {
		if len(a.Attachments) == 0 {
			return nil, errors.New("give at least one attachment")
		}
		current, err := getInvoice(ctx, a.InvoiceID)
		if err != nil {
			return nil, err
		}
		existing, _ := current["attachments"].([]any)
		if len(existing)+len(a.Attachments) > maxInvoiceAttachments {
			return nil, fmt.Errorf("an invoice can have at most %d attachments; it already has %d", maxInvoiceAttachments, len(existing))
		}
		next := int64(0)
		for _, f := range crunch.Objs(existing) {
			if id, ok := crunch.ID(f, "id").(int64); ok && id >= next {
				next = id + 1
			}
		}
		files, err := attachmentsBody(c, a.Attachments, next)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			existing = append(existing, f)
		}
		current["attachments"] = existing
		updated, err := putInvoice(ctx, a.InvoiceID, current)
		if err != nil {
			return nil, err
		}
		return Obj{"invoice_id": a.InvoiceID, "attachments": summarizeInvoiceFull(updated)["attachments"]}, nil
	})

	add(c, Def{
		Toolset: "invoices", Name: "crunch_delete_invoice", Title: "Delete invoice",
		Description: "Delete a sales invoice. Usually only drafts can be deleted (see `deletable` in crunch_get_invoice); issued invoices are corrected with credit notes in Crunch.",
		Gate:        Delete,
	}, func(ctx context.Context, a invoiceIDArgs) (any, error) {
		if err := cl.Delete(ctx, path("/invoices/%d", a.InvoiceID), nil, nil); err != nil {
			return nil, err
		}
		return Obj{"invoice_id": a.InvoiceID, "deleted": true}, nil
	})
}
