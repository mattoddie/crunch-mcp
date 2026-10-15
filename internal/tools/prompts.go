package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type prompt struct {
	name, title, description string
	args                     []*mcp.PromptArgument
	text                     func(args map[string]string) string
}

func or(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

var prompts = []prompt{
	{
		name: "business_health_check", title: "Business health check",
		description: "A plain-English review of the business: cash in the bank, money owed to and by you, overdue invoices, unpaid bills and anything that needs attention.",
		text: func(map[string]string) string {
			return `Give me a health check of my business from Crunch.
1. crunch_get_overview: company, bank and card balances, invoice totals, open accounting periods.
2. crunch_list_invoices (all: true): unpaid and overdue invoices (due date before today with an outstanding balance), grouped by customer, oldest first.
3. crunch_list_expenses for the last 90 days (start_date): unpaid or part-paid bills, and expenses without a receipt (has_attachment: false).
4. crunch_list_bank_accounts: accounts with many unmatched statement entries (reconciliation backlog).
Report: a one-line verdict, then cash position, money owed to me (with overdue detail), money I owe, and a short to-do list ordered by importance. Use GBP and keep healthy areas to one line. Don't change anything.`
		},
	},
	{
		name: "chase_overdue_invoices", title: "Chase overdue invoices",
		description: "Find overdue sales invoices and draft polite chasing emails for each customer.",
		args:        []*mcp.PromptArgument{{Name: "days_overdue", Description: "Only invoices at least this many days overdue (default 1)"}},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`Find my overdue sales invoices in Crunch and help me chase them.
1. crunch_list_invoices (all: true) and keep invoices with an outstanding balance whose due date is at least %s day(s) before today.
2. Group them by customer; use crunch_get_client for each customer's email address.
3. For each customer, draft a short, polite chasing email listing invoice numbers, dates, amounts outstanding and days overdue, with a friendlier tone for first reminders and firmer for anything over 60 days.
Show me the drafts. Don't send anything or change any records unless I ask; if I ask you to send, use crunch_email_invoice only after I confirm the recipients and message.`, or(a["days_overdue"], "1"))
		},
	},
	{
		name: "record_receipt", title: "Record a receipt or bill",
		description: "Turn a receipt, bill or description of a purchase into a correctly categorised Crunch expense.",
		args:        []*mcp.PromptArgument{{Name: "details", Description: "What was bought: supplier, date, amount, how it was paid (or attach the receipt image)", Required: true}},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`Record this purchase as an expense in Crunch: %s
1. Work out the supplier, date, gross amount, VAT and what it was for. If anything important is missing or ambiguous, ask me.
2. Find the supplier with crunch_list_suppliers (search); if it doesn't exist, plan to create it.
3. Choose the expense type with crunch_list_expense_types and the VAT type with crunch_list_vat_types (for=expenses). Explain your choice briefly, especially for anything that might not be allowable or might be a personal cost.
4. If it's been paid, pick the payment method (crunch_list_payment_methods) and the bank account or card (crunch_list_bank_accounts, crunch_list_credit_cards); for a director's own money use DIRECTORS_EXPENSES with crunch_list_persons.
5. Check the date is in an open period (crunch_list_accounting_periods).
6. Show me a summary of the expense you'll create and wait for my go-ahead, then use crunch_create_expense (attaching the receipt if I gave one).`, a["details"])
		},
	},
	{
		name: "expense_review", title: "Review expenses",
		description: "Review a period's expenses for missing receipts, unpaid bills, odd categories and possible duplicates.",
		args: []*mcp.PromptArgument{
			{Name: "start_date", Description: "First day (YYYY-MM-DD; default three months ago)"},
			{Name: "end_date", Description: "Last day (YYYY-MM-DD; default today)"},
		},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`Review my Crunch expenses from %s to %s.
1. crunch_list_expenses (all: true) for the period.
2. Look for: expenses without a receipt, unpaid or part-paid bills, possible duplicates (same supplier and amount within a few days), unusual expense types for the supplier, VAT that looks wrong (for example VAT on an Exempt or overseas supplier), and large or one-off items.
3. Total spending by expense type and by supplier (top 10).
Report findings as a prioritised list with the expense IDs, then the totals. Don't change anything.`, or(a["start_date"], "three months ago"), or(a["end_date"], "today"))
		},
	},
	{
		name: "create_invoice", title: "Create an invoice",
		description: "Raise a sales invoice for a client, checking the client, VAT rate, payment terms and bank account.",
		args: []*mcp.PromptArgument{
			{Name: "client", Description: "Who to invoice", Required: true},
			{Name: "work", Description: "What to invoice for, with amounts or day rates", Required: true},
		},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`Create a sales invoice in Crunch for %s for: %s
1. Find the client with crunch_list_clients (search). If there's no match, ask me for their details and create them with crunch_create_client.
2. Check my company's VAT status with crunch_get_company_details and pick the VAT rate from crunch_list_vat_types (for=invoices).
3. Pick the bank account to be paid into (crunch_list_bank_accounts; usually the default) and the client's payment terms.
4. Show me the draft lines, VAT, total and due date, and wait for my go-ahead before crunch_create_invoice.
5. Ask whether to issue it (crunch_issue_invoice is final) and, if email is enabled, whether to email it.`, a["client"], a["work"])
		},
	},
	{
		name: "period_summary", title: "Period summary",
		description: "Income, spending and cash summary for a month, quarter or year, for example to prepare for a VAT return or year end.",
		args: []*mcp.PromptArgument{
			{Name: "start_date", Description: "First day (YYYY-MM-DD)", Required: true},
			{Name: "end_date", Description: "Last day (YYYY-MM-DD)", Required: true},
		},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`Summarise my business in Crunch from %s to %s.
1. Sales: crunch_get_invoice_totals with issued_from_date/issued_to_date, and crunch_list_invoices (all: true) for the invoices issued in the period: net, VAT and gross by customer.
2. Spending: crunch_list_expenses (all: true, start_date/end_date): net, VAT and gross by expense type and top suppliers.
3. Cash: crunch_list_bank_accounts for current balances.
4. If the company is VAT registered (crunch_get_company_details), estimate output VAT on sales and input VAT on purchases for the period, and note the VAT scheme (flat rate schemes work differently). Make clear this is an estimate, not the VAT return.
Present it as a short report with tables. Don't change anything.`, a["start_date"], a["end_date"])
		},
	},
}

func registerPrompts(s *mcp.Server) {
	for _, p := range prompts {
		p := p
		s.AddPrompt(&mcp.Prompt{Name: p.name, Title: p.title, Description: p.description, Arguments: p.args},
			func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				args := map[string]string{}
				if req.Params != nil {
					args = req.Params.Arguments
				}
				return &mcp.GetPromptResult{
					Description: p.description,
					Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: p.text(args)}}},
				}, nil
			})
	}
}
