# Using crunch-mcp

Once the server is connected and logged in, just talk to your assistant about your business. This page shows what works well and explains how the tools behave, so you know what to expect.

- [Example questions](#example-questions)
- [Example conversations](#example-conversations)
- [Built-in prompts](#built-in-prompts)
- [Making changes](#making-changes)
- [Receipts, attachments and PDFs](#receipts-attachments-and-pdfs)
- [Lists, paging and raw objects](#lists-paging-and-raw-objects)
- [The raw API tool](#the-raw-api-tool)
- [Tips for good results](#tips-for-good-results)

## Example questions

**The big picture**

- *"How's the business doing?"* (`crunch_get_overview`)
- *"How much is in the bank, and how much are customers owing me?"*
- *"Which accounting periods are open?"*
- *"What's my VAT number and which VAT scheme am I on?"*

**Invoices and customers**

- *"Which invoices are unpaid? Which are overdue, and by how long?"*
- *"How much have I invoiced Example Client Ltd this year?"* (`crunch_get_invoice_totals`)
- *"Show me invoice 1234 and download the PDF."*
- *"Example Client Ltd paid INV-0042 in full yesterday. Record the payment."* (writes)

**Expenses and receipts**

- *"What did I spend on travel this tax year?"* (`crunch_get_expenses_total`)
- *"Which expenses since April have no receipt attached?"*
- *"Are there any bills I haven't paid yet?"*
- *"Show me the receipt for the Example Hosting expense in August."* (`crunch_download_expense_attachment`)
- *"Record this receipt."* with a photo attached (writes)

**Suppliers, banking and recurring costs**

- *"Who are my top ten suppliers by spend this year?"*
- *"What recurring expenses are set up, and when do they next run?"*
- *"Which payments on the business account aren't reconciled yet?"* (`crunch_list_bank_account_payments`)

## Example conversations

These show the kind of back-and-forth to expect for a small UK limited company or contractor. Your assistant's wording will differ.

### Recording a receipt

> **You:** *Here's a receipt from Example Rail for a return ticket to Leeds for a client meeting, £86.40, paid on the business debit card on 3 October.* (photo attached)
>
> **Assistant:** *I'll record it like this:*
>
> - *Supplier: Example Rail (new, I'll create it)*
> - *Date: 2026-10-03*
> - *Expense type: TRAVEL_AND_SUBSISTENCE, VAT type: Zero Rated (rail fares carry no VAT), gross £86.40*
> - *Paid: £86.40 on 2026-10-03 by EFT from Business Current Account*
> - *Receipt attached: receipt.jpg*
>
> *2026-10-03 is in the open period 2026-04-01 to 2027-03-31. Shall I go ahead?*
>
> **You:** *Yes.*
>
> **Assistant:** *Done. Expense 51234 is recorded as paid, with the receipt attached.*

Behind the scenes the assistant used `crunch_list_suppliers`, `crunch_list_expense_types`, `crunch_list_vat_types`, `crunch_list_bank_accounts` and `crunch_list_accounting_periods` to find valid values, then `crunch_create_expense`. The `record_receipt` prompt asks for exactly this flow.

### Chasing overdue invoices

> **You:** */mcp__crunch__chase_overdue_invoices 14*
>
> **Assistant:** *Two customers have invoices more than 14 days overdue:*
>
> | *Customer* | *Invoice* | *Due* | *Outstanding* | *Days overdue* |
> |---|---|---|---|---|
> | *Example Client Ltd* | *INV-0042* | *2026-09-30* | *£2,400.00* | *15* |
> | *Sample Studio Ltd* | *INV-0037* | *2026-08-14* | *£1,150.00* | *62* |
>
> *Here are draft emails. The first is a friendly reminder; the second is firmer because it's over 60 days… I haven't sent anything.*

With `CRUNCH_ALLOW_EMAIL=true` you can then say *"send the first one"*. The assistant shows the recipients, subject and message again and waits for your go-ahead before calling `crunch_email_invoice`.

### A monthly summary

> **You:** *Summarise September for me.*
>
> **Assistant:** *September 2026: you invoiced £9,600 net (£11,520 with VAT) to three customers, and spent £1,874 net, mostly on software subscriptions and travel. The bank balance went from £15,100 to £18,240. Two expenses have no receipt…*

The `period_summary` prompt does the same for any dates, with tables by customer, expense type and supplier.

### A VAT-period estimate

> **You:** */mcp__crunch__period_summary 2026-07-01 2026-09-30*
>
> **Assistant:** *… Estimated output VAT on sales: £5,760. Estimated input VAT on purchases: £412. You're on the standard VAT scheme, so the estimate of VAT due is about £5,348. This is an estimate from the invoices and expenses in Crunch, not your VAT return.*

crunch-mcp can't see or file VAT returns: they aren't part of Crunch's public API. Use estimates like this to sense-check, and file through Crunch as usual. On the Flat Rate Scheme, VAT due is worked out differently, and the assistant says so.

### Creating and issuing an invoice

> **You:** *Invoice Example Client Ltd for 5 days of consulting in October at £450 a day.*
>
> **Assistant:** *Here's the draft:*
>
> - *Customer: Example Client Ltd (30-day terms)*
> - *Date: 2026-10-31, due 2026-11-30*
> - *1 line: "Consulting services, October 2026", 5 × £450.00, Standard VAT (20%)*
> - *Net £2,250.00, VAT £450.00, total £2,700.00*
> - *Paid into: Business Current Account*
>
> *Shall I create it as a draft?*
>
> **You:** *Yes, and issue it.*
>
> **Assistant:** *Created draft invoice 8812. Issuing makes it final and gives it a number, and mistakes then need a credit note. Issue it now?*
>
> **You:** *Go ahead.*
>
> **Assistant:** *Issued as INV-0045.*

Your MCP client will usually also ask you to approve `crunch_issue_invoice` itself, because it's marked destructive.

## Built-in prompts

The server includes six prompts. Each one is a ready-made, multi-step request that tells the model which tools to use and how to report. Most clients show them as slash commands or in a prompt picker. In Claude Code they're `/mcp__crunch__<name>`, for example `/mcp__crunch__business_health_check`.

| Prompt | Arguments | What it does |
|---|---|---|
| `business_health_check` | | Reviews cash, money owed to and by you, overdue invoices, unpaid bills and missing receipts, and gives a short to-do list. |
| `chase_overdue_invoices` | `days_overdue` (optional, default 1) | Finds overdue invoices, groups them by customer and drafts polite chasing emails. |
| `record_receipt` | `details` | Turns a receipt, bill or description of a purchase into a correctly categorised expense, after showing you a summary. |
| `expense_review` | `start_date`, `end_date` (both optional; default the last three months) | Looks for missing receipts, unpaid bills, possible duplicates, odd categories and suspicious VAT, with totals by type and supplier. |
| `create_invoice` | `client`, `work` | Finds or creates the client, picks the VAT rate, terms and bank account, and shows you the draft before creating it. |
| `period_summary` | `start_date`, `end_date` | Sales, spending and cash for a period, with a VAT estimate if you're VAT registered. |

`business_health_check`, `expense_review` and `period_summary` tell the model not to change anything. `chase_overdue_invoices` only drafts emails unless you ask it to send them. `record_receipt` and `create_invoice` make changes only after you confirm, and need writes enabled to do so.

## Making changes

With `CRUNCH_ALLOW_WRITES=true` the assistant can change your books. The server tells the model to confirm amounts, dates and VAT treatment with you before creating or changing records, and before issuing an invoice. Good clients also ask before running tools marked *destructive*. It's still a good habit to ask for a plan first: *"What would you record? Don't do it yet."*

### Looking up valid values

Crunch is strict about some fields. Expense types, VAT types and payment methods must be exact names, and dates must fall in an open accounting period. The `reference` toolset exists so the assistant can check before it writes: `crunch_list_expense_types`, `crunch_list_vat_types`, `crunch_list_payment_methods`, `crunch_list_accounting_periods` and `crunch_list_persons`. Keep `reference` in `CRUNCH_TOOLSETS` whenever writes are on.

Amounts are in pounds and can be given as numbers (`120`) or decimal strings (`"120.00"`). Dates are `YYYY-MM-DD`. Expense lines take the **gross** amount, and Crunch works out the VAT from the VAT type. Invoice lines take the unit price **before** VAT.

### Updates only change what you ask

`update_*` tools, such as `crunch_update_expense` and `crunch_update_invoice`, fetch the current record, change **only the fields you pass**, and send the whole record back. This matters with Crunch: for invoices in particular, Crunch deletes any existing payments or attachments that are missing from an update. crunch-mcp always sends them back as they were.

Payments and attachments have their own tools, which also keep what's already there: `crunch_add_expense_payment`, `crunch_add_invoice_payment`, `crunch_add_expense_attachment` and `crunch_add_invoice_attachment`.

Some arguments replace a whole list: `line_items` on an expense and `items` on an invoice replace all existing lines, and a new `billing_address` replaces the whole address. For fields without their own argument, `changes` (on updates) and `extra` (on creates) take Crunch's own field names and are merged in last. Without `CRUNCH_ALLOW_DELETES`, an update whose `changes` would drop an existing payment or attachment is refused, and without `CRUNCH_ALLOW_EMAIL`, so is anything that turns on payment reminders.

### Issuing and emailing invoices

- `crunch_create_invoice` creates a **draft**. Drafts can be changed or deleted freely.
- `crunch_issue_invoice` gives it an invoice number and posts it to your accounts. **This is final**: corrections afterwards need a credit note in Crunch. It's marked destructive for that reason. Issuing doesn't email anyone.
- `crunch_email_invoice` sends the invoice to the customer from Crunch, as a real email. It only exists with `CRUNCH_ALLOW_EMAIL=true`, and the model is told to show you the recipients, subject and message first. It's marked *open-world* because it reaches outside your Crunch account.
- Payment reminders (`send_reminders: true` on `crunch_create_invoice` or `crunch_update_invoice`) are emails to the customer too, so turning them on also needs `CRUNCH_ALLOW_EMAIL=true`. Turning them off doesn't.

### Deleting

Deleting needs `CRUNCH_ALLOW_DELETES=true` as well. With it off, the delete tools and the tools that remove payments don't exist, and the raw API tool refuses `DELETE` requests. Crunch has its own limits too: reconciled expenses and payments can't be removed, closed accounting years can't be edited, and issued invoices usually can't be deleted.

## Receipts, attachments and PDFs

**Uploading.** Tools that take `attachments` (creating an expense or invoice, or adding to one) accept each file in one of two ways:

- `content_base64`: the file as base64 or as a `data:` URI. This is how an assistant passes a file you've attached to the chat.
- `file_path`: a path to a file **on the machine running crunch-mcp**, inside `CRUNCH_FILES_DIR` (absolute, or relative to it). It's refused unless the server sets `CRUNCH_FILES_DIR`. In Docker, mount a folder into the container and point `CRUNCH_FILES_DIR` at it.

Files can be up to 10 MB each, and an invoice can have up to five. Crunch needs attachments sent as `data:` URIs, and a raw file uploaded any other way is saved as an empty file. crunch-mcp always builds the `data:` URI for you, guessing the MIME type from the file name when you don't give one.

**Downloading.** `crunch_download_expense_attachment`, `crunch_download_invoice_attachment` and `crunch_download_invoice_pdf` return the file in the tool result:

- **Images** come back inline, so the assistant can read a receipt and check it against the expense.
- **PDFs and other files** come back as an embedded resource, which your client may let you open or save.
- Files over 8 MB aren't returned inline. Use `save_to` instead.

`save_to` also writes the file **on the machine running crunch-mcp**, inside `CRUNCH_FILES_DIR`: to a directory (keeping the file's name) or a file path, absolute or relative to it. It never overwrites an existing file, and it's refused unless the server sets `CRUNCH_FILES_DIR`. In Docker with the default read-only root filesystem, mount a writable folder, for example `./files:/files` with `CRUNCH_FILES_DIR=/files`.

## Lists, paging and raw objects

- **Compact by default.** List tools return trimmed summaries with the useful fields: names, dates, amounts, status and outstanding balances. Get tools return the full record.
- **Paging.** Expenses, invoices, recurring expenses and bank account payments are paged by Crunch. List tools fetch one page of 50 by default (`page`, `size` up to 200 and `sort` change that). `all: true` fetches every page, up to `max_items` (default 1000), and says when it stopped early.
- **Filters** like `start_date`, `end_date`, `supplier_id`, `client_id`, `has_attachment` and `status` keep responses small. Totals tools (`crunch_get_expenses_total`, `crunch_get_invoice_totals`) answer *"how much"* without listing anything.
- **`raw: true`** on many list and get tools returns Crunch's full objects when you need a field the summary leaves out.
- **IDs, not numbers.** Tools take Crunch's internal invoice ID, not the invoice number printed on it. The assistant finds the ID with `crunch_list_invoices` first.

Login tokens are never shown by any tool. `crunch_auth_status` reports where they come from and when they expire, but not the tokens themselves.

## The raw API tool

`crunch_api_request` (toolset `raw`) calls any path of the Crunch public API, such as `/expenses/vat-types` or `/banking/bank-accounts`. It's a fallback for things the other tools don't cover. It follows the same switches as everything else:

| Settings | Allowed methods |
|---|---|
| Default (read-only) | `GET` |
| `CRUNCH_ALLOW_WRITES=true` | `GET`, `POST`, `PUT` |
| `+ CRUNCH_ALLOW_DELETES=true` | All, including `DELETE` |

Without `CRUNCH_ALLOW_EMAIL`, it also refuses email and reminder endpoints and request bodies that turn on `sendReminders`. Without `CRUNCH_ALLOW_DELETES`, a `PUT` to an invoice, expense or recurring expense must send back all of its existing payments and attachments (the tool fetches the record to check). The path must be a plain, canonical API path: query parameters go in `query`, and `.` or `..` segments, `//`, `%`, `;`, spaces and control characters are refused. Leave `raw` out of `CRUNCH_TOOLSETS` if you want the assistant limited to the curated tools.

## Tips for good results

- **Give the details up front.** Supplier, date, amount, how it was paid and which account: the fewer gaps, the fewer questions.
- **Be specific about periods.** *"This tax year"*, *"Q3"* or *"since 1 April"* lead to tighter tool calls.
- **Ask for evidence.** *"…and list the invoices you based that on"* keeps answers grounded.
- **Plan, then apply.** For changes, ask for a summary, check it, then say *"go ahead"*.
- **Check VAT treatment.** The assistant picks VAT types from what Crunch offers, but you know your business. Have a look before you confirm, especially for overseas suppliers and anything exempt.
- **Fewer tools, better focus.** If the assistant picks the wrong tools, narrow `CRUNCH_TOOLSETS`.
- **It's not your accountant.** Summaries and VAT estimates are a helpful sense-check, not tax advice. Check anything important with Crunch's accountants or your own.
