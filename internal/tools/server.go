package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/crunch-mcp/internal/config"
	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

// Name is the MCP server name.
const Name = "crunch-mcp"

// Version is set at build time with -ldflags "-X github.com/mattoddie/crunch-mcp/internal/tools.Version=1.2.3".
var Version = "0.0.0-dev"

func instructions(opts config.Tools) string {
	s := "Tools for Crunch Accounting (UK online accounting for freelancers, contractors and small limited companies): expenses and receipts, suppliers, recurring expenses, sales invoices, clients (customers), bank accounts, credit cards and company details. " +
		"Start with crunch_get_overview for a snapshot of the business. If a tool says the server isn't logged in, use crunch_auth_start and crunch_auth_complete. " +
		"Amounts are in GBP. Look up valid values before creating records: crunch_list_expense_types, crunch_list_vat_types, crunch_list_payment_methods, crunch_list_bank_accounts, crunch_list_suppliers and crunch_list_clients. " +
		"List tools return compact summaries as {total, returned, items}; get tools return full records. " + listHelp
	if opts.AllowWrites {
		s += "Write tools are enabled: confirm amounts, dates and VAT treatment with the user before creating or changing records, and before issuing an invoice (issued invoices are final). " +
			"Tools that change things take dry_run: true to preview the request without sending it; use it to show the user what will change. "
	} else {
		s += "The server is read-only; write tools are disabled (CRUNCH_ALLOW_WRITES=false). "
	}
	if opts.AllowEmail {
		s += "Emailing invoices is enabled: always show the user the recipients, subject and message, and get their go-ahead, before sending."
	}
	return s
}

// NewServer creates the MCP server with the tools and prompts allowed by opts.
// registered, when non-nil, is filled with each tool's toolset and gate.
func NewServer(client *crunch.Client, opts config.Tools, registered map[string]Info) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: Name, Version: Version}, &mcp.ServerOptions{Instructions: instructions(opts)})
	c := &Context{Server: s, Client: client, Tools: opts, Registered: registered, guarded: map[string]guard{}, audit: newAuditLog(opts.AuditLog)}
	s.AddReceivingMiddleware(c.guardMiddleware)
	registerAccount(c)
	registerReference(c)
	registerBanking(c)
	registerClients(c)
	registerSuppliers(c)
	registerExpenses(c)
	registerRecurring(c)
	registerInvoices(c)
	registerRaw(c)
	registerPrompts(s)
	return s
}
