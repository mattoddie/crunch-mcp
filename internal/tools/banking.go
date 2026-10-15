package tools

import (
	"context"
	"errors"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

type listAccountsArgs struct {
	Status string `json:"status,omitempty" jsonschema:"Which to list: active (default), archived or all"`
	Search string `json:"search,omitempty" jsonschema:"Case-insensitive substring filter on names and numbers"`
	Raw    bool   `json:"raw,omitempty" jsonschema:"Return the full objects from the API instead of compact summaries (larger)"`
}

type bankAccountIDArgs struct {
	BankAccountID int64 `json:"bank_account_id" jsonschema:"Bank account ID (see crunch_list_bank_accounts)"`
}

type bankPaymentsArgs struct {
	BankAccountID int64 `json:"bank_account_id" jsonschema:"Bank account ID (see crunch_list_bank_accounts)"`
	Reconciled    bool  `json:"reconciled" jsonschema:"true for payments matched to bank statement entries, false for those still waiting to be reconciled"`
	PageArgs
	Raw bool `json:"raw,omitempty" jsonschema:"Return the full objects from the API instead of compact summaries (larger)"`
}

type openingBalancesArgs struct {
	Type       string  `json:"type,omitempty" jsonschema:"Account type: bank_accounts (default) or credit_cards"`
	AccountIDs []int64 `json:"account_ids" jsonschema:"Bank account or credit card IDs"`
}

type bankAccountFields struct {
	DisplayName   string         `json:"display_name,omitempty" jsonschema:"Name shown in Crunch, for example Business Current Account"`
	BankName      string         `json:"bank_name,omitempty" jsonschema:"Bank, for example Starling"`
	AccountName   string         `json:"account_name,omitempty" jsonschema:"Name on the account"`
	SortCode      string         `json:"sort_code,omitempty" jsonschema:"Sort code, for example 12-34-56"`
	AccountNumber string         `json:"account_number,omitempty" jsonschema:"Account number"`
	IBAN          string         `json:"iban,omitempty" jsonschema:"IBAN"`
	BIC           string         `json:"bic,omitempty" jsonschema:"BIC"`
	Personal      *bool          `json:"personal,omitempty" jsonschema:"A personal (not business) account used for business payments"`
	Extra         map[string]any `json:"extra,omitempty" jsonschema:"Additional raw fields for the request body, using Crunch's own field names (advanced). Merged last."`
}

func (f bankAccountFields) body() Obj {
	return merge(set(Obj{},
		"displayName", f.DisplayName, "bankName", f.BankName, "accountName", f.AccountName, "sortCode", f.SortCode,
		"accountNumber", f.AccountNumber, "iban", f.IBAN, "bic", f.BIC, "personal", f.Personal,
	), f.Extra)
}

type createBankAccountArgs struct {
	DisplayName string `json:"display_name" jsonschema:"Name shown in Crunch, for example Business Current Account"`
	BankName    string `json:"bank_name" jsonschema:"Bank, for example Starling"`
	bankAccountFields
}

type updateBankAccountArgs struct {
	BankAccountID int64 `json:"bank_account_id" jsonschema:"Bank account ID (see crunch_list_bank_accounts)"`
	bankAccountFields
}

type archiveBankAccountArgs struct {
	BankAccountID int64 `json:"bank_account_id" jsonschema:"Bank account ID (see crunch_list_bank_accounts)"`
	Archive       *bool `json:"archive,omitempty" jsonschema:"true to archive (default), false to restore"`
}

type creditCardIDArgs struct {
	CreditCardID int64 `json:"credit_card_id" jsonschema:"Credit card ID (see crunch_list_credit_cards)"`
}

type creditCardFields struct {
	DisplayName string         `json:"display_name,omitempty" jsonschema:"Name shown in Crunch, for example Amex Business"`
	CardNumber  string         `json:"card_number,omitempty" jsonschema:"Card number (Crunch usually only needs the last four digits)"`
	Personal    *bool          `json:"personal,omitempty" jsonschema:"A personal card used for business payments"`
	Extra       map[string]any `json:"extra,omitempty" jsonschema:"Additional raw fields for the request body, using Crunch's own field names (advanced). Merged last."`
}

func (f creditCardFields) body() Obj {
	return merge(set(Obj{}, "displayName", f.DisplayName, "cardNumber", f.CardNumber, "personal", f.Personal), f.Extra)
}

type createCreditCardArgs struct {
	DisplayName string `json:"display_name" jsonschema:"Name shown in Crunch, for example Amex Business"`
	creditCardFields
}

type updateCreditCardArgs struct {
	CreditCardID int64 `json:"credit_card_id" jsonschema:"Credit card ID (see crunch_list_credit_cards)"`
	creditCardFields
}

type openingBalanceArgs struct {
	CreditCardID   int64 `json:"credit_card_id" jsonschema:"Credit card ID (see crunch_list_credit_cards)"`
	OpeningBalance Money `json:"opening_balance" jsonschema:"Opening balance, for example 250.00"`
}

type archiveCreditCardArgs struct {
	CreditCardID int64 `json:"credit_card_id" jsonschema:"Credit card ID (see crunch_list_credit_cards)"`
	Archive      *bool `json:"archive,omitempty" jsonschema:"true to archive (default), false to restore"`
}

func archiveFlag(b *bool) bool { return b == nil || *b }

func registerBanking(c *Context) {
	cl := c.Client
	statusEnum := map[string][]string{"status": {"active", "archived", "all"}}

	// ---------------------------------------------------------------- bank accounts

	add(c, Def{
		Toolset: "banking", Name: "crunch_list_bank_accounts", Title: "List bank accounts",
		Description: "List the business's bank accounts with their current balance, sort code and account number, default flag, bank feed state and how many statement entries still need matching. Use the IDs when recording payments.",
		Enums:       statusEnum,
	}, func(ctx context.Context, a listAccountsArgs) (any, error) {
		var res []any
		var err error
		switch a.Status {
		case "all":
			err = cl.Get(ctx, "/banking/bank-accounts", nil, &res)
		case "archived":
			err = cl.Get(ctx, "/banking/bank-accounts/accounts/true", nil, &res)
		default:
			err = cl.Get(ctx, "/banking/bank-accounts/accounts/false", nil, &res)
		}
		if err != nil {
			return nil, err
		}
		items := crunch.Objs(res)
		if !a.Raw {
			items = mapItems(items, crunch.SummarizeBankAccount)
		}
		return filterAndLimit(items, a.Search, nil, func(o Obj) []string {
			return []string{crunch.Str(o, "name"), crunch.Str(o, "displayName"), crunch.Str(o, "bank"), crunch.Str(o, "bankName"), crunch.Str(o, "account_number"), crunch.Str(o, "accountNumber")}
		}), nil
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_get_bank_account", Title: "Get bank account",
		Description: "Get one bank account in full: balance, account details, bank feed, unmatched statement entries and whether it can be edited, archived or deleted.",
	}, func(ctx context.Context, a bankAccountIDArgs) (any, error) {
		var out any
		return out, cl.Get(ctx, path("/banking/bank-accounts/%d", a.BankAccountID), nil, &out)
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_list_bank_account_payments", Title: "List bank account payments",
		Description: "List payments recorded in Crunch against a bank account (supplier payments, customer payments, transfers, salaries, dividends, tax payments...), either reconciled with the bank statement or still unreconciled. Paged; newest first by default.",
	}, func(ctx context.Context, a bankPaymentsArgs) (any, error) {
		var mapFn func(Obj) Obj
		if !a.Raw {
			mapFn = crunch.SummarizePayment
		}
		return fetchPaged(ctx, cl, path("/banking/bank-accounts/%d/payments", a.BankAccountID), crunch.Query{"reconciled": a.Reconciled}, a.PageArgs, "date,desc", mapFn)
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_get_opening_balances", Title: "Get opening balances",
		Description: "Get the opening balances of bank accounts or credit cards, and whether each can still be edited.",
		Enums:       map[string][]string{"type": {"bank_accounts", "credit_cards"}},
	}, func(ctx context.Context, a openingBalancesArgs) (any, error) {
		if len(a.AccountIDs) == 0 {
			return nil, errors.New("give at least one account ID")
		}
		kind := "bank-accounts"
		if a.Type == "credit_cards" {
			kind = "credit-cards"
		}
		var res Obj
		if err := cl.Post(ctx, "/banking/"+kind+"/opening-balances", nil, Obj{"accountIds": a.AccountIDs}, &res); err != nil {
			return nil, err
		}
		return mapItems(crunch.List(res, "openingBalances"), func(o Obj) Obj {
			return compactKeepFalse(Obj{"account_id": crunch.ID(o, "accountId"), "opening_balance": crunch.Money(o, "openingBalance"), "editable": o["editable"]})
		}), nil
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_create_bank_account", Title: "Create bank account",
		Description: "Add a bank account to Crunch so payments can be recorded against it.",
		Gate:        Write,
	}, func(ctx context.Context, a createBankAccountArgs) (any, error) {
		a.bankAccountFields.DisplayName, a.bankAccountFields.BankName = a.DisplayName, a.BankName
		var out any
		return out, cl.Post(ctx, "/banking/bank-accounts", nil, a.body(), &out)
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_update_bank_account", Title: "Update bank account",
		Description: "Change a bank account's details. Only the fields you pass change; the rest are kept. Sort code and account number can't be changed once the account has a bank feed or reconciled entries.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a updateBankAccountArgs) (any, error) {
		p := path("/banking/bank-accounts/%d", a.BankAccountID)
		var current Obj
		if err := cl.Get(ctx, p, nil, &current); err != nil {
			return nil, err
		}
		var out any
		return out, cl.Put(ctx, p, nil, deepMerge(current, a.body()), &out)
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_set_default_bank_account", Title: "Set default bank account",
		Description: "Make a bank account the default one, used for new invoices and payments.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a bankAccountIDArgs) (any, error) {
		if err := cl.Put(ctx, path("/banking/bank-accounts/default/%d", a.BankAccountID), nil, nil, nil); err != nil {
			return nil, err
		}
		return Obj{"bank_account_id": a.BankAccountID, "default": true}, nil
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_archive_bank_account", Title: "Archive or restore bank account",
		Description: "Archive a bank account you no longer use (it stays in your records but is hidden), or restore an archived one with archive=false.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a archiveBankAccountArgs) (any, error) {
		var out any
		return out, cl.Put(ctx, path("/banking/bank-accounts/archive/%d", a.BankAccountID), crunch.Query{"archive": archiveFlag(a.Archive)}, nil, &out)
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_delete_bank_account", Title: "Delete bank account",
		Description: "Permanently delete a bank account. Only possible when nothing is recorded against it (see `deletable` in crunch_get_bank_account); archive it otherwise.",
		Gate:        Delete,
	}, func(ctx context.Context, a bankAccountIDArgs) (any, error) {
		if err := cl.Delete(ctx, path("/banking/bank-accounts/%d", a.BankAccountID), nil, nil); err != nil {
			return nil, err
		}
		return Obj{"bank_account_id": a.BankAccountID, "deleted": true}, nil
	})

	// ---------------------------------------------------------------- credit cards

	add(c, Def{
		Toolset: "banking", Name: "crunch_list_credit_cards", Title: "List credit cards",
		Description: "List the business's credit cards with their balances, default flag and unmatched statement entries.",
		Enums:       statusEnum,
	}, func(ctx context.Context, a listAccountsArgs) (any, error) {
		lists := []string{"false"}
		switch a.Status {
		case "archived":
			lists = []string{"true"}
		case "all":
			lists = []string{"false", "true"}
		}
		var items []Obj
		for _, archived := range lists {
			var res []any
			if err := cl.Get(ctx, "/banking/credit-cards/cards/"+archived, nil, &res); err != nil {
				return nil, err
			}
			items = append(items, crunch.Objs(res)...)
		}
		if !a.Raw {
			items = mapItems(items, crunch.SummarizeCreditCard)
		}
		return filterAndLimit(items, a.Search, nil, func(o Obj) []string {
			return []string{crunch.Str(o, "name"), crunch.Str(o, "displayName"), crunch.Str(o, "card_number"), crunch.Str(o, "cardNumber")}
		}), nil
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_get_credit_card", Title: "Get credit card",
		Description: "Get one credit card in full, including its opening balance.",
	}, func(ctx context.Context, a creditCardIDArgs) (any, error) {
		var card Obj
		if err := cl.Get(ctx, path("/banking/credit-cards/%d", a.CreditCardID), nil, &card); err != nil {
			return nil, err
		}
		var opening Obj
		if cl.Get(ctx, path("/banking/credit-cards/%d/openingBalance", a.CreditCardID), nil, &opening) == nil && opening != nil {
			card["openingBalance"] = opening
		}
		return card, nil
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_create_credit_card", Title: "Create credit card",
		Description: "Add a credit card to Crunch so expenses can be paid from it. Set its opening balance with crunch_set_credit_card_opening_balance.",
		Gate:        Write,
	}, func(ctx context.Context, a createCreditCardArgs) (any, error) {
		a.creditCardFields.DisplayName = a.DisplayName
		var out any
		return out, cl.Post(ctx, "/banking/credit-cards", nil, a.body(), &out)
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_update_credit_card", Title: "Update credit card",
		Description: "Change a credit card's details. Only the fields you pass change.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a updateCreditCardArgs) (any, error) {
		p := path("/banking/credit-cards/%d", a.CreditCardID)
		var current Obj
		if err := cl.Get(ctx, p, nil, &current); err != nil {
			return nil, err
		}
		var out any
		return out, cl.Put(ctx, p, nil, deepMerge(current, a.body()), &out)
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_set_default_credit_card", Title: "Set default credit card",
		Description: "Make a credit card the default one.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a creditCardIDArgs) (any, error) {
		if err := cl.Put(ctx, path("/banking/credit-cards/default/%d", a.CreditCardID), nil, nil, nil); err != nil {
			return nil, err
		}
		return Obj{"credit_card_id": a.CreditCardID, "default": true}, nil
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_set_credit_card_opening_balance", Title: "Set credit card opening balance",
		Description: "Set a credit card's opening balance (the amount owed when you started using Crunch for it). Creates it, or updates an existing editable one.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a openingBalanceArgs) (any, error) {
		var existing Obj
		_ = cl.Get(ctx, path("/banking/credit-cards/%d/openingBalance", a.CreditCardID), nil, &existing)
		q := crunch.Query{"accountBalance": string(a.OpeningBalance)}
		if existing != nil && existing["openingBalance"] != nil {
			if v, ok := existing["editable"].(bool); ok && !v {
				return nil, errors.New("this card's opening balance can no longer be edited")
			}
			if err := cl.Put(ctx, path("/banking/credit-cards/updateOpeningBalance/%d", a.CreditCardID), q, nil, nil); err != nil {
				return nil, err
			}
			return Obj{"credit_card_id": a.CreditCardID, "opening_balance": a.OpeningBalance, "previous": crunch.Money(existing, "openingBalance"), "updated": true}, nil
		}
		if err := cl.Post(ctx, path("/banking/credit-cards/postOpeningBalance/%d", a.CreditCardID), q, nil, nil); err != nil {
			return nil, err
		}
		return Obj{"credit_card_id": a.CreditCardID, "opening_balance": a.OpeningBalance, "created": true}, nil
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_archive_credit_card", Title: "Archive or restore credit card",
		Description: "Archive a credit card you no longer use, or restore an archived one with archive=false.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a archiveCreditCardArgs) (any, error) {
		var out any
		return out, cl.Put(ctx, path("/banking/credit-cards/archive/%d", a.CreditCardID), crunch.Query{"archive": archiveFlag(a.Archive)}, nil, &out)
	})

	add(c, Def{
		Toolset: "banking", Name: "crunch_delete_credit_card", Title: "Delete credit card",
		Description: "Permanently delete a credit card. Only possible when nothing is recorded against it; archive it otherwise.",
		Gate:        Delete,
	}, func(ctx context.Context, a creditCardIDArgs) (any, error) {
		if err := cl.Delete(ctx, path("/banking/credit-cards/%d", a.CreditCardID), nil, nil); err != nil {
			return nil, err
		}
		return Obj{"credit_card_id": a.CreditCardID, "deleted": true}, nil
	})
}
